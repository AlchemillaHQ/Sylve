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

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var (
	invalidSnapshotNameChars = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)
	jailSnapshotPathPattern  = regexp.MustCompile(`path\s*=\s*["']([^"']+)["']`)
)

const (
	jailSnapshotCleanupTimeout  = 2 * time.Minute
	jailSnapshotMutationTimeout = 10 * time.Minute
)

type JailSnapshotRollbackResult struct {
	WasRunning bool     `json:"wasRunning"`
	Restarted  bool     `json:"restarted"`
	Warnings   []string `json:"warnings"`
}

type jailSnapshotRestorePlan struct {
	Restored            jailModels.Jail
	RootDataset         string
	MountPoint          string
	Targets             []string
	HostConfigAvailable bool
	Warnings            []string
}

func detachedJailSnapshotContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func (s *Service) ListJailSnapshots(ctID uint) ([]jailModels.JailSnapshot, error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}

	var jail jailModels.Jail
	if err := s.DB.Select("id").Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_jail: %w", err)
	}

	snapshots := make([]jailModels.JailSnapshot, 0)
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
	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return nil, err
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

	rootDataset, mountPoint, rootFS, err := s.resolveJailSnapshotRoot(ctx, jail)
	if err != nil {
		return nil, err
	}

	if err := s.writeJailJSONAtMountPoint(ctID, mountPoint); err != nil {
		return nil, fmt.Errorf("failed_to_write_jail_json_before_snapshot: %w", err)
	}

	if err := s.stageHostConfigIntoDataset(ctID, mountPoint); err != nil {
		return nil, fmt.Errorf("failed_to_stage_jail_host_config: %w", err)
	}

	snapToken := sanitizeSnapshotToken(name)
	snapshotName := fmt.Sprintf("sjs_%s_%d", snapToken, time.Now().UTC().UnixMilli())

	// Metadata preparation can take long enough for replication ownership to
	// change. Re-check immediately before the physical snapshot mutation.
	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return nil, err
	}

	mutationCtx, cancelMutation := detachedJailSnapshotContext(ctx, jailSnapshotMutationTimeout)
	defer cancelMutation()
	createdSnapshot, err := rootFS.Snapshot(mutationCtx, snapshotName, true)
	if err != nil {
		s.cleanupCreatedJailSnapshot(ctx, rootDataset, snapshotName)
		return nil, fmt.Errorf("failed_to_create_jail_snapshot: %w", err)
	}

	if createdSnapshot == nil {
		s.cleanupCreatedJailSnapshot(ctx, rootDataset, snapshotName)
		return nil, fmt.Errorf("snapshot_creation_returned_nil")
	}

	var latest jailModels.JailSnapshot
	var parentID *uint
	if err := s.DB.
		Where("jid = ?", jail.ID).
		Order("created_at DESC, id DESC").
		First(&latest).Error; err == nil {
		parentID = &latest.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		s.cleanupCreatedJailSnapshot(ctx, rootDataset, snapshotName)
		return nil, fmt.Errorf("failed_to_find_latest_jail_snapshot: %w", err)
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
		s.cleanupCreatedJailSnapshot(ctx, rootDataset, snapshotName)
		return nil, fmt.Errorf("failed_to_record_jail_snapshot: %w", err)
	}

	if err := s.writeJailJSONAtMountPoint(ctID, mountPoint); err != nil {
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
) (result JailSnapshotRollbackResult, retErr error) {
	result.Warnings = []string{}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if ctID == 0 || snapshotID == 0 {
		return result, fmt.Errorf("invalid_request")
	}
	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return result, err
	}
	if err := s.RequireJailStorageTopologyMutable(ctID); err != nil {
		return result, err
	}

	current, err := s.GetJailByCTID(ctID)
	if err != nil {
		return result, fmt.Errorf("failed_to_get_jail: %w", err)
	}

	var record jailModels.JailSnapshot
	if err := s.DB.
		Where("jid = ? AND ct_id = ? AND id = ?", current.ID, ctID, snapshotID).
		First(&record).Error; err != nil {
		return result, fmt.Errorf("snapshot_not_found: %w", err)
	}

	plan, err := s.preflightJailSnapshotRestore(ctx, ctID, current, record)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, plan.Warnings...)

	wasActive, err := s.IsJailActive(ctID)
	if err != nil {
		return result, fmt.Errorf("failed_to_get_jail_state: %w", err)
	}
	result.WasRunning = wasActive

	if wasActive {
		// Once an active jail begins the stop/rollback sequence, always make a
		// best effort to restore its prior running state—even when rollback or
		// configuration restoration fails.
		defer func() {
			if active, stateErr := s.IsJailActive(ctID); stateErr == nil && active {
				result.Restarted = true
				return
			}

			startErr := s.JailActionContext(ctx, int(ctID), "start")
			stateErr := s.waitForJailActiveState(ctID, true, 45*time.Second)
			if stateErr != nil {
				warning := fmt.Sprintf("jail_did_not_reach_active_state_after_snapshot_rollback: %v", stateErr)
				if startErr != nil {
					warning = fmt.Sprintf(
						"failed_to_start_jail_after_snapshot_rollback: %v; final_state_error: %v",
						startErr,
						stateErr,
					)
				}
				result.Warnings = append(result.Warnings, warning)
				logger.L.Warn().
					Err(stateErr).
					AnErr("start_error", startErr).
					Uint("ctid", ctID).
					Msg("jail_did_not_reach_active_state_after_snapshot_rollback")
				return
			}
			if startErr != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"jail_start_reported_error_but_active_state_was_restored: %v",
					startErr,
				))
			}
			result.Restarted = true
		}()

		if err := s.JailActionContext(ctx, int(ctID), "stop"); err != nil {
			return result, fmt.Errorf("failed_to_stop_jail_before_snapshot_rollback: %w", err)
		}
		if err := s.waitForJailActiveState(ctID, false, 30*time.Second); err != nil {
			return result, err
		}
	}

	// A transition can begin while preflight or jail shutdown is in progress.
	// Repeat both guards immediately before the first destructive ZFS call.
	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return result, err
	}
	if err := s.RequireJailStorageTopologyMutable(ctID); err != nil {
		return result, err
	}

	mutationCtx, cancelMutation := detachedJailSnapshotContext(ctx, jailSnapshotMutationTimeout)
	defer cancelMutation()
	for _, fullSnapshot := range plan.Targets {
		snapshotDataset, err := s.GZFS.ZFS.Get(mutationCtx, fullSnapshot, false)
		if err != nil {
			if isDatasetNotFoundError(err) {
				return result, fmt.Errorf("jail_snapshot_dataset_missing: %s: %w", fullSnapshot, err)
			}
			return result, fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
		}
		if snapshotDataset == nil {
			return result, fmt.Errorf("jail_snapshot_dataset_missing: %s", fullSnapshot)
		}
		if err := snapshotDataset.Rollback(mutationCtx, true); err != nil {
			if isDatasetNotFoundError(err) {
				return result, fmt.Errorf("jail_snapshot_dataset_missing: %s: %w", fullSnapshot, err)
			}
			return result, fmt.Errorf("failed_to_rollback_snapshot: %w", err)
		}
	}

	if err := s.restoreHostConfigFromDataset(ctID, plan.MountPoint, plan.HostConfigAvailable); err != nil {
		return result, fmt.Errorf("failed_to_restore_jail_host_config: %w", err)
	}

	if err := s.restoreJailDatabaseFromSnapshot(ctID, plan.Restored); err != nil {
		return result, fmt.Errorf("failed_to_restore_jail_config_from_snapshot: %w", err)
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
		return result, fmt.Errorf("failed_to_prune_newer_snapshot_records: %w", err)
	}

	if err := s.writeJailJSONAtMountPoint(ctID, plan.MountPoint); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"failed_to_refresh_jail_json_after_rollback: %v",
			err,
		))
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_snapshot_rollback_%d", ctID))
	return result, nil
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
	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return err
	}
	if err := s.RequireJailStorageTopologyMutable(ctID); err != nil {
		return err
	}

	current, err := s.GetJailByCTID(ctID)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	var record jailModels.JailSnapshot
	if err := s.DB.
		Where("jid = ? AND ct_id = ? AND id = ?", current.ID, ctID, snapshotID).
		First(&record).Error; err != nil {
		return fmt.Errorf("snapshot_not_found: %w", err)
	}

	rootDataset, _, err := jailRootDatasetIdentity(current)
	if err != nil {
		return err
	}
	if strings.TrimSpace(record.RootDataset) != rootDataset {
		return fmt.Errorf(
			"jail_snapshot_root_dataset_mismatch: expected %s, found %s",
			rootDataset,
			record.RootDataset,
		)
	}

	targets, physicalMissing, err := s.collectJailSnapshotTargets(ctx, rootDataset, record.SnapshotName, false)
	if err != nil {
		return err
	}

	if err := s.requireJailSnapshotOwnership(ctID); err != nil {
		return err
	}
	if err := s.RequireJailStorageTopologyMutable(ctID); err != nil {
		return err
	}

	mutationCtx, cancelMutation := detachedJailSnapshotContext(ctx, jailSnapshotMutationTimeout)
	defer cancelMutation()
	for _, fullSnapshot := range targets {
		ds, err := s.GZFS.ZFS.Get(mutationCtx, fullSnapshot, false)
		if err != nil {
			if isDatasetNotFoundError(err) {
				physicalMissing = true
				continue
			}
			return fmt.Errorf("failed_to_get_snapshot_for_deletion: %w", err)
		}
		if ds == nil {
			physicalMissing = true
			continue
		}
		if err := ds.Destroy(mutationCtx, false, false); err != nil {
			if isDatasetNotFoundError(err) {
				physicalMissing = true
				continue
			}
			return fmt.Errorf("failed_to_delete_snapshot_dataset: %w", err)
		}
	}

	if err := reparentAndDeleteJailSnapshotRecord(s.DB, record); err != nil {
		return err
	}

	if physicalMissing {
		logger.L.Warn().
			Uint("ctid", ctID).
			Uint("snapshot_id", snapshotID).
			Msg("reconciled_missing_jail_snapshot_dataset")
	}

	if _, mountPoint, _, resolveErr := s.resolveJailSnapshotRoot(mutationCtx, current); resolveErr == nil {
		if err := s.writeJailJSONAtMountPoint(ctID, mountPoint); err != nil {
			logger.L.Warn().Err(err).Uint("ctid", ctID).Msg("failed_to_refresh_jail_json_after_snapshot_delete")
		}
	} else {
		logger.L.Warn().
			Err(resolveErr).
			Uint("ctid", ctID).
			Msg("failed_to_refresh_jail_json_after_snapshot_delete")
	}

	return nil
}

