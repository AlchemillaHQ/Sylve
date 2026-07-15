// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

// requireNoManagedGuestsWithinRestore prevents a generic dataset-mode restore
// from replacing a registered jail or VM, or any of its dataset ancestors.
// Guest-mode restores have their own quiescence and reconciliation paths; a
// generic parent restore does not. Rejecting even stopped guests also closes the
// start-during-receive race without relying on an advisory in-process lock.
func (s *Service) requireNoManagedGuestsWithinRestore(
	ctx context.Context,
	destination string,
) error {
	_ = ctx
	destination = normalizeRestoreDestinationDataset(destination)
	if destination == "" {
		return fmt.Errorf("destination_dataset_required")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("restore_managed_guest_inventory_unavailable")
	}

	blocking := make([]string, 0)
	hasJails := s.DB.Migrator().HasTable(&jailModels.Jail{})
	hasJailStorages := s.DB.Migrator().HasTable(&jailModels.Storage{})
	if hasJails != hasJailStorages {
		return fmt.Errorf("restore_jail_inventory_unavailable")
	}
	if hasJails {
		var jails []jailModels.Jail
		if err := s.DB.Preload("Storages").Find(&jails).Error; err != nil {
			return fmt.Errorf("restore_jail_inventory_failed: %w", err)
		}
		for _, jail := range jails {
			if !restoreDestinationContainsAnyDataset(destination, jailRestoreDatasetRoots(jail)) {
				continue
			}
			blocking = append(blocking, "jail:"+strconv.FormatUint(uint64(jail.CTID), 10))
		}
	}

	hasVMs := s.DB.Migrator().HasTable(&vmModels.VM{})
	hasVMStorages := s.DB.Migrator().HasTable(&vmModels.Storage{})
	hasVMDatasets := s.DB.Migrator().HasTable(&vmModels.VMStorageDataset{})
	if (hasVMs || hasVMStorages || hasVMDatasets) && !(hasVMs && hasVMStorages && hasVMDatasets) {
		return fmt.Errorf("restore_vm_inventory_unavailable")
	}
	if hasVMs {
		var vms []vmModels.VM
		if err := s.DB.Preload("Storages.Dataset").Find(&vms).Error; err != nil {
			return fmt.Errorf("restore_vm_inventory_failed: %w", err)
		}
		for _, vm := range vms {
			if !restoreDestinationContainsAnyDataset(destination, vmRestoreDatasetRoots(vm)) {
				continue
			}
			blocking = append(blocking, "vm:"+strconv.FormatUint(uint64(vm.RID), 10))
		}
	}

	if len(blocking) == 0 {
		return nil
	}
	sort.Strings(blocking)
	return fmt.Errorf(
		"restore_destination_contains_managed_guests: dataset=%s guests=%s",
		destination,
		strings.Join(blocking, ","),
	)
}

func restoreDestinationContainsAnyDataset(destination string, candidates []string) bool {
	for _, candidate := range candidates {
		candidate = normalizeDatasetPath(candidate)
		if candidate != "" && datasetWithinRoot(destination, candidate) {
			return true
		}
	}
	return false
}

func jailRestoreDatasetRoots(jail jailModels.Jail) []string {
	roots := make([]string, 0, len(jail.Storages)*2)
	for _, storage := range jail.Storages {
		if name := normalizeDatasetPath(storage.Name); strings.Contains(name, "/") {
			roots = append(roots, name)
		}
		if pool := strings.TrimSpace(storage.Pool); pool != "" && jail.CTID > 0 {
			roots = append(roots, fmt.Sprintf("%s/sylve/jails/%d", pool, jail.CTID))
		}
	}
	return roots
}

func vmRestoreDatasetRoots(vm vmModels.VM) []string {
	roots := make([]string, 0, len(vm.Storages)*2)
	for _, storage := range vm.Storages {
		if name := normalizeDatasetPath(storage.Dataset.Name); strings.Contains(name, "/") {
			roots = append(roots, name)
		}
		pool := strings.TrimSpace(storage.Dataset.Pool)
		if pool == "" {
			pool = strings.TrimSpace(storage.Pool)
		}
		if pool != "" && vm.RID > 0 {
			roots = append(roots, fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID))
		}
	}
	return roots
}
