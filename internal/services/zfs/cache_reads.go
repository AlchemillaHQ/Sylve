// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/vmihailenco/msgpack/v5"
)

const liveReadTimeout = 2 * time.Minute

func datasetCacheKey(datasetType gzfs.DatasetType) string {
	return fmt.Sprintf("zfs:datasets:%s:v1", datasetType)
}

func cacheKindFor(datasetType gzfs.DatasetType) string {
	if datasetType == gzfs.DatasetTypeSnapshot {
		return db.ZFSCacheKindSnapshot
	}
	return db.ZFSCacheKindGenericDataset
}

func (s *Service) cacheStale(ctx context.Context, kind string) bool {
	s.cacheInvalidationMutex.Lock()
	_, pending := s.pendingCacheInvalidations[kind]
	s.cacheInvalidationMutex.Unlock()
	if pending {
		return true
	}
	if s.DB == nil {
		return false
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&models.ZFSCacheInvalidation{}).Where("kind = ?", kind).Count(&count).Error; err != nil {
		return true
	}
	return count > 0
}

func sharedLiveRead[T any](s *Service, ctx context.Context, key string, read func(context.Context) (T, error)) (T, error) {
	result := s.liveReads.DoChan(key, func() (any, error) {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveReadTimeout)
		defer cancel()
		return read(readCtx)
	})
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case shared := <-result:
		if shared.Err != nil {
			return zero, shared.Err
		}
		return shared.Val.(T), nil
	}
}

func (s *Service) snapshotSummariesForRead(ctx context.Context) ([]zfsServiceInterfaces.SnapshotSummary, error) {
	if !s.cacheStale(ctx, db.ZFSCacheKindSnapshot) {
		var summaries []zfsServiceInterfaces.SnapshotSummary
		if cached, ok := db.GetValue(snapshotSummaryCacheKey); ok && msgpack.Unmarshal(cached, &summaries) == nil {
			return summaries, nil
		}
		s.SignalDSChange("", "", db.ZFSCacheKindSnapshot, "cache_miss")
	}
	return sharedLiveRead(s, ctx, "snapshot-summaries", s.listSnapshotSummaries)
}

func (s *Service) datasetsForRead(ctx context.Context, datasetType gzfs.DatasetType) ([]*gzfs.Dataset, error) {
	if datasetType != gzfs.DatasetTypeAll && !s.cacheStale(ctx, cacheKindFor(datasetType)) {
		if cached, ok := db.GetValue(datasetCacheKey(datasetType)); ok {
			if datasets, err := MsgpackDecode(cached); err == nil {
				return datasets, nil
			}
		}
		s.SignalDSChange("", "", cacheKindFor(datasetType), "cache_miss")
	}
	datasets, err := sharedLiveRead(s, ctx, "datasets:"+string(datasetType), func(ctx context.Context) ([]*gzfs.Dataset, error) {
		return s.GZFS.ZFS.ListByType(ctx, datasetType, true, "")
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s datasets: %w", datasetType, err)
	}
	return datasets, nil
}
