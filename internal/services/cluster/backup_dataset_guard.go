// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"gorm.io/gorm"
)

type managedGuestDataset struct {
	guestType string
	guestID   uint
	dataset   string
}

func normalizeManagedGuestDatasetPath(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return ""
	}

	parts := strings.Split(dataset, "/")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case ".", "..":
			return ""
		default:
			normalized = append(normalized, part)
		}
	}
	return strings.Join(normalized, "/")
}

func datasetPathEqualOrAncestor(candidateAncestor, dataset string) bool {
	candidateAncestor = normalizeManagedGuestDatasetPath(candidateAncestor)
	dataset = normalizeManagedGuestDatasetPath(dataset)
	if candidateAncestor == "" || dataset == "" {
		return false
	}
	return candidateAncestor == dataset || strings.HasPrefix(dataset, candidateAncestor+"/")
}

func backupSourceMayContainManagedGuest(sourceDataset string) bool {
	parts := strings.Split(normalizeManagedGuestDatasetPath(sourceDataset), "/")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	if len(parts) == 1 {
		// A whole-pool backup could contain any managed guest namespace.
		return true
	}
	if parts[1] != "sylve" {
		return false
	}
	if len(parts) == 2 {
		return true
	}

	switch parts[2] {
	case "jails":
		// Jail inventory currently owns the canonical jail root only. A
		// descendant cannot contain that root.
		return len(parts) <= 4
	case "virtual-machines":
		// VM storage datasets can live below the canonical VM root, so
		// inventory is required for every path in this namespace.
		return true
	default:
		return false
	}
}

func isReservedManagedBackupScope(sourceDataset string) bool {
	parts := strings.Split(normalizeManagedGuestDatasetPath(sourceDataset), "/")
	if len(parts) == 1 && parts[0] != "" {
		return true
	}
	if len(parts) < 2 || parts[0] == "" || parts[1] != "sylve" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	if parts[2] != "jails" && parts[2] != "virtual-machines" {
		return false
	}
	if len(parts) == 3 {
		return true
	}
	if len(parts) != 4 {
		return false
	}

	id, err := strconv.ParseUint(parts[3], 10, 64)
	return err == nil && id > 0
}

func canonicalManagedGuestRootID(dataset, namespace string) (uint, bool) {
	parts := strings.Split(normalizeManagedGuestDatasetPath(dataset), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "sylve" || parts[2] != namespace {
		return 0, false
	}

	id, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return 0, false
	}
	return uint(id), true
}

func addManagedGuestDataset(
	entries map[string]managedGuestDataset,
	guestType string,
	guestID uint,
	dataset string,
) {
	dataset = normalizeManagedGuestDatasetPath(dataset)
	if guestID == 0 || dataset == "" {
		return
	}
	key := guestType + ":" + strconv.FormatUint(uint64(guestID), 10) + ":" + dataset
	entries[key] = managedGuestDataset{guestType: guestType, guestID: guestID, dataset: dataset}
}

func (s *Service) managedGuestDatasets(ctx context.Context) ([]managedGuestDataset, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("managed_guest_dataset_inventory_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	entries := make(map[string]managedGuestDataset)

	var jails []jailModels.Jail
	if err := s.DB.WithContext(ctx).Preload("Storages").Find(&jails).Error; err != nil {
		return nil, fmt.Errorf("managed_jail_dataset_inventory_failed: %w", err)
	}
	for _, jail := range jails {
		if jail.CTID == 0 {
			return nil, fmt.Errorf("managed_jail_dataset_identity_invalid")
		}
		resolvedRoot := false
		for _, storage := range jail.Storages {
			if !storage.IsBase {
				continue
			}
			pool := normalizeManagedGuestDatasetPath(storage.Pool)
			if pool == "" {
				continue
			}
			resolvedRoot = true
			addManagedGuestDataset(
				entries,
				clusterModels.BackupJobModeJail,
				jail.CTID,
				fmt.Sprintf("%s/sylve/jails/%d", pool, jail.CTID),
			)
		}
		if !resolvedRoot {
			return nil, fmt.Errorf("managed_jail_root_dataset_unresolved: jail_id=%d", jail.CTID)
		}
	}

	var vms []vmModels.VM
	if err := s.DB.WithContext(ctx).
		Preload("Storages").
		Preload("Storages.Dataset").
		Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("managed_vm_dataset_inventory_failed: %w", err)
	}
	for _, vm := range vms {
		if vm.RID == 0 {
			return nil, fmt.Errorf("managed_vm_dataset_identity_invalid")
		}
		resolvedRoot := false
		for _, storage := range vm.Storages {
			pool := normalizeManagedGuestDatasetPath(storage.Pool)
			if pool == "" {
				pool = normalizeManagedGuestDatasetPath(storage.Dataset.Pool)
			}
			if pool != "" {
				resolvedRoot = true
				addManagedGuestDataset(
					entries,
					clusterModels.BackupJobModeVM,
					vm.RID,
					fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID),
				)
			}
			addManagedGuestDataset(
				entries,
				clusterModels.BackupJobModeVM,
				vm.RID,
				storage.Dataset.Name,
			)
		}
		if !resolvedRoot {
			return nil, fmt.Errorf("managed_vm_root_dataset_unresolved: vm_id=%d", vm.RID)
		}
	}

	result := make([]managedGuestDataset, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].dataset != result[j].dataset {
			return result[i].dataset < result[j].dataset
		}
		if result[i].guestType != result[j].guestType {
			return result[i].guestType < result[j].guestType
		}
		return result[i].guestID < result[j].guestID
	})
	return result, nil
}

