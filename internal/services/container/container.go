// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var _ containerServiceInterfaces.ContainerServiceInterface = (*Service)(nil)

var ErrNotFound = errors.New("container_not_found")

type Service struct {
	DB      *gorm.DB
	GZFS    *gzfs.Client
	DataDir string

	PrepareHost   func(context.Context) error
	ApplyFirewall func() error

	identity         clusterServiceInterfaces.GuestIdentityCoordinator
	leftPanelRefresh func(reason string)
	changedMu        sync.Mutex
	changing         map[uint]bool
	poolMu           sync.Mutex
	pools            map[string]bool
	serviceMu        sync.Mutex
	services         map[string]*podmanService
	stateMu          sync.Mutex
	states           map[string]containerServiceInterfaces.State
	monitorOnce      sync.Once
}

func NewContainerService(db *gorm.DB, gzfs *gzfs.Client) (*Service, error) {
	dataPath, err := config.GetDataPath()
	if err != nil {
		return nil, err
	}
	return &Service{DB: db, GZFS: gzfs, DataDir: dataPath + "/containers"}, nil
}

func (s *Service) SetGuestIdentityCoordinator(coordinator clusterServiceInterfaces.GuestIdentityCoordinator) {
	s.identity = coordinator
}

func (s *Service) SetLeftPanelRefreshEmitter(emit func(reason string)) {
	s.leftPanelRefresh = emit
}

// changed refreshes the UI on every node for a container, at most once a
// second: an action and the podman events it causes would otherwise each
// refresh it.
func (s *Service) changed(ocid uint) {
	if s.leftPanelRefresh == nil {
		return
	}
	s.changedMu.Lock()
	defer s.changedMu.Unlock()
	if s.changing[ocid] {
		return
	}
	if s.changing == nil {
		s.changing = map[uint]bool{}
	}
	s.changing[ocid] = true
	time.AfterFunc(time.Second, func() {
		s.changedMu.Lock()
		delete(s.changing, ocid)
		s.changedMu.Unlock()
		s.leftPanelRefresh(fmt.Sprintf("container_%d", ocid))
	})
}

func (s *Service) enabled(ctx context.Context) (bool, error) {
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).Limit(1).Find(&basic).Error; err != nil {
		return false, err
	}
	return slices.Contains(basic.Services, models.Containers), nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	for _, binary := range []string{podmanBinary, "/usr/local/bin/ocijail", "/usr/local/bin/conmon", cniPlugins + "/bridge", cniPlugins + "/dnsname"} {
		if _, err := os.Stat(binary); err != nil {
			return fmt.Errorf("podman_not_installed: missing %s (pkg install podman cni-dnsname)", binary)
		}
	}
	if s.PrepareHost != nil {
		if err := s.PrepareHost(ctx); err != nil {
			return err
		}
	}
	if err := s.writeShared(ctx); err != nil {
		return err
	}
	var networks []containerModels.Network
	if err := s.DB.WithContext(ctx).Find(&networks).Error; err != nil {
		return err
	}
	for _, network := range networks {
		if err := s.writeConflist(network); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Startup(ctx context.Context) error {
	s.StartStatsMonitoring(ctx)
	go s.watchEvents(ctx)
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		return err
	}
	if err := s.Reconcile(ctx); err != nil {
		return err
	}
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
		return err
	}
	for _, pool := range basic.Pools {
		if downloads, err := s.downloads(ctx, pool); err == nil {
			// podman needs the downloads folder itself for every exec, not only pulls.
			stale, _ := filepath.Glob(filepath.Join(filepath.Dir(downloads), ".pull-*"))
			left, _ := filepath.Glob(filepath.Join(downloads, "*"))
			for _, dir := range append(stale, left...) {
				if dir != downloads {
					_ = os.RemoveAll(dir)
				}
			}
		}
	}
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Find(&records).Error; err != nil {
		return err
	}
	interrupted := map[string]bool{}
	for _, record := range records {
		if !record.CreationPending {
			if err := applyLimits(ctx, record); err != nil {
				logger.L.Warn().Err(err).Uint("oc_id", record.OCID).Msg("container_limits_not_applied")
			}
			s.endConsole(ctx, record, "")
			continue
		}
		_ = s.remove(ctx, record)
		if err := s.DB.WithContext(ctx).Delete(&record).Error; err != nil {
			return err
		}
		logger.L.Warn().Uint("oc_id", record.OCID).Msg("removed_interrupted_container_creation")
		if record.Stack != "" {
			interrupted[record.Stack] = true
		}
	}
	for stack := range interrupted {
		var members []containerModels.Container
		if err := s.DB.WithContext(ctx).Where("stack = ?", stack).Find(&members).Error; err != nil {
			return err
		}
		for _, member := range members {
			if err := s.Delete(ctx, member.OCID, false); err != nil {
				logger.L.Warn().Err(err).Uint("oc_id", member.OCID).Msg("interrupted_container_stack_not_removed")
			}
		}
		if err := s.RemoveEmptyStack(ctx, stack); err != nil {
			logger.L.Warn().Err(err).Str("stack", stack).Msg("container_stack_network_not_removed")
		}
		logger.L.Warn().Str("stack", stack).Msg("removed_interrupted_container_stack")
	}
	return nil
}

