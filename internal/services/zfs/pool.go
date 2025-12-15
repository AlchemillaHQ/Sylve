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
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetPoolStatus(ctx context.Context, guid string) (*gzfs.ZPoolStatusPool, error) {
	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("pool_not_found")
	}

	status, err := pool.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_pool_status: %v", err)
	}

	return status, nil
}

func (s *Service) ScrubPool(ctx context.Context, guid string) error {
	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	err = pool.Scrub(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_start_scrub: %v", err)
	}

	return nil
}

func (s *Service) CreatePool(ctx context.Context, req zfsServiceInterfaces.CreateZPoolRequest) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	if !utils.IsValidZFSPoolName(req.Name) {
		return fmt.Errorf("invalid_pool_name")
	}

	names, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_existing_pools: %v", err)
	}

	for _, existingName := range names {
		if strings.EqualFold(existingName, req.Name) {
			return fmt.Errorf("pool_name_taken")
		}
	}

	if req.RaidType != "" {
		validRaidTypes := map[zfsServiceInterfaces.RaidType]int{
			"mirror": 2,
			"raidz":  3,
			"raidz2": 4,
			"raidz3": 5,
		}

		minDevices, ok := validRaidTypes[req.RaidType]
		if !ok {
			return fmt.Errorf("invalid_raidz_type")
		}

		for _, vdev := range req.Vdevs {
			if len(vdev.VdevDevices) < minDevices {
				return fmt.Errorf("vdev %s has insufficient devices for %s (minimum %d)", vdev.Name, req.RaidType, minDevices)
			}
		}
	} else {
		for _, vdev := range req.Vdevs {
			if len(vdev.VdevDevices) == 0 {
				return fmt.Errorf("vdev %s has no devices", vdev.Name)
			}
		}
	}

	var vdevArgs []string
	for _, vdev := range req.Vdevs {
		if req.RaidType != "" {
			vdevArgs = append(vdevArgs, string(req.RaidType))
		}
		vdevArgs = append(vdevArgs, vdev.VdevDevices...)
	}
	var args []string

	args = append(args, vdevArgs...)

	if len(req.Spares) > 0 {
		args = append(args, "spare")
		args = append(args, req.Spares...)
	}

	err = s.GZFS.Zpool.Create(ctx, req.Name, req.CreateForce, req.Properties, args...)
	if err != nil {
		return fmt.Errorf("zpool_create_failed: %v", err)
	}

	if err := s.Libvirt.CreateStoragePool(req.Name); err != nil {
		return fmt.Errorf("libvirt_create_pool_failed: %v", err)
	}

	return s.SyncToLibvirt(ctx)
}

func (s *Service) EditPool(ctx context.Context, name string, props map[string]string, spares []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := s.GZFS.Zpool.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	minSize, err := pool.RequiredSpareSize(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_minimum_spare_size: %v", err)
	}

	for _, dev := range spares {
		sz, err := disk.GetDiskSize(dev)
		if err != nil {
			return fmt.Errorf("invalid_spare_device %s: %v", dev, err)
		}

		if sz == 0 {
			return fmt.Errorf("invalid_spare_device %s: size is zero", dev)
		}

		if sz < minSize {
			return fmt.Errorf("spare_device %s is too small, minimum size is %d bytes", dev, minSize)
		}
	}

	for prop, val := range props {
		if err := pool.SetProperty(ctx, prop, val); err != nil {
			return fmt.Errorf("failed_to_set_property %s: %v", prop, err)
		}
	}

	currentSet := make(map[string]string)

	if sparesVdev, ok := pool.Vdevs["spares"]; ok && sparesVdev != nil {
		for _, dev := range sparesVdev.Vdevs {
			if dev == nil || dev.Path == "" {
				continue
			}

			base := filepath.Base(dev.Path)
			if _, seen := currentSet[base]; !seen {
				currentSet[base] = dev.Path
			}
		}
	}

	newSet := make(map[string]struct{})
	for _, dev := range spares {
		newSet[filepath.Base(dev)] = struct{}{}
	}

	removed := make(map[string]struct{})
	for base, full := range currentSet {
		if _, keep := newSet[base]; !keep {
			if _, done := removed[base]; done {
				continue
			}
			if err := pool.RemoveSpare(ctx, full); err != nil {
				return fmt.Errorf("failed_to_remove_spare %s: %v", full, err)
			}
			removed[base] = struct{}{}
			time.Sleep(100 * time.Millisecond)
		}
	}

	time.Sleep(500 * time.Millisecond)

	for _, dev := range spares {
		base := filepath.Base(dev)
		if _, exists := currentSet[base]; !exists {
			if err := pool.AddSpare(ctx, dev, false); err != nil {
				return fmt.Errorf("failed_to_add_spare %s: %v", dev, err)
			}
		}
	}

	return s.SyncToLibvirt(ctx)
}

