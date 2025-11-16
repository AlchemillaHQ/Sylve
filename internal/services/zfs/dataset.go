// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

func (s *Service) GetDatasets(t string) ([]*zfsServiceInterfaces.Dataset, error) {
	var datasets []*zfs.Dataset
	var err error

	if t == "" || t == "all" {
		datasets, err = zfs.Datasets("")
	} else if t == "filesystem" {
		datasets, err = zfs.Filesystems("")
	} else if t == "snapshot" {
		datasets, err = zfs.Snapshots("")
	} else if t == "volume" {
		datasets, err = zfs.Volumes("")
	}

	if err != nil {
		return nil, err
	}

	pools, err := s.GetUsablePools()
	if err != nil {
		return nil, err
	}

	if err != nil {
		return nil, err
	}

	var usablePools []string
	for _, pool := range pools {
		usablePools = append(usablePools, pool.Name)
	}

	var results []*zfsServiceInterfaces.Dataset

	for _, dataset := range datasets {
		dPool, err := s.PoolFromDataset(dataset.Name)
		if err != nil {
			logger.L.Err(err).Msgf("failed to get pool from dataset %s", dataset.Name)
			continue
		}

		if !utils.Contains(usablePools, dPool) {
			continue
		}

		results = append(results, &zfsServiceInterfaces.Dataset{
			Name:          dataset.Name,
			Origin:        dataset.Origin,
			GUID:          dataset.GUID,
			Used:          dataset.Used,
			Avail:         dataset.Avail,
			Recordsize:    dataset.Recordsize,
			Mountpoint:    dataset.Mountpoint,
			Compression:   dataset.Compression,
			Type:          dataset.Type,
			Written:       dataset.Written,
			Volsize:       dataset.Volsize,
			VolBlockSize:  dataset.VolBlockSize,
			Logicalused:   dataset.Logicalused,
			Usedbydataset: dataset.Usedbydataset,
			Quota:         dataset.Quota,
			Referenced:    dataset.Referenced,
			Mounted:       dataset.Mounted,
			Checksum:      dataset.Checksum,
			Dedup:         dataset.Dedup,
			ACLInherit:    dataset.ACLInherit,
			ACLMode:       dataset.ACLMode,
			PrimaryCache:  dataset.PrimaryCache,
			VolMode:       dataset.VolMode,
		})
	}

	return results, nil
}

func (s *Service) GetDatasetByGUID(guid string) (*zfs.Dataset, error) {
	datasets, err := zfs.Datasets("")
	if err != nil {
		return nil, err
	}

	for _, dataset := range datasets {
		if dataset.GUID == guid {
			return dataset, nil
		}
	}

	return nil, fmt.Errorf("dataset with guid %s not found", guid)
}

func (s *Service) GetSnapshotByGUID(guid string) (*zfs.Dataset, error) {
	datasets, err := zfs.Snapshots("")
	if err != nil {
		return nil, err
	}

	for _, dataset := range datasets {
		if dataset.GUID == guid {
			return dataset, nil
		}
	}

	return nil, fmt.Errorf("snapshot with guid %s not found", guid)
}

func (s *Service) GetFsOrVolByGUID(guid string) (*zfs.Dataset, error) {
	filesystems, err := zfs.Filesystems("")
	if err != nil {
		return nil, err
	}

	for _, fs := range filesystems {
		if fs.GUID == guid {
			return fs, nil
		}
	}

	volumes, err := zfs.Volumes("")
	if err != nil {
		return nil, err
	}

	for _, vol := range volumes {
		if vol.GUID == guid {
			return vol, nil
		}
	}

	return nil, fmt.Errorf("filesystem or volume with guid %s not found", guid)
}

func (s *Service) BulkDeleteDataset(guids []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	defer s.Libvirt.RescanStoragePools()

	var count int64
	if err := s.DB.Model(&vmModels.Storage{}).
		Joins("JOIN vm_storage_datasets ON vm_storage_datasets.id = vm_storages.dataset_id").
		Where("vm_storage_datasets.guid IN ?", guids).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if datasets are in use: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("datasets_in_use_by_vm")
	}

	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	available := make(map[string]*zfs.Dataset)
	for _, ds := range datasets {
		available[ds.GUID] = ds
	}

	for _, guid := range guids {
		if _, ok := available[guid]; !ok {
			return fmt.Errorf("dataset with guid %s not found", guid)
		}
	}

	for _, guid := range guids {
		if err := available[guid].Destroy(zfs.DestroyRecursive); err != nil {
			return fmt.Errorf("failed to delete dataset with guid %s: %w", guid, err)
		}
	}

	return nil
}

func (s *Service) IsDatasetInUse(guid string, failEarly bool) bool {
	var count int64
	if err := s.DB.Model(&vmModels.Storage{}).Where("dataset = ?", guid).
		Count(&count).Error; err != nil {
		return false
	}

	if count > 0 {
		if failEarly {
			return true
		}

		var storage vmModels.Storage

		if err := s.DB.Model(&vmModels.Storage{}).Where("dataset = ?", guid).
			First(&storage).Error; err != nil {
			return false
		}

		if storage.VMID > 0 {
			var vm vmModels.VM
			if err := s.DB.Model(&vmModels.VM{}).Where("id = ?", storage.VMID).
				First(&vm).Error; err != nil {
				return false
			}

			domain, err := s.Libvirt.GetLvDomain(vm.VmID)
			if err != nil {
				return false
			}

			if domain != nil {
				if domain.Status == "Running" || domain.Status == "Paused" {
					return true
				} else {
					return false
				}
			}
		}
	}

	return false
}