func (s *Service) record(ctx context.Context, ocid uint) (containerModels.Container, error) {
	var record containerModels.Container
	err := s.DB.WithContext(ctx).Preload("Network").Where("oc_id = ?", ocid).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return record, ErrNotFound
	}
	return record, err
}

func mainPID(record containerModels.Container) int {
	data, err := os.ReadFile(filepath.Join(runDir, record.Pool, "runroot", "zfs-containers", record.RuntimeID, "userdata", "pidfile"))
	if err != nil {
		return 0
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 && syscall.Kill(pid, 0) == nil {
		return pid
	}
	return 0
}

func (s *Service) List(ctx context.Context) ([]containerServiceInterfaces.ListItem, error) {
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Preload("Network").Order("oc_id").Find(&records).Error; err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(ctx, "/usr/sbin/jls", "name").Output()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_jails: %w", err)
	}
	jails := strings.Fields(string(output))
	items := make([]containerServiceInterfaces.ListItem, len(records))
	for i, record := range records {
		state := "stopped"
		switch {
		case record.CreationPending:
			state = "creating"
		case record.RuntimeID != "" && slices.Contains(jails, record.RuntimeID) && mainPID(record) > 0:
			state = "running"
		}
		items[i] = containerServiceInterfaces.ListItem{Container: record, State: state}
	}
	return items, nil
}

