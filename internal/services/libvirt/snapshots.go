// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/digitalocean/go-libvirt"
	"github.com/klauspost/cpuid/v2"
	"gorm.io/gorm"
)

var invalidVMSnapshotNameChars = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)

const (
	vmSnapshotCleanupTimeout  = 2 * time.Minute
	vmSnapshotMutationTimeout = 10 * time.Minute
)

type VMSnapshotRollbackResult struct {
	WasRunning              bool     `json:"wasRunning"`
	Restarted               bool     `json:"restarted"`
	NewerSnapshotsDestroyed int64    `json:"newerSnapshotsDestroyed"`
	Warnings                []string `json:"warnings"`
}

func detachedVMSnapshotContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func (s *Service) ListVMSnapshots(rid uint) ([]vmModels.VMSnapshot, error) {
	if rid == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}

	var vm vmModels.VM
	if err := s.DB.Select("id").Where("rid = ?", rid).First(&vm).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_vm: %w", err)
	}

	var snapshots []vmModels.VMSnapshot
	if err := s.DB.
		Where("vm_id = ?", vm.ID).
		Order("created_at ASC, id ASC").
		Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_vm_snapshots: %w", err)
	}

	return snapshots, nil
}

func (s *Service) CreateVMSnapshot(
	ctx context.Context,
	rid uint,
	name string,
	description string,
) (*vmModels.VMSnapshot, error) {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if rid == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
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

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm: %w", err)
	}

	rootDatasets, err := resolveVMRootDatasets(&vm)
	if err != nil {
		return nil, err
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	if err := s.WriteVMJson(rid); err != nil {
		return nil, fmt.Errorf("failed_to_write_vm_json_before_snapshot: %w", err)
	}

	snapToken := sanitizeVMSnapshotToken(name)
	snapshotName := fmt.Sprintf("svms_%s_%d", snapToken, time.Now().UTC().UnixMilli())

	createdRoots := make([]string, 0, len(rootDatasets))
	for _, rootDataset := range rootDatasets {
		rootFS, err := s.GZFS.ZFS.Get(ctx, rootDataset, false)
		if err != nil {
			s.cleanupCreatedVMSnapshot(ctx, createdRoots, snapshotName)
			return nil, fmt.Errorf("failed_to_get_vm_root_dataset: %w", err)
		}

		createdSnapshot, err := rootFS.Snapshot(ctx, snapshotName, true)
		if err != nil {
			s.cleanupCreatedVMSnapshot(ctx, createdRoots, snapshotName)
			return nil, fmt.Errorf("failed_to_create_vm_snapshot: %w", err)
		}

		if createdSnapshot == nil {
			s.cleanupCreatedVMSnapshot(ctx, createdRoots, snapshotName)
			return nil, fmt.Errorf("snapshot_creation_returned_nil")
		}

		createdRoots = append(createdRoots, rootDataset)
	}

	var latest vmModels.VMSnapshot
	var parentID *uint
	if err := s.DB.
		Where("vm_id = ?", vm.ID).
		Order("created_at DESC, id DESC").
		First(&latest).Error; err == nil {
		parentID = &latest.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		s.cleanupCreatedVMSnapshot(ctx, rootDatasets, snapshotName)
		return nil, fmt.Errorf("failed_to_find_latest_vm_snapshot: %w", err)
	}

	record := vmModels.VMSnapshot{
		VMID:             vm.ID,
		RID:              vm.RID,
		ParentSnapshotID: parentID,
		Name:             name,
		Description:      description,
		SnapshotName:     snapshotName,
		RootDatasets:     rootDatasets,
	}

	if err := s.DB.Create(&record).Error; err != nil {
		s.cleanupCreatedVMSnapshot(ctx, rootDatasets, snapshotName)
		return nil, fmt.Errorf("failed_to_record_vm_snapshot: %w", err)
	}

	if err := s.WriteVMJson(rid); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("rid", rid).
			Msg("failed_to_refresh_vm_json_after_snapshot_create")
	}

	return &record, nil
}

func (s *Service) RollbackVMSnapshot(
	ctx context.Context,
	rid uint,
	snapshotID uint,
) (VMSnapshotRollbackResult, error) {
	return s.rollbackVMSnapshot(ctx, rid, snapshotID, true)
}

func (s *Service) RollbackVMSnapshotWithDestroyNewer(
	ctx context.Context,
	rid uint,
	snapshotID uint,
	destroyNewer bool,
) (VMSnapshotRollbackResult, error) {
	return s.rollbackVMSnapshot(ctx, rid, snapshotID, destroyNewer)
}

func (s *Service) rollbackVMSnapshot(
	ctx context.Context,
	rid uint,
	snapshotID uint,
	destroyNewer bool,
) (VMSnapshotRollbackResult, error) {
	result := VMSnapshotRollbackResult{Warnings: []string{}}

	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if rid == 0 || snapshotID == 0 {
		return result, fmt.Errorf("invalid_request")
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return result, err
	}
	if err := s.requireVMStorageTopologyMutable(rid); err != nil {
		return result, err
	}

	var record vmModels.VMSnapshot
	if err := s.DB.
		Where("rid = ? AND id = ?", rid, snapshotID).
		First(&record).Error; err != nil {
		return result, fmt.Errorf("snapshot_not_found: %w", err)
	}

	newerSnapshotCount, err := countNewerVMSnapshotRecords(s.DB, record)
	if err != nil {
		return result, err
	}
	if newerSnapshotCount > 0 && !destroyNewer {
		return result, fmt.Errorf(
			"newer_snapshots_require_acknowledgement: rollback would destroy %d newer snapshot(s); retry with explicit acknowledgement",
			newerSnapshotCount,
		)
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return result, fmt.Errorf("failed_to_get_vm: %w", err)
	}

	rootDatasets := record.RootDatasets
	if len(rootDatasets) == 0 {
		resolvedRoots, err := resolveVMRootDatasets(&vm)
		if err != nil {
			return result, err
		}
		rootDatasets = resolvedRoots
	}

	rollbackTargets, err := s.collectVMSnapshotTargets(ctx, rootDatasets, record.SnapshotName)
	if err != nil {
		return result, err
	}

	restored, warnings, err := s.preflightVMSnapshotRestore(
		ctx,
		rid,
		vm,
		rootDatasets,
		record.SnapshotName,
	)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	if _, err := s.ensureConnection(); err != nil {
		return result, fmt.Errorf("libvirt_connection_unavailable: %w", err)
	}

	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if err := s.requireVMMutationOwnership(rid); err != nil {
		return result, err
	}
	if err := s.requireVMStorageTopologyMutable(rid); err != nil {
		return result, err
	}

	isShutOff, err := s.IsDomainShutOff(rid)
	if err != nil {
		if !isVMDomainNotFoundError(err) {
			return result, fmt.Errorf("failed_to_get_vm_state: %w", err)
		}
		isShutOff = true
	}

	if !isShutOff {
		if err := s.lvVMActionLocked(vm, "stop", ""); err != nil {
			return result, fmt.Errorf("failed_to_stop_vm_before_snapshot_rollback: %w", err)
		}
		if err := s.waitForVMShutOffState(rid, true, 45*time.Second); err != nil {
			return result, err
		}
		result.WasRunning = true
	}

	mutationCtx, cancelMutation := detachedVMSnapshotContext(ctx, vmSnapshotMutationTimeout)
	defer cancelMutation()

	for _, fullSnapshot := range rollbackTargets {
		snapshotDataset, err := s.GZFS.ZFS.Get(mutationCtx, fullSnapshot, false)
		if err != nil {
			return result, fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
		}
		if err := snapshotDataset.Rollback(mutationCtx, destroyNewer); err != nil {
			return result, fmt.Errorf("failed_to_rollback_snapshot: %w", err)
		}
	}

	if err := s.restoreVMRuntimeArtifactsFromSnapshot(mutationCtx, rid, rootDatasets); err != nil {
		return result, fmt.Errorf("failed_to_restore_vm_runtime_artifacts: %w", err)
	}

	if err := s.restoreVMDatabaseFromSnapshotConfig(rid, restored); err != nil {
		return result, fmt.Errorf("failed_to_restore_vm_config_from_snapshot: %w", err)
	}

	if err := s.redefineVMDomainFromDatabase(rid); err != nil {
		return result, fmt.Errorf("failed_to_redefine_vm_domain_after_snapshot_rollback: %w", err)
	}

	if err := s.DB.
		Where(
			"vm_id = ? AND (created_at > ? OR (created_at = ? AND id > ?))",
			record.VMID,
			record.CreatedAt,
			record.CreatedAt,
			record.ID,
		).
		Delete(&vmModels.VMSnapshot{}).Error; err != nil {
		return result, fmt.Errorf("failed_to_prune_newer_snapshot_records: %w", err)
	}
	result.NewerSnapshotsDestroyed = newerSnapshotCount

	if err := s.WriteVMJson(rid); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"failed_to_refresh_vm_json_after_rollback: %v",
			err,
		))
	}

	if result.WasRunning {
		freshVM, err := s.GetVMByRID(rid)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"failed_to_get_vm_for_restart_after_snapshot_rollback: %v",
				err,
			))
		} else if err := s.lvVMActionLocked(freshVM, "start", ""); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"failed_to_start_vm_after_snapshot_rollback: %v",
				err,
			))
		} else if err := s.waitForVMShutOffState(rid, false, 60*time.Second); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"vm_did_not_reach_running_state_after_snapshot_rollback: %v",
				err,
			))
		} else {
			result.Restarted = true
		}
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("vm_snapshot_rollback_%d", rid))
	return result, nil
}

