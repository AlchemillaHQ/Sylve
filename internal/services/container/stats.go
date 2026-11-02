// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	statsInterval     = time.Minute
	retentionInterval = 10 * time.Minute
)

func (s *Service) StartStatsMonitoring(ctx context.Context) {
	s.monitorOnce.Do(func() {
		go func() {
			stats, retention := time.NewTicker(statsInterval), time.NewTicker(retentionInterval)
			defer stats.Stop()
			defer retention.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-stats.C:
					if err := s.storeUsage(ctx); err != nil {
						logger.L.Warn().Err(err).Msg("failed_to_store_container_usage")
					}
				case <-retention.C:
					if err := s.applyStatsRetention(ctx); err != nil {
						logger.L.Warn().Err(err).Msg("failed_to_apply_container_stats_retention")
					}
				}
			}
		}()
	})
}

func usage(ctx context.Context, record containerModels.Container) (float64, float64, error) {
	output, err := exec.CommandContext(ctx, "/usr/bin/rctl", "-u", rctlSubject(record.RuntimeID)).Output()
	if err != nil {
		return 0, 0, err
	}
	var pcpu, memory float64
	for _, line := range strings.Fields(string(output)) {
		key, value, _ := strings.Cut(line, "=")
		number, _ := strconv.ParseFloat(value, 64)
		switch key {
		case "pcpu":
			pcpu = number
		case "memoryuse":
			memory = number
		}
	}
	if memory == 0 {
		return 0, 0, fmt.Errorf("container_not_running")
	}

	cores := float64(runtime.NumCPU())
	if record.Cores > 0 {
		cores = float64(record.Cores)
	}
	limit := float64(record.Memory)
	if limit == 0 {
		total, err := utils.GetSystemMemoryBytes()
		if err != nil {
			return 0, 0, err
		}
		limit = float64(total)
	}
	clamp := func(value float64) float64 { return math.Round(math.Max(0, math.Min(100, value))*100) / 100 }
	return clamp(pcpu / cores), clamp(memory / limit * 100), nil
}

func (s *Service) storeUsage(ctx context.Context) error {
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Where("creation_pending = ?", false).Find(&records).Error; err != nil {
		return err
	}
	for _, record := range records {
		cpu, memory, err := usage(ctx, record)
		if err != nil {
			continue
		}
		if err := s.DB.WithContext(ctx).Create(&containerModels.Stats{ContainerID: record.ID, CPUUsage: cpu, MemoryUsage: memory}).Error; err != nil {
			return err
		}
	}
	return nil
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
