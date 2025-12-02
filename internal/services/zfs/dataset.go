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
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

func (s *Service) GetDatasets(t string) ([]*zfs.Dataset, error) {
	var (
		datasets []*zfs.Dataset
		err      error
	)

	switch t {
	case "", "all":
		datasets, err = zfs.Datasets("")
	case "filesystem":
		datasets, err = zfs.Filesystems("")
	case "snapshot":
		datasets, err = zfs.Snapshots("")
	case "volume":
		datasets, err = zfs.Volumes("")
	default:
		return nil, fmt.Errorf("unknown dataset type %q", t)
	}

	if err != nil {
		return nil, err
	}

	pools, err := s.GetUsablePools()
	if err != nil {
		return nil, err
	}

	usablePools := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		usablePools[pool.Name] = struct{}{}
	}

	filtered := make([]*zfs.Dataset, 0, len(datasets))
	for _, dataset := range datasets {
		dPool, err := s.PoolFromDataset(dataset.Name)
		if err != nil {
			logger.L.Err(err).Msgf("failed to get pool from dataset %s", dataset.Name)
			continue
		}

		if _, ok := usablePools[dPool]; !ok {
			continue
		}

		filtered = append(filtered, dataset)
	}

	return filtered, nil
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
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("guid IN ?", guids).
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

	cantDelete := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}

	for _, guid := range guids {
		if _, ok := available[guid]; !ok {
			return fmt.Errorf("dataset with guid %s not found", guid)
		}

		for _, name := range cantDelete {
			if strings.HasSuffix(available[guid].Name, name) {
				return fmt.Errorf("cannot_delete_critical_filesystem")
			}
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

			domain, err := s.Libvirt.GetLvDomain(vm.RID)
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
