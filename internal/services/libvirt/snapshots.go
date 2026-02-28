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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/digitalocean/go-libvirt"
	"github.com/klauspost/cpuid/v2"
	"gorm.io/gorm"
)

var invalidVMSnapshotNameChars = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)

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

	if err := s.WriteVMJson(rid); err != nil {
		return nil, fmt.Errorf("failed_to_write_vm_json_before_snapshot: %w", err)
	}

	snapToken := sanitizeVMSnapshotToken(name)
	snapshotName := fmt.Sprintf("sylve_%s_%d", snapToken, time.Now().UTC().UnixMilli())

	createdRoots := make([]string, 0, len(rootDatasets))
	for _, rootDataset := range rootDatasets {
		rootFS, err := s.GZFS.ZFS.Get(ctx, rootDataset, false)
		if err != nil {
			s.destroyVMSnapshotFromRoots(ctx, createdRoots, snapshotName)
			return nil, fmt.Errorf("failed_to_get_vm_root_dataset: %w", err)
		}

		createdSnapshot, err := rootFS.Snapshot(ctx, snapshotName, true)
		if err != nil {
			s.destroyVMSnapshotFromRoots(ctx, createdRoots, snapshotName)
			return nil, fmt.Errorf("failed_to_create_vm_snapshot: %w", err)
		}

		if createdSnapshot == nil {
			s.destroyVMSnapshotFromRoots(ctx, createdRoots, snapshotName)
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
		s.destroyVMSnapshotFromRoots(ctx, rootDatasets, snapshotName)
		return nil, fmt.Errorf("failed_to_record_vm_snapshot: %w", err)
	}

	return &record, nil
}

func (s *Service) RollbackVMSnapshot(
	ctx context.Context,
	rid uint,
	snapshotID uint,
	destroyMoreRecent bool,
) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if rid == 0 || snapshotID == 0 {
		return fmt.Errorf("invalid_request")
	}

	var record vmModels.VMSnapshot
	if err := s.DB.
		Where("rid = ? AND id = ?", rid, snapshotID).
		First(&record).Error; err != nil {
		return fmt.Errorf("snapshot_not_found: %w", err)
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm: %w", err)
	}

	startAfter := false
	rollbackSucceeded := false
	if s.Conn != nil {
		isShutOff, err := s.IsDomainShutOff(rid)
		if err != nil {
			if !isVMDomainNotFoundError(err) {
				return fmt.Errorf("failed_to_get_vm_state: %w", err)
			}
			isShutOff = true
		}

		if !isShutOff {
			if err := s.LvVMAction(vm, "stop"); err != nil {
				return fmt.Errorf("failed_to_stop_vm_before_snapshot_rollback: %w", err)
			}
			if err := s.waitForVMShutOffState(rid, true, 45*time.Second); err != nil {
				return err
			}
			startAfter = true
		}
	}

	defer func() {
		if !startAfter {
			return
		}
		if !rollbackSucceeded {
			logger.L.Warn().
				Uint("rid", rid).
				Msg("skipping_vm_restart_after_snapshot_rollback_due_to_failure")
			return
		}

		freshVM, err := s.GetVMByRID(rid)
		if err != nil {
			logger.L.Warn().Err(err).Uint("rid", rid).Msg("failed_to_get_vm_after_snapshot_rollback")
			return
		}

		if err := s.LvVMAction(freshVM, "start"); err != nil {
			logger.L.Warn().Err(err).Uint("rid", rid).Msg("failed_to_start_vm_after_snapshot_rollback")
			return
		}
		if err := s.waitForVMShutOffState(rid, false, 60*time.Second); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("rid", rid).
				Msg("vm_did_not_reach_running_state_after_snapshot_rollback")
		}
	}()

	rootDatasets := record.RootDatasets
	if len(rootDatasets) == 0 {
		resolvedRoots, err := resolveVMRootDatasets(&vm)
		if err != nil {
			return err
		}
		rootDatasets = resolvedRoots
	}

	rollbackTargets := make([]string, 0, len(rootDatasets))
	seenTargets := make(map[string]struct{}, len(rootDatasets))
	for _, rootDataset := range rootDatasets {
		targets, err := s.listRecursiveRollbackTargets(ctx, rootDataset, record.SnapshotName)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fullSnapshot := fmt.Sprintf("%s@%s", rootDataset, record.SnapshotName)
			if _, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false); err != nil {
				return fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
			}
			targets = []string{fullSnapshot}
		}

		for _, target := range targets {
			if _, exists := seenTargets[target]; exists {
				continue
			}
			seenTargets[target] = struct{}{}
			rollbackTargets = append(rollbackTargets, target)
		}
	}

	slices.SortStableFunc(rollbackTargets, func(left, right string) int {
		leftDepth := snapshotDatasetDepth(left)
		rightDepth := snapshotDatasetDepth(right)
		if leftDepth > rightDepth {
			return -1
		}
		if leftDepth < rightDepth {
			return 1
		}
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})

	for _, fullSnapshot := range rollbackTargets {
		if _, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false); err != nil {
			return fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
		}
	}

	for _, fullSnapshot := range rollbackTargets {
		snapshotDataset, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
		if err != nil {
			return fmt.Errorf("failed_to_get_snapshot_dataset: %w", err)
		}
		if err := snapshotDataset.Rollback(ctx, destroyMoreRecent); err != nil {
			return fmt.Errorf("failed_to_rollback_snapshot: %w", err)
		}
	}

	if err := s.restoreVMRuntimeArtifactsFromSnapshot(ctx, rid, rootDatasets); err != nil {
		return fmt.Errorf("failed_to_restore_vm_runtime_artifacts: %w", err)
	}

	if err := s.restoreVMDatabaseFromSnapshotJSON(ctx, rid, rootDatasets); err != nil {
		return fmt.Errorf("failed_to_restore_vm_config_from_snapshot: %w", err)
	}

	if s.Conn != nil {
		if err := s.redefineVMDomainFromDatabase(rid); err != nil {
			return fmt.Errorf("failed_to_redefine_vm_domain_after_snapshot_rollback: %w", err)
		}
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
		return fmt.Errorf("failed_to_prune_newer_snapshot_records: %w", err)
	}

	if err := s.WriteVMJson(rid); err != nil {
		return fmt.Errorf("failed_to_refresh_vm_json_after_rollback: %w", err)
	}

	rollbackSucceeded = true
	return nil
}