func countNewerVMSnapshotRecords(db *gorm.DB, record vmModels.VMSnapshot) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("snapshot_database_not_initialized")
	}

	var count int64
	if err := db.Model(&vmModels.VMSnapshot{}).
		Where(
			"vm_id = ? AND (created_at > ? OR (created_at = ? AND id > ?))",
			record.VMID,
			record.CreatedAt,
			record.CreatedAt,
			record.ID,
		).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed_to_count_newer_snapshot_records: %w", err)
	}

	return count, nil
}

func (s *Service) DeleteVMSnapshot(ctx context.Context, rid uint, snapshotID uint) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if rid == 0 || snapshotID == 0 {
		return fmt.Errorf("invalid_request")
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return err
	}
	if err := s.requireVMStorageTopologyMutable(rid); err != nil {
		return err
	}

	var record vmModels.VMSnapshot
	if err := s.DB.
		Where("rid = ? AND id = ?", rid, snapshotID).
		First(&record).Error; err != nil {
		return fmt.Errorf("snapshot_not_found: %w", err)
	}

	rootDatasets := record.RootDatasets
	if len(rootDatasets) == 0 {
		vm, err := s.GetVMByRID(rid)
		if err != nil {
			return fmt.Errorf("failed_to_get_vm: %w", err)
		}

		resolvedRoots, err := resolveVMRootDatasets(&vm)
		if err != nil {
			return err
		}

		rootDatasets = resolvedRoots
	}

	deleteTargets, err := s.collectVMSnapshotTargets(ctx, rootDatasets, record.SnapshotName)
	if err != nil {
		return err
	}

	if err := s.requireVMMutationOwnership(rid); err != nil {
		return err
	}
	if err := s.requireVMStorageTopologyMutable(rid); err != nil {
		return err
	}

	mutationCtx, cancelMutation := detachedVMSnapshotContext(ctx, vmSnapshotMutationTimeout)
	defer cancelMutation()

	for _, fullSnapshot := range deleteTargets {
		ds, err := s.GZFS.ZFS.Get(mutationCtx, fullSnapshot, false)
		if err != nil {
			if !isVMDatasetNotFoundError(err) {
				return fmt.Errorf("failed_to_get_snapshot_for_deletion: %w", err)
			}
			continue
		}

		if err := ds.Destroy(mutationCtx, false, false); err != nil {
			return fmt.Errorf("failed_to_delete_snapshot_dataset: %w", err)
		}
	}

	if err := reparentAndDeleteVMSnapshotRecord(s.DB, record); err != nil {
		return err
	}

	if err := s.WriteVMJson(rid); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("rid", rid).
			Msg("failed_to_refresh_vm_json_after_snapshot_delete")
	}

	return nil
}

func reparentAndDeleteVMSnapshotRecord(db *gorm.DB, record vmModels.VMSnapshot) error {
	if db == nil {
		return fmt.Errorf("snapshot_database_not_initialized")
	}

	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_start_snapshot_delete_transaction: %w", tx.Error)
	}
	if err := tx.Model(&vmModels.VMSnapshot{}).
		Where("parent_snapshot_id = ?", record.ID).
		Update("parent_snapshot_id", record.ParentSnapshotID).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_reparent_vm_snapshot_children: %w", err)
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

func (s *Service) destroyVMSnapshotFromRoots(ctx context.Context, rootDatasets []string, snapshotName string) {
	for _, rootDataset := range rootDatasets {
		fullSnapshot := fmt.Sprintf("%s@%s", rootDataset, snapshotName)
		ds, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
		if err != nil {
			continue
		}
		if err := ds.Destroy(ctx, true, false); err != nil {
			logger.L.Warn().
				Err(err).
				Str("snapshot", fullSnapshot).
				Msg("failed_to_cleanup_vm_snapshot_after_error")
		}
	}
}

func (s *Service) cleanupCreatedVMSnapshot(parent context.Context, rootDatasets []string, snapshotName string) {
	if len(rootDatasets) == 0 {
		return
	}

	cleanupCtx, cancelCleanup := detachedVMSnapshotContext(parent, vmSnapshotCleanupTimeout)
	defer cancelCleanup()
	s.destroyVMSnapshotFromRoots(cleanupCtx, rootDatasets, snapshotName)
}

