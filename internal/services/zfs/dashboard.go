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
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"gorm.io/gorm"
)

const dashboardDeltaLimit = 10000

var dashboardResolutionSteps = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	6 * time.Hour,
}

func dashboardResolution(from, to time.Time, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 900
	}
	span := to.Sub(from)
	if span <= 0 {
		return time.Minute
	}
	minimum := span / time.Duration(maxPoints)
	for _, candidate := range dashboardResolutionSteps {
		if candidate >= minimum {
			return candidate
		}
	}
	return dashboardResolutionSteps[len(dashboardResolutionSteps)-1]
}

func dashboardStatusUint(value string) uint64 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func dashboardScanSummary(scan *gzfs.ZPoolStatusScanStats) *zfsServiceInterfaces.DashboardPoolScan {
	if scan == nil || strings.TrimSpace(scan.Function) == "" {
		return nil
	}

	toExamine := dashboardStatusUint(scan.ToExamine)
	examined := dashboardStatusUint(scan.Examined)
	issued := dashboardStatusUint(scan.Issued)
	processed := dashboardStatusUint(scan.Processed)
	progressValue := issued
	if strings.EqualFold(scan.Function, "resilver") && processed > 0 {
		progressValue = processed
	} else if progressValue == 0 {
		progressValue = examined
	}

	var progress *float64
	if toExamine > 0 {
		value := min(100, float64(progressValue)/float64(toExamine)*100)
		progress = &value
	}

	return &zfsServiceInterfaces.DashboardPoolScan{
		Function:        scan.Function,
		State:           scan.State,
		StartTime:       scan.StartTime,
		EndTime:         scan.EndTime,
		Examined:        examined,
		ToExamine:       toExamine,
		Issued:          issued,
		Processed:       processed,
		Errors:          dashboardStatusUint(scan.Errors),
		ProgressPercent: progress,
	}
}

func summarizeDashboardVDEVs(
	vdevs map[string]*gzfs.ZPoolStatusVDEV,
	errors *zfsServiceInterfaces.DashboardPoolErrors,
) int {
	leaves := 0
	for _, vdev := range vdevs {
		if vdev == nil {
			continue
		}
		if len(vdev.Vdevs) > 0 {
			leaves += summarizeDashboardVDEVs(vdev.Vdevs, errors)
			continue
		}
		leaves++
		errors.Read += dashboardStatusUint(vdev.ReadErrors)
		errors.Write += dashboardStatusUint(vdev.WriteErrors)
		errors.Checksum += dashboardStatusUint(vdev.ChkErrors)
	}
	return leaves
}

func summarizeDashboardPoolStatus(
	status *gzfs.ZPoolStatusPool,
) (zfsServiceInterfaces.DashboardPoolErrors, *zfsServiceInterfaces.DashboardPoolScan, zfsServiceInterfaces.DashboardPoolTopology) {
	var poolErrors zfsServiceInterfaces.DashboardPoolErrors
	var topology zfsServiceInterfaces.DashboardPoolTopology
	if status == nil {
		return poolErrors, nil, topology
	}

	topology.DataVDEVs = len(status.Vdevs)
	topology.Logs = len(status.Logs)
	topology.Cache = len(status.L2Cache)
	topology.Spares = len(status.Spares)
	topology.Special = len(status.Special)
	topology.Dedup = len(status.Dedup)
	topology.Disks += summarizeDashboardVDEVs(status.Vdevs, &poolErrors)
	topology.Disks += summarizeDashboardVDEVs(status.Logs, &poolErrors)
	topology.Disks += summarizeDashboardVDEVs(status.L2Cache, &poolErrors)
	topology.Disks += summarizeDashboardVDEVs(status.Spares, &poolErrors)
	topology.Disks += summarizeDashboardVDEVs(status.Special, &poolErrors)
	topology.Disks += summarizeDashboardVDEVs(status.Dedup, &poolErrors)
	scan := dashboardScanSummary(status.ScanStats)
	if scan != nil {
		poolErrors.Scan = scan.Errors
	}
	return poolErrors, scan, topology
}