func (s *Service) DeleteVMSnapshot(ctx context.Context, rid uint, snapshotID uint) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	if rid == 0 || snapshotID == 0 {
		return fmt.Errorf("invalid_request")
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

	for _, rootDataset := range rootDatasets {
		fullSnapshot := fmt.Sprintf("%s@%s", rootDataset, record.SnapshotName)
		ds, err := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
		if err != nil {
			if !isVMDatasetNotFoundError(err) {
				return fmt.Errorf("failed_to_get_snapshot_for_deletion: %w", err)
			}
			continue
		}

		if err := ds.Destroy(ctx, false, false); err != nil {
			return fmt.Errorf("failed_to_delete_snapshot_dataset: %w", err)
		}
	}

	if err := s.DB.Delete(&record).Error; err != nil {
		return fmt.Errorf("failed_to_delete_snapshot_record: %w", err)
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

	datasets, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, rootDataset, false)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_recursive_snapshot_targets: %w", err)
	}

	suffix := "@" + snapshotName
	targets := make([]string, 0)
	for _, dataset := range datasets {
		if dataset == nil {
			continue
		}
		name := strings.TrimSpace(dataset.Name)
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, suffix) {
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
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "domain") &&
		(strings.Contains(msg, "not found") || strings.Contains(msg, "no domain"))
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
		fmt.Sprintf("%d_vars.fd", rid),
		fmt.Sprintf("%d_tpm.log", rid),
		fmt.Sprintf("%d_tpm.state", rid),
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

