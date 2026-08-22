// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"sort"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"gorm.io/gorm"
)

const (
	VMStorageOwnershipManaged  = "managed"
	VMStorageOwnershipExternal = "external"
	VMStorageOwnershipRetained = "retained"
)

type VMStorageInfo struct {
	ID               uint                                          `json:"id"`
	Name             string                                        `json:"name"`
	Type             libvirtServiceInterfaces.StorageType          `json:"type"`
	Emulation        libvirtServiceInterfaces.StorageEmulationType `json:"emulation"`
	Pool             string                                        `json:"pool"`
	Size             int64                                         `json:"size"`
	Enabled          bool                                          `json:"enabled"`
	BootOrder        int                                           `json:"bootOrder"`
	FilesystemTarget string                                        `json:"filesystemTarget"`
	ReadOnly         bool                                          `json:"readOnly"`
	DatasetGUID      string                                        `json:"datasetGuid"`
	DownloadUUID     string                                        `json:"downloadUuid"`
	Backing          string                                        `json:"backing"`
	Ownership        string                                        `json:"ownership"`
	DeleteWithVMFlag string                                        `json:"deleteWithVmFlag"`
}

func (s *Service) ListVMStorage(rid uint) ([]VMStorageInfo, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	if rid == 0 || rid > 9999 {
		return nil, fmt.Errorf("invalid_rid")
	}

	var vm vmModels.VM
	if err := s.DB.
		Preload("Storages").
		Preload("Storages.Dataset").
		Where("rid = ?", rid).
		First(&vm).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("vm_not_found: %d", rid)
		}
		return nil, fmt.Errorf("failed_to_find_vm: %w", err)
	}

	result := make([]VMStorageInfo, 0, len(vm.Storages))
	for _, storage := range vm.Storages {
		result = append(result, DescribeVMStorage(rid, storage))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func DescribeVMStorage(rid uint, storage vmModels.Storage) VMStorageInfo {
	info := VMStorageInfo{
		ID:               storage.ID,
		Name:             storage.Name,
		Type:             libvirtServiceInterfaces.StorageType(storage.Type),
		Emulation:        libvirtServiceInterfaces.StorageEmulationType(storage.Emulation),
		Pool:             storage.Pool,
		Size:             storage.Size,
		Enabled:          storage.Enable,
		BootOrder:        storage.BootOrder,
		FilesystemTarget: storage.FilesystemTarget,
		ReadOnly:         storage.ReadOnly,
		DatasetGUID:      storage.Dataset.GUID,
		DownloadUUID:     storage.DownloadUUID,
	}

	switch storage.Type {
	case vmModels.VMStorageTypeRaw:
		info.Backing = vmManagedStorageDatasetForRemoval(storage, rid)
		info.Ownership = VMStorageOwnershipManaged
		info.DeleteWithVMFlag = "--delete-raw-disks"
	case vmModels.VMStorageTypeZVol:
		info.Backing = vmManagedStorageDatasetForRemoval(storage, rid)
		info.Ownership = VMStorageOwnershipManaged
		info.DeleteWithVMFlag = "--delete-volumes"
	case vmModels.VMStorageTypeDiskImage:
		info.Backing = strings.TrimSpace(storage.DownloadUUID)
		info.Ownership = VMStorageOwnershipRetained
	case vmModels.VMStorageTypeFilesystem:
		info.Backing = strings.TrimSpace(storage.Dataset.Name)
		info.Ownership = VMStorageOwnershipExternal
	default:
		info.Backing = strings.TrimSpace(storage.Dataset.Name)
		info.Ownership = VMStorageOwnershipRetained
	}
	return info
}