func dashboardIOStats(stat poolIOStat) zfsServiceInterfaces.DashboardIOStats {
	result := zfsServiceInterfaces.DashboardIOStats{
		IntervalSeconds:     poolIOSampleIntervalSeconds,
		Valid:               !stat.SampledAt.IsZero(),
		LatencyAvailable:    stat.ReadLatencyAvailable || stat.WriteLatencyAvailable,
		ReadIOPS:            stat.ReadIOPS,
		WriteIOPS:           stat.WriteIOPS,
		ReadBytesPerSecond:  stat.ReadBytesPerSecond,
		WriteBytesPerSecond: stat.WriteBytesPerSecond,
	}
	if !stat.SampledAt.IsZero() {
		result.SampledAt = stat.SampledAt.UnixMilli()
	}
	if stat.ReadLatencyAvailable {
		readLatency := stat.ReadLatencyNanos
		result.ReadLatencyNanos = &readLatency
	}
	if stat.WriteLatencyAvailable {
		writeLatency := stat.WriteLatencyNanos
		result.WriteLatencyNanos = &writeLatency
	}
	return result
}

func (s *Service) GetDashboardSnapshot(ctx context.Context) (zfsServiceInterfaces.DashboardSnapshot, error) {
	now := time.Now().UTC()
	result := zfsServiceInterfaces.DashboardSnapshot{
		Pools:       make([]zfsServiceInterfaces.DashboardPoolSnapshot, 0),
		GeneratedAt: now.UnixMilli(),
	}

	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return result, fmt.Errorf("load managed pools for dashboard snapshot: %w", err)
	}
	pools := scope.pools
	s.setManagedPoolIOPools(pools)
	sort.Slice(pools, func(i, j int) bool {
		if pools[i] == nil {
			return false
		}
		if pools[j] == nil {
			return true
		}
		return pools[i].Name < pools[j].Name
	})

	var latestIO time.Time
	for _, pool := range pools {
		if pool == nil {
			continue
		}
		ioStat := s.getPoolIOStat(pool.Name, now)
		if ioStat.SampledAt.After(latestIO) {
			latestIO = ioStat.SampledAt
		}
		poolSnapshot := zfsServiceInterfaces.DashboardPoolSnapshot{
			GUID:          pool.PoolGUID,
			Name:          pool.Name,
			State:         string(pool.State),
			Size:          pool.Size,
			Allocated:     pool.Alloc,
			Free:          pool.Free,
			Fragmentation: pool.Fragmentation,
			DedupRatio:    pool.DedupRatio,
			IO:            dashboardIOStats(ioStat),
		}

		status, statusErr := pool.Status(ctx)
		if statusErr == nil && status != nil {
			poolSnapshot.StatusAvailable = true
			poolSnapshot.State = status.State
			poolSnapshot.Status = status.Status
			poolSnapshot.Action = status.Action
			poolSnapshot.Errors, poolSnapshot.Scan, poolSnapshot.Topology = summarizeDashboardPoolStatus(status)
		}
		result.Pools = append(result.Pools, poolSnapshot)
	}

	var arcRows []infoModels.ZFSARCHistorical
	if err := s.TelemetryDB.WithContext(ctx).Order("created_at DESC").Limit(2).Find(&arcRows).Error; err != nil {
		return result, fmt.Errorf("load ARC dashboard snapshot: %w", err)
	}
	if len(arcRows) > 0 {
		var previous *infoModels.ZFSARCHistorical
		if len(arcRows) > 1 {
			previous = &arcRows[1]
		}
		arc := arcPointFromRows(arcRows[0], previous)
		result.ARC = &arc
	}

	if !latestIO.IsZero() {
		result.SampledAt = latestIO.UnixMilli()
	} else if result.ARC != nil {
		result.SampledAt = result.ARC.Time
	}
	result.Stale = result.SampledAt == 0 || now.Sub(time.UnixMilli(result.SampledAt)) > poolIOStaleAfter
	return result, nil
}

