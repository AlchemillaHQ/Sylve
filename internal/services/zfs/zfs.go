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
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"

	"gorm.io/gorm"
)

var _ zfsServiceInterfaces.ZfsServiceInterface = (*Service)(nil)

type Service struct {
	DB                        *gorm.DB
	TelemetryDB               *gorm.DB
	GZFS                      *gzfs.Client
	Libvirt                   libvirtServiceInterfaces.LibvirtServiceInterface
	syncMutex                 *sync.Mutex
	poolTelemetryMutex        sync.Mutex
	poolIOMutex               sync.RWMutex
	poolIOStats               map[string]poolIOStat
	managedPoolIONames        map[string]struct{}
	cacheInvalidationMutex    sync.Mutex
	cacheInvalidationSequence uint64
	pendingCacheInvalidations map[string]uint64
	OnDatasetsDeleted         func(context.Context, []string) error
	listHostPools             func(context.Context) ([]*gzfs.ZPool, error)
}

func NewZfsService(db *gorm.DB, telemetryDB *gorm.DB, libvirt libvirtServiceInterfaces.LibvirtServiceInterface, gzfsClient *gzfs.Client) zfsServiceInterfaces.ZfsServiceInterface {
	return &Service{
		DB:                        db,
		TelemetryDB:               telemetryDB,
		GZFS:                      gzfsClient,
		Libvirt:                   libvirt,
		syncMutex:                 &sync.Mutex{},
		poolIOStats:               make(map[string]poolIOStat),
		managedPoolIONames:        make(map[string]struct{}),
		pendingCacheInvalidations: make(map[string]uint64, 2),
	}
}

func datasetGUIDsInTrees(datasets []*gzfs.Dataset, roots []*gzfs.Dataset) []string {
	seen := make(map[string]struct{})
	guids := make([]string, 0, len(roots))
	for _, dataset := range datasets {
		if dataset == nil || dataset.GUID == "" {
			continue
		}
		for _, root := range roots {
			if root != nil && datasetNameInTree(dataset.Name, root.Name) {
				if _, exists := seen[dataset.GUID]; !exists {
					seen[dataset.GUID] = struct{}{}
					guids = append(guids, dataset.GUID)
				}
				break
			}
		}
	}
	return guids
}

func datasetNameInTree(name, root string) bool {
	if name == root {
		return true
	}
	if strings.Contains(root, "@") {
		return false
	}
	return strings.HasPrefix(name, root+"/") || strings.HasPrefix(name, root+"@")
}

func (s *Service) notifyDatasetsDeleted(ctx context.Context, guids []string) error {
	if s.OnDatasetsDeleted == nil || len(guids) == 0 {
		return nil
	}
	if err := s.OnDatasetsDeleted(ctx, guids); err != nil {
		return fmt.Errorf("datasets_deleted_but_dependent_services_failed_to_reconcile: %w", err)
	}
	return nil
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
	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return nil, err
	}
	return scope.pools, nil
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
