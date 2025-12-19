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
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	existingPools, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		logger.L.Debug().
			Err(err).
			Msg("zfs_cron: failed to list zpools")
		return
	}

	existingSet := make(map[string]struct{}, len(existingPools))
	for _, p := range existingPools {
		existingSet[p] = struct{}{}
	}

	var storedNames []string
	if err := s.DB.
		Model(&infoModels.ZPoolHistorical{}).
		Distinct("name").
		Pluck("name", &storedNames).Error; err != nil {

		logger.L.Debug().
			Err(err).
			Msg("zfs_cron: failed to load historical pool names")
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

	result := s.DB.
		Where("name IN ?", namesToDelete).
		Delete(&infoModels.ZPoolHistorical{})

	if result.Error != nil {
		logger.L.Debug().
			Err(result.Error).
			Msg("zfs_cron: failed to delete non-existent pool entries")
		return
	}

	if result.RowsAffected == 0 {
		return
	}

	logger.L.Debug().
		Int64("deleted_count", result.RowsAffected).
		Strs("names", namesToDelete).
		Msg("zfs_cron: deleted non-existent pool entries")

	go func() {
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer refreshCancel()

		if err := s.RefreshDatasetsCache(refreshCtx); err != nil {
			logger.L.Debug().
				Err(err).
				Msg("zfs_cron: failed to refresh datasets cache")
		}
	}()
}

func (s *Service) RefreshDatasetsCache(ctx context.Context) error {
	for _, t := range []gzfs.DatasetType{
		gzfs.DatasetTypeFilesystem,
		gzfs.DatasetTypeVolume,
		gzfs.DatasetTypeSnapshot,
	} {
		if err := s.RefreshDatasets(ctx, t, 604800); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON[zfsServiceInterfaces.ZFSHistoryBatchJob](
		"zfs_history_batch",
		s.HandleZFSHistoryBatch,
	)
}

func (s *Service) Cron() {
	tickerFast := time.NewTicker(10 * time.Second)
	tickerSlow := time.NewTicker(10 * time.Minute)
	tickerJob := time.NewTicker(1 * time.Minute)

	defer tickerFast.Stop()
	defer tickerSlow.Stop()

	s.StoreStats()
	s.RemoveNonExistentPools()
	s.RefreshDatasetsCache(context.Background())
	s.DevdJobQueuer(context.Background())

	for {
		select {
		case <-tickerFast.C:
			s.StoreStats()
		case <-tickerSlow.C:
			s.RemoveNonExistentPools()
		case <-tickerJob.C:
			s.DevdJobQueuer(context.Background())
		}
	}
}

func (s *Service) DevdJobQueuer(ctx context.Context) {
	const batchSize = 500

	var events []models.DevdEvent

	if err := s.DB.
		Where("processed = ?", false).
		Where("system = ?", "ZFS").
		Where("type = ?", "sysevent.fs.zfs.history_event").
		Order("created_at").
		Limit(batchSize).
		Find(&events).Error; err != nil {

		logger.L.Debug().
			Err(err).
			Msg("devd_job_queuer: failed to load devd events")
		return
	}

	if len(events) == 0 {
		return
	}

	type bucket struct {
		EventIDs []uint
		Datasets map[string]struct{}
		Actions  map[string]struct{}
		MinTXG   string
		MaxTXG   string
		Pool     string
	}

	buckets := map[string]*bucket{
		"snapshot":        {Datasets: map[string]struct{}{}, Actions: map[string]struct{}{}},
		"generic-dataset": {Datasets: map[string]struct{}{}, Actions: map[string]struct{}{}},
	}

	var processedIDs []uint

	for _, ev := range events {
		attrs := ev.Attrs

		pool := attrs["pool"]
		if pool == "" || !s.IsPoolAllowed(pool) {
			processedIDs = append(processedIDs, ev.ID)
			continue
		}

		ds := attrs["history_dsname"]
		action := attrs["history_internal_name"]
		txg := attrs["history_txg"]

		var kind string
		if strings.Contains(ds, "@") {
			kind = "snapshot"
		} else {
			kind = "generic-dataset"
		}

		b := buckets[kind]
		b.Pool = pool
		b.EventIDs = append(b.EventIDs, ev.ID)
		b.Datasets[ds] = struct{}{}
		b.Actions[action] = struct{}{}

		if b.MinTXG == "" || txg < b.MinTXG {
			b.MinTXG = txg
		}
		if txg > b.MaxTXG {
			b.MaxTXG = txg
		}

		processedIDs = append(processedIDs, ev.ID)
	}

	for kind, b := range buckets {
		if len(b.EventIDs) == 0 {
			continue
		}

		job := zfsServiceInterfaces.ZFSHistoryBatchJob{
			Pool:     b.Pool,
			Kind:     kind,
			EventIDs: b.EventIDs,
			Datasets: utils.MapKeys(b.Datasets),
			Actions:  utils.MapKeys(b.Actions),
			MinTXG:   b.MinTXG,
			MaxTXG:   b.MaxTXG,
		}

		enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := db.EnqueueJSON(enqueueCtx, "zfs_history_batch", job); err != nil {
			logger.L.Debug().
				Err(err).
				Str("dataset_type", kind).
				Msg("devd_job_queuer: failed to enqueue batch job")
			return
		}
	}

	if err := s.DB.
		Model(&models.DevdEvent{}).
		Where("id IN ?", processedIDs).
		Update("processed", true).Error; err != nil {

		logger.L.Debug().
			Err(err).
			Msg("devd_job_queuer: failed to mark events processed")
	}
}

func (s *Service) HandleZFSHistoryBatch(
	ctx context.Context,
	job zfsServiceInterfaces.ZFSHistoryBatchJob,
) error {
	logger.L.Info().
		Str("pool", job.Pool).
		Str("kind", job.Kind).
		Int("events", len(job.EventIDs)).
		Msg("Processing ZFS history batch")

	switch job.Kind {
	case "snapshot":
		err := s.RefreshDatasets(ctx, gzfs.DatasetTypeSnapshot, 604800)
		if err != nil {
			return err
		}

	case "generic-dataset":
		err := s.RefreshDatasets(ctx, gzfs.DatasetTypeFilesystem, 604800)
		if err != nil {
			return err
		}

		return s.RefreshDatasets(ctx, gzfs.DatasetTypeVolume, 604800)

	default:
		return nil
	}

	return nil
}
