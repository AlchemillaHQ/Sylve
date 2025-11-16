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
	"sync"

	"github.com/alchemillahq/sylve/internal/db/models"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/zfs"

	"gorm.io/gorm"
)

var _ zfsServiceInterfaces.ZfsServiceInterface = (*Service)(nil)

type Service struct {
	DB        *gorm.DB
	Libvirt   libvirtServiceInterfaces.LibvirtServiceInterface
	syncMutex *sync.Mutex
}

func NewZfsService(db *gorm.DB, libvirt libvirtServiceInterfaces.LibvirtServiceInterface) zfsServiceInterfaces.ZfsServiceInterface {
	return &Service{
		DB:        db,
		Libvirt:   libvirt,
		syncMutex: &sync.Mutex{},
	}
}

func (s *Service) SyncLibvirtPools() error {
	zfsPools, err := zfs.ListZpools()

	if err != nil {
		return err
	}

	lvPools, err := s.Libvirt.ListStoragePools()

	if err != nil {
		return err
	}

	for _, pool := range zfsPools {
		exists := false
		for _, lvPool := range lvPools {
			if pool.Name == lvPool.Source {
				exists = true
				break
			}
		}

		if !exists {
			err := s.Libvirt.CreateStoragePool(pool.Name)
			if err != nil {
				logger.L.Error().Err(err).Msgf("Failed to create storage pool %s in libvirt", pool.Name)
				return err
			}
		}
	}

	return nil
}

func (s *Service) PoolFromDataset(dataset string) (string, error) {
	if dataset == "" {
		return "", fmt.Errorf("dataset cannot be empty")
	}

	parts := strings.SplitN(dataset, "/", 2)

	if len(parts) == 1 {
		return parts[0], nil
	}

	return parts[0], nil
}

func (s *Service) GetUsablePools() ([]*zfs.Zpool, error) {
	var basicSettings models.BasicSettings
	var pools []*zfs.Zpool

	if err := s.DB.First(&basicSettings).Error; err != nil {
		return pools, err
	}

	for _, name := range basicSettings.Pools {
		pool, err := zfs.GetZpool(name)
		if err != nil {
			return pools, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) GetValidPool(identifier string) (*zfs.Zpool, error) {
	usable, err := s.GetUsablePools()
	if err != nil {
		return nil, fmt.Errorf("error_fetching_usable_pools: %w", err)
	}

	for _, pool := range usable {
		if pool.Name == identifier || pool.GUID == identifier {
			return pool, nil
		}
	}

	return nil, nil
}
