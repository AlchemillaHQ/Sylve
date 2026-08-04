// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	zfsRawHistoryWindow = time.Hour
	zfsHistoryRetention = 70 * 24 * time.Hour
	zfsDeleteBatchSize   = 500
)

type zpoolRollupKey struct {
	identity        string
	intervalSeconds int64
	bucket          int64
}

func zpoolHistoryInterval(age time.Duration) time.Duration {
	switch {
	case age <= zfsRawHistoryWindow:
		return 0
	case age <= 7*24*time.Hour:
		return 10 * time.Minute
	case age <= 30*24*time.Hour:
		return time.Hour
	case age <= zfsHistoryRetention:
		return 6 * time.Hour
	default:
		return -1
	}
}

func zpoolHistoryIdentity(row infoModels.ZPoolHistorical) string {
	if row.GUID != "" {
		return row.GUID
	}
	return "name:" + row.Name
}

func normalizedSampleCount(row infoModels.ZPoolHistorical) uint64 {
	if row.SampleCount == 0 {
		return 1
	}
	return uint64(row.SampleCount)
}

func maxUint64(left, right uint64) uint64 {
	if right > left {
		return right
	}
	return left
}

func worsePoolHealth(left, right string) string {
	if poolHealthRank(right) > poolHealthRank(left) {
		return right
	}
	return left
}

func buildZpoolRollup(rows []infoModels.ZPoolHistorical, interval time.Duration) infoModels.ZPoolHistorical {
	latest := rows[0]
	worstHealth := latest.WorstHealth
	if worstHealth == "" {
		worstHealth = latest.Health
	}

	var sampleCount uint64
	var readIOPSSum float64
	var writeIOPSSum float64
	var readBandwidthSum float64
	var writeBandwidthSum float64
	var readLatencySum float64
	var writeLatencySum float64
	var readLatencyOperations uint64
	var writeLatencyOperations uint64
	var maxReadIOPS uint64
	var maxWriteIOPS uint64
	var maxReadBandwidth uint64
	var maxWriteBandwidth uint64
	var maxReadLatency uint64
	var maxWriteLatency uint64

	for _, row := range rows {
		if row.CreatedAt.After(latest.CreatedAt) {
			latest = row
		}
		rowWorst := row.WorstHealth
		if rowWorst == "" {
			rowWorst = row.Health
		}
		worstHealth = worsePoolHealth(worstHealth, rowWorst)

		weight := normalizedSampleCount(row)
		sampleCount += weight
		readIOPSSum += float64(row.ReadIOPS) * float64(weight)
		writeIOPSSum += float64(row.WriteIOPS) * float64(weight)
		readBandwidthSum += float64(row.ReadBytesPerSecond) * float64(weight)
		writeBandwidthSum += float64(row.WriteBytesPerSecond) * float64(weight)
		readOperations := row.ReadIOPS * weight
		writeOperations := row.WriteIOPS * weight
		if readOperations > 0 {
			readLatencySum += float64(row.ReadLatencyNanos) * float64(readOperations)
			readLatencyOperations += readOperations
		}
		if writeOperations > 0 {
			writeLatencySum += float64(row.WriteLatencyNanos) * float64(writeOperations)
			writeLatencyOperations += writeOperations
		}

		maxReadIOPS = maxUint64(maxReadIOPS, maxUint64(row.MaxReadIOPS, row.ReadIOPS))
		maxWriteIOPS = maxUint64(maxWriteIOPS, maxUint64(row.MaxWriteIOPS, row.WriteIOPS))
		maxReadBandwidth = maxUint64(maxReadBandwidth, maxUint64(row.MaxReadBytesPerSecond, row.ReadBytesPerSecond))
		maxWriteBandwidth = maxUint64(maxWriteBandwidth, maxUint64(row.MaxWriteBytesPerSecond, row.WriteBytesPerSecond))
		maxReadLatency = maxUint64(maxReadLatency, maxUint64(row.MaxReadLatencyNanos, row.ReadLatencyNanos))
		maxWriteLatency = maxUint64(maxWriteLatency, maxUint64(row.MaxWriteLatencyNanos, row.WriteLatencyNanos))
	}

	if sampleCount == 0 {
		sampleCount = 1
	}
	divisor := float64(sampleCount)
	averageReadLatency := uint64(0)
	averageWriteLatency := uint64(0)
	if readLatencyOperations > 0 {
		averageReadLatency = uint64(readLatencySum / float64(readLatencyOperations))
	}
	if writeLatencyOperations > 0 {
		averageWriteLatency = uint64(writeLatencySum / float64(writeLatencyOperations))
	}
	return infoModels.ZPoolHistorical{
		GUID:                       latest.GUID,
		Name:                       latest.Name,
		Health:                     latest.Health,
		WorstHealth:                worstHealth,
		Allocated:                  latest.Allocated,
		Size:                       latest.Size,
		Free:                       latest.Free,
		Fragmentation:              latest.Fragmentation,
		DedupRatio:                 latest.DedupRatio,
		ReadIOPS:                   uint64(readIOPSSum / divisor),
		WriteIOPS:                  uint64(writeIOPSSum / divisor),
		ReadBytesPerSecond:         uint64(readBandwidthSum / divisor),
		WriteBytesPerSecond:        uint64(writeBandwidthSum / divisor),
		ReadLatencyNanos:           averageReadLatency,
		WriteLatencyNanos:          averageWriteLatency,
		MaxReadIOPS:                maxReadIOPS,
		MaxWriteIOPS:               maxWriteIOPS,
		MaxReadBytesPerSecond:      maxReadBandwidth,
		MaxWriteBytesPerSecond:     maxWriteBandwidth,
		MaxReadLatencyNanos:        maxReadLatency,
		MaxWriteLatencyNanos:       maxWriteLatency,
		SampleCount:                 uint32(sampleCount),
		IntervalSeconds:             uint32(interval / time.Second),
		CreatedAt:                   latest.CreatedAt,
	}
}