func (s *Service) requireJailSnapshotOwnership(ctID uint) error {
	allowed, err := s.canMutateProtectedJail(ctID)
	if err != nil {
		return fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("replication_lease_not_owned")
	}
	return nil
}

func usableJailSnapshotMountpoint(raw string) (string, bool) {
	mountPoint := strings.TrimSpace(raw)
	switch strings.ToLower(mountPoint) {
	case "", "-", "none", "legacy":
		return "", false
	}
	if !filepath.IsAbs(mountPoint) {
		return "", false
	}
	mountPoint = filepath.Clean(mountPoint)
	if mountPoint == string(filepath.Separator) {
		return "", false
	}
	return mountPoint, true
}
func (s *Service) resolveJailSnapshotRoot(
	ctx context.Context,
	jail *jailModels.Jail,
) (string, string, *gzfs.Dataset, error) {
	rootDataset, storage, err := jailRootDatasetIdentity(jail)
	if err != nil {
		return "", "", nil, err
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return "", "", nil, fmt.Errorf("gzfs_not_initialized")
	}

	dataset, err := s.GZFS.ZFS.Get(ctx, rootDataset, false)
	if err != nil {
		if isDatasetNotFoundError(err) {
			return "", "", nil, fmt.Errorf("jail_snapshot_dataset_missing: %s: %w", rootDataset, err)
		}
		return "", "", nil, fmt.Errorf("failed_to_get_jail_root_dataset: %w", err)
	}
	if dataset == nil {
		return "", "", nil, fmt.Errorf("jail_snapshot_dataset_missing: %s", rootDataset)
	}
	if dataset.Type != "" && dataset.Type != gzfs.DatasetTypeFilesystem {
		return "", "", nil, fmt.Errorf("jail_snapshot_root_not_filesystem: %s", rootDataset)
	}

	if err := dataset.Mount(ctx, false); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already mounted") {
			return "", "", nil, fmt.Errorf("failed_to_mount_jail_snapshot_root: %w", err)
		}
	}

	mountPoint, err := validateFilesystemDatasetMountpoint(dataset, rootDataset, storage.GUID)
	if err != nil {
		return "", "", nil, fmt.Errorf("jail_snapshot_mountpoint_unusable: %w", err)
	}

	return rootDataset, mountPoint, dataset, nil
}

