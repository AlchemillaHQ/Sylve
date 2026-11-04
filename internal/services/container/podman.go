// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	podmanBinary = "/usr/local/bin/podman"
	cniPlugins   = "/usr/local/libexec/cni"
	runDir       = "/var/run/sylve/podman"
	dnsnameDir   = "/run/containers/cni/dnsname"
	commandPath  = "/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin"
	apiURL       = "http://podman/v5.0.0/libpod"
)

func (s *Service) path(elem ...string) string {
	return filepath.Join(append([]string{s.DataDir}, elem...)...)
}

func (s *Service) podmanEnv(pool string) []string {
	return []string{
		"PATH=" + commandPath,
		"HOME=" + s.path("home"),
		"CONTAINERS_CONF=" + s.path("pools", pool, "containers.conf"),
		"CONTAINERS_STORAGE_CONF=" + s.path("pools", pool, "storage.conf"),
		"REGISTRY_AUTH_FILE=" + s.path("auth.json"),
	}
}

func (s *Service) podmanCommand(ctx context.Context, pool string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, podmanBinary, args...)
	cmd.Env = s.podmanEnv(pool)
	return cmd
}

func (s *Service) podman(ctx context.Context, pool string, env []string, args ...string) (string, error) {
	cmd := s.podmanCommand(ctx, pool, args...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stderr.String()), "Error: "))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("podman_%s_failed: %s", args[0], message)
	}
	return stdout.String(), nil
}

type podmanService struct {
	process *os.Process
	binary  os.FileInfo
	exited  chan struct{}
}

// api sends body as JSON to the pool's podman service and decodes the reply
// into out. Failures read like the matching podman command's:
// podman_<command>_failed.
func (s *Service) api(ctx context.Context, pool, command, method, path string, body, out any) error {
	response, err := s.request(ctx, pool, command, method, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if out == nil || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// request calls the pool's podman service, starting it when nothing answers
// on its socket. Every podman command loads the whole store before doing
// anything; the service loads it once and runs until Sylve exits or the pool
// is released.
func (s *Service) request(ctx context.Context, pool, command, method, path string, body any) (*http.Response, error) {
	s.stopUpgradedService(pool)
	response, err := s.send(ctx, pool, method, path, body)
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		if err = s.startService(ctx, pool); err != nil {
			return nil, err
		}
		response, err = s.send(ctx, pool, method, path, body)
	}
	if err != nil {
		return nil, fmt.Errorf("podman_%s_failed: %w", command, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, failure(command, response)
	}
	return response, nil
}

// upgrade sends a request that podman answers by handing over the
// connection, such as starting an exec session, and returns the connection.
func (s *Service) upgrade(ctx context.Context, pool, command, path string, body any) (io.ReadWriteCloser, error) {
	request, err := newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "tcp")
	response, err := serviceClient(pool).Do(request)
	if err != nil {
		return nil, fmt.Errorf("podman_%s_failed: %w", command, err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, failure(command, response)
	}
	return response.Body.(io.ReadWriteCloser), nil
}

func failure(command string, response *http.Response) error {
	defer response.Body.Close()
	var failure struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(response.Body).Decode(&failure)
	return fmt.Errorf("podman_%s_failed: %s", command, cmp.Or(failure.Message, response.Status))
}

func (s *Service) send(ctx context.Context, pool, method, path string, body any) (*http.Response, error) {
	request, err := newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	return serviceClient(pool).Do(request)
}

func newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, apiURL+path, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func serviceClient(pool string) *http.Client {
	socket := serviceSocket(pool)
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}}
}

func (s *Service) startService(ctx context.Context, pool string) error {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()

	ping := func() bool {
		response, err := s.send(ctx, pool, http.MethodGet, "/_ping", nil)
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	}
	if ping() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.stopService(pool)
	socket := serviceSocket(pool)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	binary, err := os.Stat(podmanBinary)
	if err != nil {
		return fmt.Errorf("podman_service_failed: %w", err)
	}
	cmd := s.podmanCommand(context.Background(), pool, "system", "service", "--time", "0", "unix://"+socket)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("podman_service_failed: %w", err)
	}
	service := &podmanService{process: cmd.Process, binary: binary, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(service.exited)
	}()
	if s.services == nil {
		s.services = map[string]*podmanService{}
	}
	s.services[pool] = service

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-service.exited:
			return fmt.Errorf("podman_service_failed: %s", cmd.ProcessState)
		case <-ctx.Done():
			return fmt.Errorf("podman_service_failed: %w", ctx.Err())
		case <-ticker.C:
			if ping() {
				return nil
			}
		}
	}
}