func deleteZFSHistoryRows(tx *gorm.DB, model any, ids []uint) error {
	for start := 0; start < len(ids); start += zfsDeleteBatchSize {
		end := start + zfsDeleteBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := tx.Unscoped().Delete(model, ids[start:end]).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) compactZpoolHistory(now time.Time) error {
	var rows []infoModels.ZPoolHistorical
	if err := s.TelemetryDB.
		Where("created_at < ?", now.Add(-zfsRawHistoryWindow)).
		Order("guid ASC, name ASC, created_at ASC").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load zpool history for rollup: %w", err)
	}

	groups := make(map[zpoolRollupKey][]infoModels.ZPoolHistorical)
	deleteIDs := make([]uint, 0)
	for _, row := range rows {
		interval := zpoolHistoryInterval(now.Sub(row.CreatedAt))
		if interval < 0 {
			deleteIDs = append(deleteIDs, row.ID)
			continue
		}
		if interval == 0 {
			continue
		}
		key := zpoolRollupKey{
			identity:        zpoolHistoryIdentity(row),
			intervalSeconds: int64(interval / time.Second),
			bucket:          row.CreatedAt.Unix() / int64(interval/time.Second),
		}
		groups[key] = append(groups[key], row)
	}

	rollups := make([]infoModels.ZPoolHistorical, 0, len(groups))
	for key, groupRows := range groups {
		interval := time.Duration(key.intervalSeconds) * time.Second
		if len(groupRows) == 1 &&
			groupRows[0].IntervalSeconds >= uint32(key.intervalSeconds) &&
			groupRows[0].SampleCount > 0 &&
			groupRows[0].WorstHealth != "" {
			continue
		}
		rollups = append(rollups, buildZpoolRollup(groupRows, interval))
		for _, row := range groupRows {
			deleteIDs = append(deleteIDs, row.ID)
		}
	}

	if len(rollups) == 0 && len(deleteIDs) == 0 {
		return nil
	}
	if err := s.TelemetryDB.Transaction(func(tx *gorm.DB) error {
		if len(rollups) > 0 {
			if err := tx.CreateInBatches(&rollups, 100).Error; err != nil {
				return fmt.Errorf("insert zpool history rollups: %w", err)
			}
		}
		if err := deleteZFSHistoryRows(tx, &infoModels.ZPoolHistorical{}, deleteIDs); err != nil {
			return fmt.Errorf("delete compacted zpool history: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) pruneARCHistory(now time.Time) error {
	var rows []infoModels.ZFSARCHistorical
	if err := s.TelemetryDB.
		Select("id", "created_at").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load ARC history for retention: %w", err)
	}

	_, deleteIDs := db.ApplyGFS(now, rows)
	if len(deleteIDs) == 0 {
		return nil
	}
	if err := deleteZFSHistoryRows(s.TelemetryDB, &infoModels.ZFSARCHistorical{}, deleteIDs); err != nil {
		return fmt.Errorf("delete expired ARC history: %w", err)
	}
	return nil
}

func (s *Service) PruneHistoricalStats() {
	now := time.Now().UTC()
	if err := s.compactZpoolHistory(now); err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: failed to compact zpool history")
	}
	if err := s.pruneARCHistory(now); err != nil {
		logger.L.Debug().Err(err).Msg("zfs_cron: failed to prune ARC history")
	}
}