func (s *Service) writeJailJSONAtMountPoint(ctID uint, mountPoint string) error {
	mountPoint, usable := usableJailSnapshotMountpoint(mountPoint)
	if !usable {
		return fmt.Errorf("jail_snapshot_mountpoint_unusable")
	}

	jail, err := s.GetJailByCTID(ctID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(jail, "", "  ")
	if err != nil {
		return fmt.Errorf("failed_to_marshal_jail_to_json: %w", err)
	}

	sylveDir := filepath.Join(mountPoint, ".sylve")
	if err := os.MkdirAll(sylveDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_.sylve_directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sylveDir, "jail.json"), data, 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_json_file: %w", err)
	}
	return nil
}

func jailSnapshotDatasetDepth(fullSnapshot string) int {
	dataset := strings.TrimSpace(fullSnapshot)
	if at := strings.LastIndex(dataset, "@"); at >= 0 {
		dataset = dataset[:at]
	}
	return strings.Count(dataset, "/")
}

func (s *Service) collectJailSnapshotTargets(
	ctx context.Context,
	rootDataset string,
	snapshotName string,
	requireRoot bool,
) ([]string, bool, error) {
	rootDataset = strings.TrimSpace(rootDataset)
	snapshotName = strings.TrimSpace(snapshotName)
	if rootDataset == "" || snapshotName == "" {
		return nil, false, fmt.Errorf("invalid_snapshot_name")
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, false, fmt.Errorf("gzfs_not_initialized")
	}

	datasets, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, rootDataset, true)
	if err != nil {
		if isDatasetNotFoundError(err) {
			if requireRoot {
				return nil, true, fmt.Errorf("jail_snapshot_dataset_missing: %s@%s: %w", rootDataset, snapshotName, err)
			}
			return []string{}, true, nil
		}
		return nil, false, fmt.Errorf("failed_to_list_recursive_jail_snapshot_targets: %w", err)
	}

	suffix := "@" + snapshotName
	rootPrefix := rootDataset + "/"
	rootSnapshot := rootDataset + suffix
	rootFound := false
	targets := make([]string, 0)
	for _, dataset := range datasets {
		if dataset == nil {
			continue
		}
		name := strings.TrimSpace(dataset.Name)
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		datasetPart := strings.TrimSuffix(name, suffix)
		if datasetPart != rootDataset && !strings.HasPrefix(datasetPart, rootPrefix) {
			continue
		}
		if name == rootSnapshot {
			rootFound = true
		}
		targets = append(targets, name)
	}

	slices.SortStableFunc(targets, func(left, right string) int {
		leftDepth := jailSnapshotDatasetDepth(left)
		rightDepth := jailSnapshotDatasetDepth(right)
		if leftDepth > rightDepth {
			return -1
		}
		if leftDepth < rightDepth {
			return 1
		}
		return strings.Compare(left, right)
	})

	if !rootFound && requireRoot {
		return nil, true, fmt.Errorf("jail_snapshot_dataset_missing: %s", rootSnapshot)
	}
	return targets, !rootFound, nil
}

