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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	poolScrubSchedulerJobName = "zfs_scrub_scheduler_tick"
	poolScrubEventJobName     = "zfs_scrub_event_deliver"
	poolScrubCatchupWindow    = 24 * time.Hour
	poolScrubDispatchLease    = 2 * time.Minute
	poolScrubCommandTimeout   = 30 * time.Second
)

type PoolScrubScheduleInput struct {
	PoolGUID string `json:"poolGuid" binding:"required"`
	CronExpr string `json:"cronExpr" binding:"required"`
	Enabled  bool   `json:"enabled"`
}

type PoolScrubScheduleView struct {
	zfsModels.PoolScrubSchedule
	Active bool `json:"active"`
}

type poolScrubSchedulerJob struct {
	RequestedAt   time.Time `json:"requestedAt"`
	DispatchToken string    `json:"dispatchToken"`
}

func validateScrubGUID(guid string) error {
	value, err := strconv.ParseUint(guid, 10, 64)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != guid {
		return classifyError(ErrInvalidRequest, "invalid_pool_guid")
	}
	return nil
}

func parsePoolScrubSchedule(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if len(expr) > 256 || len(strings.Fields(expr)) != 5 {
		return nil, classifyError(ErrInvalidRequest, "scrub_schedule_requires_five_field_cron")
	}
	parsed, err := cron.ParseStandard(expr)
	if err != nil || parsed.Next(time.Now().In(time.Local)).IsZero() {
		return nil, classifyError(ErrInvalidRequest, "invalid_scrub_schedule_cron")
	}
	return parsed, nil
}

func nextPoolScrubRun(parsed cron.Schedule, now time.Time) time.Time {
	return parsed.Next(now.In(time.Local)).UTC()
}

func (s *Service) withPoolScrubLease(ctx context.Context, apply func(context.Context) error) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("zfs_service_not_initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	token, err := db.AcquirePoolScrubLease(ctx, s.DB)
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer releaseCancel()
		if err := db.ReleasePoolScrubLease(releaseCtx, s.DB, token); err != nil {
			logger.L.Error().Err(err).Msg("zfs_scrub_lease_release_failed")
		}
	}()
	return apply(ctx)
}

func (s *Service) resolveManagedScrubPool(ctx context.Context, guid string) (*gzfs.ZPool, error) {
	if err := validateScrubGUID(guid); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
	defer cancel()
	scope, err := s.loadManagedPoolScope(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScrubStatusUnavailable, err)
	}
	for _, pool := range scope.pools {
		if pool.PoolGUID == guid {
			return pool, nil
		}
	}
	return nil, classifyError(ErrPoolNotFound, "scrub_pool_unavailable_or_unmanaged: %s", guid)
}

func (s *Service) withPoolScrubMutation(ctx context.Context, guid string, apply func(context.Context) error) error {
	return s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		if err := s.requireNoActivePoolScrub(ctx, guid); err != nil {
			return err
		}
		return apply(ctx)
	})
}

func (s *Service) ListPoolScrubSchedules(ctx context.Context) ([]PoolScrubScheduleView, error) {
	var records []zfsModels.PoolScrubSchedule
	if err := s.DB.WithContext(ctx).Order("pool_name ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	views := make([]PoolScrubScheduleView, len(records))
	var activeGUIDs []string
	if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubRun{}).
		Where("status IN ?", poolScrubActiveStatuses).Pluck("pool_guid", &activeGUIDs).Error; err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(activeGUIDs))
	for _, guid := range activeGUIDs {
		active[guid] = true
	}
	for i := range records {
		views[i] = PoolScrubScheduleView{PoolScrubSchedule: records[i], Active: active[records[i].PoolGUID]}
	}
	return views, nil
}

func (s *Service) CreatePoolScrubSchedule(ctx context.Context, input PoolScrubScheduleInput) (*PoolScrubScheduleView, error) {
	var view PoolScrubScheduleView
	err := s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		parsed, err := parsePoolScrubSchedule(input.CronExpr)
		if err != nil {
			return err
		}
		pool, err := s.resolveManagedScrubPool(ctx, input.PoolGUID)
		if err != nil {
			return err
		}
		activeErr := s.requireNoActivePoolScrub(ctx, input.PoolGUID)
		active := errors.Is(activeErr, db.ErrPoolScrubActive)
		if activeErr != nil && !active {
			return activeErr
		}
		next := nextPoolScrubRun(parsed, time.Now())
		record := zfsModels.PoolScrubSchedule{
			PoolGUID: input.PoolGUID, PoolName: pool.Name, CronExpr: strings.TrimSpace(input.CronExpr),
			Enabled: input.Enabled, NextRunAt: &next, LastStatus: "idle", ProgressPct: -1,
		}
		if err := s.DB.WithContext(ctx).Create(&record).Error; err != nil {
			return err
		}
		view = PoolScrubScheduleView{PoolScrubSchedule: record, Active: active}
		return nil
	})
	return &view, err
}