// ValidateDatasetBackupSource rejects dataset-mode backup roots that would
// contain a managed jail or VM. Such workloads must use their guest-aware
// backup mode so lifecycle and restore safety checks cannot be bypassed.
// The exported method is also used by runtime execution to revalidate jobs
// created before this guard existed.
func (s *Service) ValidateDatasetBackupSource(ctx context.Context, sourceDataset string) error {
	sourceDataset = normalizeManagedGuestDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return fmt.Errorf("invalid_dataset_backup_source")
	}
	if isReservedManagedBackupScope(sourceDataset) {
		return fmt.Errorf("dataset_backup_source_reserved_managed_scope: source=%s", sourceDataset)
	}
	if !backupSourceMayContainManagedGuest(sourceDataset) {
		return nil
	}

	managed, err := s.managedGuestDatasets(ctx)
	if err != nil {
		return err
	}
	for _, entry := range managed {
		if !datasetPathEqualOrAncestor(sourceDataset, entry.dataset) {
			continue
		}
		return fmt.Errorf(
			"dataset_backup_source_contains_managed_guest: source=%s guest_type=%s guest_id=%d dataset=%s",
			sourceDataset,
			entry.guestType,
			entry.guestID,
			entry.dataset,
		)
	}
	return nil
}

// ValidateJailBackupRoot requires one exact canonical root derived from a
// registered jail's base-storage pool and CTID. Parent roots and stale CTIDs
// are deliberately rejected instead of being treated as broad jail sets.
func (s *Service) ValidateJailBackupRoot(ctx context.Context, jailRootDataset string) error {
	jailRootDataset = normalizeManagedGuestDatasetPath(jailRootDataset)
	if jailRootDataset == "" {
		return fmt.Errorf("jail_root_dataset_required")
	}
	jailID, canonical := canonicalManagedGuestRootID(jailRootDataset, "jails")
	if !canonical {
		return fmt.Errorf(
			"jail_backup_requires_registered_canonical_root: dataset=%s",
			jailRootDataset,
		)
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("managed_jail_dataset_inventory_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var jail jailModels.Jail
	if err := s.DB.WithContext(ctx).
		Preload("Storages").
		Where(&jailModels.Jail{CTID: jailID}).
		First(&jail).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf(
				"jail_backup_requires_registered_canonical_root: dataset=%s",
				jailRootDataset,
			)
		}
		return fmt.Errorf("managed_jail_dataset_inventory_failed: %w", err)
	}
	for _, storage := range jail.Storages {
		if !storage.IsBase {
			continue
		}
		pool := normalizeManagedGuestDatasetPath(storage.Pool)
		if pool != "" && fmt.Sprintf("%s/sylve/jails/%d", pool, jail.CTID) == jailRootDataset {
			return nil
		}
	}
	return fmt.Errorf(
		"jail_backup_requires_registered_canonical_root: dataset=%s",
		jailRootDataset,
	)
}

// ValidateVMBackupRoot requires recursive replication from one exact canonical
// root derived from a registered VM's storage pool and RID. This prevents a VM
// job from silently omitting child disk datasets or targeting a jail/broad root.
func (s *Service) ValidateVMBackupRoot(ctx context.Context, sourceDataset string, recursive bool) error {
	if !recursive {
		return fmt.Errorf("vm_backup_requires_recursive")
	}

	sourceDataset = normalizeManagedGuestDatasetPath(sourceDataset)
	vmID, canonical := canonicalManagedGuestRootID(sourceDataset, "virtual-machines")
	if !canonical {
		return fmt.Errorf(
			"vm_backup_requires_registered_canonical_root: dataset=%s",
			sourceDataset,
		)
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("managed_vm_dataset_inventory_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var vm vmModels.VM
	if err := s.DB.WithContext(ctx).
		Preload("Storages").
		Preload("Storages.Dataset").
		Where(&vmModels.VM{RID: vmID}).
		First(&vm).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf(
				"vm_backup_requires_registered_canonical_root: dataset=%s",
				sourceDataset,
			)
		}
		return fmt.Errorf("managed_vm_dataset_inventory_failed: %w", err)
	}
	for _, storage := range vm.Storages {
		pool := normalizeManagedGuestDatasetPath(storage.Pool)
		if pool == "" {
			pool = normalizeManagedGuestDatasetPath(storage.Dataset.Pool)
		}
		if pool != "" && fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID) == sourceDataset {
			return nil
		}
	}

	return fmt.Errorf(
		"vm_backup_requires_registered_canonical_root: dataset=%s",
		sourceDataset,
	)
}

// ValidateBackupJobSafety is the shared create/update and runtime contract for
// backup mode boundaries. Runtime callers must use it as well so legacy jobs
// cannot bypass newer validation.
func (s *Service) ValidateBackupJobSafety(ctx context.Context, job *clusterModels.BackupJob) error {
	if job == nil {
		return fmt.Errorf("backup_job_required")
	}

	switch strings.ToLower(strings.TrimSpace(job.Mode)) {
	case clusterModels.BackupJobModeDataset:
		return s.ValidateDatasetBackupSource(ctx, job.SourceDataset)
	case clusterModels.BackupJobModeJail:
		return s.ValidateJailBackupRoot(ctx, job.JailRootDataset)
	case clusterModels.BackupJobModeVM:
		return s.ValidateVMBackupRoot(ctx, job.SourceDataset, job.Recursive)
	default:
		return fmt.Errorf("invalid_backup_job_mode")
	}
}

// ValidateBackupJobSafetyWithDB lets execution services revalidate persisted
// jobs without depending on a fully initialized cluster Service instance.
func ValidateBackupJobSafetyWithDB(ctx context.Context, db *gorm.DB, job *clusterModels.BackupJob) error {
	return (&Service{DB: db}).ValidateBackupJobSafety(ctx, job)
}