func (s *Service) cleanupCreatedJailSnapshot(parent context.Context, rootDataset, snapshotName string) {
	if strings.TrimSpace(rootDataset) == "" || strings.TrimSpace(snapshotName) == "" ||
		s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return
	}

	cleanupCtx, cancelCleanup := detachedJailSnapshotContext(parent, jailSnapshotCleanupTimeout)
	defer cancelCleanup()
	fullSnapshot := fmt.Sprintf("%s@%s", rootDataset, snapshotName)
	dataset, err := s.GZFS.ZFS.Get(cleanupCtx, fullSnapshot, false)
	if err != nil || dataset == nil {
		if err != nil && !isDatasetNotFoundError(err) {
			logger.L.Warn().Err(err).Str("snapshot", fullSnapshot).Msg("failed_to_find_jail_snapshot_for_cleanup")
		}
		return
	}
	if err := dataset.Destroy(cleanupCtx, true, false); err != nil {
		logger.L.Warn().Err(err).Str("snapshot", fullSnapshot).Msg("failed_to_cleanup_jail_snapshot_after_error")
	}
}

func reparentAndDeleteJailSnapshotRecord(db *gorm.DB, record jailModels.JailSnapshot) error {
	if db == nil {
		return fmt.Errorf("snapshot_database_not_initialized")
	}

	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_start_snapshot_delete_transaction: %w", tx.Error)
	}
	if err := tx.Model(&jailModels.JailSnapshot{}).
		Where("jid = ? AND parent_snapshot_id = ?", record.JailID, record.ID).
		Update("parent_snapshot_id", record.ParentSnapshotID).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_reparent_jail_snapshot_children: %w", err)
	}
	if err := tx.Delete(&record).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_delete_snapshot_record: %w", err)
	}
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_snapshot_delete: %w", err)
	}
	return nil
}

