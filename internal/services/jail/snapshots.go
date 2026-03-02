// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var invalidSnapshotNameChars = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)

func (s *Service) ListJailSnapshots(ctID uint) ([]jailModels.JailSnapshot, error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}

	var jail jailModels.Jail
	if err := s.DB.Select("id").Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_jail: %w", err)
	}

	var snapshots []jailModels.JailSnapshot
	if err := s.DB.
		Where("jid = ?", jail.ID).
		Order("created_at ASC, id ASC").
		Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_jail_snapshots: %w", err)
	}

	return snapshots, nil
}

func (s *Service) CreateJailSnapshot(
	ctx context.Context,
	ctID uint,
	name string,
	description string,
) (*jailModels.JailSnapshot, error) {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}

	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return nil, fmt.Errorf("snapshot_name_required")
	}
	if len(name) > 128 {
		return nil, fmt.Errorf("snapshot_name_too_long")
	}
	if len(description) > 4096 {
		return nil, fmt.Errorf("snapshot_description_too_long")
	}

	jail, err := s.GetJailByCTID(ctID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_jail: %w", err)
	}

	rootDataset, mountPoint, err := resolveJailRootDataset(jail)
	if err != nil {
		return nil, err
	}

	if err := s.WriteJailJSON(ctID); err != nil {
		return nil, fmt.Errorf("failed_to_write_jail_json_before_snapshot: %w", err)
	}

	if err := s.stageHostConfigIntoDataset(ctID, mountPoint); err != nil {
		return nil, fmt.Errorf("failed_to_stage_jail_host_config: %w", err)
	}

	rootFS, err := s.GZFS.ZFS.Get(ctx, rootDataset, false)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_jail_root_dataset: %w", err)
	}

	snapToken := sanitizeSnapshotToken(name)
	snapshotName := fmt.Sprintf("sjs_%s_%d", snapToken, time.Now().UTC().UnixMilli())

	createdSnapshot, err := rootFS.Snapshot(ctx, snapshotName, true)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_jail_snapshot: %w", err)
	}

	if createdSnapshot == nil {
		return nil, fmt.Errorf("snapshot_creation_returned_nil")
	}

	var latest jailModels.JailSnapshot
	var parentID *uint
	if err := s.DB.
		Where("jid = ?", jail.ID).
		Order("created_at DESC, id DESC").
		First(&latest).Error; err == nil {
		parentID = &latest.ID
	}

	record := jailModels.JailSnapshot{
		JailID:           jail.ID,
		CTID:             jail.CTID,
		ParentSnapshotID: parentID,
		Name:             name,
		Description:      description,
		SnapshotName:     snapshotName,
		RootDataset:      rootDataset,
	}

	if err := s.DB.Create(&record).Error; err != nil {
		return nil, fmt.Errorf("failed_to_record_jail_snapshot: %w", err)
	}

	if err := s.WriteJailJSON(ctID); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("ctid", ctID).
			Msg("failed_to_refresh_jail_json_after_snapshot_create")
	}

	return &record, nil
}

