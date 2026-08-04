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
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

func (s *Service) StoreStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	arcStat, arcErr := collectARCStats(now)
	if arcErr != nil {
		logger.L.Debug().Err(arcErr).Msg("zfs_cron: ARC telemetry unavailable")
	}

	pools, err := s.GZFS.Zpool.List(ctx)
	if err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to list zpools")
		pools = nil
	}

	stats := make([]infoModels.ZPoolHistorical, 0, len(pools))
	for _, pool := range pools {
		if pool == nil {
			continue
		}

		ioStat := s.getPoolIOStat(pool.Name, now)
		stats = append(stats, infoModels.ZPoolHistorical{
			Name:                       pool.Name,
			GUID:                       pool.PoolGUID,
			Health:                     string(pool.State),
			WorstHealth:                string(pool.State),
			Allocated:                  pool.Alloc,
			Size:                       pool.Size,
			Free:                       pool.Free,
			Fragmentation:              pool.Fragmentation,
			DedupRatio:                 pool.DedupRatio,
			ReadIOPS:               ioStat.ReadIOPS,
			WriteIOPS:              ioStat.WriteIOPS,
			ReadBytesPerSecond:     ioStat.ReadBytesPerSecond,
			WriteBytesPerSecond:    ioStat.WriteBytesPerSecond,
			ReadLatencyNanos:       ioStat.ReadLatencyNanos,
			WriteLatencyNanos:      ioStat.WriteLatencyNanos,
			MaxReadIOPS:            ioStat.ReadIOPS,
			MaxWriteIOPS:           ioStat.WriteIOPS,
			MaxReadBytesPerSecond:  ioStat.ReadBytesPerSecond,
			MaxWriteBytesPerSecond: ioStat.WriteBytesPerSecond,
			MaxReadLatencyNanos:    ioStat.ReadLatencyNanos,
			MaxWriteLatencyNanos:   ioStat.WriteLatencyNanos,
			SampleCount:            1,
			IntervalSeconds:        poolIOSampleIntervalSeconds,
			CreatedAt:              now,
		})
	}

	if len(stats) == 0 && arcErr != nil {
		return
	}

	if err := s.TelemetryDB.Transaction(func(tx *gorm.DB) error {
		if len(stats) > 0 {
			if err := tx.CreateInBatches(&stats, 100).Error; err != nil {
				return err
			}
		}
		if arcErr == nil {
			if err := tx.Create(&arcStat).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to insert ZFS telemetry")
	}
}

func (s *Service) RemoveNonExistentPools() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	existingPools, err := s.GZFS.Zpool.List(ctx)
	if err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: failed to list zpools")
		return
	}

	existingGUIDs := make(map[string]struct{}, len(existingPools))
	existingNames := make(map[string]struct{}, len(existingPools))
	for _, pool := range existingPools {
		if pool == nil {
			continue
		}
		existingGUIDs[pool.PoolGUID] = struct{}{}
		existingNames[pool.Name] = struct{}{}
	}

	var storedPools []struct {
		GUID string
		Name string
	}
	if err := s.TelemetryDB.
		Model(&infoModels.ZPoolHistorical{}).
		Distinct("guid", "name").
		Find(&storedPools).Error; err != nil {

		logger.L.Debug().Err(err).Msg("zfs_cron: failed to load historical pool names")
		return
	}

	guidsToDelete := make(map[string]struct{})
	namesToDelete := make(map[string]struct{})
	for _, stored := range storedPools {
		if stored.GUID != "" {
			if _, exists := existingGUIDs[stored.GUID]; !exists {
				guidsToDelete[stored.GUID] = struct{}{}
			}
			continue
		}
		if _, exists := existingNames[stored.Name]; !exists {
			namesToDelete[stored.Name] = struct{}{}
		}
	}

	if len(guidsToDelete) == 0 && len(namesToDelete) == 0 {
		return
	}

	query := s.TelemetryDB.Unscoped()
	if len(guidsToDelete) > 0 && len(namesToDelete) > 0 {
		query = query.Where("guid IN ? OR (guid = '' AND name IN ?)", mapKeys(guidsToDelete), mapKeys(namesToDelete))
	} else if len(guidsToDelete) > 0 {
		query = query.Where("guid IN ?", mapKeys(guidsToDelete))
	} else {
		query = query.Where("guid = '' AND name IN ?", mapKeys(namesToDelete))
	}
	result := query.Delete(&infoModels.ZPoolHistorical{})

	if result.Error != nil {
		logger.L.Debug().Err(result.Error).Msg("zfs_cron: failed to delete non-existent pool entries")
		return
	}

	if result.RowsAffected > 0 {
		logger.L.Debug().
			Int64("deleted_count", result.RowsAffected).
			Strs("guids", mapKeys(guidsToDelete)).
			Strs("legacy_names", mapKeys(namesToDelete)).
			Msg("zfs_cron: deleted non-existent pool entries")
	}

	s.SignalDSChange("", "", db.ZFSCacheKindGenericDataset, "remove_nonexistent_pool")
	s.SignalDSChange("", "", db.ZFSCacheKindSnapshot, "remove_nonexistent_pool")
}

func (s *Service) Cron(ctx context.Context) {
	tickerFast := time.NewTicker(10 * time.Second)
	tickerMaintenance := time.NewTicker(10 * time.Minute)

	defer tickerFast.Stop()
	defer tickerMaintenance.Stop()

	s.SignalDSChange("", "", db.ZFSCacheKindGenericDataset, "startup")
	s.SignalDSChange("", "", db.ZFSCacheKindSnapshot, "startup")
	go s.runCacheInvalidationWorker(ctx)
	go s.monitorPoolIO(ctx)
	s.StoreStats()
	s.PruneHistoricalStats()
	s.RemoveNonExistentPools()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("Shutting down ZFS cron workers")
			return
		case <-tickerFast.C:
			s.StoreStats()
		case <-tickerMaintenance.C:
			s.PruneHistoricalStats()
			s.RemoveNonExistentPools()
		}
	}
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	return keys
}