func (s *Service) UpdatePoolScrubSchedule(ctx context.Context, id uint, input PoolScrubScheduleInput) (*PoolScrubScheduleView, error) {
	var view PoolScrubScheduleView
	err := s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		var record zfsModels.PoolScrubSchedule
		if err := s.DB.WithContext(ctx).First(&record, id).Error; err != nil {
			return err
		}
		if input.PoolGUID != record.PoolGUID {
			return classifyError(ErrInvalidRequest, "scrub_schedule_pool_cannot_change")
		}
		if err := s.requireNoActivePoolScrub(ctx, record.PoolGUID); err != nil {
			return err
		}
		parsed, err := parsePoolScrubSchedule(input.CronExpr)
		if err != nil {
			return err
		}
		// Missing pools must not prevent administrators from disabling a schedule.
		if input.Enabled {
			pool, err := s.resolveManagedScrubPool(ctx, input.PoolGUID)
			if err != nil {
				return err
			}
			record.PoolName = pool.Name
		}
		next := nextPoolScrubRun(parsed, time.Now())
		record.CronExpr = strings.TrimSpace(input.CronExpr)
		record.Enabled = input.Enabled
		record.NextRunAt = &next
		record.QueuedAt = nil
		record.ScheduledFor = nil
		record.LastError = ""
		if record.LastStatus == "queued" {
			record.LastStatus = "idle"
		}
		if err := s.DB.WithContext(ctx).Save(&record).Error; err != nil {
			return err
		}
		view = PoolScrubScheduleView{PoolScrubSchedule: record}
		return nil
	})
	return &view, err
}

func (s *Service) DeletePoolScrubSchedule(ctx context.Context, id uint) error {
	return s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		var record zfsModels.PoolScrubSchedule
		if err := s.DB.WithContext(ctx).First(&record, id).Error; err != nil {
			return err
		}
		if err := s.requireNoActivePoolScrub(ctx, record.PoolGUID); err != nil {
			return err
		}
		// Runs and pending notification events outlive their schedule.
		return s.DB.WithContext(ctx).Delete(&record).Error
	})
}

func (s *Service) RegisterScrubJobs() {
	db.QueueRegisterJSON(poolScrubSchedulerJobName, s.handlePoolScrubSchedulerJob)
	db.QueueRegisterJSON(poolScrubEventJobName, s.handlePoolScrubEventJob)
}

func (s *Service) SetScrubSchedulerReady(ready bool) {
	s.scrubJobsReady.Store(ready)
}

func (s *Service) StartPoolScrubScheduler(ctx context.Context) {
	scheduleTicker := time.NewTicker(time.Minute)
	trackingTicker := time.NewTicker(15 * time.Second)
	eventTicker := time.NewTicker(5 * time.Second)
	defer scheduleTicker.Stop()
	defer trackingTicker.Stop()
	defer eventTicker.Stop()
	enqueue := func(now time.Time) {
		if err := s.enqueuePoolScrubTick(ctx, now); err != nil && !errors.Is(err, context.Canceled) {
			logger.L.Error().Err(err).Msg("zfs_scrub_scheduler_enqueue_failed")
		}
	}
	enqueue(time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-scheduleTicker.C:
			enqueue(now.UTC())
		case now := <-trackingTicker.C:
			var count int64
			if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubRun{}).Where("status IN ?", poolScrubActiveStatuses).Count(&count).Error; err != nil {
				logger.L.Error().Err(err).Msg("zfs_scrub_active_lookup_failed")
			} else if count > 0 {
				enqueue(now.UTC())
			}
		case now := <-eventTicker.C:
			if s.scrubJobsReady.Load() {
				if err := s.dispatchPoolScrubEvents(ctx, now.UTC()); err != nil && !errors.Is(err, context.Canceled) {
					logger.L.Error().Err(err).Msg("zfs_scrub_event_enqueue_failed")
				}
			}
		}
	}
}