func poolPointFromRow(row infoModels.ZPoolHistorical, timestamp int64) zfsServiceInterfaces.PoolStatPoint {
	worstHealth := row.WorstHealth
	if worstHealth == "" {
		worstHealth = row.Health
	}
	intervalSeconds := row.IntervalSeconds
	if intervalSeconds == 0 {
		intervalSeconds = poolIOSampleIntervalSeconds
	}
	sampleCount := row.SampleCount
	if sampleCount == 0 {
		sampleCount = 1
	}

	return zfsServiceInterfaces.PoolStatPoint{
		ID:                     row.ID,
		Time:                   timestamp,
		Health:                 row.Health,
		WorstHealth:            worstHealth,
		Allocated:              row.Allocated,
		Free:                   row.Free,
		Size:                   row.Size,
		Fragmentation:          row.Fragmentation,
		DedupRatio:             row.DedupRatio,
		ReadIOPS:               row.ReadIOPS,
		WriteIOPS:              row.WriteIOPS,
		ReadBytesPerSecond:     row.ReadBytesPerSecond,
		WriteBytesPerSecond:    row.WriteBytesPerSecond,
		ReadLatencyNanos:       row.ReadLatencyNanos,
		WriteLatencyNanos:      row.WriteLatencyNanos,
		MaxReadIOPS:            maxUint64(row.MaxReadIOPS, row.ReadIOPS),
		MaxWriteIOPS:           maxUint64(row.MaxWriteIOPS, row.WriteIOPS),
		MaxReadBytesPerSecond:  maxUint64(row.MaxReadBytesPerSecond, row.ReadBytesPerSecond),
		MaxWriteBytesPerSecond: maxUint64(row.MaxWriteBytesPerSecond, row.WriteBytesPerSecond),
		MaxReadLatencyNanos:    maxUint64(row.MaxReadLatencyNanos, row.ReadLatencyNanos),
		MaxWriteLatencyNanos:   maxUint64(row.MaxWriteLatencyNanos, row.WriteLatencyNanos),
		SampleCount:            sampleCount,
		IntervalSeconds:        intervalSeconds,
	}
}

func downsamplePoolRows(rows []infoModels.ZPoolHistorical, resolution time.Duration) []zfsServiceInterfaces.DashboardPoolSeries {
	if resolution <= 0 {
		resolution = 10 * time.Second
	}

	type bucketKey struct {
		identity string
		bucket   int64
	}
	buckets := make(map[bucketKey][]infoModels.ZPoolHistorical)
	identities := make(map[string]infoModels.ZPoolHistorical)
	seconds := int64(resolution / time.Second)
	for _, row := range rows {
		identity := zpoolHistoryIdentity(row)
		key := bucketKey{identity: identity, bucket: row.CreatedAt.Unix() / seconds}
		buckets[key] = append(buckets[key], row)
		identities[identity] = row
	}

	seriesPoints := make(map[string][]zfsServiceInterfaces.PoolStatPoint)
	for key, bucketRows := range buckets {
		rollup := buildZpoolRollup(bucketRows, resolution)
		for _, row := range bucketRows {
			if row.ID > rollup.ID {
				rollup.ID = row.ID
			}
		}
		seriesPoints[key.identity] = append(
			seriesPoints[key.identity],
			poolPointFromRow(rollup, key.bucket*seconds*int64(time.Second/time.Millisecond)),
		)
	}

	series := make([]zfsServiceInterfaces.DashboardPoolSeries, 0, len(seriesPoints))
	for identity, points := range seriesPoints {
		sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
		row := identities[identity]
		series = append(series, zfsServiceInterfaces.DashboardPoolSeries{
			GUID:   row.GUID,
			Name:   row.Name,
			Points: points,
		})
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Name < series[j].Name })
	return series
}

