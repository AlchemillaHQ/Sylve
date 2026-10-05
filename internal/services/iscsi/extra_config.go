// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func (s *Service) targetSettings() (*iscsiModels.ISCSISettings, error) {
	settings := iscsiModels.ISCSISettings{ID: 1}
	if err := s.DB.Where("id = ?", 1).FirstOrCreate(&settings).Error; err != nil {
		return nil, errors.New("failed_to_read_extra_config")
	}
	return &settings, nil
}

func (s *Service) mergeTargetConfig(ctx context.Context, managed, extra string) (string, error) {
	state, err := s.inspectManagedTargetConfig(managed)
	if err != nil {
		return "", errors.New("invalid_managed_target_configuration")
	}
	_, rendered, err := s.parseExtraTargetConfig(ctx, extra, state)
	if err != nil {
		return "", err
	}
	if extra == "" {
		return managed, nil
	}
	return strings.TrimRight(managed, "\n") + "\n\n" + extraConfigBanner + "\n" + rendered + "\n", nil
}

func (s *Service) generateTargetConfigContext(ctx context.Context) (string, error) {
	managed, err := s.generateManagedTargetConfigContext(ctx)
	if err != nil {
		return "", err
	}
	settings, err := s.targetSettings()
	if err != nil {
		return "", err
	}
	return s.mergeTargetConfig(ctx, managed, settings.ExtraTargetConfig)
}

func (s *Service) preflightStoredExtra(candidate *iscsiModels.ISCSITarget, portal *iscsiModels.ISCSITargetPortal) error {
	settings, err := s.targetSettings()
	if err != nil {
		return err
	}
	if settings.ExtraTargetConfig == "" {
		return nil
	}
	var targets []iscsiModels.ISCSITarget
	if err := s.DB.Preload("Portals").Preload("LUNs").Order("id").Find(&targets).Error; err != nil {
		return errors.New("failed_to_read_targets")
	}
	ctx, cancel := s.targetContext()
	defer cancel()
	if candidate != nil {
		found := false
		for i := range targets {
			if candidate.ID != 0 && targets[i].ID == candidate.ID {
				replacement := *candidate
				replacement.Portals, replacement.LUNs = targets[i].Portals, targets[i].LUNs
				targets[i], found = replacement, true
			}
		}
		if !found {
			targets = append(targets, *candidate)
		}
	}
	if portal != nil {
		for i := range targets {
			if targets[i].ID == portal.TargetID {
				targets[i].Portals = append(targets[i].Portals, *portal)
			}
		}
	}
	managed, err := s.renderManagedTargetConfigContext(ctx, targets)
	if err != nil {
		return resourceConflict("invalid_stored_target_configuration", nil)
	}
	text, err := s.mergeTargetConfig(ctx, managed, settings.ExtraTargetConfig)
	if err != nil {
		return resourceConflict("stored_extra_config_conflict", nil)
	}
	state, err := s.prepareTargetConfig(ctx, text)
	if err != nil {
		return resourceConflict("invalid_stored_extra_config", nil)
	}
	if err := s.preflightListenerTransition(state.activeEndpoints()); err != nil {
		return resourceConflict("iscsi_listener_normalization_requires_stop", nil)
	}
	return nil
}

func (s *Service) statExtraBacking(path string) (os.FileInfo, error) {
	stat := s.backingStat
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(path)
	if err != nil || info == nil || !(info.Mode().IsRegular() || info.Mode()&os.ModeDevice != 0) {
		return nil, errors.New("extra_config_invalid_backing")
	}
	return info, nil
}

func (s *Service) prepareTargetLUNs(ctx context.Context, state *targetConfiguration) error {
	prepared := make(map[string]targetLUN)
	for _, lun := range state.configuredLUNs() {
		if lun.backend == "" {
			lun.backend = "block"
		}
		if lun.backend == "block" {
			var err error
			if lun.line == 0 {
				lun.backing, err = s.statBacking(lun.path)
			} else {
				lun.backing, err = s.statExtraBacking(lun.path)
			}
			if err != nil {
				if lun.line != 0 {
					return invalidExtraConfig("extra_config_invalid_backing", lun.line)
				}
				return err
			}
			if lun.size == 0 {
				if lun.backing.Mode().IsRegular() {
					if lun.backing.Size() <= 0 {
						return invalidExtraConfig("extra_config_invalid_backing", lun.line)
					}
					lun.size = uint64(lun.backing.Size())
				} else {
					out, err := s.runTargetCommand(ctx, "", "/usr/sbin/diskinfo", lun.path)
					fields := strings.Fields(out)
					if err != nil || len(fields) < 3 {
						return errors.New("failed_to_check_backing_geometry")
					}
					lun.size, err = strconv.ParseUint(fields[2], 10, 64)
					if err != nil || lun.size == 0 {
						return errors.New("invalid_backing_geometry")
					}
				}
			}
		}
		if lun.blocksize == 0 || lun.size == 0 || lun.size%lun.blocksize != 0 {
			return errors.New("invalid_backing_geometry")
		}
		prepared[lun.name] = lun
	}
	for i := range state.luns {
		state.luns[i] = prepared[state.luns[i].name]
	}
	for i := range state.targets {
		for j := range state.targets[i].luns {
			lun := &state.targets[i].luns[j]
			number := lun.number
			*lun = prepared[lun.name]
			lun.number = number
		}
	}
	return nil
}

func readOwnedTargetFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("target_config_not_available")
	}
	defer file.Close()
	if err := privateTargetFile(file); err != nil {
		return nil, errors.New("target_config_not_private")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4*MaxRequestBodyBytes+1))
	if err != nil || len(data) > int(4*MaxRequestBodyBytes) || !strings.HasPrefix(string(data), configMarker+"\n") {
		return nil, errors.New("target_config_not_owned")
	}
	return data, nil
}

func (s *Service) checkTargetStopped(ctx context.Context) error {
	_, statusErr := s.targetPID(ctx)
	if !errors.Is(statusErr, errTargetStopped) {
		return errors.New("target_stop_pending")
	}
	out, err := s.runTargetCommand(ctx, "", "/bin/pgrep", "-x", "ctld")
	var exitErr *exec.ExitError
	if strings.TrimSpace(out) != "" || err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
		return errors.New("target_stop_pending")
	}
	listeners, err := s.targetListeners(ctx, 0)
	if err != nil || len(listeners) != 0 {
		return errors.New("target_stop_pending")
	}
	ports, err := s.readCTLPorts(ctx)
	if err != nil {
		return errors.New("target_stop_pending")
	}
	for _, port := range ports.Ports {
		if (port.Group != "" || port.TransportGroup != "") && port.Online != "NO" {
			return errors.New("target_stop_pending")
		}
	}
	return nil
}

func configResult(extra, status string, err error) *iscsiModels.ISCSIConfig {
	result := &iscsiModels.ISCSIConfig{ExtraTargetConfig: extra, ApplyStatus: status}
	if err != nil {
		reason := "target_runtime_checks_pending"
		var detail *ExtraConfigError
		if errors.As(err, &detail) {
			reason, result.LineNumber = detail.ReasonCode, detail.LineNumber
		} else if status == "invalid" {
			reason = "invalid_target_configuration"
		}
		result.ReasonCode = &reason
	}
	return result
}

func (s *Service) GetConfig() (*iscsiModels.ISCSIConfig, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return s.getConfig()
}

func (s *Service) getConfig() (*iscsiModels.ISCSIConfig, error) {
	settings, err := s.targetSettings()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.targetContext()
	defer cancel()
	managed, err := s.generateManagedTargetConfigContext(ctx)
	if err != nil {
		return configResult(settings.ExtraTargetConfig, "invalid", err), nil
	}
	text, err := s.mergeTargetConfig(ctx, managed, settings.ExtraTargetConfig)
	if err != nil {
		return configResult(settings.ExtraTargetConfig, "invalid", err), nil
	}
	state, err := s.prepareTargetConfig(ctx, text)
	if err != nil {
		return configResult(settings.ExtraTargetConfig, "invalid", err), nil
	}
	data, err := readOwnedTargetFile(s.targetPath())
	if err != nil || string(data) != text {
		return configResult(settings.ExtraTargetConfig, "pending", errors.New("target_config_mismatch")), nil
	}
	enabled, err := s.desiredTargetEnabled()
	if err != nil {
		return nil, errors.New("failed_to_read_target_service_state")
	}
	status := "checked"
	if enabled {
		_, err = s.checkTargetRuntime(ctx, state)
	} else {
		status = "disabled"
		err = s.checkTargetStopped(ctx)
	}
	if err != nil {
		return configResult(settings.ExtraTargetConfig, "pending", err), nil
	}
	return configResult(settings.ExtraTargetConfig, status, nil), nil
}

func (s *Service) SetExtraTargetConfig(extra *string) (*iscsiModels.ISCSIConfig, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if extra == nil {
		return s.getConfig()
	}
	ctx, cancel := s.targetContext()
	defer cancel()
	extraState, _, err := s.parseExtraTargetConfig(ctx, *extra, nil)
	if err != nil {
		return nil, err
	}
	if err := s.prepareTargetLUNs(ctx, extraState); err != nil {
		if errors.Is(err, ErrInvalidExtraConfig) {
			return nil, err
		}
		return nil, invalidExtraConfig("extra_config_invalid_backing", 0)
	}
	managed, err := s.generateManagedTargetConfigContext(ctx)
	if err != nil {
		return nil, resourceConflict("invalid_managed_target_configuration", nil)
	}
	if _, err := s.prepareTargetConfig(ctx, managed); err != nil {
		return nil, resourceConflict("invalid_managed_target_configuration", nil)
	}
	text, err := s.mergeTargetConfig(ctx, managed, *extra)
	if err != nil {
		return nil, err
	}
	state, err := s.prepareTargetConfig(ctx, text)
	if err != nil {
		if errors.Is(err, ErrInvalidExtraConfig) {
			return nil, err
		}
		return nil, invalidExtraConfig("extra_config_native_validation_failed", 0)
	}
	if err := s.preflightListenerTransition(state.activeEndpoints()); err != nil {
		return nil, resourceConflict("iscsi_listener_normalization_requires_stop", nil)
	}
	if _, err := s.targetSettings(); err != nil {
		return nil, err
	}
	if err := s.DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).
		Model(&iscsiModels.ISCSISettings{}).Where("id = ?", 1).Update("extra_target_config", *extra).Error; err != nil {
		return nil, errors.New("failed_to_save_extra_config")
	}
	if err := s.writePreparedTargets(ctx, text, state, true); err != nil {
		return configResult(*extra, "pending", err), applyFailed("iscsi_configuration_saved_apply_pending", nil)
	}
	enabled, err := s.desiredTargetEnabled()
	if err != nil {
		return configResult(*extra, "pending", err), applyFailed("iscsi_configuration_saved_apply_pending", nil)
	}
	status := "checked"
	if !enabled {
		status = "disabled"
	}
	return configResult(*extra, status, nil), nil
}
