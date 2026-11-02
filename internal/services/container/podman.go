// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func (s *Service) podmanJSON(ctx context.Context, pool string, out any, args ...string) error {
	output, err := s.podman(ctx, pool, nil, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(output), out)
}

func (s *Service) ensurePool(ctx context.Context, pool string) error {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()

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
	return os.MkdirAll(poolRun, 0o700)
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