func (s *Service) collectVMSnapshotTargets(
	ctx context.Context,
	rootDatasets []string,
	snapshotName string,
) ([]string, error) {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	targetsByName := make(map[string]struct{}, len(rootDatasets))
	for _, rootDataset := range rootDatasets {
		targets, err := s.listRecursiveRollbackTargets(ctx, rootDataset, snapshotName)
		if err != nil {
			if isVMDatasetNotFoundError(err) {
				return nil, fmt.Errorf("vm_snapshot_dataset_missing: %s@%s: %w", rootDataset, snapshotName, err)
			}
			return nil, err
		}
		if len(targets) == 0 {
			targets = []string{fmt.Sprintf("%s@%s", rootDataset, snapshotName)}
		}
		for _, target := range targets {
			targetsByName[target] = struct{}{}
		}
	}

	targets := make([]string, 0, len(targetsByName))
	for target := range targetsByName {
		targets = append(targets, target)
	}
	slices.SortStableFunc(targets, func(left, right string) int {
		leftDepth := snapshotDatasetDepth(left)
		rightDepth := snapshotDatasetDepth(right)
		if leftDepth > rightDepth {
			return -1
		}
		if leftDepth < rightDepth {
			return 1
		}
		return strings.Compare(left, right)
	})

	for _, target := range targets {
		if _, err := s.GZFS.ZFS.Get(ctx, target, false); err != nil {
			if isVMDatasetNotFoundError(err) {
				return nil, fmt.Errorf("vm_snapshot_dataset_missing: %s: %w", target, err)
			}
			return nil, fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
		}
	}

	return targets, nil
}

func resolveVMRootDatasets(vm *vmModels.VM) ([]string, error) {
	if vm == nil {
		return nil, fmt.Errorf("vm_not_found")
	}

	rootsByName := make(map[string]struct{})
	for _, storage := range vm.Storages {
		if storage.Type != vmModels.VMStorageTypeRaw && storage.Type != vmModels.VMStorageTypeZVol {
			continue
		}

		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			pool = strings.TrimSpace(storage.Dataset.Pool)
		}
		if pool == "" {
			pool = poolFromDatasetName(storage.Dataset.Name)
		}
		if pool == "" {
			continue
		}

		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID)
		rootsByName[rootDataset] = struct{}{}
	}

	if len(rootsByName) == 0 {
		return nil, fmt.Errorf("vm_snapshot_requires_zfs_storage")
	}

	roots := make([]string, 0, len(rootsByName))
	for root := range rootsByName {
		roots = append(roots, root)
	}
	slices.Sort(roots)

	return roots, nil
}

func poolFromDatasetName(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return ""
	}
	parts := strings.SplitN(dataset, "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *Service) listRecursiveRollbackTargets(ctx context.Context, rootDataset, snapshotName string) ([]string, error) {
	rootDataset = strings.TrimSpace(rootDataset)
	snapshotName = strings.TrimSpace(snapshotName)
	if rootDataset == "" || snapshotName == "" {
		return nil, nil
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	datasets, err := s.GZFS.ZFS.ListWithPrefix(ctx, "snapshot", rootDataset, true)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_recursive_snapshot_targets: %w", err)
	}

	suffix := "@" + snapshotName
	rootPrefix := rootDataset + "/"
	targets := make([]string, 0)
	for _, dataset := range datasets {
		if dataset == nil {
			continue
		}
		name := strings.TrimSpace(dataset.Name)
		if name == "" {
			continue
		}
		if !strings.HasSuffix(name, suffix) {
			continue
		}

		datasetPart := name[:len(name)-len(suffix)]
		if datasetPart == rootDataset || strings.HasPrefix(datasetPart, rootPrefix) {
			targets = append(targets, name)
		}
	}

	return targets, nil
}

func snapshotDatasetDepth(fullSnapshot string) int {
	fullSnapshot = strings.TrimSpace(fullSnapshot)
	if fullSnapshot == "" {
		return 0
	}

	dataset := fullSnapshot
	if at := strings.LastIndex(dataset, "@"); at > 0 {
		dataset = dataset[:at]
	}

	return strings.Count(dataset, "/")
}

func sanitizeVMSnapshotToken(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "-")
	value = invalidVMSnapshotNameChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.:_")
	if value == "" {
		value = "snapshot"
	}
	if len(value) > 48 {
		value = value[:48]
	}
	return value
}

func isVMDatasetNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dataset does not exist") ||
		strings.Contains(msg, "no such dataset") ||
		strings.Contains(msg, "not found")
}

func isVMDomainNotFoundError(err error) bool {
	return libvirtServiceInterfaces.IsDomainNotFoundError(err)
}