func (s *Service) preflightJailSnapshotRestore(
	ctx context.Context,
	ctID uint,
	current *jailModels.Jail,
	record jailModels.JailSnapshot,
) (jailSnapshotRestorePlan, error) {
	plan := jailSnapshotRestorePlan{Warnings: []string{}}
	if current == nil || current.ID == 0 || current.CTID != ctID || record.JailID != current.ID || record.CTID != ctID {
		return plan, fmt.Errorf("snapshot_jail_identity_mismatch")
	}

	rootDataset, mountPoint, _, err := s.resolveJailSnapshotRoot(ctx, current)
	if err != nil {
		return plan, err
	}
	if strings.TrimSpace(record.RootDataset) != rootDataset {
		return plan, fmt.Errorf(
			"jail_snapshot_root_dataset_mismatch: expected %s, found %s",
			rootDataset,
			record.RootDataset,
		)
	}

	targets, _, err := s.collectJailSnapshotTargets(ctx, rootDataset, record.SnapshotName, true)
	if err != nil {
		return plan, err
	}

	snapshotName := strings.TrimSpace(record.SnapshotName)
	if snapshotName == "" || snapshotName == "." || snapshotName == ".." || strings.ContainsAny(snapshotName, "/\\") {
		return plan, fmt.Errorf("invalid_snapshot_name")
	}
	snapshotRoot := filepath.Join(mountPoint, ".zfs", "snapshot", snapshotName)
	metadata, err := os.ReadFile(filepath.Join(snapshotRoot, ".sylve", "jail.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return plan, fmt.Errorf("snapshot_jail_json_not_found")
		}
		return plan, fmt.Errorf("failed_to_read_snapshot_jail_json: %w", err)
	}

	var restored jailModels.Jail
	if err := json.Unmarshal(metadata, &restored); err != nil {
		return plan, fmt.Errorf("invalid_snapshot_jail_json: %w", err)
	}
	if restored.CTID != ctID {
		return plan, fmt.Errorf(
			"snapshot_jail_identity_mismatch: expected ctid %d, found %d",
			ctID,
			restored.CTID,
		)
	}
	if restored.ExecTimeout == 0 {
		restored.ExecTimeout = jailModels.DefaultExecTimeoutSeconds
	}

	// CTID, database identity, display name, and the current root storage are
	// live resource identity. Snapshot rollback restores configuration without
	// silently renaming or relocating the jail.
	restored.ID = current.ID
	restored.CTID = current.CTID
	restored.Name = current.Name
	restored.Storages, err = s.normalizeRestoredJailSnapshotStorages(ctx, current, restored.Storages)
	if err != nil {
		return plan, err
	}

	restored.Networks, plan.Warnings, err = s.normalizeRestoredJailSnapshotNetworks(restored.Networks)
	if err != nil {
		return plan, err
	}
	for _, warning := range plan.Warnings {
		logger.L.Warn().Uint("ctid", ctID).Str("warning", warning).Msg("jail_snapshot_restore_preflight_warning")
	}

	hostConfigRoot := filepath.Join(snapshotRoot, ".sylve", "host-config")
	hostConfigInfo, err := os.Stat(hostConfigRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return plan, fmt.Errorf("snapshot_host_config_missing")
		}
		return plan, fmt.Errorf("snapshot_host_config_invalid: %w", err)
	} else {
		if !hostConfigInfo.IsDir() {
			return plan, fmt.Errorf("snapshot_host_config_invalid: staging path is not a directory")
		}
		confPath := filepath.Join(hostConfigRoot, fmt.Sprintf("%d.conf", ctID))
		confInfo, statErr := os.Stat(confPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return plan, fmt.Errorf("snapshot_jail_conf_not_found")
			}
			return plan, fmt.Errorf("snapshot_host_config_invalid: %w", statErr)
		}
		if !confInfo.Mode().IsRegular() {
			return plan, fmt.Errorf("snapshot_host_config_invalid: jail config is not a regular file")
		}
		confData, readErr := os.ReadFile(confPath)
		if readErr != nil {
			return plan, fmt.Errorf("snapshot_host_config_invalid: %w", readErr)
		}
		matches := jailSnapshotPathPattern.FindSubmatch(confData)
		if len(matches) < 2 {
			return plan, fmt.Errorf("snapshot_host_config_invalid: jail path is missing")
		}
		savedMountPoint, savedUsable := usableJailSnapshotMountpoint(string(matches[1]))
		if !savedUsable || savedMountPoint != mountPoint {
			return plan, fmt.Errorf(
				"jail_snapshot_mountpoint_mismatch: dataset=%s snapshot=%s",
				mountPoint,
				savedMountPoint,
			)
		}
		plan.HostConfigAvailable = true
	}

	plan.Restored = restored
	plan.RootDataset = rootDataset
	plan.MountPoint = mountPoint
	plan.Targets = targets
	return plan, nil
}

