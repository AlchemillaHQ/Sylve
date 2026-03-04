// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) checkPoolUsage(poolName string) error {
	var count int64

	if err := s.DB.Model(&vmModels.Storage{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to query vm_storages for pool %s: %w", poolName, err)
	}
	if count > 0 {
		return fmt.Errorf("removing_pool_not_allowed_in_use_by_vm_storage: %s", poolName)
	}

	if err := s.DB.Model(&vmModels.VMStorageDataset{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to query vm_storage_datasets for pool %s: %w", poolName, err)
	}
	if count > 0 {
		return fmt.Errorf("removing_pool_not_allowed_in_use_by_vm_dataset: %s", poolName)
	}

	if err := s.DB.Model(&jailModels.Storage{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to query jail_storages for pool %s: %w", poolName, err)
	}
	if count > 0 {
		return fmt.Errorf("removing_pool_not_allowed_in_use_by_jail_storage: %s", poolName)
	}

	if err := s.DB.Model(&zfsModels.PeriodicSnapshot{}).Where("pool = ?", poolName).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to query periodic_snapshots for pool %s: %w", poolName, err)
	}
	if count > 0 {
		return fmt.Errorf("removing_pool_not_allowed_in_use_by_periodic_snapshot: %s", poolName)
	}

	return nil
}

func (s *Service) AddUsablePools(ctx context.Context, pools []string) error {
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return err
	}

	zpools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return err
	}

	zfsSet := make(map[string]struct{}, len(zpools))
	for _, p := range zpools {
		zfsSet[p] = struct{}{}
	}

	existingSet := make(map[string]struct{}, len(basicSettings.Pools))
	for _, p := range basicSettings.Pools {
		existingSet[p] = struct{}{}
	}

	for _, p := range pools {
		if _, ok := zfsSet[p]; !ok {
			return fmt.Errorf("zfs_pool_not_found: %s", p)
		}
	}

	for p := range existingSet {
		found := false
		for _, incoming := range pools {
			if incoming == p {
				found = true
				break
			}
		}

		if !found {
			if err := s.checkPoolUsage(p); err != nil {
				return err
			}
		}
	}

	toCreate := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}
	var newSets []*gzfs.Dataset

	for _, poolName := range pools {
		for _, dataset := range toCreate {
			datasetPath := fmt.Sprintf("%s/%s", poolName, dataset)
			_, err := s.GZFS.ZFS.Get(ctx, datasetPath, false)
			if err != nil {
				created, err := s.GZFS.ZFS.CreateFilesystem(ctx, datasetPath, nil)
				if err != nil {
					for i := len(newSets) - 1; i >= 0; i-- {
						newSets[i].Destroy(ctx, true, false)
					}
					return fmt.Errorf("failed_to_create_dataset_%s: %w", datasetPath, err)
				}

				newSets = append(newSets, created)
			}
		}
	}

	basicSettings.Pools = pools
	return s.DB.Save(&basicSettings).Error
}

func (s *Service) ToggleDHCPServer(enable bool) error {
	if enable {
		if !pkg.IsPackageInstalled("dnsmasq") {
			return fmt.Errorf("dnsmasq package is not installed")
		}

		_, err := utils.RunCommand("/usr/sbin/service", "dnsmasq", "start")
		if err != nil {
			if !strings.Contains(err.Error(), "already running") {
				return err
			}
		}
	} else {
		_, err := utils.RunCommand("/usr/sbin/service", "dnsmasq", "stop")
		return err
	}

	return nil
}

func (s *Service) ServiceToggle(service models.AvailableService) error {
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return err
	}

	serviceSet := make(map[models.AvailableService]bool)
	for _, srv := range basicSettings.Services {
		serviceSet[srv] = true
	}

	enabled := !serviceSet[service]
	serviceSet[service] = enabled
	if !enabled {
		delete(serviceSet, service)
	}

	switch service {
	case models.DHCPServer:
		if err := s.ToggleDHCPServer(enabled); err != nil {
			return err
		}
	}

	basicSettings.Services = basicSettings.Services[:0]
	for srv := range serviceSet {
		basicSettings.Services = append(basicSettings.Services, srv)
	}

	return s.DB.Save(&basicSettings).Error
}
