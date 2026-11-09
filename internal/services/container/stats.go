// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/metrics"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	statsInterval        = time.Minute
	metricsStatsInterval = 15 * time.Second
	retentionInterval    = 10 * time.Minute
)

func (s *Service) StartStatsMonitoring(ctx context.Context) {
	s.monitorOnce.Do(func() {
		go func() {
			// Metrics snapshots expire after a minute, so sample faster while
			// still keeping one history row per minute.
			interval := statsInterval
			if config.GetMetricsConfig().Enabled {
				interval = metricsStatsInterval
			}
			persistEvery := int(statsInterval / interval)
			stats, retention := time.NewTicker(interval), time.NewTicker(retentionInterval)
			defer stats.Stop()
			defer retention.Stop()
			for ticks := 0; ; {
				select {
				case <-ctx.Done():
					return
				case <-stats.C:
					if err := s.storeUsage(ctx, ticks%persistEvery == 0); err != nil {
						logger.L.Warn().Err(err).Msg("failed_to_store_container_usage")
					}
					ticks++
				case <-retention.C:
					if err := s.applyStatsRetention(ctx); err != nil {
						logger.L.Warn().Err(err).Msg("failed_to_apply_container_stats_retention")
					}
				}
			}
		}()
	})
}

type racctUsage struct {
	cputime, pcpu, memory float64
}

var runRctl = func(ctx context.Context, runtimeID string) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/bin/rctl", "-u", rctlSubject(runtimeID)).Output()
}

// rctl reports zeros rather than an error for a jail that doesn't exist.
func readUsage(ctx context.Context, runtimeID string) (racctUsage, error) {
	output, err := runRctl(ctx, runtimeID)
	if err != nil {
		return racctUsage{}, err
	}
	var current racctUsage
	for _, line := range strings.Fields(string(output)) {
		key, value, _ := strings.Cut(line, "=")
		number, _ := strconv.ParseFloat(value, 64)
		switch key {
		case "cputime":
			current.cputime = number
		case "pcpu":
			current.pcpu = number
		case "memoryuse":
			current.memory = number
		}
	}
	return current, nil
}

func cpuCores(record containerModels.Container) float64 {
	if record.Cores > 0 {
		return float64(record.Cores)
	}
	return float64(runtime.NumCPU())
}

func usagePercent(record containerModels.Container, current racctUsage) (float64, float64, error) {
	limit := float64(record.Memory)
	if limit == 0 {
		total, err := utils.GetSystemMemoryBytes()
		if err != nil {
			return 0, 0, err
		}
		limit = float64(total)
	}
	clamp := func(value float64) float64 { return math.Round(math.Max(0, math.Min(100, value))*100) / 100 }
	return clamp(current.pcpu / cpuCores(record)), clamp(current.memory / limit * 100), nil
}

func (s *Service) storeUsage(ctx context.Context, persist bool) error {
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Where("creation_pending = ?", false).Find(&records).Error; err != nil {
		s.metricsCache.Fail()
		return err
	}
	samples := make([]metrics.Container, 0, len(records))
	var persistErr error
	for _, record := range records {
		current, err := readUsage(ctx, record.RuntimeID)
		if err != nil {
			// rctl fails for every jail at once, e.g. with RACCT disabled;
			// collector health reports it.
			s.metricsCache.Fail()
			return nil
		}
		running := current.memory > 0
		samples = append(samples, metrics.Container{
			OCID: record.OCID, Name: record.Name, Image: record.Image, Pool: record.Pool, Stack: record.Stack,
			Running:         running,
			CPUSeconds:      metrics.OptionalValue{Value: current.cputime, Available: running},
			MemoryRSS:       metrics.OptionalValue{Value: current.memory, Available: running},
			CPUCores:        cpuCores(record),
			MemoryLimit:     record.Memory,
			UpdateAvailable: record.UpdateDigest != "",
		})
		if !persist || !running || persistErr != nil {
			continue
		}
		if cpu, memory, err := usagePercent(record, current); err == nil {
			persistErr = s.DB.WithContext(ctx).Create(&containerModels.Stats{ContainerID: record.ID, CPUUsage: cpu, MemoryUsage: memory}).Error
		}
	}
	s.metricsCache.Store(samples, true)
	return persistErr
}

func (s *Service) MetricsSnapshot() metrics.Snapshot[metrics.Container] {
	return s.metricsCache.Snapshot()
}

func (s *Service) applyStatsRetention(ctx context.Context) error {
	var ids []uint
	if err := s.DB.WithContext(ctx).Model(&containerModels.Stats{}).Distinct().Pluck("container_id", &ids).Error; err != nil {
		return err
	}
	now := time.Now()
	for _, id := range ids {
		var stats []containerModels.Stats
		if err := s.DB.WithContext(ctx).Where("container_id = ?", id).Order("created_at ASC").Find(&stats).Error; err != nil {
			return err
		}
		if _, deleteIDs := db.ApplyGFS(now, stats); len(deleteIDs) > 0 {
			if err := s.DB.WithContext(ctx).Where("id IN ?", deleteIDs).Delete(&containerModels.Stats{}).Error; err != nil {
				return err
			}
		}
	}
	return s.DB.WithContext(ctx).Where("container_id NOT IN (?)", s.DB.Model(&containerModels.Container{}).Select("id")).
		Delete(&containerModels.Stats{}).Error
}

func (s *Service) Usage(ctx context.Context, ocid uint, step db.GFSStep) ([]containerModels.Stats, error) {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return nil, err
	}
	window, err := step.Window()
	if err != nil {
		return nil, err
	}
	var stats []containerModels.Stats
	err = s.DB.WithContext(ctx).Where("container_id = ? AND created_at >= ?", record.ID, time.Now().Add(-window)).
		Order("created_at ASC").Find(&stats).Error
	return stats, err
}

func (s *Service) UsageBootstrap(ctx context.Context, ocid uint) (db.StatsBootstrap[containerModels.Stats], error) {
	now := time.Now()
	record, err := s.record(ctx, ocid)
	if err != nil {
		return db.BuildStatsBootstrap[containerModels.Stats](now, nil, nil), err
	}
	var latest containerModels.Stats
	var latestAt *time.Time
	result := s.DB.WithContext(ctx).Where("container_id = ?", record.ID).Order("created_at DESC").Limit(1).Find(&latest)
	if result.Error != nil {
		return db.BuildStatsBootstrap[containerModels.Stats](now, nil, nil), result.Error
	}
	if result.RowsAffected > 0 {
		latestAt = &latest.CreatedAt
	}
	availability := db.ResolveStatsAvailability(now, latestAt)
	if availability.ResolvedStep == nil {
		return db.BuildStatsBootstrap[containerModels.Stats](now, nil, latestAt), nil
	}
	points, err := s.Usage(ctx, ocid, *availability.ResolvedStep)
	return db.BuildStatsBootstrap(now, points, latestAt), err
}
