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
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
)

const poolScrubMinimumGap = 24 * time.Hour
const poolScrubOverlapReason = "scheduled occurrence skipped because another scrub is active or starting on this pool"

type poolScrubSkippedError struct{ reason string }

func (e *poolScrubSkippedError) Error() string { return e.reason }

func poolScrubCooldownReason(completedAt time.Time, due *time.Time, now time.Time) string {
	until := completedAt.Add(poolScrubMinimumGap)
	if now.Before(until) || due != nil && due.Before(until) {
		return fmt.Sprintf("scheduled occurrence skipped by the 24-hour minimum gap after scrub completion at %s (UTC)", completedAt.UTC().Format(time.RFC3339))
	}
	return ""
}

func (s *Service) scheduledPoolScrubSkipReason(ctx context.Context, schedule *zfsModels.PoolScrubSchedule, now time.Time) (string, error) {
	if err := s.requireNoActivePoolScrub(ctx, schedule.PoolGUID); err != nil {
		if errors.Is(err, db.ErrPoolScrubActive) {
			return poolScrubOverlapReason, nil
		}
		return "", err
	}
	var cooldown zfsModels.PoolScrubCooldown
	if err := s.DB.WithContext(ctx).Where("pool_guid = ?", schedule.PoolGUID).Find(&cooldown).Error; err != nil {
		return "", err
	}
	if cooldown.PoolGUID == "" {
		return "", nil
	}
	return poolScrubCooldownReason(cooldown.LastCompletedAt, schedule.ScheduledFor, now), nil
}

func (s *Service) checkScheduledPoolScrubScan(ctx context.Context, schedule *zfsModels.PoolScrubSchedule, status *gzfs.ZPoolStatusPool, now time.Time) error {
	if status == nil || status.PoolGUID != schedule.PoolGUID {
		return fmt.Errorf("%w: pool identity missing", ErrScrubStatusUnavailable)
	}
	scan := status.ScanStats
	if scan == nil {
		// A verified no-scan baseline is still checked by the shared start path.
		return nil
	}
	function, state := strings.ToUpper(scan.Function), strings.ToUpper(scan.State)
	if function != "SCRUB" && function != "ERRORSCRUB" {
		return nil
	}
	if state == "SCANNING" || state == "ERRORSCRUBBING" || state == "PAUSED" {
		return &poolScrubSkippedError{reason: poolScrubOverlapReason}
	}
	if function != "SCRUB" || state != "FINISHED" && state != "CANCELED" && state != "CANCELLED" {
		return nil
	}
	start, end := scrubScanTime(scan.StartTime), scrubScanTime(scan.EndTime)
	if start == nil || end == nil || end.Before(*start) || end.After(now.Add(time.Minute)) {
		return fmt.Errorf("%w: previous scrub completion time unavailable", ErrScrubStatusUnavailable)
	}
	// External and manual scrubs also cover scheduled work, without inventing
	// tracked history or taking ownership of the native scan.
	if err := db.RecordPoolScrubCompletion(s.DB.WithContext(ctx), schedule.PoolGUID, *end); err != nil {
		return err
	}
	if reason := poolScrubCooldownReason(*end, schedule.ScheduledFor, now); reason != "" {
		return &poolScrubSkippedError{reason: reason}
	}
	return nil
}

func (s *Service) inspectQueuedPoolScrubScan(ctx context.Context, schedule *zfsModels.PoolScrubSchedule, now time.Time) error {
	if _, err := s.resolveManagedScrubPool(ctx, schedule.PoolGUID); err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
	defer cancel()
	status, err := s.poolScrubBackend().Read(readCtx, schedule.PoolGUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrScrubStatusUnavailable, err)
	}
	return s.checkScheduledPoolScrubScan(ctx, schedule, status, now)
}

func (s *Service) skipRedundantQueuedPoolScrubs(ctx context.Context, now time.Time) error {
	var queued []zfsModels.PoolScrubSchedule
	if err := s.DB.WithContext(ctx).Where("enabled = ? AND last_status = 'queued'", true).Order("id ASC").Find(&queued).Error; err != nil {
		return err
	}
	for i := range queued {
		reason, err := s.scheduledPoolScrubSkipReason(ctx, &queued[i], now)
		if err != nil {
			return err
		}
		if reason != "" {
			if err := s.finishUnstartedPoolScrub(ctx, &queued[i], now, "skipped", reason); err != nil {
				return err
			}
		}
	}
	return nil
}