func (s *Service) RollbackJailSnapshot(
	ctx context.Context,
	ctID uint,
	snapshotID uint,
	destroyMoreRecent bool,
) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if ctID == 0 || snapshotID == 0 {
		return fmt.Errorf("invalid_request")
	}

	var record jailModels.JailSnapshot
	if err := s.DB.
		Where("ct_id = ? AND id = ?", ctID, snapshotID).
		First(&record).Error; err != nil {
		return fmt.Errorf("snapshot_not_found: %w", err)
	}

	wasActive, err := s.IsJailActive(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_state: %w", err)
	}

	if wasActive {
		if err := s.JailAction(int(ctID), "stop"); err != nil {
			return fmt.Errorf("failed_to_stop_jail_before_snapshot_rollback: %w", err)
		}
		if err := s.waitForJailActiveState(ctID, false, 30*time.Second); err != nil {
			return err
		}
	}

	startAfter := wasActive
	rollbackSucceeded := false
	defer func() {
		if !startAfter {
			return
		}
		if !rollbackSucceeded {
			logger.L.Warn().
				Uint("ctid", ctID).
				Msg("skipping_jail_restart_after_snapshot_rollback_due_to_failure")
			return
		}
		if err := s.JailAction(int(ctID), "start"); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("ctid", ctID).
				Msg("failed_to_start_jail_after_snapshot_rollback")
			return
		}
		if err := s.waitForJailActiveState(ctID, true, 45*time.Second); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("ctid", ctID).
				Msg("jail_did_not_reach_active_state_after_snapshot_rollback")
		}
	}()

	fullSnapshot := fmt.Sprintf("%s@%s", record.RootDataset, record.SnapshotName)
	snapshotDataset, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
	if err != nil {
		return fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
	}

	if err := snapshotDataset.Rollback(ctx, destroyMoreRecent); err != nil {
		return fmt.Errorf("failed_to_rollback_snapshot: %w", err)
	}

	mountPoint := "/" + strings.TrimLeft(record.RootDataset, "/")
	if err := s.restoreHostConfigFromDataset(ctID, mountPoint); err != nil {
		return fmt.Errorf("failed_to_restore_jail_host_config: %w", err)
	}

	if err := s.restoreJailDatabaseFromSnapshotJSON(ctID, mountPoint); err != nil {
		return fmt.Errorf("failed_to_restore_jail_config_from_snapshot: %w", err)
	}

	if err := s.DB.
		Where(
			"jid = ? AND (created_at > ? OR (created_at = ? AND id > ?))",
			record.JailID,
			record.CreatedAt,
			record.CreatedAt,
			record.ID,
		).
		Delete(&jailModels.JailSnapshot{}).Error; err != nil {
		return fmt.Errorf("failed_to_prune_newer_snapshot_records: %w", err)
	}

	if err := s.WriteJailJSON(ctID); err != nil {
		return fmt.Errorf("failed_to_refresh_jail_json_after_rollback: %w", err)
	}

	rollbackSucceeded = true
	return nil
}

func (s *Service) waitForJailActiveState(ctID uint, shouldBeActive bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		active, err := s.IsJailActive(ctID)
		if err == nil && active == shouldBeActive {
			return nil
		}

		if time.Now().After(deadline) {
			target := "inactive"
			if shouldBeActive {
				target = "active"
			}
			if err != nil {
				return fmt.Errorf("jail_failed_to_reach_%s_state: %w", target, err)
			}
			return fmt.Errorf("jail_failed_to_reach_%s_state", target)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (s *Service) DeleteJailSnapshot(ctx context.Context, ctID uint, snapshotID uint) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if ctID == 0 || snapshotID == 0 {
		return fmt.Errorf("invalid_request")
	}

	var record jailModels.JailSnapshot
	if err := s.DB.
		Where("ct_id = ? AND id = ?", ctID, snapshotID).
		First(&record).Error; err != nil {
		return fmt.Errorf("snapshot_not_found: %w", err)
	}

	fullSnapshot := fmt.Sprintf("%s@%s", record.RootDataset, record.SnapshotName)
	ds, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
	if err != nil {
		if !isDatasetNotFoundError(err) {
			return fmt.Errorf("failed_to_get_snapshot_for_deletion: %w", err)
		}
	} else {
		if err := ds.Destroy(ctx, false, false); err != nil {
			return fmt.Errorf("failed_to_delete_snapshot_dataset: %w", err)
		}
	}

	if err := s.DB.Delete(&record).Error; err != nil {
		return fmt.Errorf("failed_to_delete_snapshot_record: %w", err)
	}

	if err := s.WriteJailJSON(ctID); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("ctid", ctID).
			Msg("failed_to_refresh_jail_json_after_snapshot_delete")
	}

	return nil
}

