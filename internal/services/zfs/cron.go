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
	pools, err := s.GZFS.Zpool.List(context.Background())
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

		if err := s.DB.Create(&newStat).Error; err != nil {
			logger.L.Debug().Err(err).Msg("zfs_cron: Failed to insert zpool data")
		}
	}

	now := time.Now()

	var rows []*infoModels.ZPoolHistorical
	if err := s.DB.
		Select("id", "name", "created_at").
		Order("name, created_at ASC").
		Find(&rows).Error; err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to load zpool historical rows for GFS")
		return
	}

	if len(rows) == 0 {
		return
	}

	groups := make(map[string][]db.ReflectRow, 8)
	for _, r := range rows {
		groups[r.Name] = append(groups[r.Name], db.ReflectRow{Ptr: r})
	}

	delSet := make(map[uint]struct{})
	for _, adapters := range groups {
		_, deleteIDs := db.ApplyGFS(now, adapters)
		for _, id := range deleteIDs {
			delSet[id] = struct{}{}
		}
	}

	if len(delSet) == 0 {
		return
	}

	allDeleteIDs := make([]uint, 0, len(delSet))
	for id := range delSet {
		allDeleteIDs = append(allDeleteIDs, id)
	}

	const batchSize = 500
	for i := 0; i < len(allDeleteIDs); i += batchSize {
		end := i + batchSize
		if end > len(allDeleteIDs) {
			end = len(allDeleteIDs)
		}
		batch := allDeleteIDs[i:end]

		if err := s.DB.Delete(&infoModels.ZPoolHistorical{}, batch).Error; err != nil {
			logger.L.Debug().Err(err).Msg("zfs_cron: Failed to prune zpool historical data (batch delete)")
		}
	}
}

func (s *Service) RemoveNonExistentPools() {
	ctx := context.Background()

	existingPools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to list zpools")
		return
	}

	var storedNames []string
	if err := s.DB.
		Model(&infoModels.ZPoolHistorical{}).
		Distinct("name").
		Pluck("name", &storedNames).Error; err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: Failed to load historical pool names")
		return
	}

	var namesToDelete []string
	for _, name := range storedNames {
		found := false
		for _, existing := range existingPools {
			if name == existing {
				found = true
				break
			}
		}
		if !found {
			namesToDelete = append(namesToDelete, name)
		}
	}

	if len(namesToDelete) == 0 {
		return
	}

	result := s.DB.
		Where("name IN ?", namesToDelete).
		Delete(&infoModels.ZPoolHistorical{})
	if result.Error != nil {
		logger.L.Debug().Err(result.Error).Msg("zfs_cron: Failed to delete non-existent pool entries")
		return
	}

	if result.RowsAffected > 0 {
		logger.L.Debug().
			Int64("deleted_count", result.RowsAffected).
			Strs("names", namesToDelete).
			Msg("zfs_cron: Deleted non-existent pool entries")
	}
}

func (s *Service) Cron() {
	tickerFast := time.NewTicker(10 * time.Second)
	tickerSlow := time.NewTicker(10 * time.Minute)

	defer tickerFast.Stop()
	defer tickerSlow.Stop()

	s.StoreStats()
	s.RemoveNonExistentPools()

	for {
		select {
		case <-tickerFast.C:
			s.StoreStats()
		case <-tickerSlow.C:
			s.RemoveNonExistentPools()
		}
	}
}