func (s *Service) normalizeRestoredJailSnapshotStorages(
	ctx context.Context,
	current *jailModels.Jail,
	restored []jailModels.Storage,
) ([]jailModels.Storage, error) {
	if current == nil {
		return nil, fmt.Errorf("jail_not_found")
	}
	currentBaseIdx := slices.IndexFunc(current.Storages, func(storage jailModels.Storage) bool {
		return storage.IsBase
	})
	if currentBaseIdx < 0 {
		return nil, fmt.Errorf("jail_base_storage_not_found")
	}
	currentBase := current.Storages[currentBaseIdx]
	currentBase.GUID = strings.TrimSpace(currentBase.GUID)
	if currentBase.GUID == "" {
		return nil, fmt.Errorf("jail_base_storage_dataset_missing")
	}

	restoredBaseCount := 0
	normalized := make([]jailModels.Storage, 0, len(restored))
	seenGUIDs := map[string]struct{}{currentBase.GUID: {}}
	for _, storage := range restored {
		if storage.IsBase {
			restoredBaseCount++
			continue
		}
		storage.GUID = strings.TrimSpace(storage.GUID)
		if storage.GUID == "" {
			return nil, fmt.Errorf("restored_jail_storage_dataset_missing: empty guid")
		}
		if _, exists := seenGUIDs[storage.GUID]; exists {
			return nil, fmt.Errorf("restored_jail_storage_duplicate: guid %s", storage.GUID)
		}
		seenGUIDs[storage.GUID] = struct{}{}
		dataset, err := s.GZFS.ZFS.GetByGUID(ctx, storage.GUID, false)
		if err != nil {
			if isDatasetNotFoundError(err) {
				return nil, fmt.Errorf("restored_jail_storage_dataset_missing: guid %s: %w", storage.GUID, err)
			}
			return nil, fmt.Errorf("failed_to_lookup_restored_jail_storage: %w", err)
		}
		if dataset == nil {
			return nil, fmt.Errorf("restored_jail_storage_dataset_missing: guid %s", storage.GUID)
		}
		if dataset.Type != "" && dataset.Type != gzfs.DatasetTypeFilesystem {
			return nil, fmt.Errorf("restored_jail_storage_not_filesystem: guid %s", storage.GUID)
		}

		var count int64
		if err := s.DB.Model(&jailModels.Storage{}).
			Where("guid = ? AND jid <> ?", storage.GUID, current.ID).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("failed_to_check_restored_jail_storage_ownership: %w", err)
		}
		if count > 0 {
			return nil, fmt.Errorf("restored_jail_storage_in_use: guid %s", storage.GUID)
		}

		storage.ID = 0
		storage.JailID = current.ID
		normalized = append(normalized, storage)
	}
	if restoredBaseCount != 1 {
		return nil, fmt.Errorf("restored_jail_base_storage_not_found")
	}

	currentBase.ID = 0
	currentBase.JailID = current.ID
	normalized = append(normalized, currentBase)
	return normalized, nil
}