func rawPoolSeries(rows []infoModels.ZPoolHistorical) []zfsServiceInterfaces.DashboardPoolSeries {
	grouped := make(map[string][]zfsServiceInterfaces.PoolStatPoint)
	identities := make(map[string]infoModels.ZPoolHistorical)
	for _, row := range rows {
		identity := zpoolHistoryIdentity(row)
		identities[identity] = row
		grouped[identity] = append(grouped[identity], poolPointFromRow(row, row.CreatedAt.UnixMilli()))
	}

	series := make([]zfsServiceInterfaces.DashboardPoolSeries, 0, len(grouped))
	for identity, points := range grouped {
		sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
		row := identities[identity]
		series = append(series, zfsServiceInterfaces.DashboardPoolSeries{
			GUID:   row.GUID,
			Name:   row.Name,
			Points: points,
		})
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Name < series[j].Name })
	return series
}

func counterDelta(current, previous uint64) (uint64, bool) {
	if current < previous {
		return 0, false
	}
	return current - previous, true
}

func ratioFromCounters(hits, misses uint64) *float64 {
	total := hits + misses
	if total == 0 {
		return nil
	}
	value := float64(hits) / float64(total) * 100
	return &value
}

func deltaRatio(currentHits, previousHits, currentMisses, previousMisses uint64) *float64 {
	hits, hitsOK := counterDelta(currentHits, previousHits)
	misses, missesOK := counterDelta(currentMisses, previousMisses)
	if !hitsOK || !missesOK {
		return nil
	}
	return ratioFromCounters(hits, misses)
}

func arcPointFromRows(current infoModels.ZFSARCHistorical, previous *infoModels.ZFSARCHistorical) zfsServiceInterfaces.DashboardARCPoint {
	point := zfsServiceInterfaces.DashboardARCPoint{
		ID:               current.ID,
		Time:             current.CreatedAt.UnixMilli(),
		Size:             current.Size,
		TargetSize:       current.TargetSize,
		MinSize:          current.MinSize,
		MaxSize:          current.MaxSize,
		DataSize:         current.DataSize,
		MetadataSize:     current.MetadataSize,
		OtherSize:        current.OtherSize,
		HeaderSize:       current.HeaderSize,
		CompressedSize:   current.CompressedSize,
		UncompressedSize: current.UncompressedSize,
		L2DeviceCount:    current.L2DeviceCount,
		L2Size:           current.L2Size,
		L2Allocated:      current.L2Allocated,
	}

	if previous == nil || !current.CreatedAt.After(previous.CreatedAt) {
		point.HitRatio = ratioFromCounters(current.Hits, current.Misses)
		point.DemandHitRatio = ratioFromCounters(
			current.DemandDataHits+current.DemandMetadataHits,
			current.DemandDataMisses+current.DemandMetadataMisses,
		)
		point.PrefetchHitRatio = ratioFromCounters(
			current.PrefetchDataHits+current.PrefetchMetadataHits,
			current.PrefetchDataMisses+current.PrefetchMetadataMisses,
		)
		point.L2HitRatio = ratioFromCounters(current.L2Hits, current.L2Misses)
		return point
	}

	point.HitRatio = deltaRatio(current.Hits, previous.Hits, current.Misses, previous.Misses)
	point.DemandHitRatio = deltaRatio(
		current.DemandDataHits+current.DemandMetadataHits,
		previous.DemandDataHits+previous.DemandMetadataHits,
		current.DemandDataMisses+current.DemandMetadataMisses,
		previous.DemandDataMisses+previous.DemandMetadataMisses,
	)
	point.PrefetchHitRatio = deltaRatio(
		current.PrefetchDataHits+current.PrefetchMetadataHits,
		previous.PrefetchDataHits+previous.PrefetchMetadataHits,
		current.PrefetchDataMisses+current.PrefetchMetadataMisses,
		previous.PrefetchDataMisses+previous.PrefetchMetadataMisses,
	)
	point.L2HitRatio = deltaRatio(current.L2Hits, previous.L2Hits, current.L2Misses, previous.L2Misses)

	elapsedSeconds := current.CreatedAt.Sub(previous.CreatedAt).Seconds()
	if elapsedSeconds <= 0 {
		return point
	}
	if delta, ok := counterDelta(current.Deleted, previous.Deleted); ok {
		point.EvictionsPerSecond = float64(delta) / elapsedSeconds
	}
	if delta, ok := counterDelta(current.MemoryThrottleCount, previous.MemoryThrottleCount); ok {
		point.MemoryThrottleEvents = delta
	}
	if delta, ok := counterDelta(current.EvictNotEnough, previous.EvictNotEnough); ok {
		point.EvictNotEnoughEvents = delta
	}
	if delta, ok := counterDelta(current.L2ReadBytes, previous.L2ReadBytes); ok {
		point.L2ReadBytesPerSecond = uint64(float64(delta) / elapsedSeconds)
	}
	if delta, ok := counterDelta(current.L2WriteBytes, previous.L2WriteBytes); ok {
		point.L2WriteBytesPerSecond = uint64(float64(delta) / elapsedSeconds)
	}
	return point
}