func (s *Service) restoreJailDatabaseFromSnapshotJSON(ctID uint, mountPoint string) error {
	metaPath := filepath.Join(mountPoint, ".sylve", "jail.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("failed_to_read_snapshot_jail_json: %w", err)
	}

	var restored jailModels.Jail
	if err := json.Unmarshal(data, &restored); err != nil {
		return fmt.Errorf("invalid_snapshot_jail_json: %w", err)
	}

	normalizedNetworks, networkWarnings, err := s.normalizeRestoredJailSnapshotNetworks(restored.Networks)
	if err != nil {
		return err
	}
	for _, warning := range networkWarnings {
		logger.L.Warn().
			Uint("ctid", ctID).
			Str("warning", warning).
			Msg("jail_snapshot_restore_network_warning")
	}
	restored.Networks = normalizedNetworks

	current, err := s.GetJailByCTID(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_current_jail: %w", err)
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_start_transaction: %w", tx.Error)
	}

	jailUpdate := jailModels.Jail{
		Name:              restored.Name,
		Hostname:          restored.Hostname,
		Description:       restored.Description,
		Type:              restored.Type,
		StartAtBoot:       restored.StartAtBoot,
		StartOrder:        restored.StartOrder,
		InheritIPv4:       restored.InheritIPv4,
		InheritIPv6:       restored.InheritIPv6,
		ResourceLimits:    restored.ResourceLimits,
		Cores:             restored.Cores,
		CPUSet:            restored.CPUSet,
		Memory:            restored.Memory,
		DevFSRuleset:      restored.DevFSRuleset,
		Fstab:             restored.Fstab,
		CleanEnvironment:  restored.CleanEnvironment,
		AdditionalOptions: restored.AdditionalOptions,
		AllowedOptions:    restored.AllowedOptions,
		MetadataMeta:      restored.MetadataMeta,
		MetadataEnv:       restored.MetadataEnv,
	}

	if err := tx.Model(&jailModels.Jail{}).
		Where("id = ?", current.ID).
		Select(
			"Name",
			"Hostname",
			"Description",
			"Type",
			"StartAtBoot",
			"StartOrder",
			"InheritIPv4",
			"InheritIPv6",
			"ResourceLimits",
			"Cores",
			"CPUSet",
			"Memory",
			"DevFSRuleset",
			"Fstab",
			"CleanEnvironment",
			"AdditionalOptions",
			"AllowedOptions",
			"MetadataMeta",
			"MetadataEnv",
		).
		Updates(jailUpdate).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_update_jail_from_snapshot: %w", err)
	}

	if err := tx.Where("jid = ?", current.ID).Delete(&jailModels.JailHooks{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_jail_hooks: %w", err)
	}

	if err := tx.Where("jid = ?", current.ID).Delete(&jailModels.Storage{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_jail_storages: %w", err)
	}

	if err := tx.Where("jid = ?", current.ID).Delete(&jailModels.Network{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_jail_networks: %w", err)
	}

	hooks := make([]jailModels.JailHooks, 0, len(restored.JailHooks))
	for _, hook := range restored.JailHooks {
		hook.ID = 0
		hook.JailID = current.ID
		hooks = append(hooks, hook)
	}
	if len(hooks) > 0 {
		if err := tx.Create(&hooks).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_jail_hooks_from_snapshot: %w", err)
		}
	}

	storages := make([]jailModels.Storage, 0, len(restored.Storages))
	for _, storage := range restored.Storages {
		storage.ID = 0
		storage.JailID = current.ID
		storages = append(storages, storage)
	}
	if len(storages) > 0 {
		if err := tx.Create(&storages).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_jail_storages_from_snapshot: %w", err)
		}
	}

	networks := make([]jailModels.Network, 0, len(restored.Networks))
	for _, network := range restored.Networks {
		network.ID = 0
		network.JailID = current.ID
		networks = append(networks, network)
	}
	if len(networks) > 0 {
		if err := tx.Create(&networks).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_jail_networks_from_snapshot: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_snapshot_reconciliation: %w", err)
	}

	return nil
}

func (s *Service) normalizeRestoredJailSnapshotNetworks(
	networks []jailModels.Network,
) ([]jailModels.Network, []string, error) {
	if len(networks) == 0 {
		return []jailModels.Network{}, nil, nil
	}

	warnings := make([]string, 0)
	out := make([]jailModels.Network, 0, len(networks))

	for _, network := range networks {
		switchType := strings.ToLower(strings.TrimSpace(network.SwitchType))
		if switchType == "" {
			switchType = "standard"
		}

		switch switchType {
		case "standard":
			var sw networkModels.StandardSwitch
			if err := s.DB.Select("id").Where("id = ?", network.SwitchID).First(&sw).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					warnings = append(warnings, fmt.Sprintf(
						"standard_switch_%d_not_found; skipped network restore",
						network.SwitchID,
					))
					continue
				}
				return nil, nil, fmt.Errorf("failed_to_lookup_standard_switch_for_snapshot_restore: %w", err)
			}
			network.SwitchType = "standard"
			out = append(out, network)
		case "manual":
			var sw networkModels.ManualSwitch
			if err := s.DB.Select("id").Where("id = ?", network.SwitchID).First(&sw).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					warnings = append(warnings, fmt.Sprintf(
						"manual_switch_%d_not_found; skipped network restore",
						network.SwitchID,
					))
					continue
				}
				return nil, nil, fmt.Errorf("failed_to_lookup_manual_switch_for_snapshot_restore: %w", err)
			}
			network.SwitchType = "manual"
			out = append(out, network)
		default:
			warnings = append(warnings, fmt.Sprintf(
				"switch_type_%q_invalid_for_network_restore; skipped",
				network.SwitchType,
			))
		}
	}

	return out, warnings, nil
}