func (s *Service) waitForVMShutOffState(rid uint, shouldBeShutOff bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		isShutOff, err := s.IsDomainShutOff(rid)
		if err == nil && isShutOff == shouldBeShutOff {
			return nil
		}

		if err != nil && isVMDomainNotFoundError(err) {
			if shouldBeShutOff {
				return nil
			}
		}

		if time.Now().After(deadline) {
			target := "running"
			if shouldBeShutOff {
				target = "shutoff"
			}
			if err != nil {
				return fmt.Errorf("vm_failed_to_reach_%s_state: %w", target, err)
			}
			return fmt.Errorf("vm_failed_to_reach_%s_state", target)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (s *Service) restoreVMRuntimeArtifactsFromSnapshot(
	ctx context.Context,
	rid uint,
	rootDatasets []string,
) error {
	if rid == 0 {
		return fmt.Errorf("invalid_rid")
	}
	if len(rootDatasets) == 0 {
		return fmt.Errorf("vm_snapshot_root_dataset_not_found")
	}

	vmConfigDir, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_config_directory: %w", err)
	}

	if err := os.MkdirAll(vmConfigDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_vm_config_directory: %w", err)
	}

	artifactNames := []string{
		fmt.Sprintf("%d_tpm.log", rid),
		fmt.Sprintf("%d_tpm.state", rid),
	}

	if hostUsesSplitFirmware() {
		artifactNames = append(artifactNames, fmt.Sprintf("%d_vars.fd", rid))
	}

	for _, artifactName := range artifactNames {
		copied := false
		relativePath := filepath.Join(".sylve", artifactName)

		for _, rootDataset := range rootDatasets {
			artifactBytes, found, err := s.readVMSnapshotFileFromDataset(ctx, rootDataset, relativePath)
			if err != nil {
				return err
			}
			if !found {
				continue
			}

			dstPath := filepath.Join(vmConfigDir, artifactName)
			if err := os.WriteFile(dstPath, artifactBytes, 0644); err != nil {
				return fmt.Errorf("failed_to_write_vm_artifact_%s: %w", artifactName, err)
			}
			copied = true
			break
		}

		if !copied {
			logger.L.Debug().
				Uint("rid", rid).
				Str("artifact", artifactName).
				Msg("snapshot_vm_artifact_not_found")
		}
	}

	return nil
}

func (s *Service) preflightVMSnapshotRestore(
	ctx context.Context,
	rid uint,
	current vmModels.VM,
	rootDatasets []string,
	snapshotName string,
) (vmModels.VM, []string, error) {
	if rid == 0 {
		return vmModels.VM{}, nil, fmt.Errorf("invalid_rid")
	}

	metadataRaw, found, err := s.readVMSnapshotFileFromCandidatesAtSnapshot(
		ctx,
		rootDatasets,
		snapshotName,
		".sylve/vm.json",
	)
	if err != nil {
		return vmModels.VM{}, nil, fmt.Errorf("failed_to_read_snapshot_vm_json: %w", err)
	}
	if !found {
		return vmModels.VM{}, nil, fmt.Errorf("snapshot_vm_json_not_found")
	}

	var restored vmModels.VM
	if err := json.Unmarshal(metadataRaw, &restored); err != nil {
		return vmModels.VM{}, nil, fmt.Errorf("invalid_snapshot_vm_json: %w", err)
	}
	if restored.RID != 0 && restored.RID != rid {
		return vmModels.VM{}, nil, fmt.Errorf(
			"snapshot_vm_identity_mismatch: expected rid %d, found %d",
			rid,
			restored.RID,
		)
	}

	warnings := make([]string, 0)
	restored.ID = current.ID
	restored.RID = rid
	restored.Name = current.Name

	normalizedPins, pinWarnings, err := s.normalizeRestoredCPUPinning(rid, restored.CPUPinning)
	if err != nil {
		return vmModels.VM{}, nil, err
	}
	warnings = append(warnings, pinWarnings...)
	restored.CPUPinning = normalizedPins

	normalizedPCI, pciWarnings, err := s.normalizeRestoredPCIDevices(rid, restored.PCIDevices)
	if err != nil {
		return vmModels.VM{}, nil, err
	}
	warnings = append(warnings, pciWarnings...)
	restored.PCIDevices = normalizedPCI

	normalizedNetworks, networkWarnings, err := s.normalizeRestoredVMNetworks(current.ID, restored.Networks)
	if err != nil {
		return vmModels.VM{}, nil, err
	}
	warnings = append(warnings, networkWarnings...)
	restored.Networks = normalizedNetworks

	vncWarnings, err := s.normalizeRestoredVNC(rid, current, &restored)
	if err != nil {
		return vmModels.VM{}, nil, err
	}
	warnings = append(warnings, vncWarnings...)

	normalizedStorages, storageWarnings, err := s.normalizeRestoredVMStorages(
		ctx,
		rid,
		current.ID,
		rootDatasets,
		snapshotName,
		restored.Storages,
	)
	if err != nil {
		return vmModels.VM{}, nil, err
	}
	warnings = append(warnings, storageWarnings...)
	restored.Storages = normalizedStorages

	for _, warning := range warnings {
		logger.L.Warn().
			Uint("rid", rid).
			Str("warning", warning).
			Msg("vm_snapshot_restore_preflight_warning")
	}

	return restored, warnings, nil
}

func (s *Service) restoreVMDatabaseFromSnapshotConfig(
	rid uint,
	restored vmModels.VM,
) error {
	if rid == 0 {
		return fmt.Errorf("invalid_rid")
	}

	current, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_current_vm: %w", err)
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_start_transaction: %w", tx.Error)
	}

	vmUpdate := vmModels.VM{
		Description:            restored.Description,
		CPUSockets:             restored.CPUSockets,
		CPUCores:               restored.CPUCores,
		CPUThreads:             restored.CPUThreads,
		RAM:                    restored.RAM,
		TPMEmulation:           restored.TPMEmulation,
		ShutdownWaitTime:       restored.ShutdownWaitTime,
		Serial:                 restored.Serial,
		VNCEnabled:             restored.VNCEnabled,
		VNCBind:                NormalizeVNCBindAddress(restored.VNCBind),
		VNCPort:                restored.VNCPort,
		VNCPassword:            restored.VNCPassword,
		VNCResolution:          restored.VNCResolution,
		VNCWait:                restored.VNCWait,
		StartAtBoot:            restored.StartAtBoot,
		StartOrder:             restored.StartOrder,
		WoL:                    restored.WoL,
		TimeOffset:             restored.TimeOffset,
		BootROM:                normalizeBootROMValue(restored.BootROM),
		PCIDevices:             restored.PCIDevices,
		ACPI:                   restored.ACPI,
		APIC:                   restored.APIC,
		CloudInitData:          restored.CloudInitData,
		CloudInitMetaData:      restored.CloudInitMetaData,
		CloudInitNetworkConfig: restored.CloudInitNetworkConfig,
		ExtraBhyveOptions:      append([]string(nil), restored.ExtraBhyveOptions...),
		IgnoreUMSR:             restored.IgnoreUMSR,
		QemuGuestAgent:         restored.QemuGuestAgent,
	}

	if err := tx.Model(&vmModels.VM{}).
		Where("id = ?", current.ID).
		Select(
			"Description",
			"CPUSockets",
			"CPUCores",
			"CPUThreads",
			"RAM",
			"TPMEmulation",
			"ShutdownWaitTime",
			"Serial",
			"VNCEnabled",
			"VNCBind",
			"VNCPort",
			"VNCPassword",
			"VNCResolution",
			"VNCWait",
			"StartAtBoot",
			"StartOrder",
			"WoL",
			"TimeOffset",
			"BootROM",
			"PCIDevices",
			"ACPI",
			"APIC",
			"CloudInitData",
			"CloudInitMetaData",
			"CloudInitNetworkConfig",
			"ExtraBhyveOptions",
			"IgnoreUMSR",
			"QemuGuestAgent",
		).
		Updates(vmUpdate).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_update_vm_from_snapshot: %w", err)
	}

	previousDatasetIDs := []uint{}
	if err := tx.Model(&vmModels.Storage{}).
		Where("vm_id = ?", current.ID).
		Where("dataset_id IS NOT NULL").
		Pluck("dataset_id", &previousDatasetIDs).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_collect_vm_dataset_ids_before_snapshot_restore: %w", err)
	}

	if err := tx.Where("vm_id = ?", current.ID).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_vm_cpu_pinning: %w", err)
	}

	if err := tx.Where("vm_id = ?", current.ID).Delete(&vmModels.Network{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_vm_networks: %w", err)
	}

	if err := tx.Where("vm_id = ?", current.ID).Delete(&vmModels.Storage{}).Error; err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed_to_replace_vm_storages: %w", err)
	}

	restoredPinning := make([]vmModels.VMCPUPinning, 0, len(restored.CPUPinning))
	for _, pin := range restored.CPUPinning {
		pin.ID = 0
		pin.VMID = current.ID
		restoredPinning = append(restoredPinning, pin)
	}
	if len(restoredPinning) > 0 {
		if err := tx.Create(&restoredPinning).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_vm_cpu_pinning_from_snapshot: %w", err)
		}
	}

	restoredNetworks := make([]vmModels.Network, 0, len(restored.Networks))
	for _, network := range restored.Networks {
		network.ID = 0
		network.VMID = current.ID
		restoredNetworks = append(restoredNetworks, network)
	}
	if len(restoredNetworks) > 0 {
		if err := tx.Create(&restoredNetworks).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_vm_networks_from_snapshot: %w", err)
		}
	}

	restoredStorages, err := prepareRestoredVMStorages(tx, rid, current.ID, restored.Storages)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(restoredStorages) > 0 {
		if err := tx.Create(&restoredStorages).Error; err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_insert_vm_storages_from_snapshot: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_snapshot_reconciliation: %w", err)
	}

	for _, datasetID := range uniqueUintValues(previousDatasetIDs) {
		var refCount int64
		if err := s.DB.Model(&vmModels.Storage{}).Where("dataset_id = ?", datasetID).Count(&refCount).Error; err != nil {
			return fmt.Errorf("failed_to_count_vm_storage_dataset_references: %w", err)
		}
		if refCount > 0 {
			continue
		}

		if err := s.DB.Delete(&vmModels.VMStorageDataset{}, datasetID).Error; err != nil {
			return fmt.Errorf("failed_to_delete_orphan_vm_storage_dataset: %w", err)
		}
	}

	return nil
}