func (s *Service) DeletePool(ctx context.Context, guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)

	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	datasets, err := pool.Datasets(ctx, gzfs.DatasetTypeAll)
	if err != nil {
		return fmt.Errorf("failed_to_get_datasets: %v", err)
	}

	if len(datasets) > 0 {
		for _, ds := range datasets {
			inUse := s.IsDatasetInUse(ds.GUID, true)

			if inUse {
				return fmt.Errorf("dataset %s is in use and cannot be deleted", ds.Name)
			}
		}
	}

	err = pool.Destroy(ctx)

	if err != nil {
		return err
	}

	result := s.DB.Where("guid = ?", guid).Delete(&infoModels.ZPoolHistorical{})
	if result.Error != nil {
		return fmt.Errorf("failed_to_delete_historical_data: %v", result.Error)
	}

	if err := s.Libvirt.DeleteStoragePool(pool.Name); err != nil {
		if !strings.Contains(err.Error(), "failed to lookup storage pool") &&
			!strings.Contains(err.Error(), "Storage pool not found") {
			return err
		}
	}

	return s.SyncToLibvirt(ctx)
}

func (s *Service) ReplaceDevice(ctx context.Context, guid, old, latest string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)

	if err := pool.ReplaceDevice(ctx, old, latest, false); err != nil {
		return fmt.Errorf("failed_to_replace_device %s: %v", old, err)
	}

	pool, err = s.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil {
		return fmt.Errorf("pool_not_found_after_replace")
	}

	return nil
}

func (s *Service) SyncToLibvirt(ctx context.Context) error {
	defer s.Libvirt.RescanStoragePools()

	sPools, err := s.Libvirt.ListStoragePools()
	if err != nil {
		return fmt.Errorf("failed_to_list_libvirt_pools: %v", err)
	}

	zPools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_list_zfs_pools: %v", err)
	}

	existing := make(map[string]struct{}, len(sPools))
	for _, sp := range sPools {
		existing[sp.Name] = struct{}{}
	}

	for _, sp := range sPools {
		if !slices.Contains(zPools, sp.Name) {
			if derr := s.Libvirt.DeleteStoragePool(sp.Name); derr != nil {
				return fmt.Errorf("failed_to_delete_libvirt_pool %s: %v", sp.Name, derr)
			}
		}
	}

	for _, name := range zPools {
		if _, ok := existing[name]; ok {
			continue
		}

		if err := s.Libvirt.CreateStoragePool(name); err != nil {
			return fmt.Errorf("failed_to_create_libvirt_pool %s: %w", name, err)
		}
	}

	return nil
}

func (s *Service) GetZpoolHistoricalStats(intervalMinutes int, limit int) (map[string][]zfsServiceInterfaces.PoolStatPoint, int, error) {
	// if intervalMinutes <= 0 {
	// 	return nil, 0, fmt.Errorf("invalid interval: must be > 0")
	// }

	// var records []infoModels.ZPoolHistorical
	// if err := s.DB.
	// 	Order("created_at ASC").
	// 	Find(&records).Error; err != nil {
	// 	return nil, 0, err
	// }

	// count := len(records)
	// intervalMs := int64(intervalMinutes) * 60 * 1000

	// buckets := make(map[string]map[int64]zfsServiceInterfaces.PoolStatPoint)
	// for _, rec := range records {
	// 	bucketTime := (rec.CreatedAt / intervalMs) * intervalMs
	// 	name := zfs.Zpool(rec.Pools).Name

	// 	if buckets[name] == nil {
	// 		buckets[name] = make(map[int64]zfsServiceInterfaces.PoolStatPoint)
	// 	}

	// 	if _, seen := buckets[name][bucketTime]; !seen {
	// 		p := zfs.Zpool(rec.Pools)
	// 		buckets[name][bucketTime] = zfsServiceInterfaces.PoolStatPoint{
	// 			Time:       bucketTime,
	// 			Allocated:  p.Allocated,
	// 			Free:       p.Free,
	// 			Size:       p.Size,
	// 			DedupRatio: p.DedupRatio,
	// 		}
	// 	}
	// }

	// result := make(map[string][]zfsServiceInterfaces.PoolStatPoint, len(buckets))
	// for name, mp := range buckets {
	// 	pts := make([]zfsServiceInterfaces.PoolStatPoint, 0, len(mp))
	// 	for _, pt := range mp {
	// 		pts = append(pts, pt)
	// 	}
	// 	sort.Slice(pts, func(i, j int) bool {
	// 		return pts[i].Time < pts[j].Time
	// 	})

	// 	if limit > 0 && len(pts) > limit {
	// 		pts = pts[len(pts)-limit:]
	// 	}

	// 	result[name] = pts
	// }

	// return result, count, nil

	return nil, 0, fmt.Errorf("zpool_historical_stats_not_implemented")
}