func (s *Service) enqueuePoolScrubTick(ctx context.Context, now time.Time) error {
	if !s.scrubJobsReady.Load() {
		return nil
	}
	lease := zfsModels.PoolScrubSchedulerLease{ID: 1}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error; err != nil {
		return err
	}
	token := utils.GenerateRandomUUID()
	claim := s.DB.WithContext(ctx).Model(&lease).
		Where("id = 1 AND (dispatch_token = '' OR dispatched_at IS NULL OR dispatched_at < ?)", now.Add(-poolScrubDispatchLease)).
		Updates(map[string]any{"dispatch_token": token, "dispatched_at": now})
	if claim.Error != nil || claim.RowsAffected == 0 {
		return claim.Error
	}
	job := poolScrubSchedulerJob{RequestedAt: now, DispatchToken: token}
	var err error
	if s.scrubJobEnqueue != nil {
		err = s.scrubJobEnqueue(ctx, job)
	} else {
		err = db.EnqueueJSON(ctx, poolScrubSchedulerJobName, job)
	}
	if err != nil {
		return errors.Join(err, s.releasePoolScrubDispatch(context.WithoutCancel(ctx), token))
	}
	return nil
}

func (s *Service) releasePoolScrubDispatch(ctx context.Context, token string) error {
	return s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubSchedulerLease{}).
		Where("id = 1 AND dispatch_token = ?", token).
		Updates(map[string]any{"dispatch_token": "", "dispatched_at": nil}).Error
}

func (s *Service) handlePoolScrubSchedulerJob(ctx context.Context, job poolScrubSchedulerJob) error {
	if job.DispatchToken == "" || job.RequestedAt.IsZero() {
		return nil
	}
	defer s.releasePoolScrubDispatch(context.WithoutCancel(ctx), job.DispatchToken)
	now := time.Now().UTC()
	if !s.scrubJobsReady.Load() || now.Sub(job.RequestedAt) > poolScrubDispatchLease || job.RequestedAt.After(now.Add(time.Minute)) {
		return nil
	}
	var lease zfsModels.PoolScrubSchedulerLease
	if err := s.DB.WithContext(ctx).Where("id = 1 AND dispatch_token = ?", job.DispatchToken).First(&lease).Error; err != nil {
		return err
	}
	if err := s.runPoolScrubTick(ctx, now); err != nil && !errors.Is(err, db.ErrPoolScrubBusy) {
		logger.L.Error().Err(err).Msg("zfs_scrub_scheduler_tick_failed")
	}
	// Due work and reconciliation are persisted, so the next tick retries them.
	return nil
}

// Find only the latest occurrence within the catch-up window. Five-field cron
// bounds this loop to at most 1441 iterations, even after years of downtime.
func latestDuePoolScrub(parsed cron.Schedule, due, now time.Time) time.Time {
	latest := due.UTC()
	if latest.Before(now.Add(-poolScrubCatchupWindow)) {
		candidate := nextPoolScrubRun(parsed, now.Add(-poolScrubCatchupWindow-time.Nanosecond))
		if candidate.After(now) {
			return latest
		}
		latest = candidate
	}
	for next := nextPoolScrubRun(parsed, latest); !next.IsZero() && !next.After(now); next = nextPoolScrubRun(parsed, latest) {
		latest = next
	}
	return latest
}

