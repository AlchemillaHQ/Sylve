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
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	datasetCacheTTL              = 7 * 24 * 60 * 60
	cacheInvalidationQuietPeriod = 2 * time.Second
	cacheInvalidationMaxDelay    = 30 * time.Second
	cacheInvalidationPollPeriod  = time.Second
	cacheInvalidationWriteLimit  = 5 * time.Second
)

type cacheRefreshFunc func(context.Context, string) error

func (s *Service) processCacheInvalidations(ctx context.Context, now time.Time) error {
	return processCacheInvalidations(ctx, s.DB, now, s.refreshInvalidatedCache)
}

func processCacheInvalidations(
	ctx context.Context,
	conn *gorm.DB,
	now time.Time,
	refresh cacheRefreshFunc,
) error {
	now = now.UTC()

	var invalidations []models.ZFSCacheInvalidation
	if err := conn.
		Where("last_dirty_at <= ? OR first_dirty_at <= ?", now.Add(-cacheInvalidationQuietPeriod), now.Add(-cacheInvalidationMaxDelay)).
		Order("first_dirty_at ASC").
		Find(&invalidations).Error; err != nil {
		return fmt.Errorf("load zfs cache invalidations: %w", err)
	}

	var refreshErrors []error
	for i := range invalidations {
		if err := ctx.Err(); err != nil {
			refreshErrors = append(refreshErrors, err)
			break
		}

		invalidation := invalidations[i]
		if err := refresh(ctx, invalidation.Kind); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh %s zfs cache: %w", invalidation.Kind, err))
			continue
		}

		if err := completeCacheInvalidation(conn, invalidation); err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("complete %s zfs cache invalidation: %w", invalidation.Kind, err))
		}
	}

	return errors.Join(refreshErrors...)
}

func (s *Service) runCacheInvalidationWorker(ctx context.Context) {
	process := func(now time.Time) {
		s.retryPendingCacheInvalidations(ctx)
		if err := s.processCacheInvalidations(ctx, now); err != nil {
			logger.L.Debug().Err(err).Msg("zfs_cron: failed to process cache invalidations")
		}
	}

	process(time.Now())
	ticker := time.NewTicker(cacheInvalidationPollPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cacheInvalidationWriteLimit)
			s.retryPendingCacheInvalidations(shutdownCtx)
			cancel()
			return
		case now := <-ticker.C:
			process(now)
		}
	}
}

func (s *Service) invalidateCache(ctx context.Context, kind string) error {
	if !db.IsZFSCacheInvalidationKind(kind) {
		return fmt.Errorf("unsupported zfs cache invalidation kind %q", kind)
	}

	generation := s.rememberCacheInvalidation(kind)
	if err := s.persistCacheInvalidation(ctx, kind); err != nil {
		return err
	}

	s.clearPendingCacheInvalidation(kind, generation)
	return nil
}

func (s *Service) retryPendingCacheInvalidations(ctx context.Context) {
	pending := s.pendingCacheInvalidationSnapshot()
	for kind, generation := range pending {
		if err := s.persistCacheInvalidation(ctx, kind); err != nil {
			logger.L.Error().Err(err).Str("kind", kind).Msg("Failed to retry ZFS cache invalidation")
			continue
		}

		s.clearPendingCacheInvalidation(kind, generation)
	}
}

func (s *Service) persistCacheInvalidation(ctx context.Context, kind string) error {
	writeCtx, cancel := context.WithTimeout(ctx, cacheInvalidationWriteLimit)
	defer cancel()
	return db.InvalidateZFSCache(s.DB.WithContext(writeCtx), kind)
}

func (s *Service) rememberCacheInvalidation(kind string) uint64 {
	s.cacheInvalidationMutex.Lock()
	defer s.cacheInvalidationMutex.Unlock()

	if s.pendingCacheInvalidations == nil {
		s.pendingCacheInvalidations = make(map[string]uint64, 2)
	}
	s.cacheInvalidationSequence++
	s.pendingCacheInvalidations[kind] = s.cacheInvalidationSequence
	return s.cacheInvalidationSequence
}

func (s *Service) pendingCacheInvalidationSnapshot() map[string]uint64 {
	s.cacheInvalidationMutex.Lock()
	defer s.cacheInvalidationMutex.Unlock()

	pending := make(map[string]uint64, len(s.pendingCacheInvalidations))
	for kind, generation := range s.pendingCacheInvalidations {
		pending[kind] = generation
	}
	return pending
}

func (s *Service) clearPendingCacheInvalidation(kind string, generation uint64) {
	s.cacheInvalidationMutex.Lock()
	defer s.cacheInvalidationMutex.Unlock()

	if s.pendingCacheInvalidations[kind] == generation {
		delete(s.pendingCacheInvalidations, kind)
	}
}

func completeCacheInvalidation(conn *gorm.DB, invalidation models.ZFSCacheInvalidation) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		result := tx.
			Where("id = ? AND generation = ?", invalidation.ID, invalidation.Generation).
			Delete(&models.ZFSCacheInvalidation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}

		// A newer event arrived during the refresh. Keep it pending while
		// starting a fresh maximum-delay window for the next refresh.
		return tx.Model(&models.ZFSCacheInvalidation{}).
			Where("id = ? AND generation <> ?", invalidation.ID, invalidation.Generation).
			UpdateColumn("first_dirty_at", gorm.Expr("last_dirty_at")).Error
	})
}

func (s *Service) refreshInvalidatedCache(ctx context.Context, kind string) error {
	switch kind {
	case db.ZFSCacheKindSnapshot:
		return s.RefreshDatasets(ctx, gzfs.DatasetTypeSnapshot, datasetCacheTTL)
	case db.ZFSCacheKindGenericDataset:
		if err := s.RefreshDatasets(ctx, gzfs.DatasetTypeFilesystem, datasetCacheTTL); err != nil {
			return err
		}
		return s.RefreshDatasets(ctx, gzfs.DatasetTypeVolume, datasetCacheTTL)
	default:
		return fmt.Errorf("unsupported zfs cache invalidation kind %q", kind)
	}
}