func downsampleARCRows(rows []infoModels.ZFSARCHistorical, previous *infoModels.ZFSARCHistorical, resolution time.Duration) []zfsServiceInterfaces.DashboardARCPoint {
	if len(rows) == 0 {
		return make([]zfsServiceInterfaces.DashboardARCPoint, 0)
	}
	seconds := int64(resolution / time.Second)
	selected := make([]infoModels.ZFSARCHistorical, 0)
	for _, row := range rows {
		bucket := row.CreatedAt.Unix() / seconds
		if len(selected) == 0 || selected[len(selected)-1].CreatedAt.Unix()/seconds != bucket {
			selected = append(selected, row)
			continue
		}
		selected[len(selected)-1] = row
	}

	points := make([]zfsServiceInterfaces.DashboardARCPoint, 0, len(selected))
	prior := previous
	for index := range selected {
		points = append(points, arcPointFromRows(selected[index], prior))
		prior = &selected[index]
	}
	return points
}

func maxDashboardCursors(tx *gorm.DB, scope managedPoolScope) (zfsServiceInterfaces.DashboardCursors, error) {
	var cursors zfsServiceInterfaces.DashboardCursors
	poolCursorQuery := scope.filterHistoricalPools(tx.Model(&infoModels.ZPoolHistorical{}))
	if err := poolCursorQuery.Select("COALESCE(MAX(id), 0)").Scan(&cursors.Pool).Error; err != nil {
		return cursors, fmt.Errorf("read zpool dashboard cursor: %w", err)
	}
	if err := tx.Model(&infoModels.ZFSARCHistorical{}).Select("COALESCE(MAX(id), 0)").Scan(&cursors.ARC).Error; err != nil {
		return cursors, fmt.Errorf("read ARC dashboard cursor: %w", err)
	}
	return cursors, nil
}

func (s *Service) GetDashboardHistory(ctx context.Context, query zfsServiceInterfaces.DashboardHistoryQuery) (zfsServiceInterfaces.DashboardHistory, error) {
	result := zfsServiceInterfaces.DashboardHistory{
		Pools:       make([]zfsServiceInterfaces.DashboardPoolSeries, 0),
		ARC:         make([]zfsServiceInterfaces.DashboardARCPoint, 0),
		GeneratedAt: time.Now().UTC().UnixMilli(),
	}
	if !query.To.After(query.From) {
		return result, fmt.Errorf("invalid dashboard history window")
	}
	query.PoolGUID = strings.TrimSpace(query.PoolGUID)
	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return result, fmt.Errorf("load managed pools for dashboard history: %w", err)
	}
	if query.PoolGUID != "" && !scope.containsGUID(query.PoolGUID) {
		return result, classifyError(ErrPoolNotFound, "dashboard_pool_not_managed")
	}
	resolution := dashboardResolution(query.From, query.To, query.MaxPoints)
	result.ResolutionSeconds = int64(resolution / time.Second)

	err = s.TelemetryDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cursors, err := maxDashboardCursors(tx, scope)
		if err != nil {
			return err
		}
		result.Cursors = cursors

		poolQuery := scope.filterHistoricalPools(tx.
			Where("created_at >= ? AND created_at <= ?", query.From, query.To).
			Order("guid ASC, created_at ASC"))
		if query.PoolGUID != "" {
			poolQuery = poolQuery.Where("guid = ?", query.PoolGUID)
		}
		var poolRows []infoModels.ZPoolHistorical
		if err := poolQuery.Find(&poolRows).Error; err != nil {
			return fmt.Errorf("load zpool dashboard history: %w", err)
		}
		result.Pools = downsamplePoolRows(poolRows, resolution)

		var previousARC *infoModels.ZFSARCHistorical
		var predecessor infoModels.ZFSARCHistorical
		if err := tx.Where("created_at < ?", query.From).Order("created_at DESC").First(&predecessor).Error; err == nil {
			previousARC = &predecessor
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load ARC dashboard predecessor: %w", err)
		}
		var arcRows []infoModels.ZFSARCHistorical
		if err := tx.
			Where("created_at >= ? AND created_at <= ?", query.From, query.To).
			Order("created_at ASC").
			Find(&arcRows).Error; err != nil {
			return fmt.Errorf("load ARC dashboard history: %w", err)
		}
		result.ARC = downsampleARCRows(arcRows, previousARC, resolution)
		return nil
	})
	if err != nil {
		return zfsServiceInterfaces.DashboardHistory{}, err
	}
	return result, nil
}

