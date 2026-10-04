// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/iscsi"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

var _ iscsiServiceInterfaces.ISCSIServiceInterface = (*Service)(nil)

type initiatorZPoolChecker interface {
	ActiveISCSIZPool(context.Context) (string, error)
}

type Service struct {
	DB                    *gorm.DB
	initiatorZPoolChecker initiatorZPoolChecker

	mutationMu      sync.Mutex
	runtime         *targetRuntime
	interfaceLookup func(string) (*net.Interface, error)
	portalGroupName func(portalEndpoint) string
	backingStat     func(string) (os.FileInfo, error)
	ipv6Only        func() bool
}

func NewISCSIService(db *gorm.DB) iscsiServiceInterfaces.ISCSIServiceInterface {
	return &Service{DB: db}
}

func (s *Service) SetInitiatorZPoolChecker(checker initiatorZPoolChecker) {
	s.initiatorZPoolChecker = checker
}

func (s *Service) SetBackingStatForTest(stat func(string) (os.FileInfo, error)) func() {
	previous := s.backingStat
	s.backingStat = stat
	return func() { s.backingStat = previous }
}

var errTargetStopped = errors.New("target_service_stopped")
var targetPIDPattern = regexp.MustCompile(`\bpid ([0-9]+)\b`)

type targetRuntime struct {
	run           func(context.Context, string, string, ...string) (string, error)
	configFile    string
	initiatorFile string
	pidFile       string
	deadline      time.Duration
	settle        time.Duration
	interval      time.Duration
}

func (s *Service) runTargetCommand(ctx context.Context, input, command string, args ...string) (string, error) {
	if s.runtime != nil && s.runtime.run != nil {
		return s.runtime.run(ctx, input, command, args...)
	}
	return utils.RunCommandWithInputContext(ctx, input, command, args...)
}

func (s *Service) targetPath() string {
	if s.runtime != nil && s.runtime.configFile != "" {
		return s.runtime.configFile
	}
	return targetConfigPath
}

func (s *Service) initiatorPath() string {
	if s.runtime != nil && s.runtime.initiatorFile != "" {
		return s.runtime.initiatorFile
	}
	return configPath
}

func (s *Service) runInitiatorCommand(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.runTargetCommand(ctx, "", "/usr/bin/iscsictl", append([]string{"-c", s.initiatorPath()}, args...)...)
}

func (s *Service) targetContext() (context.Context, context.CancelFunc) {
	deadline := 10 * time.Second
	if s.runtime != nil && s.runtime.deadline > 0 {
		deadline = s.runtime.deadline
	}
	return context.WithTimeout(context.Background(), deadline)
}

func (s *Service) targetTimings() (time.Duration, time.Duration) {
	settle, interval := time.Second, 250*time.Millisecond
	if s.runtime != nil {
		if s.runtime.settle > 0 {
			settle = s.runtime.settle
		}
		if s.runtime.interval > 0 {
			interval = s.runtime.interval
		}
	}
	return settle, interval
}

func waitTarget(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) targetService(ctx context.Context, action string) (string, error) {
	if s.runtime == nil || s.runtime.pidFile == "" {
		return s.runTargetCommand(ctx, "", "/usr/sbin/service", "ctld", action)
	}
	if action == "onestart" {
		return s.runTargetCommand(ctx, "", "/usr/sbin/ctld", "-f", s.targetPath())
	}
	data, err := os.ReadFile(s.runtime.pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errTargetStopped
		}
		return "", errors.New("failed_to_read_target_pidfile")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return "", errors.New("invalid_target_pidfile")
	}
	comm, err := s.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", errTargetStopped
		}
		return "", errors.New("failed_to_check_target_process")
	}
	if filepath.Base(strings.TrimSpace(comm)) != "ctld" {
		return "", errTargetStopped
	}
	switch action {
	case "onestatus":
		return "ctld is running as pid " + strconv.Itoa(pid) + ".", nil
	case "onereload":
		if err := s.checkFixtureProcess(ctx, pid); err != nil {
			return "", err
		}
		return s.runTargetCommand(ctx, "", "/bin/kill", "-HUP", strconv.Itoa(pid))
	case "onestop":
		if err := s.checkFixtureProcess(ctx, pid); err != nil {
			return "", err
		}
		return s.runTargetCommand(ctx, "", "/bin/kill", "-TERM", strconv.Itoa(pid))
	default:
		return "", errors.New("invalid_target_service_action")
	}
}

