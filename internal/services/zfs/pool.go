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
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/pkg/disk"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) GetPoolStatus(ctx context.Context, guid string) (*gzfs.ZPoolStatusPool, error) {
	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil || pool == nil {
		return nil, poolLookupError(err, "pool_not_found")
	}

	status, err := pool.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_pool_status: %v", err)
	}

	return status, nil
}

func (s *Service) ScrubPool(ctx context.Context, guid string) error {
	return s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		_, err := s.startPoolScrubRun(ctx, guid, nil, time.Now().UTC())
		return err
	})
}

func (s *Service) CreatePool(ctx context.Context, req zfsServiceInterfaces.CreateZPoolRequest) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	if !utils.IsValidZFSPoolName(req.Name) {
		return classifyError(ErrInvalidRequest, "invalid_pool_name")
	}

	mountpoint, err := normalizePoolMountpoint(req.Mountpoint, req.Properties)
	if err != nil {
		return err
	}

	names, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_existing_pools: %v", err)
	}

	for _, existingName := range names {
		if strings.EqualFold(existingName, req.Name) {
			return classifyError(ErrConflict, "pool_name_taken")
		}
	}

	validRaidMinDisks := map[zfsServiceInterfaces.RaidType]int{
		zfsServiceInterfaces.RaidTypeStripe: 1,
		zfsServiceInterfaces.RaidTypeMirror: 2,
		zfsServiceInterfaces.RaidTypeRaidZ:  3,
		zfsServiceInterfaces.RaidTypeRaidZ2: 4,
		zfsServiceInterfaces.RaidTypeRaidZ3: 5,
	}

	if len(req.Vdevs) == 0 {
		return classifyError(ErrInvalidRequest, "at_least_one_vdev_required")
	}

	for _, vdev := range req.Vdevs {
		raidType := vdev.RaidType
		if raidType == "" {
			raidType = zfsServiceInterfaces.RaidTypeStripe
		}
		minDisks, ok := validRaidMinDisks[raidType]
		if !ok {
			return classifyError(ErrInvalidRequest, "vdev %s has invalid raid type: %s", vdev.Name, raidType)
		}
		if len(vdev.VdevDevices) < minDisks {
			return classifyError(ErrInvalidRequest, "vdev %s has insufficient devices for %s (minimum %d)", vdev.Name, raidType, minDisks)
		}
	}

	var dataVdevs, logVdevs, cacheVdevs, specialVdevs, dedupVdevs []zfsServiceInterfaces.Vdev
	for _, vdev := range req.Vdevs {
		t := vdev.Type
		if t == "" {
			t = zfsServiceInterfaces.VdevTypeData
		}
		switch t {
		case zfsServiceInterfaces.VdevTypeData:
			dataVdevs = append(dataVdevs, vdev)
		case zfsServiceInterfaces.VdevTypeLog:
			logVdevs = append(logVdevs, vdev)
		case zfsServiceInterfaces.VdevTypeCache:
			cacheVdevs = append(cacheVdevs, vdev)
		case zfsServiceInterfaces.VdevTypeSpecial:
			specialVdevs = append(specialVdevs, vdev)
		case zfsServiceInterfaces.VdevTypeDedup:
			dedupVdevs = append(dedupVdevs, vdev)
		default:
			return classifyError(ErrInvalidRequest, "vdev %s has invalid type: %s", vdev.Name, t)
		}
	}

	if len(dataVdevs) == 0 {
		return classifyError(ErrInvalidRequest, "at_least_one_data_vdev_required")
	}

	var args []string
	args = append(args, buildVdevArgs(dataVdevs)...)

	if len(logVdevs) > 0 {
		args = append(args, "log")
		args = append(args, buildVdevArgs(logVdevs)...)
	}

	if len(cacheVdevs) > 0 {
		args = append(args, "cache")
		args = append(args, buildVdevArgs(cacheVdevs)...)
	}

	if len(specialVdevs) > 0 {
		args = append(args, "special")
		args = append(args, buildVdevArgs(specialVdevs)...)
	}

	if len(dedupVdevs) > 0 {
		args = append(args, "dedup")
		args = append(args, buildVdevArgs(dedupVdevs)...)
	}

	if len(req.Spares) > 0 {
		args = append(args, "spare")
		args = append(args, req.Spares...)
	}
	release, err := s.guardDownloadStorageMutation(ctx, req.Name)
	if err != nil {
		return err
	}
	defer release()

	effectiveMountpoint := mountpoint
	if effectiveMountpoint == "" {
		effectiveMountpoint = "/" + req.Name
	}
	releaseTopology, err := s.guardStorageTopologyMutation(ctx, &gzfs.Dataset{Name: req.Name, Mountpoint: effectiveMountpoint})
	if err != nil {
		return err
	}
	defer releaseTopology()
	err = s.GZFS.Zpool.CreateWithOptions(ctx, req.Name, gzfs.ZPoolCreateOptions{
		Force:      req.CreateForce,
		Mountpoint: mountpoint,
		Properties: req.Properties,
	}, args...)
	if err != nil {
		return fmt.Errorf("zpool_create_failed: %v", err)
	}

	_, err = storagezfs.EnsureSylveNamespace(ctx, s.GZFS, req.Name)
	if err != nil {
		return fmt.Errorf("failed_to_prepare_sylve_datasets: %w", err)
	}

	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_get_basic_settings: %v", err)
	}

	if !slices.Contains(basicSettings.Pools, req.Name) {
		basicSettings.Pools = append(basicSettings.Pools, req.Name)
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&basicSettings).Error; err != nil {
			return err
		}
		return db.InvalidateZFSCaches(tx)
	}); err != nil {
		return fmt.Errorf("failed_to_update_basic_settings: %v", err)
	}

	return nil
}