func resolveJailRootDataset(jail *jailModels.Jail) (string, string, error) {
	if jail == nil {
		return "", "", fmt.Errorf("jail_not_found")
	}

	baseStorageIdx := slices.IndexFunc(jail.Storages, func(storage jailModels.Storage) bool {
		return storage.IsBase
	})
	if baseStorageIdx < 0 {
		return "", "", fmt.Errorf("jail_base_storage_not_found")
	}

	basePool := strings.TrimSpace(jail.Storages[baseStorageIdx].Pool)
	if basePool == "" {
		return "", "", fmt.Errorf("jail_base_pool_not_found")
	}

	rootDataset := fmt.Sprintf("%s/sylve/jails/%d", basePool, jail.CTID)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", basePool, jail.CTID)
	return rootDataset, mountPoint, nil
}

func sanitizeSnapshotToken(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "-")
	value = invalidSnapshotNameChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.:_")
	if value == "" {
		value = "snapshot"
	}
	if len(value) > 48 {
		value = value[:48]
	}
	return value
}

func isDatasetNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dataset does not exist") ||
		strings.Contains(msg, "no such dataset") ||
		strings.Contains(msg, "not found")
}

func (s *Service) stageHostConfigIntoDataset(ctID uint, mountPoint string) error {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctID))
	stagingRoot := filepath.Join(mountPoint, ".sylve", "host-config")
	if err := os.RemoveAll(stagingRoot); err != nil {
		return fmt.Errorf("failed_to_reset_snapshot_staging_directory: %w", err)
	}
	if err := os.MkdirAll(stagingRoot, 0755); err != nil {
		return fmt.Errorf("failed_to_create_snapshot_staging_directory: %w", err)
	}

	confPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID))
	if _, err := os.Stat(confPath); err == nil {
		if err := copyFile(confPath, filepath.Join(stagingRoot, fmt.Sprintf("%d.conf", ctID))); err != nil {
			return fmt.Errorf("failed_to_stage_jail_conf: %w", err)
		}
	}

	fstabPath := filepath.Join(jailDir, "fstab")
	if _, err := os.Stat(fstabPath); err == nil {
		if err := copyFile(fstabPath, filepath.Join(stagingRoot, "fstab")); err != nil {
			return fmt.Errorf("failed_to_stage_jail_fstab: %w", err)
		}
	}

	scriptsPath := filepath.Join(jailDir, "scripts")
	if _, err := os.Stat(scriptsPath); err == nil {
		if err := copyDir(scriptsPath, filepath.Join(stagingRoot, "scripts")); err != nil {
			return fmt.Errorf("failed_to_stage_jail_scripts: %w", err)
		}
	}

	return nil
}

func (s *Service) restoreHostConfigFromDataset(ctID uint, mountPoint string) error {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctID))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_config_directory: %w", err)
	}

	stagingRoot := filepath.Join(mountPoint, ".sylve", "host-config")
	if _, err := os.Stat(stagingRoot); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed_to_stat_snapshot_staging_directory: %w", err)
	}

	confSource := filepath.Join(stagingRoot, fmt.Sprintf("%d.conf", ctID))
	if _, err := os.Stat(confSource); err == nil {
		if err := copyFile(confSource, filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID))); err != nil {
			return fmt.Errorf("failed_to_restore_jail_conf: %w", err)
		}
	}

	fstabSource := filepath.Join(stagingRoot, "fstab")
	if _, err := os.Stat(fstabSource); err == nil {
		if err := copyFile(fstabSource, filepath.Join(jailDir, "fstab")); err != nil {
			return fmt.Errorf("failed_to_restore_jail_fstab: %w", err)
		}
	}

	scriptsSource := filepath.Join(stagingRoot, "scripts")
	if _, err := os.Stat(scriptsSource); err == nil {
		hostScripts := filepath.Join(jailDir, "scripts")
		if err := os.RemoveAll(hostScripts); err != nil {
			return fmt.Errorf("failed_to_reset_host_scripts_directory: %w", err)
		}
		if err := copyDir(scriptsSource, hostScripts); err != nil {
			return fmt.Errorf("failed_to_restore_jail_scripts: %w", err)
		}
	}

	return nil
}

func copyDir(srcDir string, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}

func copyFile(src string, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return nil
}