func (s *Service) GetDashboardHistoryDelta(ctx context.Context, query zfsServiceInterfaces.DashboardDeltaQuery) (zfsServiceInterfaces.DashboardHistory, error) {
	result := zfsServiceInterfaces.DashboardHistory{
		Pools:       make([]zfsServiceInterfaces.DashboardPoolSeries, 0),
		ARC:         make([]zfsServiceInterfaces.DashboardARCPoint, 0),
		GeneratedAt: time.Now().UTC().UnixMilli(),
	}
	query.PoolGUID = strings.TrimSpace(query.PoolGUID)
	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return result, fmt.Errorf("load managed pools for dashboard delta: %w", err)
	}
	if query.PoolGUID != "" && !scope.containsGUID(query.PoolGUID) {
		return result, classifyError(ErrPoolNotFound, "dashboard_pool_not_managed")
	}

	err = s.TelemetryDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cursors, err := maxDashboardCursors(tx, scope)
		if err != nil {
			return err
		}
		result.Cursors = cursors

		poolQuery := scope.filterHistoricalPools(
			tx.Where("id > ?", query.PoolAfter).Order("id ASC").Limit(dashboardDeltaLimit + 1),
		)
		if query.PoolGUID != "" {
			poolQuery = poolQuery.Where("guid = ?", query.PoolGUID)
		}
		var poolRows []infoModels.ZPoolHistorical
		if err := poolQuery.Find(&poolRows).Error; err != nil {
			return fmt.Errorf("load zpool dashboard delta: %w", err)
		}
		if len(poolRows) > dashboardDeltaLimit {
			result.ResetRequired = true
			return nil
		}
		result.Pools = rawPoolSeries(poolRows)

		var previousARC *infoModels.ZFSARCHistorical
		if query.ARCAfter > 0 {
			var predecessor infoModels.ZFSARCHistorical
			if err := tx.Where("id <= ?", query.ARCAfter).Order("id DESC").First(&predecessor).Error; err == nil {
				previousARC = &predecessor
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("load ARC dashboard delta predecessor: %w", err)
			}
		}
		var arcRows []infoModels.ZFSARCHistorical
		if err := tx.Where("id > ?", query.ARCAfter).Order("id ASC").Limit(dashboardDeltaLimit + 1).Find(&arcRows).Error; err != nil {
			return fmt.Errorf("load ARC dashboard delta: %w", err)
		}
		if len(arcRows) > dashboardDeltaLimit {
			result.ResetRequired = true
			return nil
		}
		result.ARC = make([]zfsServiceInterfaces.DashboardARCPoint, 0, len(arcRows))
		prior := previousARC
		for index := range arcRows {
			result.ARC = append(result.ARC, arcPointFromRows(arcRows[index], prior))
			prior = &arcRows[index]
		}
		return nil
	})
	if err != nil {
		return zfsServiceInterfaces.DashboardHistory{}, err
	}
	return result, nil
}