func (s *Service) runPoolScrubTick(ctx context.Context, now time.Time) error {
	return s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		now = now.UTC()
		var active []zfsModels.PoolScrubRun
		if err := s.DB.WithContext(ctx).Where("status IN ?", poolScrubActiveStatuses).Order("id ASC").Find(&active).Error; err != nil {
			return err
		}
		for i := range active {
			if err := s.reconcilePoolScrubRun(ctx, &active[i], now); err != nil {
				return err
			}
		}
		if err := s.skipRedundantQueuedPoolScrubs(ctx, now); err != nil {
			return err
		}
		var schedules []zfsModels.PoolScrubSchedule
		if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&schedules).Error; err != nil {
			return err
		}
		for i := range schedules {
			record := &schedules[i]
			if record.NextRunAt != nil && record.NextRunAt.After(now) {
				continue
			}
			parsed, err := parsePoolScrubSchedule(record.CronExpr)
			if err != nil {
				record.Enabled = false
				record.LastStatus = "failed"
				record.LastError = err.Error()
				record.NextRunAt = nil
				record.QueuedAt = nil
				record.ScheduledFor = nil
				if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
					return err
				}
				continue
			}
			due := now
			if record.NextRunAt != nil {
				due = latestDuePoolScrub(parsed, *record.NextRunAt, now)
			}
			next := nextPoolScrubRun(parsed, now)
			record.NextRunAt = &next
			if poolScrubStatusActive(record.LastStatus) {
				occurrence := *record
				occurrence.ScheduledFor = &due
				if err := s.finishUnstartedPoolScrub(ctx, &occurrence, now, "skipped", poolScrubOverlapReason); err != nil {
					return err
				}
				continue
			}
			if record.LastStatus == "queued" {
				// Coalesce while waiting for another pool's scheduled slot or a resilver.
				if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
					return err
				}
				continue
			}
			record.ScheduledFor = &due
			if now.Sub(due) > poolScrubCatchupWindow {
				if err := s.finishUnstartedPoolScrub(ctx, record, now, "missed", "scheduled run was missed while Sylve was unavailable"); err != nil {
					return err
				}
				continue
			}
			reason, err := s.scheduledPoolScrubSkipReason(ctx, record, now)
			if err != nil {
				return err
			}
			if reason != "" {
				if err := s.finishUnstartedPoolScrub(ctx, record, now, "skipped", reason); err != nil {
					return err
				}
				continue
			}
			record.QueuedAt = &now
			record.LastStatus = "queued"
			record.LastError = ""
			record.ProgressPct = -1
			record.ProgressKnown = false
			if err := s.DB.WithContext(ctx).Save(record).Error; err != nil {
				return err
			}
		}
		var count int64
		if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubRun{}).
			Where("source = 'scheduled' AND status IN ?", poolScrubActiveStatuses).Count(&count).Error; err != nil {
			return err
		}
		var queued []zfsModels.PoolScrubSchedule
		if err := s.DB.WithContext(ctx).Where("enabled = ? AND last_status = 'queued'", true).Order("queued_at ASC, id ASC").Find(&queued).Error; err != nil {
			return err
		}
		for i := range queued {
			record := &queued[i]
			var run *zfsModels.PoolScrubRun
			var err error
			if count > 0 {
				// Observe overlaps even while another pool holds the node slot;
				// the external scan may be replaced before that slot is free.
				err = s.inspectQueuedPoolScrubScan(ctx, record, now)
				if err == nil {
					continue
				}
			} else {
				run, err = s.startPoolScrubRun(ctx, record.PoolGUID, record, now)
			}
			if run != nil {
				// Once intent exists, reconciliation owns this occurrence even if
				// the command or subsequent persistence returned an error.
				return errors.Join(err, s.skipRedundantQueuedPoolScrubs(ctx, now))
			}
			var skipped *poolScrubSkippedError
			if errors.As(err, &skipped) {
				if err := s.finishUnstartedPoolScrub(ctx, record, now, "skipped", skipped.reason); err != nil {
					return err
				}
				continue
			}
			if errors.Is(err, db.ErrPoolScrubActive) || errors.Is(err, ErrConflict) || errors.Is(err, ErrScrubStatusUnavailable) {
				record.LastError = err.Error()
				if saveErr := s.DB.WithContext(ctx).Save(record).Error; saveErr != nil {
					return saveErr
				}
				continue
			}
			if errors.Is(err, ErrPoolNotFound) {
				if err := s.finishUnstartedPoolScrub(ctx, record, now, "failed", err.Error()); err != nil {
					return err
				}
				continue
			}
			return err
		}
		return nil
	})
}

// Pool-scope persistence and schedule suspension share the same start lease.
// The caller supplies the existing settings/cache transaction, so failures roll
// back both settings and schedule changes together.
func (s *Service) WithUsablePoolsChange(ctx context.Context, names []string, update func(*gorm.DB) error) error {
	return s.withPoolScrubLease(ctx, func(ctx context.Context) error {
		var guids []string
		if len(names) > 0 {
			lookupCtx, cancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
			defer cancel()
			var pools []*gzfs.ZPool
			var err error
			if s.listHostPools != nil {
				pools, err = s.listHostPools(lookupCtx)
			} else {
				pools, err = s.GZFS.Zpool.List(lookupCtx)
			}
			if err != nil {
				return err
			}
			for _, pool := range pools {
				if pool != nil && slices.Contains(names, pool.Name) {
					guids = append(guids, pool.PoolGUID)
				}
			}
		}
		return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			runs := tx.Model(&zfsModels.PoolScrubRun{}).Where("status IN ?", poolScrubActiveStatuses)
			schedules := tx.Model(&zfsModels.PoolScrubSchedule{})
			if len(guids) > 0 {
				runs = runs.Where("pool_guid NOT IN ?", guids)
				schedules = schedules.Where("pool_guid NOT IN ?", guids)
			}
			var count int64
			if err := runs.Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return db.ErrPoolScrubActive
			}
			if err := schedules.Updates(map[string]any{
				"enabled": false, "queued_at": nil, "scheduled_for": nil, "next_run_at": nil,
				"last_status": "idle", "last_error": "pool is no longer managed by Sylve",
			}).Error; err != nil {
				return err
			}
			return update(tx)
		})
	})
}