func (s *Service) restoreVMDatabaseFromSnapshotJSON(
	ctx context.Context,
	rid uint,
	rootDatasets []string,
) error {
	if rid == 0 {
		return fmt.Errorf("invalid_rid")
	}

	metadataRaw, found, err := s.readVMSnapshotFileFromCandidates(ctx, rootDatasets, ".sylve/vm.json")
	if err != nil {
		return fmt.Errorf("failed_to_read_snapshot_vm_json: %w", err)
	}
	if !found {
		return fmt.Errorf("snapshot_vm_json_not_found")
	}

	var restored vmModels.VM
	if err := json.Unmarshal(metadataRaw, &restored); err != nil {
		return fmt.Errorf("invalid_snapshot_vm_json: %w", err)
	}

	normalizedPins, pinWarnings, err := s.normalizeRestoredCPUPinning(rid, restored.CPUPinning)
	if err != nil {
		return err
	}
	for _, warning := range pinWarnings {
		logger.L.Warn().
			Uint("rid", rid).
			Str("warning", warning).
			Msg("vm_snapshot_restore_cpu_pinning_warning")
	}
	restored.CPUPinning = normalizedPins

	normalizedPCI, pciWarnings, err := s.normalizeRestoredPCIDevices(rid, restored.PCIDevices)
	if err != nil {
		return err
	}
	for _, warning := range pciWarnings {
		logger.L.Warn().
			Uint("rid", rid).
			Str("warning", warning).
			Msg("vm_snapshot_restore_pci_warning")
	}
	restored.PCIDevices = normalizedPCI

	normalizedNetworks, networkWarnings, err := s.normalizeRestoredVMNetworks(restored.Networks)
	if err != nil {
		return err
	}
	for _, warning := range networkWarnings {
		logger.L.Warn().
			Uint("rid", rid).
			Str("warning", warning).
			Msg("vm_snapshot_restore_network_warning")
	}
	restored.Networks = normalizedNetworks

	current, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_current_vm: %w", err)
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_start_transaction: %w", tx.Error)
	}

	vmUpdate := vmModels.VM{
		Name:                   restored.Name,
		Description:            restored.Description,
		CPUSockets:             restored.CPUSockets,
		CPUCores:               restored.CPUCores,
		CPUThreads:             restored.CPUThreads,
		RAM:                    restored.RAM,
		TPMEmulation:           restored.TPMEmulation,
		ShutdownWaitTime:       restored.ShutdownWaitTime,
		Serial:                 restored.Serial,
		VNCEnabled:             restored.VNCEnabled,
		VNCPort:                restored.VNCPort,
		VNCPassword:            restored.VNCPassword,
		VNCResolution:          restored.VNCResolution,
		VNCWait:                restored.VNCWait,
		StartAtBoot:            restored.StartAtBoot,
		StartOrder:             restored.StartOrder,
		WoL:                    restored.WoL,
		TimeOffset:             restored.TimeOffset,
		PCIDevices:             restored.PCIDevices,
		ACPI:                   restored.ACPI,
		APIC:                   restored.APIC,
		CloudInitData:          restored.CloudInitData,
		CloudInitMetaData:      restored.CloudInitMetaData,
		CloudInitNetworkConfig: restored.CloudInitNetworkConfig,
		IgnoreUMSR:             restored.IgnoreUMSR,
		QemuGuestAgent:         restored.QemuGuestAgent,
	}

	if err := tx.Model(&vmModels.VM{}).
		Where("id = ?", current.ID).
		Select(
			"Name",
			"Description",
			"CPUSockets",
			"CPUCores",
			"CPUThreads",
			"RAM",
			"TPMEmulation",
			"ShutdownWaitTime",
			"Serial",
			"VNCEnabled",
			"VNCPort",
			"VNCPassword",
			"VNCResolution",
			"VNCWait",
			"StartAtBoot",
			"StartOrder",
			"WoL",
			"TimeOffset",
			"PCIDevices",
			"ACPI",
			"APIC",
			"CloudInitData",
			"CloudInitMetaData",
			"CloudInitNetworkConfig",
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
	if err := s.DB.Select("rid", "pci_devices").Where("rid <> ?", rid).Find(&otherVMs).Error; err != nil {
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
	networks []vmModels.Network,
) ([]vmModels.Network, []string, error) {
	if len(networks) == 0 {
		return []vmModels.Network{}, nil, nil
	}

	warnings := make([]string, 0)
	out := make([]vmModels.Network, 0, len(networks))

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

	if vm.CloudInitData != "" || vm.CloudInitMetaData != "" {
		if err := s.CreateCloudInitISO(vm); err != nil {
			return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
		}
	}

	domain, err := s.Conn.DomainLookupByName(fmt.Sprintf("%d", rid))
	if err == nil {
		if state, _, stateErr := s.Conn.DomainGetState(domain, 0); stateErr == nil {
			if state != int32(libvirt.DomainShutoff) {
				if destroyErr := s.Conn.DomainDestroy(domain); destroyErr != nil {
					lower := strings.ToLower(destroyErr.Error())
					if !strings.Contains(lower, "is not running") {
						return fmt.Errorf("failed_to_destroy_vm_domain_before_redefine: %w", destroyErr)
					}
				}
			}
		}

		if err := s.Conn.DomainUndefine(domain); err != nil {
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

	if _, err := s.Conn.DomainDefineXML(xml); err != nil {
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
