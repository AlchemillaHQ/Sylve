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
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type VMRemovalResult struct {
	Warnings             []string `json:"warnings"`
	RetainedDatasets     []string `json:"retainedDatasets"`
	DeletedMACObjectIDs  []uint   `json:"deletedMacObjectIds"`
	RetainedMACObjectIDs []uint   `json:"retainedMacObjectIds"`
}

type vmStorageRemovalPlan struct {
	deleteDatasets       []string
	deleteRawDatasets    []string
	deleteZVOLDatasets   []string
	retainedDatasets     []string
	rootDatasets         []string
	preserveRoots        map[string]struct{}
	ownedSnapshotsByRoot map[string][]string
	warnings             []string
}

type vmRemovalPreparation struct {
	vm       vmModels.VM
	usedMACs []uint
	plan     vmStorageRemovalPlan
}

const vmRootDatasetDestroyMaxAttempts = 3

func (s *Service) RemoveVMWithWarnings(
	rid uint,
	cleanUpMacs bool,
	deleteRawDisks bool,
	deleteVolumes bool,
	ctx context.Context,
) (VMRemovalResult, error) {
	result := emptyVMRemovalResult()
	if s == nil || s.DB == nil {
		return result, fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 {
		return result, fmt.Errorf("invalid_vm_rid")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := s.RequireVMDeletionDetached(rid); err != nil {
		return result, err
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return result, err
	}

	var identityClaim *clusterServiceInterfaces.GuestIdentityReservation
	if s.guestIdentityCoordinator != nil {
		claim, claimErr := s.guestIdentityCoordinator.GuestIdentityClaim(ctx, clusterModels.ReplicationGuestTypeVM, rid, "")
		if claimErr != nil {
			return result, claimErr
		}
		identityClaim = &claim
		defer s.guestIdentityCoordinator.CancelGuestIdentityClaim(claim)
	}

	result, err := s.removeVMWithWarningsWithIdentity(
		rid,
		cleanUpMacs,
		deleteRawDisks,
		deleteVolumes,
		ctx,
		s.removeLvVmWithoutCRUDLock,
		identityClaim,
	)
	if err != nil {
		return result, err
	}
	if identityClaim == nil {
		return result, nil
	}
	if releaseErr := s.guestIdentityCoordinator.ReleaseGuestIdentities(ctx, *identityClaim); releaseErr != nil {
		return result, fmt.Errorf("guest_identity_release_pending: %w", releaseErr)
	}
	return result, nil
}

func emptyVMRemovalResult() VMRemovalResult {
	return VMRemovalResult{
		Warnings:             make([]string, 0),
		RetainedDatasets:     make([]string, 0),
		DeletedMACObjectIDs:  make([]uint, 0),
		RetainedMACObjectIDs: make([]uint, 0),
	}
}

func (s *Service) removeVMWithWarnings(
	rid uint,
	cleanUpMacs bool,
	deleteRawDisks bool,
	deleteVolumes bool,
	ctx context.Context,
	removeRuntime func(uint) error,
) (VMRemovalResult, error) {
	return s.removeVMWithWarningsWithIdentity(
		rid,
		cleanUpMacs,
		deleteRawDisks,
		deleteVolumes,
		ctx,
		removeRuntime,
		nil,
	)
}

func (s *Service) removeVMWithWarningsWithIdentity(
	rid uint,
	cleanUpMacs bool,
	deleteRawDisks bool,
	deleteVolumes bool,
	ctx context.Context,
	removeRuntime func(uint) error,
	identityClaim *clusterServiceInterfaces.GuestIdentityReservation,
) (VMRemovalResult, error) {
	result := emptyVMRemovalResult()
	if s == nil || s.DB == nil {
		return result, fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 {
		return result, fmt.Errorf("invalid_vm_rid")
	}
	if removeRuntime == nil {
		return result, fmt.Errorf("vm_runtime_remover_not_configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if identityClaim != nil {
		if err := s.guestIdentityCoordinator.ValidateGuestIdentityClaim(ctx, *identityClaim); err != nil {
			return result, fmt.Errorf("guest_identity_claim_revalidation_failed: %w", err)
		}
	}

	s.crudMutex.Lock()
	crudSectionHeld := true
	defer func() {
		if crudSectionHeld {
			s.crudMutex.Unlock()
		}
	}()

	s.actionMutex.Lock()
	actionSectionHeld := true
	defer func() {
		if actionSectionHeld {
			s.actionMutex.Unlock()
		}
	}()

	if err := requireVMDeletionDetachedDB(s.DB.WithContext(ctx), rid); err != nil {
		return result, err
	}

	prepared, err := s.prepareVMRemoval(ctx, rid, deleteRawDisks, deleteVolumes)
	if err != nil {
		return result, err
	}
	vm := prepared.vm
	usedMACs := prepared.usedMACs
	plan := prepared.plan
	result.RetainedDatasets = append(result.RetainedDatasets, plan.retainedDatasets...)

	if identityClaim != nil {
		if len(identityClaim.Entries) != 1 ||
			identityClaim.Entries[0].GuestKind != clusterModels.ReplicationGuestTypeVM ||
			identityClaim.Entries[0].GuestID != vm.RID ||
			strings.TrimSpace(identityClaim.OwnerNodeID) == "" {
			return result, fmt.Errorf("%w: vm_release_claim_mismatch", clusterModels.ErrGuestIdentityClaimConflict)
		}
		if identityClaim.Clustered {
			if strings.TrimSpace(identityClaim.Token) == "" {
				return result, fmt.Errorf("%w: vm_release_claim_token_missing", clusterModels.ErrGuestIdentityClaimConflict)
			}
		}
	}
	if err := removeRuntime(rid); err != nil {
		return emptyVMRemovalResult(), fmt.Errorf("failed_to_remove_lv_vm: %w", err)
	}

	if err := s.removeVMIdentityTransaction(ctx, vm); err != nil {
		return emptyVMRemovalResult(), err
	}
	s.actionMutex.Unlock()
	actionSectionHeld = false
	s.crudMutex.Unlock()
	crudSectionHeld = false

	result.Warnings = append(result.Warnings, plan.warnings...)
	storageWarnings, cleanupLeftovers := s.cleanupRequestedVMStorage(ctx, plan)
	result.Warnings = append(result.Warnings, storageWarnings...)
	for _, dataset := range cleanupLeftovers {
		appendUniqueString(&result.RetainedDatasets, dataset)
	}
	sort.Strings(result.RetainedDatasets)

	usedMACs = uniqueUintValues(usedMACs)
	if cleanUpMacs {
		if err := s.cleanupVMMACObjects(true, usedMACs); err != nil {
			result.RetainedMACObjectIDs = append(result.RetainedMACObjectIDs, usedMACs...)
			appendUniqueString(&result.Warnings, fmt.Sprintf("vm_cleanup_incomplete: mac_objects: %v", err))
			logger.L.Warn().Uint("rid", rid).Err(err).Msg("vm_mac_cleanup_incomplete_after_delete")
		} else {
			result.DeletedMACObjectIDs = append(result.DeletedMACObjectIDs, usedMACs...)
		}
	} else {
		result.RetainedMACObjectIDs = append(result.RetainedMACObjectIDs, usedMACs...)
	}
	slices.Sort(result.DeletedMACObjectIDs)
	slices.Sort(result.RetainedMACObjectIDs)

	logPath := fmt.Sprintf("/var/log/libvirt/bhyve/%d.log", rid)
	if err := utils.DeleteFileIfExists(logPath); err != nil {
		appendUniqueString(&result.Warnings, fmt.Sprintf("vm_cleanup_incomplete: log_file: %v", err))
		logger.L.Warn().Uint("rid", rid).Err(err).Str("path", logPath).Msg("vm_log_cleanup_incomplete_after_delete")
	}

	return result, nil
}

func (s *Service) prepareVMRemoval(
	ctx context.Context,
	rid uint,
	deleteRawDisks bool,
	deleteVolumes bool,
) (vmRemovalPreparation, error) {
	prepared := vmRemovalPreparation{
		usedMACs: make([]uint, 0),
	}
	if s == nil || s.DB == nil {
		return prepared, fmt.Errorf("libvirt_service_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := s.DB.WithContext(ctx).First(&prepared.vm, "rid = ?", rid).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return prepared, fmt.Errorf("vm_not_found: %d", rid)
		}
		return prepared, fmt.Errorf("failed_to_find_vm: %w", err)
	}
	if err := s.DB.WithContext(ctx).
		Preload("Dataset").
		Where("vm_id = ?", prepared.vm.ID).
		Find(&prepared.vm.Storages).Error; err != nil {
		return prepared, fmt.Errorf("failed_to_find_vm_storages: %w", err)
	}

	snapshots := make([]vmModels.VMSnapshot, 0)
	if err := s.DB.WithContext(ctx).
		Where("vm_id = ? OR rid = ?", prepared.vm.ID, prepared.vm.RID).
		Find(&snapshots).Error; err != nil {
		return prepared, fmt.Errorf("failed_to_find_vm_snapshots: %w", err)
	}
	if err := s.DB.WithContext(ctx).
		Model(&vmModels.Network{}).
		Where("vm_id = ? AND mac_id IS NOT NULL", prepared.vm.ID).
		Pluck("mac_id", &prepared.usedMACs).Error; err != nil {
		return prepared, fmt.Errorf("failed_to_find_vm_mac_ids: %w", err)
	}

	prepared.usedMACs = uniqueUintValues(prepared.usedMACs)
	prepared.plan = buildVMStorageRemovalPlan(prepared.vm, deleteRawDisks, deleteVolumes)
	s.discoverOrphanVMStorageRoots(ctx, prepared.vm.RID, &prepared.plan)
	addOwnedVMSnapshotsToRemovalPlan(&prepared.plan, snapshots)
	return prepared, nil
}

func (s *Service) removeVMIdentityTransaction(
	ctx context.Context,
	vm vmModels.VM,
) error {
	if vm.ID == 0 || vm.RID == 0 {
		return fmt.Errorf("invalid_vm_identity")
	}

	datasetIDs := make([]uint, 0, len(vm.Storages))
	for _, storage := range vm.Storages {
		if storage.DatasetID != nil {
			datasetIDs = append(datasetIDs, *storage.DatasetID)
		}
	}

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireVMDeletionDetachedDB(tx, vm.RID); err != nil {
			return err
		}
		if err := tx.Where("vm_id = ? OR rid = ?", vm.ID, vm.RID).
			Delete(&vmModels.VMSnapshot{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_snapshots: %w", err)
		}
		if err := tx.Where("vm_id = ?", vm.ID).Delete(&vmModels.VMStats{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_stats: %w", err)
		}
		if err := tx.Where("vm_id = ?", vm.ID).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_cpu_pinning: %w", err)
		}
		if err := tx.Where("vm_id = ?", vm.ID).Delete(&vmModels.Network{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_networks: %w", err)
		}
		if err := tx.Where("vm_id = ?", vm.ID).Delete(&vmModels.Storage{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_storages: %w", err)
		}

		deleteResult := tx.Where("id = ? AND rid = ?", vm.ID, vm.RID).Delete(&vmModels.VM{})
		if deleteResult.Error != nil {
			return fmt.Errorf("failed_to_delete_vm: %w", deleteResult.Error)
		}
		if deleteResult.RowsAffected != 1 {
			return fmt.Errorf("failed_to_delete_vm: vm_identity_changed")
		}

		var patternDatasetIDs []uint
		if err := tx.Model(&vmModels.VMStorageDataset{}).
			Where("name LIKE ? OR name LIKE ? OR name LIKE ? OR name LIKE ?",
				fmt.Sprintf("%%/sylve/virtual-machines/%d", vm.RID),
				fmt.Sprintf("%%/sylve/virtual-machines/%d/%%", vm.RID),
				fmt.Sprintf("%%/sylve/virtual-machines/%d.%%", vm.RID),
				fmt.Sprintf("%%/sylve/virtual-machines/%d\\_%%", vm.RID)).
			Pluck("id", &patternDatasetIDs).Error; err != nil {
			return fmt.Errorf("failed_to_lookup_vm_storage_dataset_rows: %w", err)
		}

		for _, datasetID := range uniqueUintValues(append(datasetIDs, patternDatasetIDs...)) {
			var refs int64
			if err := tx.Model(&vmModels.Storage{}).
				Where("dataset_id = ?", datasetID).
				Count(&refs).Error; err != nil {
				return fmt.Errorf("failed_to_count_storage_refs_for_dataset_%d: %w", datasetID, err)
			}
			if refs > 0 {
				continue
			}
			if err := tx.Delete(&vmModels.VMStorageDataset{}, datasetID).Error; err != nil {
				return fmt.Errorf("failed_to_delete_vm_storage_dataset_%d: %w", datasetID, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed_to_remove_vm_identity: %w", err)
	}

	return nil
}

func buildVMStorageRemovalPlan(
	vm vmModels.VM,
	deleteRawDisks bool,
	deleteVolumes bool,
) vmStorageRemovalPlan {
	plan := vmStorageRemovalPlan{
		deleteDatasets:       make([]string, 0),
		deleteRawDatasets:    make([]string, 0),
		deleteZVOLDatasets:   make([]string, 0),
		retainedDatasets:     make([]string, 0),
		rootDatasets:         make([]string, 0),
		preserveRoots:        make(map[string]struct{}),
		ownedSnapshotsByRoot: make(map[string][]string),
		warnings:             make([]string, 0),
	}
	deleteSet := make(map[string]struct{})
	retainedSet := make(map[string]struct{})
	rootSet := make(map[string]struct{})

	for _, storage := range vm.Storages {
		rootDataset := vmStorageRootDatasetForRemoval(storage, vm.RID)
		if rootDataset != "" && storage.Type != vmModels.VMStorageTypeDiskImage {
			rootSet[rootDataset] = struct{}{}
		}

		switch storage.Type {
		case vmModels.VMStorageTypeRaw, vmModels.VMStorageTypeZVol:
			datasetName := vmManagedStorageDatasetForRemoval(storage, vm.RID)
			preserve := shouldPreserveVMStorageRootDataset(storage.Type, deleteRawDisks, deleteVolumes)
			if preserve {
				if rootDataset != "" {
					plan.preserveRoots[rootDataset] = struct{}{}
					retainedSet[rootDataset] = struct{}{}
				}
				if datasetName != "" {
					retainedSet[datasetName] = struct{}{}
				}
				continue
			}

			if datasetName == "" {
				appendUniqueString(
					&plan.warnings,
					fmt.Sprintf("storage_cleanup_incomplete: unable_to_resolve_dataset: storage_id=%d", storage.ID),
				)
				continue
			}
			deleteSet[datasetName] = struct{}{}
			if storage.Type == vmModels.VMStorageTypeRaw {
				appendUniqueString(&plan.deleteRawDatasets, datasetName)
			} else {
				appendUniqueString(&plan.deleteZVOLDatasets, datasetName)
			}
		case vmModels.VMStorageTypeFilesystem:
			if rootDataset != "" {
				plan.preserveRoots[rootDataset] = struct{}{}
				retainedSet[rootDataset] = struct{}{}
			}
			if datasetName := strings.TrimSpace(storage.Dataset.Name); datasetName != "" {
				retainedSet[datasetName] = struct{}{}
			}
		}
	}

	for dataset := range deleteSet {
		plan.deleteDatasets = append(plan.deleteDatasets, dataset)
	}
	for dataset := range retainedSet {
		plan.retainedDatasets = append(plan.retainedDatasets, dataset)
	}
	for dataset := range rootSet {
		plan.rootDatasets = append(plan.rootDatasets, dataset)
	}
	sort.Strings(plan.deleteDatasets)
	sort.Strings(plan.deleteRawDatasets)
	sort.Strings(plan.deleteZVOLDatasets)
	sort.Strings(plan.retainedDatasets)
	sort.Strings(plan.rootDatasets)

	return plan
}

func addOwnedVMSnapshotsToRemovalPlan(plan *vmStorageRemovalPlan, snapshots []vmModels.VMSnapshot) {
	if plan == nil || len(plan.rootDatasets) == 0 || len(snapshots) == 0 {
		return
	}
	if plan.ownedSnapshotsByRoot == nil {
		plan.ownedSnapshotsByRoot = make(map[string][]string)
	}

	knownRoots := make(map[string]struct{}, len(plan.rootDatasets))
	for _, rootDataset := range plan.rootDatasets {
		knownRoots[rootDataset] = struct{}{}
	}

	for _, snapshot := range snapshots {
		snapshotName := strings.TrimSpace(snapshot.SnapshotName)
		if !strings.HasPrefix(snapshotName, "svms_") {
			continue
		}

		roots := snapshot.RootDatasets
		if len(roots) == 0 {
			roots = plan.rootDatasets
		}
		for _, rootDataset := range roots {
			rootDataset = strings.TrimSpace(rootDataset)
			if _, known := knownRoots[rootDataset]; !known {
				continue
			}
			names := plan.ownedSnapshotsByRoot[rootDataset]
			appendUniqueString(&names, snapshotName)
			plan.ownedSnapshotsByRoot[rootDataset] = names
		}
	}

	for rootDataset := range plan.ownedSnapshotsByRoot {
		sort.Strings(plan.ownedSnapshotsByRoot[rootDataset])
	}
}

func (s *Service) discoverOrphanVMStorageRoots(ctx context.Context, rid uint, plan *vmStorageRemovalPlan) {
	if s == nil || plan == nil || rid == 0 || s.System == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return
	}

	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		appendUniqueString(
			&plan.warnings,
			fmt.Sprintf("storage_cleanup_incomplete: canonical_root_discovery: %v", err),
		)
		return
	}

	knownRoots := make(map[string]struct{}, len(plan.rootDatasets))
	for _, rootDataset := range plan.rootDatasets {
		knownRoots[rootDataset] = struct{}{}
	}
	retainedSet := make(map[string]struct{}, len(plan.retainedDatasets))
	for _, dataset := range plan.retainedDatasets {
		retainedSet[dataset] = struct{}{}
	}

	for _, pool := range pools {
		if pool == nil || strings.TrimSpace(pool.Name) == "" {
			continue
		}
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", strings.TrimSpace(pool.Name), rid)
		if _, known := knownRoots[rootDataset]; known {
			continue
		}

		root, getErr := s.GZFS.ZFS.Get(ctx, rootDataset, false)
		if getErr != nil {
			if !isVMDatasetNotFoundError(getErr) {
				appendUniqueString(
					&plan.warnings,
					fmt.Sprintf("storage_cleanup_incomplete: canonical_root_discovery: dataset=%s: %v", rootDataset, getErr),
				)
			}
			continue
		}
		if root == nil {
			continue
		}

		knownRoots[rootDataset] = struct{}{}
		plan.rootDatasets = append(plan.rootDatasets, rootDataset)
		plan.preserveRoots[rootDataset] = struct{}{}
		if _, retained := retainedSet[rootDataset]; !retained {
			retainedSet[rootDataset] = struct{}{}
			plan.retainedDatasets = append(plan.retainedDatasets, rootDataset)
		}
	}

	sort.Strings(plan.rootDatasets)
	sort.Strings(plan.retainedDatasets)
}

func vmStorageRootDatasetForRemoval(storage vmModels.Storage, rid uint) string {
	pool := strings.TrimSpace(storage.Pool)
	if pool == "" {
		pool = strings.TrimSpace(storage.Dataset.Pool)
	}
	if pool == "" {
		datasetName := strings.TrimSpace(storage.Dataset.Name)
		if slash := strings.Index(datasetName, "/"); slash > 0 {
			pool = strings.TrimSpace(datasetName[:slash])
		}
	}
	if pool == "" || rid == 0 {
		return ""
	}
	return fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, rid)
}

func vmManagedStorageDatasetForRemoval(storage vmModels.Storage, rid uint) string {
	if datasetName := strings.TrimSpace(storage.Dataset.Name); datasetName != "" {
		return datasetName
	}
	rootDataset := vmStorageRootDatasetForRemoval(storage, rid)
	if rootDataset == "" || storage.ID == 0 {
		return ""
	}
	switch storage.Type {
	case vmModels.VMStorageTypeRaw:
		return fmt.Sprintf("%s/raw-%d", rootDataset, storage.ID)
	case vmModels.VMStorageTypeZVol:
		return fmt.Sprintf("%s/zvol-%d", rootDataset, storage.ID)
	default:
		return ""
	}
}

func (s *Service) cleanupRequestedVMStorage(ctx context.Context, plan vmStorageRemovalPlan) ([]string, []string) {
	warnings := make([]string, 0)
	leftovers := make([]string, 0)
	if !vmStorageRemovalPlanNeedsCleanup(plan) {
		return warnings, leftovers
	}
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		for _, dataset := range plan.deleteDatasets {
			appendUniqueString(&leftovers, dataset)
		}
		for _, rootDataset := range plan.rootDatasets {
			if _, preserve := plan.preserveRoots[rootDataset]; !preserve {
				appendUniqueString(&leftovers, rootDataset)
			}
		}
		sort.Strings(leftovers)
		return []string{"storage_cleanup_incomplete: zfs_client_not_initialized"}, leftovers
	}

	s.cleanupOwnedVMStorageSnapshots(ctx, plan, &warnings)

	for _, datasetName := range plan.deleteDatasets {
		dataset, err := s.GZFS.ZFS.Get(ctx, datasetName, false)
		if err != nil {
			if isVMDatasetNotFoundError(err) {
				continue
			}
			appendStorageCleanupWarning(&warnings, datasetName, err)
			appendUniqueString(&leftovers, datasetName)
			continue
		}
		if dataset == nil {
			continue
		}
		if err := dataset.Destroy(ctx, true, false); err != nil {
			appendStorageCleanupWarning(&warnings, datasetName, err)
			appendUniqueString(&leftovers, datasetName)
		}
	}

	for _, rootDataset := range plan.rootDatasets {
		if _, preserve := plan.preserveRoots[rootDataset]; preserve {
			continue
		}

		hasChildren, destroyErr := s.destroyEmptyVMRootDataset(ctx, rootDataset)
		if destroyErr != nil {
			if !isVMDatasetNotFoundError(destroyErr) {
				appendStorageCleanupWarning(&warnings, rootDataset, destroyErr)
				appendUniqueString(&leftovers, rootDataset)
			}
			continue
		}
		if hasChildren {
			appendUniqueString(
				&warnings,
				fmt.Sprintf("storage_cleanup_incomplete: root_not_empty: %s", rootDataset),
			)
			appendUniqueString(&leftovers, rootDataset)
		}
	}

	sort.Strings(leftovers)
	return warnings, leftovers
}

func (s *Service) destroyEmptyVMRootDataset(ctx context.Context, rootDataset string) (bool, error) {
	rootDataset = strings.TrimSpace(rootDataset)
	if rootDataset == "" {
		return false, fmt.Errorf("vm_root_dataset_required")
	}
	var lastErr error
	for attempt := 0; attempt < vmRootDatasetDestroyMaxAttempts; attempt++ {
		hasChildren, err := s.vmRootDatasetHasChildren(ctx, rootDataset)
		if err != nil {
			return false, err
		}
		if hasChildren {
			return true, nil
		}

		snapshots, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, rootDataset, true)
		if err != nil {
			return false, err
		}
		directSnapshots := make([]*gzfs.Dataset, 0, len(snapshots))
		for _, snapshot := range snapshots {
			if snapshot == nil {
				continue
			}
			name := strings.TrimSpace(snapshot.Name)
			separator := strings.LastIndex(name, "@")
			if separator <= 0 || name[:separator] != rootDataset {
				continue
			}
			directSnapshots = append(directSnapshots, snapshot)
		}
		sort.Slice(directSnapshots, func(i, j int) bool {
			return directSnapshots[i].Name < directSnapshots[j].Name
		})
		for _, snapshot := range directSnapshots {
			if err := snapshot.Destroy(ctx, false, false); err != nil {
				if isVMDatasetNotFoundError(err) {
					continue
				}
				return false, fmt.Errorf("vm_root_snapshot_destroy_failed: %s: %w", snapshot.Name, err)
			}
		}

		hasChildren, err = s.vmRootDatasetHasChildren(ctx, rootDataset)
		if err != nil {
			return false, err
		}
		if hasChildren {
			return true, nil
		}

		root, err := s.GZFS.ZFS.Get(ctx, rootDataset, false)
		if err != nil {
			return false, err
		}
		if root == nil {
			return false, nil
		}
		if err := root.Destroy(ctx, false, false); err == nil {
			return false, nil
		} else if !vmRootDatasetDestroyRetryable(err) {
			return false, err
		} else {
			lastErr = err
		}
	}
	return false, lastErr
}

func vmRootDatasetDestroyRetryable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "filesystem has children") ||
		strings.Contains(text, "dataset has children") ||
		strings.Contains(text, "snapshot") && strings.Contains(text, "exist")
}

func vmStorageRemovalPlanNeedsCleanup(plan vmStorageRemovalPlan) bool {
	if len(plan.deleteDatasets) > 0 {
		return true
	}
	for _, rootDataset := range plan.rootDatasets {
		if _, preserve := plan.preserveRoots[rootDataset]; !preserve {
			return true
		}
	}
	return false
}

func (s *Service) cleanupOwnedVMStorageSnapshots(
	ctx context.Context,
	plan vmStorageRemovalPlan,
	warnings *[]string,
) {
	for _, rootDataset := range plan.rootDatasets {
		if _, preserve := plan.preserveRoots[rootDataset]; preserve {
			continue
		}

		for _, snapshotName := range plan.ownedSnapshotsByRoot[rootDataset] {
			targets, err := s.listRecursiveRollbackTargets(ctx, rootDataset, snapshotName)
			if err != nil {
				if !isVMDatasetNotFoundError(err) {
					appendStorageCleanupWarning(
						warnings,
						fmt.Sprintf("%s@%s", rootDataset, snapshotName),
						err,
					)
				}
				continue
			}

			sort.SliceStable(targets, func(i, j int) bool {
				leftDepth := snapshotDatasetDepth(targets[i])
				rightDepth := snapshotDatasetDepth(targets[j])
				if leftDepth != rightDepth {
					return leftDepth > rightDepth
				}
				return targets[i] < targets[j]
			})

			for _, fullSnapshot := range targets {
				dataset, getErr := s.GZFS.ZFS.Get(ctx, fullSnapshot, false)
				if getErr != nil {
					if !isVMDatasetNotFoundError(getErr) {
						appendStorageCleanupWarning(warnings, fullSnapshot, getErr)
					}
					continue
				}
				if dataset == nil {
					continue
				}
				if destroyErr := dataset.Destroy(ctx, false, false); destroyErr != nil {
					appendStorageCleanupWarning(warnings, fullSnapshot, destroyErr)
				}
			}
		}
	}
}

func (s *Service) vmRootDatasetHasChildren(ctx context.Context, rootDataset string) (bool, error) {
	for _, datasetType := range []gzfs.DatasetType{gzfs.DatasetTypeFilesystem, gzfs.DatasetTypeVolume} {
		datasets, err := s.GZFS.ZFS.ListByType(ctx, datasetType, true, rootDataset)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "operation not applicable to datasets of this type") {
				continue
			}
			return false, err
		}
		for _, dataset := range datasets {
			if dataset != nil && strings.HasPrefix(strings.TrimSpace(dataset.Name), rootDataset+"/") {
				return true, nil
			}
		}
	}
	return false, nil
}

func appendStorageCleanupWarning(warnings *[]string, dataset string, err error) {
	warning := fmt.Sprintf("storage_cleanup_incomplete: dataset=%s", dataset)
	if err != nil {
		warning = fmt.Sprintf("%s: %v", warning, err)
	}
	appendUniqueString(warnings, warning)
	logger.L.Warn().Str("dataset", dataset).Err(err).Msg("vm_storage_cleanup_incomplete_after_delete")
}

func appendUniqueString(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}