func (s *Service) normalizeRestoredCPUPinning(
	rid uint,
	pins []vmModels.VMCPUPinning,
) ([]vmModels.VMCPUPinning, []string, error) {
	if len(pins) == 0 {
		return []vmModels.VMCPUPinning{}, nil, nil
	}

	socketCount := utils.GetSocketCount(cpuid.CPU.PhysicalCores, cpuid.CPU.ThreadsPerCore)
	if socketCount <= 0 {
		socketCount = 1
	}

	logicalCores := utils.GetLogicalCores()
	if logicalCores <= 0 {
		logicalCores = cpuid.CPU.LogicalCores
	}
	if logicalCores <= 0 {
		return []vmModels.VMCPUPinning{}, []string{
			"host_cpu_topology_unavailable; skipping restored cpu pinning",
		}, nil
	}

	coresPerSocket := logicalCores / socketCount
	if coresPerSocket <= 0 {
		coresPerSocket = logicalCores
	}

	var vms []vmModels.VM
	if err := s.DB.
		Select("id", "rid").
		Preload("CPUPinning").
		Find(&vms).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_fetch_vms_for_cpu_pinning_restore: %w", err)
	}

	occupied := make(map[int]uint, 256)
	for _, vm := range vms {
		if vm.RID == rid {
			continue
		}
		for _, p := range vm.CPUPinning {
			for _, localCore := range p.HostCPU {
				globalCore := p.HostSocket*coresPerSocket + localCore
				occupied[globalCore] = vm.RID
			}
		}
	}

	warnings := make([]string, 0)
	selected := make(map[int]struct{}, 256)
	out := make([]vmModels.VMCPUPinning, 0, len(pins))

	for _, pin := range pins {
		if pin.HostSocket < 0 || pin.HostSocket >= socketCount {
			warnings = append(warnings, fmt.Sprintf(
				"socket_%d_out_of_range(max_%d); dropped restored pin set",
				pin.HostSocket,
				socketCount-1,
			))
			continue
		}

		localSeen := make(map[int]struct{}, len(pin.HostCPU))
		kept := make([]int, 0, len(pin.HostCPU))

		for _, localCore := range pin.HostCPU {
			if localCore < 0 || localCore >= coresPerSocket {
				warnings = append(warnings, fmt.Sprintf(
					"core_%d_invalid_for_socket_%d; dropped restored pin",
					localCore,
					pin.HostSocket,
				))
				continue
			}

			if _, dup := localSeen[localCore]; dup {
				continue
			}

			globalCore := pin.HostSocket*coresPerSocket + localCore
			if globalCore < 0 || globalCore >= logicalCores {
				warnings = append(warnings, fmt.Sprintf(
					"global_core_%d_out_of_range(max_%d); dropped restored pin",
					globalCore,
					logicalCores-1,
				))
				continue
			}

			if ownerRID, used := occupied[globalCore]; used {
				warnings = append(warnings, fmt.Sprintf(
					"core_%d(socket_%d) already used by vm_%d; skipped",
					localCore,
					pin.HostSocket,
					ownerRID,
				))
				continue
			}

			if _, alreadySelected := selected[globalCore]; alreadySelected {
				warnings = append(warnings, fmt.Sprintf(
					"core_%d(socket_%d) duplicated in restored config; skipped",
					localCore,
					pin.HostSocket,
				))
				continue
			}

			localSeen[localCore] = struct{}{}
			selected[globalCore] = struct{}{}
			kept = append(kept, localCore)
		}

		if len(kept) == 0 {
			if len(pin.HostCPU) > 0 {
				warnings = append(warnings, fmt.Sprintf(
					"all pins dropped for socket_%d due to conflicts/validation",
					pin.HostSocket,
				))
			}
			continue
		}

		slices.Sort(kept)
		out = append(out, vmModels.VMCPUPinning{
			HostSocket: pin.HostSocket,
			HostCPU:    kept,
		})
	}

	return out, warnings, nil
}

func (s *Service) normalizeRestoredPCIDevices(rid uint, pciDevices []int) ([]int, []string, error) {
	if len(pciDevices) == 0 {
		return []int{}, nil, nil
	}

	var passthrough []models.PassedThroughIDs
	if err := s.DB.Select("id").Find(&passthrough).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_list_passthrough_devices_for_snapshot_restore: %w", err)
	}

	available := make(map[int]struct{}, len(passthrough))
	for _, dev := range passthrough {
		available[dev.ID] = struct{}{}
	}

	var otherVMs []vmModels.VM
	if err := s.DB.Select("rid", vmPCIDevicesColumn).Where("rid <> ?", rid).Find(&otherVMs).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_list_vm_pci_assignments_for_snapshot_restore: %w", err)
	}

	inUseByVM := make(map[int]uint, 64)
	for _, vm := range otherVMs {
		for _, pciID := range vm.PCIDevices {
			inUseByVM[pciID] = vm.RID
		}
	}

	warnings := make([]string, 0)
	seen := make(map[int]struct{}, len(pciDevices))
	out := make([]int, 0, len(pciDevices))

	for _, pciID := range pciDevices {
		if _, dup := seen[pciID]; dup {
			continue
		}
		seen[pciID] = struct{}{}

		if _, ok := available[pciID]; !ok {
			warnings = append(warnings, fmt.Sprintf(
				"pci_device_%d_not_available_on_host; skipped",
				pciID,
			))
			continue
		}

		if ownerRID, used := inUseByVM[pciID]; used {
			warnings = append(warnings, fmt.Sprintf(
				"pci_device_%d_already_assigned_to_vm_%d; skipped",
				pciID,
				ownerRID,
			))
			continue
		}

		out = append(out, pciID)
	}

	slices.Sort(out)
	return out, warnings, nil
}