type podmanInspect struct {
	State struct {
		Status     string    `json:"Status"`
		ExitCode   int       `json:"ExitCode"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
	} `json:"State"`
	Image           string `json:"Image"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress  string `json:"IPAddress"`
			MacAddress string `json:"MacAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

func (s *Service) inspect(ctx context.Context, record containerModels.Container) (podmanInspect, error) {
	var inspected podmanInspect
	err := s.api(ctx, record.Pool, "container", http.MethodGet, "/containers/"+record.RuntimeName()+"/json", nil, &inspected)
	return inspected, err
}

func (s *Service) runtimeState(ctx context.Context, record containerModels.Container) (*containerServiceInterfaces.State, error) {
	info, err := s.inspect(ctx, record)
	if err != nil {
		return nil, err
	}
	state := &containerServiceInterfaces.State{State: info.State.Status, ExitCode: info.State.ExitCode,
		StartedAt: info.State.StartedAt, FinishedAt: info.State.FinishedAt, IPs: []string{}, MACs: []string{}}
	for _, network := range info.NetworkSettings.Networks {
		if network.IPAddress != "" {
			state.IPs = append(state.IPs, network.IPAddress)
		}
		if network.MacAddress != "" {
			state.MACs = append(state.MACs, network.MacAddress)
		}
	}
	return state, nil
}

// stateWait is how long Get waits for podman, which can't inspect a container
// while a pull writes a layer to the pool's storage, for minutes on big images.
const stateWait = 2 * time.Second

// currentState inspects the container, or when podman takes longer than
// stateWait, answers with its last inspected state if its jail still agrees
// whether it runs, and with only that otherwise.
func (s *Service) currentState(ctx context.Context, record containerModels.Container) (*containerServiceInterfaces.State, error) {
	inspectCtx, cancel := context.WithTimeout(ctx, stateWait)
	defer cancel()
	state, err := s.runtimeState(inspectCtx, record)
	if err == nil {
		s.stateMu.Lock()
		if s.states == nil {
			s.states = map[string]containerServiceInterfaces.State{}
		}
		s.states[record.RuntimeID] = *state
		s.stateMu.Unlock()
		return state, nil
	}
	if ctx.Err() != nil || inspectCtx.Err() == nil {
		return nil, err
	}

	running := exec.CommandContext(ctx, "/usr/sbin/jls", "-j", record.RuntimeID, "jid").Run() == nil && mainPID(record) > 0
	s.stateMu.Lock()
	last, ok := s.states[record.RuntimeID]
	s.stateMu.Unlock()
	if ok && (last.State == "running") == running {
		return &last, nil
	}
	status := "stopped"
	if running {
		status = "running"
	}
	return &containerServiceInterfaces.State{State: status, IPs: []string{}, MACs: []string{}}, nil
}

func (s *Service) ImageInfo(ctx context.Context, ocid uint) (containerServiceInterfaces.ImageInfo, error) {
	var info containerServiceInterfaces.ImageInfo
	record, err := s.record(ctx, ocid)
	if err != nil {
		return info, err
	}
	container, err := s.inspect(ctx, record)
	if err != nil {
		return info, err
	}
	var image struct {
		ID     string            `json:"Id"`
		Digest string            `json:"Digest"`
		Size   int64             `json:"Size"`
		Labels map[string]string `json:"Labels"`
		Config struct {
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
			Volumes      map[string]struct{} `json:"Volumes"`
		} `json:"Config"`
	}
	if err := s.api(ctx, record.Pool, "image", http.MethodGet, "/images/"+container.Image+"/json", nil, &image); err != nil {
		return info, err
	}
	info = containerServiceInterfaces.ImageInfo{ID: image.ID, Digest: image.Digest, Size: image.Size,
		Title: image.Labels["org.opencontainers.image.title"], Description: image.Labels["org.opencontainers.image.description"],
		Version: image.Labels["org.opencontainers.image.version"], URL: image.Labels["org.opencontainers.image.url"],
		ExposedPorts: slices.Sorted(maps.Keys(image.Config.ExposedPorts)), Volumes: slices.Sorted(maps.Keys(image.Config.Volumes))}
	return info, nil
}

func (s *Service) Get(ctx context.Context, ocid uint) (containerServiceInterfaces.Detail, error) {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return containerServiceInterfaces.Detail{}, err
	}
	detail := containerServiceInterfaces.Detail{Container: record}
	if !record.CreationPending {
		if detail.Runtime, err = s.currentState(ctx, record); err != nil && ctx.Err() == nil {
			logger.L.Debug().Err(err).Uint("oc_id", ocid).Msg("container_state_unavailable")
		}
	}
	if detail.Runtime != nil && detail.Runtime.State == "running" {
		detail.CPUUsage, detail.MemoryUsage, _ = usage(ctx, record)
	}
	return detail, nil
}

func (s *Service) Logs(ctx context.Context, ocid uint, tail uint) (string, error) {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return "", err
	}
	response, err := s.request(ctx, record.Pool, "logs", http.MethodGet,
		fmt.Sprintf("/containers/%s/logs?stdout=true&stderr=true&tail=%d", record.RuntimeName(), tail), nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	// Each frame is an 8-byte header ending in the frame's length, then the output.
	var output strings.Builder
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(response.Body, header); err == io.EOF {
			return output.String(), nil
		} else if err != nil {
			return "", fmt.Errorf("podman_logs_failed: %w", err)
		}
		if _, err := io.CopyN(&output, response.Body, int64(binary.BigEndian.Uint32(header[4:]))); err != nil {
			return "", fmt.Errorf("podman_logs_failed: %w", err)
		}
	}
}

func (s *Service) Command(ctx context.Context, ocid uint, args ...string) (*exec.Cmd, error) {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return nil, err
	}
	if record.CreationPending {
		return nil, fmt.Errorf("container_creation_pending")
	}
	args = slices.Clone(args)
	for i, arg := range args {
		if arg == "{}" {
			args[i] = record.RuntimeName()
		}
	}
	cmd := exec.Command(podmanBinary, args...)
	cmd.Env = append(s.podmanEnv(record.Pool), "TERM=xterm")
	return cmd, nil
}

const consoleEnv = "SYLVE_CONSOLE"

// Console starts shell in the container through an exec session, its
// environment marked with a session that endConsole looks for.
func (s *Service) Console(ctx context.Context, ocid uint, shell string, rows, cols uint16) (containerServiceInterfaces.Console, error) {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return nil, err
	}
	if record.CreationPending {
		return nil, fmt.Errorf("container_creation_pending")
	}
	session := rand.Text()
	var created struct {
		ID string `json:"Id"`
	}
	if err := s.api(ctx, record.Pool, "exec", http.MethodPost, "/containers/"+record.RuntimeName()+"/exec", map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
		"Cmd": []string{shell}, "Env": []string{"TERM=xterm", consoleEnv + "=" + session},
	}, &created); err != nil {
		return nil, err
	}
	terminal, err := s.upgrade(ctx, record.Pool, "exec", "/exec/"+created.ID+"/start", map[string]any{"Tty": true, "h": rows, "w": cols})
	if err != nil {
		return nil, err
	}
	return &console{ReadWriteCloser: terminal, service: s, record: record, exec: created.ID, session: session}, nil
}

type console struct {
	io.ReadWriteCloser
	service *Service
	record  containerModels.Container
	exec    string
	session string
}

func (c *console) Resize(rows, cols uint16) error {
	return c.service.api(context.Background(), c.record.Pool, "exec", http.MethodPost, fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", c.exec, rows, cols), nil, nil)
}

// Close ends the shell and what it started too, which keep running when
// their terminal goes away.
func (c *console) Close() error {
	err := c.ReadWriteCloser.Close()
	c.service.endConsole(context.Background(), c.record, c.session)
	return err
}

func (s *Service) endConsole(ctx context.Context, record containerModels.Container, session string) {
	if record.RuntimeID == "" {
		return
	}
	output, err := exec.CommandContext(ctx, "/bin/ps", "-e", "-J", record.RuntimeID, "-o", "pid=", "-o", "command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		marked := slices.ContainsFunc(fields, func(field string) bool {
			value, ok := strings.CutPrefix(field, consoleEnv+"=")
			return ok && (session == "" || value == session)
		})
		if !marked {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (s *Service) UpdateOptions(ctx context.Context, ocid uint, req containerServiceInterfaces.OptionsRequest) error {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	updates := map[string]any{}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if utf8.RuneCountInString(title) > 63 {
			return fmt.Errorf("invalid_container_title")
		}
		updates["title"] = title
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Cores != nil || req.Memory != nil {
		if req.Cores != nil {
			record.Cores = *req.Cores
		}
		if req.Memory != nil {
			record.Memory = *req.Memory
		}
		if err := validateLimits(record.Cores, record.Memory); err != nil {
			return err
		}
		if err := applyLimits(ctx, record); err != nil {
			return err
		}
		updates["cores"], updates["memory"] = record.Cores, record.Memory
	}
	if req.StartAtBoot != nil {
		updates["start_at_boot"] = *req.StartAtBoot
	}
	if req.StartOrder != nil {
		updates["start_order"] = *req.StartOrder
	}
	if len(updates) == 0 {
		return nil
	}
	if err := s.DB.WithContext(ctx).Model(&record).Updates(updates).Error; err != nil {
		return err
	}
	s.changed(ocid)
	return nil
}

func (s *Service) StartAtBootOCIDs(ctx context.Context) ([]uint, error) {
	var ids []uint
	err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).
		Where("start_at_boot = ? AND creation_pending = ?", true, false).
		Order("start_order ASC, oc_id ASC").Pluck("oc_id", &ids).Error
	return ids, err
}

func (s *Service) reserveIdentity(ctx context.Context, ocid uint) (*clusterServiceInterfaces.GuestIdentityReservation, error) {
	if s.identity == nil {
		return nil, nil
	}
	reservation, err := s.identity.ReserveGuestIdentities(ctx, clusterModels.GuestIdentityKindContainer, []uint{ocid})
	if err != nil {
		return nil, err
	}
	return &reservation, nil
}
