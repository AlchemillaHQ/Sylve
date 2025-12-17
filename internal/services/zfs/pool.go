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
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
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

	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_get_basic_settings: %v", err)
	}

	basicSettings.Pools = append(basicSettings.Pools, req.Name)

	if err := s.DB.Save(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_update_basic_settings: %v", err)
	}

	return nil
}

func (s *Service) EditPool(ctx context.Context, name string, props map[string]string, spares []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	seen := make(map[string]struct{})

	for i, dev := range spares {
		dev = filepath.Clean(dev)

		if dev == "" || dev == "." || dev == "/" {
			return fmt.Errorf("invalid_spare_device %q", dev)
		}

		if !strings.HasPrefix(dev, "/dev/") {
			return fmt.Errorf("invalid_spare_device %s: must be under /dev", dev)
		}

		if _, ok := seen[dev]; ok {
			return fmt.Errorf("duplicate_spare_device %s", dev)
		}
		seen[dev] = struct{}{}

		spares[i] = dev
	}

	pool, err := s.GZFS.Zpool.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	currentByPath := make(map[string]struct{})
	currentByBase := make(map[string]string)

	for _, dev := range pool.Spares {
		if dev == nil || dev.Path == "" {
			continue
		}
		currentByPath[dev.Path] = struct{}{}
		currentByBase[filepath.Base(dev.Path)] = dev.Path
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

	for _, dev := range pool.Spares {
		if dev == nil || dev.Path == "" {
			continue
		}
		currentSet[filepath.Base(dev.Path)] = dev.Path
	}

	newSet := make(map[string]struct{})
	for _, dev := range spares {
		newSet[filepath.Base(dev)] = struct{}{}
	}

	for base, full := range currentSet {
		if _, keep := newSet[base]; !keep {
			if err := pool.RemoveSpare(ctx, full); err != nil {
				return fmt.Errorf("failed_to_remove_spare %s: %v", full, err)
			}
		}
	}

	time.Sleep(500 * time.Millisecond)

	for _, dev := range spares {
		if _, ok := currentByPath[dev]; ok {
			continue
		}

		base := filepath.Base(dev)

		if _, ok := currentByBase[base]; ok {
			continue
		}

		if err := pool.AddSpare(ctx, dev, false); err != nil {
			return fmt.Errorf("failed_to_add_spare %s: %v", dev, err)
		}
	}

	return nil
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

	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_get_basic_settings: %v", err)
	}

	updatedPools := []string{}
	for _, p := range basicSettings.Pools {
		if p != pool.Name {
			updatedPools = append(updatedPools, p)
		}
	}

	basicSettings.Pools = updatedPools

	if err := s.DB.Save(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_update_basic_settings: %v", err)
	}

	return nil
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