func (s *Service) restoreJailDatabaseFromSnapshot(ctID uint, restored jailModels.Jail) error {
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
		WoL:               restored.WoL,
		InheritIPv4:       restored.InheritIPv4,
		InheritIPv6:       restored.InheritIPv6,
		ResourceLimits:    restored.ResourceLimits,
		Cores:             restored.Cores,
		CPUSet:            restored.CPUSet,
		Memory:            restored.Memory,
		DevFSRuleset:      restored.DevFSRuleset,
		Fstab:             restored.Fstab,
		ResolvConf:        restored.ResolvConf,
		CleanEnvironment:  restored.CleanEnvironment,
		ExecTimeout:       restored.ExecTimeout,
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
			"WoL",
			"InheritIPv4",
			"InheritIPv6",
			"ResourceLimits",
			"Cores",
			"CPUSet",
			"Memory",
			"DevFSRuleset",
			"Fstab",
			"ResolvConf",
			"CleanEnvironment",
			"ExecTimeout",
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
	mountPoint, usable := usableJailSnapshotMountpoint(mountPoint)
	if !usable {
		return fmt.Errorf("jail_snapshot_mountpoint_unusable")
	}
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
	if err := copyFile(confPath, filepath.Join(stagingRoot, fmt.Sprintf("%d.conf", ctID))); err != nil {
		return fmt.Errorf("failed_to_stage_jail_conf: %w", err)
	}

	fstabPath := filepath.Join(jailDir, "fstab")
	if _, err := os.Stat(fstabPath); err == nil {
		if err := copyFile(fstabPath, filepath.Join(stagingRoot, "fstab")); err != nil {
			return fmt.Errorf("failed_to_stage_jail_fstab: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed_to_stat_jail_fstab: %w", err)
	}

	scriptsPath := filepath.Join(jailDir, "scripts")
	if info, err := os.Stat(scriptsPath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("failed_to_stage_jail_scripts: scripts path is not a directory")
		}
		if err := copyDir(scriptsPath, filepath.Join(stagingRoot, "scripts")); err != nil {
			return fmt.Errorf("failed_to_stage_jail_scripts: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed_to_stat_jail_scripts: %w", err)
	}

	return nil
}

func (s *Service) restoreHostConfigFromDataset(ctID uint, mountPoint string, available bool) error {
	if !available {
		return nil
	}
	mountPoint, usable := usableJailSnapshotMountpoint(mountPoint)
	if !usable {
		return fmt.Errorf("jail_snapshot_mountpoint_unusable")
	}
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
	if err := copyFile(confSource, filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID))); err != nil {
		return fmt.Errorf("failed_to_restore_jail_conf: %w", err)
	}

	fstabSource := filepath.Join(stagingRoot, "fstab")
	if _, err := os.Stat(fstabSource); err == nil {
		if err := copyFile(fstabSource, filepath.Join(jailDir, "fstab")); err != nil {
			return fmt.Errorf("failed_to_restore_jail_fstab: %w", err)
		}
	} else if os.IsNotExist(err) {
		if err := os.Remove(filepath.Join(jailDir, "fstab")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed_to_remove_jail_fstab: %w", err)
		}
	} else {
		return fmt.Errorf("failed_to_stat_snapshot_jail_fstab: %w", err)
	}

	hostScripts := filepath.Join(jailDir, "scripts")
	if err := os.RemoveAll(hostScripts); err != nil {
		return fmt.Errorf("failed_to_reset_host_scripts_directory: %w", err)
	}
	scriptsSource := filepath.Join(stagingRoot, "scripts")
	if info, err := os.Stat(scriptsSource); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("failed_to_restore_jail_scripts: snapshot scripts path is not a directory")
		}
		if err := copyDir(scriptsSource, hostScripts); err != nil {
			return fmt.Errorf("failed_to_restore_jail_scripts: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed_to_stat_snapshot_jail_scripts: %w", err)
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
