// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) GetDatasets(ctx context.Context, t string) ([]*gzfs.Dataset, error) {
	var (
		datasets []*gzfs.Dataset
		err      error
	)

	switch t {
	case "", "all":
		datasets, err = s.GZFS.ZFS.List(ctx, true)
	case "filesystem":
		datasets, err = s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeFilesystem,
			true,
			"",
		)
	case "snapshot":
		datasets, err = s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeSnapshot,
			true,
			"",
		)
	case "volume":
		datasets, err = s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeVolume,
			true,
			"",
		)
	default:
		return nil, fmt.Errorf("unknown dataset type %q", t)
	}

	if err != nil {
		return nil, err
	}

	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return nil, err
	}

	usablePools := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		usablePools[pool.Name] = struct{}{}
	}

	filtered := make([]*gzfs.Dataset, 0, len(datasets))
	for _, dataset := range datasets {
		if dataset.Pool == "" {
			logger.L.Warn().Msgf("dataset %s has no pool associated!!", dataset.Name)
			continue
		}

		if _, ok := usablePools[dataset.Pool]; !ok {
			continue
		}

		filtered = append(filtered, dataset)
	}

	return filtered, nil
}

func (s *Service) BulkDeleteDataset(ctx context.Context, guids []string) error {
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

	datasets, err := s.GZFS.ZFS.List(
		ctx,
		true,
		"",
	)

	if err != nil {
		return err
	}

	available := make(map[string]*gzfs.Dataset)
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
		if err := available[guid].Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_dataset_with_guid_%s:_%w", guid, err)
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