func (s *Service) checkFixtureProcess(ctx context.Context, pid int) error {
	out, err := s.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(pid), "-o", "args=")
	if err != nil {
		return errors.New("failed_to_check_target_process")
	}
	args := strings.Fields(out)
	for i := 1; i < len(args); i++ {
		if args[i-1] == "-f" && args[i] == s.targetPath() {
			return nil
		}
	}
	return errors.New("target_pidfile_process_not_owned")
}

func (s *Service) targetPID(ctx context.Context) (int, error) {
	status, err := s.targetService(ctx, "onestatus")
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.Is(err, errTargetStopped) || errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return 0, errTargetStopped
		}
		return 0, errors.New("failed_to_check_target_service")
	}
	match := targetPIDPattern.FindStringSubmatch(status)
	if len(match) != 2 {
		return 0, errors.New("invalid_target_service_status")
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil || pid <= 1 {
		return 0, errors.New("invalid_target_service_status")
	}
	return pid, nil
}

func (s *Service) desiredTargetEnabled() (bool, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return false, err
	}
	return slices.Contains(settings.Services, models.ISCSI), nil
}

func (s *Service) SetEnabled(enabled bool) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return err
	}
	services := make([]models.AvailableService, 0, len(settings.Services)+1)
	for _, service := range settings.Services {
		if service != models.ISCSI {
			services = append(services, service)
		}
	}
	if enabled {
		services = append(services, models.ISCSI)
	}
	if err := s.DB.Model(&settings).Select("Services").Updates(&models.BasicSettings{Services: services}).Error; err != nil {
		return err
	}
	if !enabled {
		ctx, cancel := s.targetContext()
		defer cancel()
		return s.stopTarget(ctx)
	}
	var initiatorErr error
	if err := s.writeConfig(false); err != nil {
		initiatorErr = applyFailed("failed_to_write_iscsi_config", nil)
	}
	ctx, cancel := s.targetContext()
	if _, err := s.runTargetCommand(ctx, "", "/usr/sbin/service", "iscsid", "onestatus"); err != nil {
		if _, err = s.runTargetCommand(ctx, "", "/usr/sbin/service", "iscsid", "onestart"); err != nil {
			initiatorErr = applyFailed("failed_to_start_initiator_service", nil)
		}
	}
	cancel()
	targetErr := s.writeTargetConfig(true)
	if initiatorErr == nil {
		if _, err := s.runInitiatorCommand("-Aa"); err != nil {
			initiatorErr = applyFailed("failed_to_add_iscsi_sessions", nil)
		}
	}
	if targetErr != nil {
		return targetErr
	}
	return initiatorErr
}

func (s *Service) stopTarget(ctx context.Context) error {
	pid, err := s.targetPID(ctx)
	if err != nil && !errors.Is(err, errTargetStopped) {
		return applyFailed("failed_to_check_target_service", nil)
	}
	if pid != 0 {
		if _, err := s.targetService(ctx, "onestop"); err != nil {
			return applyFailed("failed_to_stop_target_service", nil)
		}
	}
	_, interval := s.targetTimings()
	for {
		_, statusErr := s.targetPID(ctx)
		processes, processErr := s.runTargetCommand(ctx, "", "/bin/pgrep", "-x", "ctld")
		listeners, listenErr := s.targetListeners(ctx, 0)
		ports, portErr := s.readCTLPorts(ctx)
		stopped := errors.Is(statusErr, errTargetStopped) && strings.TrimSpace(processes) == "" && listenErr == nil && len(listeners) == 0 && portErr == nil
		if processErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(processErr, &exitErr) || exitErr.ExitCode() != 1 {
				stopped = false
			}
		}
		if stopped {
			for _, port := range ports.Ports {
				if (port.Group != "" || port.TransportGroup != "") && port.Online != "NO" {
					stopped = false
					break
				}
			}
		}
		if stopped {
			return nil
		}
		if waitTarget(ctx, interval) != nil {
			return applyFailed("target_stop_pending", nil)
		}
	}
}