// ReleasePool stops the pool's podman service, which keeps the pool's
// container store open, so the pool can be destroyed. The pool is set up
// again if it's used afterwards.
func (s *Service) ReleasePool(pool string) {
	s.poolMu.Lock()
	delete(s.pools, pool)
	s.poolMu.Unlock()
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	s.stopService(pool)
}

// stopUpgradedService stops the pool's service when podman was upgraded
// under it, so the request starts the new one. The old one can't find its own
// binary anymore, which it names as the exit command of every container and
// exec session it creates, so those fail with "no such file or directory".
func (s *Service) stopUpgradedService(pool string) {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	service := s.services[pool]
	if service == nil {
		return
	}
	if binary, err := os.Stat(podmanBinary); err == nil && !os.SameFile(binary, service.binary) {
		s.stopService(pool)
	}
}

func (s *Service) stopService(pool string) {
	service := s.services[pool]
	if service == nil {
		return
	}
	delete(s.services, pool)
	_ = service.process.Signal(syscall.SIGTERM)
	select {
	case <-service.exited:
	case <-time.After(10 * time.Second):
		_ = service.process.Kill()
		<-service.exited
	}
}

func serviceSocket(pool string) string {
	return filepath.Join(runDir, pool, "podman.sock")
}

func (s *Service) ensurePool(ctx context.Context, pool string) error {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	if s.pools[pool] {
		return nil
	}

	if _, err := storagezfs.EnsureContainerNamespace(ctx, s.GZFS, pool); err != nil {
		return err
	}
	images, err := s.GZFS.ZFS.Get(ctx, pool+"/"+containerModels.ImagesDataset, false)
	if err != nil {
		return err
	}
	graphRoot, err := storagezfs.FilesystemMountpoint(images)
	if err != nil {
		return err
	}

	poolRun := filepath.Join(runDir, pool)
	containersConf := fmt.Sprintf(`[containers]
log_size_max = 10485760

[network]
network_backend = "cni"
network_config_dir = %q
cni_plugin_dirs = [%q, %q]

[engine]
runtime = "ocijail"
tmp_dir = %q
`, s.path("cni", "net.d"), s.path("cni", "plugins"), cniPlugins, filepath.Join(poolRun, "libpod"))
	storageConf := fmt.Sprintf(`[storage]
driver = "zfs"
graphroot = %q
runroot = %q

[storage.options.zfs]
fsname = %q
`, graphRoot, filepath.Join(poolRun, "runroot"), images.Name)

	files := map[string]string{
		s.path("pools", pool, "containers.conf"): containersConf,
		s.path("pools", pool, "storage.conf"):    storageConf,
	}
	for name, content := range files {
		if err := writeFile(name, content, 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(poolRun, 0o700); err != nil {
		return err
	}
	if s.pools == nil {
		s.pools = map[string]bool{}
	}
	s.pools[pool] = true
	return nil
}

func (s *Service) writeShared(ctx context.Context) error {
	registries := `unqualified-search-registries = ["docker.io"]
short-name-mode = "permissive"
`
	policy := `{"default":[{"type":"insecureAcceptAnything"}]}
`
	bridge := fmt.Sprintf("#!/bin/sh\nPATH=%s:%s exec %s \"$@\"\n", s.path("cni", "shims"), commandPath, filepath.Join(cniPlugins, "bridge"))
	sysctl := "#!/bin/sh\n[ \"$*\" = \"net.link.bridge.pfil_member=1\" ] && exit 0\nexec /sbin/sysctl \"$@\"\n"

	for name, content := range map[string]string{
		s.path("home", ".config", "containers", "registries.conf"): registries,
		s.path("home", ".config", "containers", "policy.json"):     policy,
	} {
		if err := writeFile(name, content, 0o600); err != nil {
			return err
		}
	}
	for name, content := range map[string]string{
		s.path("cni", "plugins", "bridge"): bridge,
		s.path("cni", "shims", "sysctl"):   sysctl,
	} {
		if err := writeFile(name, content, 0o700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(s.path("cni", "net.d"), 0o700); err != nil {
		return err
	}
	return s.writeAuthFile(ctx)
}

func (s *Service) writeAuthFile(ctx context.Context) error {
	var credentials []containerModels.RegistryCredential
	if err := s.DB.WithContext(ctx).Find(&credentials).Error; err != nil {
		return err
	}
	auths := map[string]map[string]string{}
	for _, credential := range credentials {
		token := base64.StdEncoding.EncodeToString([]byte(credential.Username + ":" + credential.Password))
		auths[credential.Registry] = map[string]string{"auth": token}
	}
	data, err := json.Marshal(map[string]any{"auths": auths})
	if err != nil {
		return err
	}
	return writeFile(s.path("auth.json"), string(data), 0o600)
}

func writeFile(name, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	return utils.AtomicWriteFile(name, []byte(content), mode)
}
