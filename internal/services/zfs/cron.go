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
)

func (s *Service) StoreStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pools, err := s.GZFS.Zpool.List(ctx)
	if err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to list zpools")
		return
	}

	for _, pool := range pools {
		newStat := infoModels.ZPoolHistorical{
			Name:          pool.Name,
			GUID:          pool.PoolGUID,
			Allocated:     pool.Alloc,
			Size:          pool.Size,
			Free:          pool.Free,
			Fragmentation: pool.Fragmentation,
			DedupRatio:    pool.DedupRatio,
		}

		if err := s.TelemetryDB.Create(&newStat).Error; err != nil {
			logger.L.Debug().Err(err).Msg("zfs_cron: Failed to insert zpool data")
		}
	}

	now := time.Now()

	var rows []infoModels.ZPoolHistorical
	if err := s.TelemetryDB.
		Select("id", "name", "created_at").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to load zpool historical rows for GFS")
		return
	}

	if len(rows) == 0 {
		return
	}

	groups := make(map[string][]infoModels.ZPoolHistorical)
	for _, r := range rows {
		groups[r.Name] = append(groups[r.Name], r)
	}

	var allDeleteIDs []uint
	for _, poolRows := range groups {
		_, deleteIDs := db.ApplyGFS(now, poolRows)
		allDeleteIDs = append(allDeleteIDs, deleteIDs...)
	}

	if len(allDeleteIDs) == 0 {
		return
	}

	const batchSize = 500
	for i := 0; i < len(allDeleteIDs); i += batchSize {
		end := i + batchSize
		if end > len(allDeleteIDs) {
			end = len(allDeleteIDs)
		}
		batch := allDeleteIDs[i:end]

		if err := s.TelemetryDB.Unscoped().Delete(&infoModels.ZPoolHistorical{}, batch).Error; err != nil {
			logger.L.Debug().Err(err).Msg("zfs_cron: Failed to prune zpool historical data (batch delete)")
		}
	}
}

func (s *Service) RemoveNonExistentPools() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	existingPools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: failed to list zpools")
		return
	}

	existingSet := make(map[string]struct{}, len(existingPools))
	for _, p := range existingPools {
		existingSet[p] = struct{}{}
	}

	var storedNames []string
	if err := s.TelemetryDB.
		Model(&infoModels.ZPoolHistorical{}).
		Distinct("name").
		Pluck("name", &storedNames).Error; err != nil {

		logger.L.Debug().Err(err).Msg("zfs_cron: failed to load historical pool names")
		return
	}

	var namesToDelete []string
	for _, name := range storedNames {
		if _, exists := existingSet[name]; !exists {
			namesToDelete = append(namesToDelete, name)
		}
	}

	if len(namesToDelete) == 0 {
		return
	}

	result := s.TelemetryDB.Unscoped().
		Where("name IN ?", namesToDelete).
		Delete(&infoModels.ZPoolHistorical{})

	if result.Error != nil {
		logger.L.Debug().Err(result.Error).Msg("zfs_cron: failed to delete non-existent pool entries")
		return
	}

	if result.RowsAffected > 0 {
		logger.L.Debug().
			Int64("deleted_count", result.RowsAffected).
			Strs("names", namesToDelete).
			Msg("zfs_cron: deleted non-existent pool entries")
	}

	s.SignalDSChange("", "", db.ZFSCacheKindGenericDataset, "remove_nonexistent_pool")
	s.SignalDSChange("", "", db.ZFSCacheKindSnapshot, "remove_nonexistent_pool")
}

func (s *Service) Cron(ctx context.Context) {
	tickerFast := time.NewTicker(10 * time.Second)
	tickerSlow := time.NewTicker(10 * time.Minute)

	defer tickerFast.Stop()
	defer tickerSlow.Stop()

	s.SignalDSChange("", "", db.ZFSCacheKindGenericDataset, "startup")
	s.SignalDSChange("", "", db.ZFSCacheKindSnapshot, "startup")
	go s.runCacheInvalidationWorker(ctx)
	s.StoreStats()
	s.RemoveNonExistentPools()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("Shutting down ZFS cron workers")
			return
		case <-tickerFast.C:
			s.StoreStats()
		case <-tickerSlow.C:
			s.RemoveNonExistentPools()
		}
	}
}