func normalizePoolMountpoint(raw string, properties map[string]string) (string, error) {
	mountpoint := strings.TrimSpace(raw)
	for property, value := range properties {
		if strings.EqualFold(strings.TrimSpace(property), "altroot") {
			value = strings.TrimSpace(value)
			if value != "" && value != "-" {
				return "", classifyError(ErrInvalidRequest, "pool_altroot_not_supported")
			}
		}
	}

	if mountpoint == "" {
		return "", nil
	}

	mountpoint, err := storagezfs.NormalizeMountpoint(raw)
	if err != nil {
		return "", classifyError(ErrInvalidRequest, "invalid_pool_mountpoint")
	}

	return mountpoint, nil
}

func buildVdevArgs(vdevs []zfsServiceInterfaces.Vdev) []string {
	var args []string
	for _, vdev := range vdevs {
		if vdev.RaidType != "" && vdev.RaidType != zfsServiceInterfaces.RaidTypeStripe {
			args = append(args, string(vdev.RaidType))
		}
		args = append(args, vdev.VdevDevices...)
	}
	return args
}

func (s *Service) EditPool(ctx context.Context, guid string, props map[string]string, spares *[]string) error {
	return s.withPoolScrubMutation(ctx, guid, func(ctx context.Context) error {
		return s.editPool(ctx, guid, props, spares)
	})
}

func (s *Service) guardPoolStorageMutation(ctx context.Context, pool *gzfs.ZPool) (func(), error) {
	releaseDownload, err := s.guardDownloadStorageMutation(ctx, pool.Name)
	if err != nil {
		return nil, err
	}
	releaseTopology, err := s.guardStorageTopologyMutation(ctx, &gzfs.Dataset{Name: pool.Name})
	if err != nil {
		releaseDownload()
		return nil, err
	}
	return func() {
		releaseTopology()
		releaseDownload()
	}, nil
}

