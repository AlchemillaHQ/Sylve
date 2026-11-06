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

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
)

// requireNoManagedGuestsWithinRestore prevents a generic dataset-mode restore
// from replacing a registered jail, VM, container, or download payload tree or its ancestors.
// Guest-mode restores have their own quiescence and reconciliation paths; a
// generic parent restore does not. Rejecting even stopped guests also closes the
// start-during-receive race without relying on an advisory in-process lock.
func (s *Service) requireNoManagedGuestsWithinRestore(
	ctx context.Context,
	destination string,
) error {
	destination = normalizeRestoreDestinationDataset(destination)
	if destination == "" {
		return fmt.Errorf("destination_dataset_required")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("restore_managed_guest_inventory_unavailable")
	}
	if err := downloadstorage.RequireDatasetUnused(ctx, s.DB, destination); err != nil {
		return err
	}

	blocking := make([]string, 0)
	var jails []jailModels.Jail
	if err := s.DB.Preload("Storages").Find(&jails).Error; err != nil {
		return fmt.Errorf("restore_jail_inventory_failed: %w", err)
	}
	for _, jail := range jails {
		if restoreDestinationContainsAnyDataset(destination, jailRestoreDatasetRoots(jail)) {
			blocking = append(blocking, "jail:"+strconv.FormatUint(uint64(jail.CTID), 10))
		}
	}

	var vms []vmModels.VM
	if err := s.DB.Preload("Storages.Dataset").Find(&vms).Error; err != nil {
		return fmt.Errorf("restore_vm_inventory_failed: %w", err)
	}
	for _, vm := range vms {
		if restoreDestinationContainsAnyDataset(destination, vmRestoreDatasetRoots(vm)) {
			blocking = append(blocking, "vm:"+strconv.FormatUint(uint64(vm.RID), 10))
		}
	}

	var containers []containerModels.Container
	if err := s.DB.Find(&containers).Error; err != nil {
		return fmt.Errorf("restore_container_inventory_failed: %w", err)
	}
	for _, container := range containers {
		if restoreDestinationContainsAnyDataset(destination, container.DatasetRoots()) {
			blocking = append(blocking, "container:"+strconv.FormatUint(uint64(container.OCID), 10))
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
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			continue
		}
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
