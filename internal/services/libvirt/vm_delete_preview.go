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
	"sort"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

type VMRemovalRegistrationPreview struct {
	RID  uint   `json:"rid"`
	Name string `json:"name"`
}

type VMRemovalPreview struct {
	Registration            VMRemovalRegistrationPreview `json:"registration"`
	DeleteMACObjectIDs      []uint                       `json:"deleteMacObjectIds"`
	RetainMACObjectIDs      []uint                       `json:"retainMacObjectIds"`
	DeleteRawDatasets       []string                     `json:"deleteRawDatasets"`
	DeleteZVOLDatasets      []string                     `json:"deleteZvolDatasets"`
	DeleteContainerDatasets []string                     `json:"deleteContainerDatasets"`
	DeleteSnapshots         []string                     `json:"deleteSnapshots"`
	RetainedDatasets        []string                     `json:"retainedDatasets"`
	RetainedImageUUIDs      []string                     `json:"retainedImageUuids"`
	Warnings                []string                     `json:"warnings"`
}

func emptyVMRemovalPreview() VMRemovalPreview {
	return VMRemovalPreview{
		DeleteMACObjectIDs:      make([]uint, 0),
		RetainMACObjectIDs:      make([]uint, 0),
		DeleteRawDatasets:       make([]string, 0),
		DeleteZVOLDatasets:      make([]string, 0),
		DeleteContainerDatasets: make([]string, 0),
		DeleteSnapshots:         make([]string, 0),
		RetainedDatasets:        make([]string, 0),
		RetainedImageUUIDs:      make([]string, 0),
		Warnings:                make([]string, 0),
	}
}

func (s *Service) PreviewVMRemoval(
	rid uint,
	cleanUpMacs bool,
	deleteRawDisks bool,
	deleteVolumes bool,
	ctx context.Context,
) (VMRemovalPreview, error) {
	preview := emptyVMRemovalPreview()
	if s == nil || s.DB == nil {
		return preview, fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 || rid > 9999 {
		return preview, fmt.Errorf("invalid_vm_rid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.RequireVMDeletionDetached(rid); err != nil {
		return preview, err
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return preview, err
	}

	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if err := requireVMDeletionDetachedDB(s.DB.WithContext(ctx), rid); err != nil {
		return preview, err
	}
	prepared, err := s.prepareVMRemoval(ctx, rid, deleteRawDisks, deleteVolumes)
	if err != nil {
		return preview, err
	}

	preview.Registration = VMRemovalRegistrationPreview{
		RID: prepared.vm.RID, Name: prepared.vm.Name,
	}
	if cleanUpMacs {
		preview.DeleteMACObjectIDs = append(preview.DeleteMACObjectIDs, prepared.usedMACs...)
	} else {
		preview.RetainMACObjectIDs = append(preview.RetainMACObjectIDs, prepared.usedMACs...)
	}
	preview.DeleteRawDatasets = append(preview.DeleteRawDatasets, prepared.plan.deleteRawDatasets...)
	preview.DeleteZVOLDatasets = append(preview.DeleteZVOLDatasets, prepared.plan.deleteZVOLDatasets...)
	preview.RetainedDatasets = append(preview.RetainedDatasets, prepared.plan.retainedDatasets...)
	preview.Warnings = append(preview.Warnings, prepared.plan.warnings...)

	for _, root := range prepared.plan.rootDatasets {
		if _, preserved := prepared.plan.preserveRoots[root]; !preserved {
			appendUniqueString(&preview.DeleteContainerDatasets, root)
		}
	}
	if vmStorageRemovalPlanNeedsCleanup(prepared.plan) {
		for root, snapshotNames := range prepared.plan.ownedSnapshotsByRoot {
			for _, snapshotName := range snapshotNames {
				appendUniqueString(&preview.DeleteSnapshots, root+"@"+snapshotName)
			}
		}
	}
	for _, storage := range prepared.vm.Storages {
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			appendUniqueString(&preview.RetainedImageUUIDs, strings.TrimSpace(storage.DownloadUUID))
		}
	}

	sort.Slice(preview.DeleteMACObjectIDs, func(i, j int) bool {
		return preview.DeleteMACObjectIDs[i] < preview.DeleteMACObjectIDs[j]
	})
	sort.Slice(preview.RetainMACObjectIDs, func(i, j int) bool {
		return preview.RetainMACObjectIDs[i] < preview.RetainMACObjectIDs[j]
	})
	sort.Strings(preview.DeleteContainerDatasets)
	sort.Strings(preview.DeleteSnapshots)
	sort.Strings(preview.RetainedImageUUIDs)
	sort.Strings(preview.Warnings)
	return preview, nil
}