func (s *Service) editPool(ctx context.Context, guid string, props map[string]string, spares *[]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if len(props) == 0 && spares == nil {
		return classifyError(ErrInvalidRequest, "no_pool_changes_provided")
	}

	requestedSpares := make([]string, 0)
	if spares != nil {
		requestedSpares = append(requestedSpares, (*spares)...)
		seen := make(map[string]struct{}, len(requestedSpares))
		for i, dev := range requestedSpares {
			dev = filepath.Clean(dev)
			if dev == "" || dev == "." || dev == "/" {
				return classifyError(ErrInvalidRequest, "invalid_spare_device %q", dev)
			}
			if !strings.HasPrefix(dev, "/dev/") {
				return classifyError(ErrInvalidRequest, "invalid_spare_device %s: must be under /dev", dev)
			}
			if _, ok := seen[dev]; ok {
				return classifyError(ErrInvalidRequest, "duplicate_spare_device %s", dev)
			}
			seen[dev] = struct{}{}
			requestedSpares[i] = dev
		}
	}

	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil || pool == nil {
		return poolLookupError(err, "pool_not_found")
	}
	if pool.PoolGUID != guid {
		return classifyError(ErrConflict, "pool_identity_changed")
	}
	if len(props) > 0 {
		release, err := s.guardPoolStorageMutation(ctx, pool)
		if err != nil {
			return err
		}
		defer release()
	}

	if len(requestedSpares) > 0 {
		minSize, err := pool.RequiredSpareSize(ctx)
		if err != nil {
			return fmt.Errorf("failed_to_get_minimum_spare_size: %v", err)
		}
		for _, dev := range requestedSpares {
			sz, err := disk.GetDiskSize(dev)
			if err != nil {
				return classifyError(ErrInvalidRequest, "invalid_spare_device %s: %v", dev, err)
			}
			if sz == 0 {
				return classifyError(ErrInvalidRequest, "invalid_spare_device %s: size is zero", dev)
			}
			if sz < minSize {
				return classifyError(ErrInvalidRequest, "spare_device %s is too small, minimum size is %d bytes", dev, minSize)
			}
		}
	}

	for prop, val := range props {
		if err := pool.SetProperty(ctx, prop, val); err != nil {
			return fmt.Errorf("failed_to_set_property %s: %v", prop, err)
		}
	}
	if spares == nil {
		return nil
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

	currentSet := make(map[string]string)

	for _, dev := range pool.Spares {
		if dev == nil || dev.Path == "" {
			continue
		}
		currentSet[filepath.Base(dev.Path)] = dev.Path
	}

	newSet := make(map[string]struct{})
	for _, dev := range requestedSpares {
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

	for _, dev := range requestedSpares {
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
	return s.withPoolScrubMutation(ctx, guid, func(ctx context.Context) error {
		return s.deletePool(ctx, guid)
	})
}

func (s *Service) deletePool(ctx context.Context, guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)

	if err != nil || pool == nil {
		return poolLookupError(err, "pool_not_found")
	}
	if pool.PoolGUID != guid {
		return classifyError(ErrConflict, "pool_identity_changed")
	}
	release, err := s.guardPoolStorageMutation(ctx, pool)
	if err != nil {
		return err
	}
	defer release()

	datasets, err := pool.Datasets(ctx, gzfs.DatasetTypeAll)
	if err != nil {
		return fmt.Errorf("failed_to_get_datasets: %v", err)
	}

	if len(datasets) > 0 {
		for _, ds := range datasets {
			inUse := s.IsDatasetInUse(ds.GUID, true)

			if inUse {
				return classifyError(ErrConflict, "dataset %s is in use and cannot be deleted", ds.Name)
			}
		}
	}

	err = pool.Destroy(ctx)

	if err != nil {
		return err
	}

	if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubSchedule{}).Where("pool_guid = ?", guid).
		Updates(map[string]any{"enabled": false, "queued_at": nil, "scheduled_for": nil, "next_run_at": nil,
			"last_status": "idle", "last_error": "pool was destroyed"}).Error; err != nil {
		return err
	}

	result := s.TelemetryDB.Where("guid = ?", guid).Delete(&infoModels.ZPoolHistorical{})
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

	s.SignalDSChange(pool.Name, "", "snapshot", "delete")
	s.SignalDSChange(pool.Name, "", "generic-dataset", "delete")

	deletedGUIDs := make([]string, 0, len(datasets))
	for _, dataset := range datasets {
		deletedGUIDs = append(deletedGUIDs, dataset.GUID)
	}
	return s.notifyDatasetsDeleted(ctx, deletedGUIDs)
}

func (s *Service) ReplaceDevice(ctx context.Context, guid, old, latest string) error {
	return s.withPoolScrubMutation(ctx, guid, func(ctx context.Context) error {
		s.syncMutex.Lock()
		defer s.syncMutex.Unlock()

		pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
		if err != nil || pool == nil {
			return poolLookupError(err, "pool_not_found")
		}

		if err := pool.ReplaceDevice(ctx, old, latest, false); err != nil {
			return classifyError(ErrConflict, "failed_to_replace_device %s: %v", old, err)
		}

		pool, err = s.GZFS.Zpool.GetByGUID(ctx, guid)
		if err != nil || pool == nil {
			return fmt.Errorf("pool_not_found_after_replace")
		}

		return nil
	})
}

func (s *Service) DetachDevice(ctx context.Context, guid, device string) error {
	return s.withPoolScrubMutation(ctx, guid, func(ctx context.Context) error {
		s.syncMutex.Lock()
		defer s.syncMutex.Unlock()

		pool, err := s.GZFS.Zpool.GetByGUID(ctx, guid)
		if err != nil || pool == nil {
			return poolLookupError(err, "pool_not_found")
		}

		if err := pool.Detach(ctx, device); err != nil {
			return classifyError(ErrConflict, "detach_failed: %v", err)
		}

		return nil
	})
}