func (s *Service) normalizeRestoredVMNetworks(
	currentVMID uint,
	networks []vmModels.Network,
) ([]vmModels.Network, []string, error) {
	if len(networks) == 0 {
		return []vmModels.Network{}, nil, nil
	}

	warnings := make([]string, 0)
	out := make([]vmModels.Network, 0, len(networks))
	seenMACObjects := make(map[uint]struct{}, len(networks))
	seenRawMACs := make(map[string]struct{}, len(networks))

	for _, network := range networks {
		switchType := strings.ToLower(strings.TrimSpace(network.SwitchType))
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
		default:
			warnings = append(warnings, fmt.Sprintf(
				"switch_type_%q_invalid_for_network_restore; skipped",
				network.SwitchType,
			))
			continue
		}

		if network.MacID != nil && *network.MacID != 0 {
			macID := *network.MacID
			if _, duplicate := seenMACObjects[macID]; duplicate {
				warnings = append(warnings, fmt.Sprintf(
					"mac_object_%d_duplicated_in_restored_config; skipped network restore",
					macID,
				))
				continue
			}

			var macObject networkModels.Object
			if err := s.DB.Preload("Entries").Where("id = ? AND type = ?", macID, "Mac").First(&macObject).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					warnings = append(warnings, fmt.Sprintf(
						"mac_object_%d_not_found; skipped network restore",
						macID,
					))
					continue
				}
				return nil, nil, fmt.Errorf("failed_to_lookup_mac_object_for_snapshot_restore: %w", err)
			}
			if len(macObject.Entries) == 0 {
				warnings = append(warnings, fmt.Sprintf(
					"mac_object_%d_has_no_entries; skipped network restore",
					macID,
				))
				continue
			}
			macAddress := strings.ToLower(strings.TrimSpace(macObject.Entries[0].Value))
			if _, err := net.ParseMAC(macAddress); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"mac_object_%d_has_invalid_address; skipped network restore",
					macID,
				))
				continue
			}

			var otherVMReferences int64
			if err := s.DB.Model(&vmModels.Network{}).
				Where("mac_id = ? AND vm_id <> ?", macID, currentVMID).
				Count(&otherVMReferences).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_check_restored_mac_vm_usage: %w", err)
			}
			var jailReferences int64
			if err := s.DB.Table("jail_networks").
				Where("mac_id = ?", macID).
				Count(&jailReferences).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_check_restored_mac_jail_usage: %w", err)
			}
			if otherVMReferences > 0 || jailReferences > 0 {
				warnings = append(warnings, fmt.Sprintf(
					"mac_object_%d_already_in_use; skipped network restore",
					macID,
				))
				continue
			}

			seenMACObjects[macID] = struct{}{}
			network.MAC = ""
		} else if rawMAC := strings.ToLower(strings.TrimSpace(network.MAC)); rawMAC != "" {
			if _, err := net.ParseMAC(rawMAC); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"raw_mac_%q_invalid; skipped network restore",
					network.MAC,
				))
				continue
			}
			if _, duplicate := seenRawMACs[rawMAC]; duplicate {
				warnings = append(warnings, fmt.Sprintf(
					"raw_mac_%s_duplicated_in_restored_config; skipped network restore",
					rawMAC,
				))
				continue
			}

			var otherVMReferences int64
			if err := s.DB.Table("vm_networks").
				Joins("LEFT JOIN object_entries ON object_entries.object_id = vm_networks.mac_id").
				Where("vm_networks.vm_id <> ?", currentVMID).
				Where("LOWER(vm_networks.mac) = ? OR LOWER(object_entries.value) = ?", rawMAC, rawMAC).
				Count(&otherVMReferences).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_check_restored_raw_mac_vm_usage: %w", err)
			}
			var jailReferences int64
			if err := s.DB.Table("jail_networks").
				Joins("LEFT JOIN object_entries ON object_entries.object_id = jail_networks.mac_id").
				Where("LOWER(object_entries.value) = ?", rawMAC).
				Count(&jailReferences).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_check_restored_raw_mac_jail_usage: %w", err)
			}
			if otherVMReferences > 0 || jailReferences > 0 {
				warnings = append(warnings, fmt.Sprintf(
					"raw_mac_%s_already_in_use; skipped network restore",
					rawMAC,
				))
				continue
			}
			seenRawMACs[rawMAC] = struct{}{}
			network.MAC = rawMAC
		}

		out = append(out, network)
	}

	return out, warnings, nil
}

func copyVNCSettings(dst *vmModels.VM, src vmModels.VM) {
	dst.VNCEnabled = src.VNCEnabled
	dst.VNCBind = src.VNCBind
	dst.VNCPort = src.VNCPort
	dst.VNCPassword = src.VNCPassword
	dst.VNCResolution = src.VNCResolution
	dst.VNCWait = src.VNCWait
}

func (s *Service) normalizeRestoredVNC(
	rid uint,
	current vmModels.VM,
	restored *vmModels.VM,
) ([]string, error) {
	if restored == nil {
		return nil, fmt.Errorf("invalid_snapshot_vm_json")
	}
	if !restored.VNCEnabled {
		restored.VNCPort = 0
		restored.VNCBind = NormalizeVNCBindAddress(restored.VNCBind)
		return nil, nil
	}

	preserveCurrent := func(reason string) ([]string, error) {
		copyVNCSettings(restored, current)
		return []string{reason + "; preserved current vnc settings"}, nil
	}

	restored.VNCBind = NormalizeVNCBindAddress(restored.VNCBind)
	if err := ValidateVNCBindAddress(restored.VNCBind); err != nil {
		return preserveCurrent("restored vnc bind address is invalid")
	}
	if restored.VNCPort < 1 || restored.VNCPort > 65535 {
		return preserveCurrent("restored vnc port is outside 1-65535")
	}

	widthRaw, heightRaw, found := strings.Cut(strings.TrimSpace(restored.VNCResolution), "x")
	width, widthErr := strconv.Atoi(widthRaw)
	height, heightErr := strconv.Atoi(heightRaw)
	if !found || widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return preserveCurrent("restored vnc resolution is invalid")
	}

	var otherVMs int64
	if err := s.DB.Model(&vmModels.VM{}).
		Where("vnc_enabled = ? AND vnc_port = ? AND rid <> ?", true, restored.VNCPort, rid).
		Count(&otherVMs).Error; err != nil {
		return nil, fmt.Errorf("failed_to_check_restored_vnc_port_usage: %w", err)
	}
	if otherVMs > 0 {
		return preserveCurrent("restored vnc port is assigned to another vm")
	}

	if (restored.VNCPort != current.VNCPort || !current.VNCEnabled) && utils.IsTCPPortInUse(restored.VNCPort) {
		return preserveCurrent("restored vnc port is used by another service")
	}

	return nil, nil
}

func datasetBelongsToVMRoots(datasetName string, rootDatasets []string) bool {
	datasetName = strings.TrimSpace(datasetName)
	for _, rootDataset := range rootDatasets {
		rootDataset = strings.TrimSuffix(strings.TrimSpace(rootDataset), "/")
		if datasetName == rootDataset || strings.HasPrefix(datasetName, rootDataset+"/") {
			return true
		}
	}
	return false
}

func restoredVMStorageDatasetName(rid uint, storage vmModels.Storage) string {
	datasetName := strings.TrimSpace(storage.Dataset.Name)
	if datasetName != "" {
		return datasetName
	}

	pool := strings.TrimSpace(storage.Pool)
	if pool == "" {
		pool = strings.TrimSpace(storage.Dataset.Pool)
	}
	if pool == "" || storage.ID == 0 {
		return ""
	}

	prefix := "raw"
	if storage.Type == vmModels.VMStorageTypeZVol {
		prefix = "zvol"
	}
	return fmt.Sprintf("%s/sylve/virtual-machines/%d/%s-%d", pool, rid, prefix, storage.ID)
}

