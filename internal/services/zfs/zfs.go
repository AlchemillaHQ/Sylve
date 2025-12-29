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
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"

	"gorm.io/gorm"
)

var _ zfsServiceInterfaces.ZfsServiceInterface = (*Service)(nil)

type Service struct {
	DB        *gorm.DB
	GZFS      *gzfs.Client
	Libvirt   libvirtServiceInterfaces.LibvirtServiceInterface
	syncMutex *sync.Mutex
}

func NewZfsService(db *gorm.DB, libvirt libvirtServiceInterfaces.LibvirtServiceInterface, gzfsClient *gzfs.Client) zfsServiceInterfaces.ZfsServiceInterface {
	return &Service{
		DB:        db,
		GZFS:      gzfsClient,
		Libvirt:   libvirt,
		syncMutex: &sync.Mutex{},
	}
}

func (s *Service) PoolFromDataset(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("dataset_name_cannot_be_empty")
	}

	dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
	if err != nil {
		return "", fmt.Errorf("error_getting_dataset_%s: %w", name, err)
	}

	return dataset.Pool, nil
}

func (s *Service) GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error) {
	var basicSettings models.BasicSettings
	var pools []*gzfs.ZPool

	if err := s.DB.First(&basicSettings).Error; err != nil {
		return pools, err
	}

	for _, name := range basicSettings.Pools {
		pool, err := s.GZFS.Zpool.Get(ctx, strings.TrimSpace(name))
		if err != nil {
			return pools, err
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func (s *Service) GetDisksUsage(ctx context.Context) (zfsServiceInterfaces.SimpleZFSDiskUsage, error) {
	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return zfsServiceInterfaces.SimpleZFSDiskUsage{}, err
	}

	var totalSize uint64
	var totalUsed uint64

	for _, pool := range pools {
		size := pool.Size
		used := pool.Alloc

		totalSize += size
		totalUsed += used
	}

	usage := float64(0)
	if totalSize > 0 {
		usage = (float64(totalUsed) / float64(totalSize)) * 100
	} else {
		usage = 0
	}

	return zfsServiceInterfaces.SimpleZFSDiskUsage{
		Total: float64(totalSize),
		Usage: usage,
	}, nil
}