func (s *Service) normalizeRestoredVMStorages(
	ctx context.Context,
	rid uint,
	currentVMID uint,
	rootDatasets []string,
	snapshotName string,
	storages []vmModels.Storage,
) ([]vmModels.Storage, []string, error) {
	if len(storages) == 0 {
		return []vmModels.Storage{}, nil, nil
	}

	if len(rootDatasets) == 0 {
		return nil, nil, fmt.Errorf("vm_snapshot_root_dataset_not_found")
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, nil, fmt.Errorf("gzfs_not_initialized")
	}

	warnings := make([]string, 0)
	out := make([]vmModels.Storage, 0, len(storages))
	seenStorageIDs := make(map[uint]struct{}, len(storages))
	seenDatasets := make(map[string]struct{}, len(storages))

	for _, storage := range storages {
		if storage.ID != 0 {
			if _, duplicate := seenStorageIDs[storage.ID]; duplicate {
				return nil, nil, fmt.Errorf("restored_vm_storage_id_conflict: %d", storage.ID)
			}
			seenStorageIDs[storage.ID] = struct{}{}

			var conflictingStorage int64
			if err := s.DB.Model(&vmModels.Storage{}).
				Where("id = ? AND vm_id <> ?", storage.ID, currentVMID).
				Count(&conflictingStorage).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_validate_restored_vm_storage_id: %w", err)
			}
			if conflictingStorage > 0 {
				return nil, nil, fmt.Errorf("restored_vm_storage_id_conflict: %d", storage.ID)
			}
		}

		switch storage.Type {
		case vmModels.VMStorageTypeRaw, vmModels.VMStorageTypeZVol:
			if storage.ID == 0 {
				return nil, nil, fmt.Errorf("invalid_restored_storage_id")
			}
			datasetName := restoredVMStorageDatasetName(rid, storage)
			if datasetName == "" {
				return nil, nil, fmt.Errorf("invalid_restored_vm_storage_dataset_name")
			}
			if !datasetBelongsToVMRoots(datasetName, rootDatasets) {
				return nil, nil, fmt.Errorf("restored_storage_dataset_outside_vm_roots: %s", datasetName)
			}
			if _, duplicate := seenDatasets[datasetName]; duplicate {
				return nil, nil, fmt.Errorf("restored_vm_storage_dataset_in_use: %s", datasetName)
			}
			seenDatasets[datasetName] = struct{}{}

			if _, err := s.GZFS.ZFS.Get(ctx, datasetName, false); err != nil {
				return nil, nil, fmt.Errorf("restored_storage_dataset_missing: %s: %w", datasetName, err)
			}
			fullSnapshot := fmt.Sprintf("%s@%s", datasetName, snapshotName)
			if _, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false); err != nil {
				return nil, nil, fmt.Errorf("restored_storage_snapshot_missing: %s: %w", fullSnapshot, err)
			}

			var otherVMReferences int64
			if err := s.DB.Table("vm_storages").
				Joins("JOIN vm_storage_datasets ON vm_storage_datasets.id = vm_storages.dataset_id").
				Where("vm_storage_datasets.name = ? AND vm_storages.vm_id <> ?", datasetName, currentVMID).
				Count(&otherVMReferences).Error; err != nil {
				return nil, nil, fmt.Errorf("failed_to_check_restored_vm_storage_dataset_usage: %w", err)
			}
			if otherVMReferences > 0 {
				return nil, nil, fmt.Errorf("restored_vm_storage_dataset_in_use: %s", datasetName)
			}

			storage.Dataset.Name = datasetName
			if storage.Pool == "" {
				storage.Pool = poolFromDatasetName(datasetName)
			}
			storage.Dataset.Pool = storage.Pool
			out = append(out, storage)
		case vmModels.VMStorageTypeDiskImage:
			if storage.Enable {
				if strings.TrimSpace(storage.DownloadUUID) == "" {
					warnings = append(warnings, "restored disk image has no uuid; skipped storage restore")
					continue
				}
				if _, err := s.FindISOByUUID(storage.DownloadUUID, true); err != nil {
					warnings = append(warnings, fmt.Sprintf(
						"restored disk image %s is unavailable; skipped storage restore",
						storage.DownloadUUID,
					))
					continue
				}
			}
			storage.DatasetID = nil
			storage.Dataset = vmModels.VMStorageDataset{}
			out = append(out, storage)
		case vmModels.VMStorageTypeFilesystem:
			datasetName := strings.TrimSpace(storage.Dataset.Name)
			if datasetName == "" || !isValidFilesystemTargetName(storage.FilesystemTarget) {
				warnings = append(warnings, fmt.Sprintf(
					"restored filesystem storage %d is incomplete; skipped storage restore",
					storage.ID,
				))
				continue
			}
			if _, err := s.GZFS.ZFS.Get(ctx, datasetName, false); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"restored filesystem dataset %s is unavailable; skipped storage restore",
					datasetName,
				))
				continue
			}
			out = append(out, storage)
		default:
			warnings = append(warnings, fmt.Sprintf(
				"restored storage %d has unsupported type %q; skipped storage restore",
				storage.ID,
				storage.Type,
			))
		}
	}

	return out, warnings, nil
}

func prepareRestoredVMStorages(tx *gorm.DB, rid uint, vmID uint, storages []vmModels.Storage) ([]vmModels.Storage, error) {
	out := make([]vmModels.Storage, 0, len(storages))

	for _, storage := range storages {
		cleaned := storage
		cleaned.VMID = vmID
		cleaned.Dataset = vmModels.VMStorageDataset{}

		if cleaned.ID != 0 {
			var conflictCount int64
			if err := tx.Model(&vmModels.Storage{}).
				Where("id = ? AND vm_id <> ?", cleaned.ID, vmID).
				Count(&conflictCount).Error; err != nil {
				return nil, fmt.Errorf("failed_to_validate_restored_vm_storage_id: %w", err)
			}
			if conflictCount > 0 {
				return nil, fmt.Errorf("restored_vm_storage_id_conflict: %d", cleaned.ID)
			}
		}

		switch cleaned.Type {
		case vmModels.VMStorageTypeRaw, vmModels.VMStorageTypeZVol:
			if cleaned.ID == 0 {
				return nil, fmt.Errorf("invalid_restored_storage_id")
			}

			datasetName := strings.TrimSpace(storage.Dataset.Name)
			if datasetName == "" {
				prefix := "raw"
				if cleaned.Type == vmModels.VMStorageTypeZVol {
					prefix = "zvol"
				}
				datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/%s-%d", cleaned.Pool, rid, prefix, cleaned.ID)
			}

			if cleaned.Pool == "" {
				cleaned.Pool = strings.TrimSpace(storage.Dataset.Pool)
			}
			if cleaned.Pool == "" {
				cleaned.Pool = poolFromDatasetName(datasetName)
			}

			datasetRecord, err := ensureVMStorageDatasetRecord(tx, datasetName, cleaned.Pool, storage.Dataset.GUID)
			if err != nil {
				return nil, err
			}
			cleaned.DatasetID = &datasetRecord.ID
		case vmModels.VMStorageTypeFilesystem:
			datasetName := strings.TrimSpace(storage.Dataset.Name)
			if datasetName == "" {
				return nil, fmt.Errorf("invalid_restored_vm_storage_dataset_name")
			}
			if cleaned.Pool == "" {
				cleaned.Pool = strings.TrimSpace(storage.Dataset.Pool)
			}
			if cleaned.Pool == "" {
				cleaned.Pool = poolFromDatasetName(datasetName)
			}
			datasetRecord, err := ensureVMStorageDatasetRecord(tx, datasetName, cleaned.Pool, storage.Dataset.GUID)
			if err != nil {
				return nil, err
			}
			cleaned.DatasetID = &datasetRecord.ID
		case vmModels.VMStorageTypeDiskImage:
			cleaned.DatasetID = nil
		default:
			cleaned.DatasetID = nil
		}

		out = append(out, cleaned)
	}

	return out, nil
}

func ensureVMStorageDatasetRecord(tx *gorm.DB, datasetName string, pool string, guid string) (vmModels.VMStorageDataset, error) {
	datasetName = strings.TrimSpace(datasetName)
	if datasetName == "" {
		return vmModels.VMStorageDataset{}, fmt.Errorf("invalid_restored_vm_storage_dataset_name")
	}

	if pool == "" {
		pool = poolFromDatasetName(datasetName)
	}

	var existing vmModels.VMStorageDataset
	if err := tx.Where("name = ?", datasetName).First(&existing).Error; err == nil {
		updated := false
		if strings.TrimSpace(existing.Pool) == "" && strings.TrimSpace(pool) != "" {
			existing.Pool = strings.TrimSpace(pool)
			updated = true
		}
		if strings.TrimSpace(existing.GUID) == "" && strings.TrimSpace(guid) != "" {
			existing.GUID = strings.TrimSpace(guid)
			updated = true
		}
		if updated {
			if err := tx.Save(&existing).Error; err != nil {
				return vmModels.VMStorageDataset{}, fmt.Errorf("failed_to_update_vm_storage_dataset_record: %w", err)
			}
		}

		return existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return vmModels.VMStorageDataset{}, fmt.Errorf("failed_to_lookup_vm_storage_dataset_record: %w", err)
	}

	record := vmModels.VMStorageDataset{
		Pool: strings.TrimSpace(pool),
		Name: datasetName,
		GUID: strings.TrimSpace(guid),
	}
	if err := tx.Create(&record).Error; err != nil {
		return vmModels.VMStorageDataset{}, fmt.Errorf("failed_to_create_vm_storage_dataset_record: %w", err)
	}

	return record, nil
}

func (s *Service) redefineVMDomainFromDatabase(rid uint) error {
	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}

	vmPath, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_config_directory: %w", err)
	}

	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed_to_create_vm_config_directory: %w", err)
	}

	vm.BootROM = normalizeBootROMValue(vm.BootROM)
	if err := s.ensureVMBootROMArtifacts(vm.RID, vm.BootROM, vmPath); err != nil {
		return fmt.Errorf("failed_to_prepare_boot_rom_artifacts: %w", err)
	}

	if vm.CloudInitData != "" || vm.CloudInitMetaData != "" {
		if err := s.CreateCloudInitISO(vm); err != nil {
			return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
		}
	}

	domain, err := s.conn().DomainLookupByName(fmt.Sprintf("%d", rid))
	if err == nil {
		if state, _, stateErr := s.conn().DomainGetState(domain, 0); stateErr == nil {
			if state != int32(libvirt.DomainShutoff) {
				if destroyErr := s.conn().DomainDestroy(domain); destroyErr != nil {
					lower := strings.ToLower(destroyErr.Error())
					if !strings.Contains(lower, "is not running") {
						return fmt.Errorf("failed_to_destroy_vm_domain_before_redefine: %w", destroyErr)
					}
				}
			}
		}

		if err := s.conn().DomainUndefine(domain); err != nil {
			return fmt.Errorf("failed_to_undefine_vm_domain_before_redefine: %w", err)
		}
	} else {
		lower := strings.ToLower(err.Error())
		if !strings.Contains(lower, "not found") &&
			!strings.Contains(lower, "no domain") {
			return fmt.Errorf("failed_to_lookup_vm_domain_before_redefine: %w", err)
		}
	}

	xml, err := s.CreateVmXML(vm, vmPath)
	if err != nil {
		return fmt.Errorf("failed_to_generate_vm_xml_after_snapshot_rollback: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(xml); err != nil {
		return fmt.Errorf("failed_to_define_vm_domain_after_snapshot_rollback: %w", err)
	}

	return nil
}

func (s *Service) readVMSnapshotFileFromCandidates(
	ctx context.Context,
	rootDatasets []string,
	relativePath string,
) ([]byte, bool, error) {
	for _, rootDataset := range rootDatasets {
		raw, found, err := s.readVMSnapshotFileFromDataset(ctx, rootDataset, relativePath)
		if err != nil {
			return nil, false, err
		}
		if found {
			return raw, true, nil
		}
	}

	return nil, false, nil
}

func (s *Service) readVMSnapshotFileFromCandidatesAtSnapshot(
	ctx context.Context,
	rootDatasets []string,
	snapshotName string,
	relativePath string,
) ([]byte, bool, error) {
	snapshotName = strings.TrimSpace(snapshotName)
	if snapshotName == "" ||
		snapshotName == "." ||
		snapshotName == ".." ||
		strings.ContainsAny(snapshotName, "/\\") {
		return nil, false, fmt.Errorf("invalid_snapshot_name")
	}

	snapshotRelativePath := filepath.Join(
		".zfs",
		"snapshot",
		snapshotName,
		strings.TrimLeft(relativePath, "/"),
	)
	return s.readVMSnapshotFileFromCandidates(ctx, rootDatasets, snapshotRelativePath)
}

func (s *Service) readVMSnapshotFileFromDataset(
	ctx context.Context,
	dataset string,
	relativePath string,
) ([]byte, bool, error) {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return nil, false, nil
	}

	ds, err := s.GZFS.ZFS.Get(ctx, dataset, false)
	if err != nil {
		return nil, false, fmt.Errorf("failed_to_get_snapshot_root_dataset: %w", err)
	}

	if err := ds.Mount(ctx, false); err != nil {
		lower := strings.ToLower(err.Error())
		if !strings.Contains(lower, "already mounted") {
			return nil, false, fmt.Errorf("failed_to_mount_snapshot_root_dataset: %w", err)
		}
	}

	mountPoint := strings.TrimSpace(ds.Mountpoint)
	if mountPoint == "" || mountPoint == "-" || mountPoint == "none" || mountPoint == "legacy" {
		refreshed, getErr := s.GZFS.ZFS.Get(ctx, dataset, false)
		if getErr == nil && refreshed != nil {
			mountPoint = strings.TrimSpace(refreshed.Mountpoint)
		}
	}

	if mountPoint == "" || mountPoint == "-" || mountPoint == "none" || mountPoint == "legacy" {
		return nil, false, nil
	}

	metaPath := filepath.Join(strings.TrimSuffix(mountPoint, "/"), strings.TrimLeft(relativePath, "/"))
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed_to_read_snapshot_metadata_file: %w", err)
	}

	return raw, true, nil
}
