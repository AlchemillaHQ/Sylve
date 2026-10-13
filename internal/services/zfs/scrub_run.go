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
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrScrubStatusUnavailable = errors.New("scrub_status_unavailable")

var poolScrubActiveStatuses = []string{"starting", "running", "paused"}

const poolScrubHistoryLimit = 64
const poolScrubAcknowledgementWindow = 5 * time.Minute

type PoolScrubRunView zfsModels.PoolScrubRun

type poolScrubBackend interface {
	Read(context.Context, string) (*gzfs.ZPoolStatusPool, error)
	Start(context.Context, string, *gzfs.ZPoolStatusScanStats) error
}

type libraryPoolScrubBackend struct{ service *Service }

func (b libraryPoolScrubBackend) Read(ctx context.Context, guid string) (*gzfs.ZPoolStatusPool, error) {
	pool, err := b.service.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil || pool == nil {
		return nil, poolLookupError(err, "pool_not_found")
	}
	return pool.Status(ctx)
}

func (b libraryPoolScrubBackend) Start(ctx context.Context, guid string, baseline *gzfs.ZPoolStatusScanStats) error {
	pool, err := b.service.GZFS.Zpool.GetByGUID(ctx, guid)
	if err != nil || pool == nil {
		return poolLookupError(err, "pool_not_found")
	}
	status, err := pool.Status(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrScrubStatusUnavailable, err)
	}
	current, err := poolScrubStartBaseline(status, guid)
	if err != nil {
		return err
	}
	if current.StartTime != baseline.StartTime || current.Function != baseline.Function {
		return classifyError(ErrConflict, "pool_scan_changed_before_scrub_start")
	}
	return pool.Scrub(ctx)
}

func (s *Service) poolScrubBackend() poolScrubBackend {
	if s.scrubDriver != nil {
		return s.scrubDriver
	}
	return libraryPoolScrubBackend{service: s}
}

func poolScrubStatusActive(status string) bool {
	return status == "starting" || status == "running" || status == "paused"
}

func scrubScanTime(value string) *time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 || seconds > 253402300799 {
		return nil
	}
	valueTime := time.Unix(seconds, 0).UTC()
	return &valueTime
}

func scrubCounter(value string) *string {
	number, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil
	}
	canonical := strconv.FormatUint(number, 10)
	return &canonical
}

func validatePoolScrubStatus(status *gzfs.ZPoolStatusPool, guid string) error {
	if status == nil || status.PoolGUID != guid || status.ScanStats == nil {
		return fmt.Errorf("%w: pool identity or scan data missing", ErrScrubStatusUnavailable)
	}
	return nil
}

func poolScrubStartBaseline(status *gzfs.ZPoolStatusPool, guid string) (*gzfs.ZPoolStatusScanStats, error) {
	// OpenZFS omits scan_stats before any scan exists. A verified pool
	// configuration can supply an idle start baseline, never a run result.
	if status != nil && status.ScanStats == nil {
		root := status.Vdevs[status.Name]
		if status.Name != "" && status.State != "" && root != nil && root.VdevType == "root" && root.GUID == guid {
			normalized := *status
			normalized.ScanStats = &gzfs.ZPoolStatusScanStats{Function: "NONE", State: "NONE", StartTime: "0"}
			status = &normalized
		}
	}
	if err := validatePoolScrubStatus(status, guid); err != nil {
		return nil, err
	}
	switch strings.ToUpper(status.ScanStats.State) {
	case "SCANNING", "ERRORSCRUBBING", "PAUSED":
		return nil, classifyError(ErrConflict, "pool_scan_already_active")
	case "NONE", "FINISHED", "CANCELED", "CANCELLED":
		return status.ScanStats, nil
	default:
		return nil, fmt.Errorf("%w: unrecognized pool scan state", ErrScrubStatusUnavailable)
	}
}

func (s *Service) requireNoActivePoolScrub(ctx context.Context, guid string) error {
	var count int64
	if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubRun{}).
		Where("pool_guid = ? AND status IN ?", guid, poolScrubActiveStatuses).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return db.ErrPoolScrubActive
	}
	return nil
}

func (s *Service) ListPoolScrubRuns(ctx context.Context, guid string) ([]PoolScrubRunView, error) {
	if err := validateScrubGUID(guid); err != nil {
		return nil, err
	}
	views := make([]PoolScrubRunView, 0)
	if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubRun{}).
		Where("pool_guid = ?", guid).Order("requested_at DESC, id DESC").Limit(poolScrubHistoryLimit + 1).Find(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

func scheduledPoolScrubRunKey(record *zfsModels.PoolScrubSchedule) string {
	return fmt.Sprintf("scheduled|%d|%s|%s", record.ID, record.PoolGUID, record.ScheduledFor.UTC().Format(time.RFC3339Nano))
}

func (s *Service) startPoolScrubRun(ctx context.Context, guid string, schedule *zfsModels.PoolScrubSchedule, now time.Time) (*zfsModels.PoolScrubRun, error) {
	if schedule != nil {
		if schedule.ScheduledFor == nil {
			return nil, fmt.Errorf("scrub_schedule_occurrence_missing")
		}
		reason, err := s.scheduledPoolScrubSkipReason(ctx, schedule, now)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			return nil, &poolScrubSkippedError{reason: reason}
		}
	} else {
		if err := s.requireNoActivePoolScrub(ctx, guid); err != nil {
			return nil, err
		}
		var reserved int64
		if err := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubSchedule{}).
			Where("pool_guid = ? AND enabled = ? AND last_status = 'queued'", guid, true).Count(&reserved).Error; err != nil {
			return nil, err
		}
		if reserved > 0 {
			return nil, classifyError(ErrConflict, "pool_scrub_schedule_queued")
		}
	}
	pool, err := s.resolveManagedScrubPool(ctx, guid)
	if err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
	status, err := s.poolScrubBackend().Read(readCtx, guid)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScrubStatusUnavailable, err)
	}
	if schedule != nil {
		if err := s.checkScheduledPoolScrubScan(ctx, schedule, status, now); err != nil {
			return nil, err
		}
	}
	baseline, err := poolScrubStartBaseline(status, guid)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	run := &zfsModels.PoolScrubRun{
		PoolGUID: guid, PoolName: pool.Name,
		Source: "manual", RequestedAt: now, Status: "starting", ProgressPct: -1,
		BaselineStart: scrubScanTime(baseline.StartTime),
	}
	if schedule != nil {
		run.RunKey = scheduledPoolScrubRunKey(schedule)
		run.Source = "scheduled"
		run.ScheduleID = &schedule.ID
		run.ScheduledFor = schedule.ScheduledFor
	} else {
		run.RunKey = "manual|" + utils.GenerateRandomUUID()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Persist intent before touching ZFS. A crash after this point is reconciled,
	// never retried by issuing another scrub command for the same occurrence.
	if err := s.savePoolScrubRun(ctx, run); err != nil {
		return nil, err
	}
	commandCtx, commandCancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
	startErr := s.poolScrubBackend().Start(commandCtx, guid, baseline)
	commandCancelled := commandCtx.Err() != nil
	commandCancel()
	commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(ctx), poolScrubCommandTimeout)
	defer commitCancel()
	if startErr != nil {
		run.Error = startErr.Error()
		var commandError *gzfs.CmdError
		definite := !commandCancelled && (errors.As(startErr, &commandError) || errors.Is(startErr, ErrConflict) || errors.Is(startErr, ErrPoolNotFound))
		if definite {
			run.Status = "failed"
			run.CompletedAt = &now
			if err := s.savePoolScrubRun(commitCtx, run, newPoolScrubEvent(*run, "scrub_start_failed")); err != nil {
				return run, errors.Join(startErr, err)
			}
			return run, startErr
		}
		if err := s.savePoolScrubRun(commitCtx, run); err != nil {
			return run, errors.Join(startErr, err)
		}
		return run, fmt.Errorf("%w: start result uncertain; waiting for scan evidence: %v", ErrScrubStatusUnavailable, startErr)
	}
	return run, s.reconcilePoolScrubRun(commitCtx, run, time.Now().UTC())
}

func (s *Service) reconcilePoolScrubRun(ctx context.Context, run *zfsModels.PoolScrubRun, now time.Time) error {
	readCtx, cancel := context.WithTimeout(ctx, poolScrubCommandTimeout)
	status, err := s.poolScrubBackend().Read(readCtx, run.PoolGUID)
	cancel()
	if err == nil {
		err = validatePoolScrubStatus(status, run.PoolGUID)
	}
	if err != nil {
		return s.poolScrubObservationLost(ctx, run, now, err.Error())
	}
	run.PoolName = status.Name
	scan := status.ScanStats
	start := scrubScanTime(scan.StartTime)
	if run.StartedAt == nil {
		newScan := strings.EqualFold(scan.Function, "SCRUB") && start != nil &&
			(run.BaselineStart == nil || !start.Equal(*run.BaselineStart)) &&
			!start.Before(run.RequestedAt.Truncate(time.Second)) &&
			!start.After(run.RequestedAt.Add(poolScrubAcknowledgementWindow))
		if !newScan {
			run.Error = "waiting for evidence of a new scrub scan"
			if now.Sub(run.RequestedAt) >= poolScrubAcknowledgementWindow {
				return s.finishPoolScrubUnknown(ctx, run, now, "scrub start could not be correlated with a new scan")
			}
			return s.savePoolScrubRun(ctx, run)
		}
		run.StartedAt = start
	} else if start == nil || !strings.EqualFold(scan.Function, "SCRUB") || !start.Equal(*run.StartedAt) {
		return s.finishPoolScrubUnknown(ctx, run, now, "scrub scan was replaced or its result is no longer available")
	}
	state := strings.ToUpper(scan.State)
	if state != "SCANNING" && state != "PAUSED" && state != "FINISHED" && state != "CANCELED" && state != "CANCELLED" {
		return s.poolScrubObservationLost(ctx, run, now, "unrecognized scrub scan state")
	}
	run.ObservationLostAt = nil
	run.Error = ""
	run.Errors = scrubCounter(scan.Errors)
	run.RepairedBytes = scrubCounter(scan.Processed)
	run.ExaminedBytes = scrubCounter(scan.Examined)
	run.IssuedBytes = scrubCounter(scan.Issued)
	run.TotalBytes = scrubCounter(scan.ToExamine)
	run.ProgressPct = -1
	run.ProgressKnown = false
	if run.TotalBytes != nil {
		total, _ := strconv.ParseUint(*run.TotalBytes, 10, 64)
		progressValue := run.IssuedBytes
		if progressValue == nil || *progressValue == "0" {
			progressValue = run.ExaminedBytes
		}
		if total > 0 && progressValue != nil {
			issued, _ := strconv.ParseUint(*progressValue, 10, 64)
			run.ProgressPct = int(min(1.0, float64(issued)/float64(total)) * 100)
			run.ProgressKnown = true
		}
	}
	var events []zfsModels.PoolScrubEvent
	if !run.StartedEventRecorded {
		run.StartedEventRecorded = true
		events = append(events, newPoolScrubEvent(*run, "scrub_started"))
	}
	if state == "SCANNING" || state == "PAUSED" {
		run.Status = "running"
		if scrubScanTime(scan.ScrubPause) != nil || state == "PAUSED" {
			run.Status = "paused"
		}
		return s.savePoolScrubRun(ctx, run, events...)
	}
	end := scrubScanTime(scan.EndTime)
	if end == nil || end.Before(*run.StartedAt) || end.After(now.Add(time.Minute)) {
		return s.finishPoolScrubUnknown(ctx, run, now, "scrub result has invalid completion timestamps", events...)
	}
	run.CompletedAt = end
	run.Status = "cancelled"
	if state == "FINISHED" {
		if run.Errors == nil {
			return s.finishPoolScrubUnknown(ctx, run, now, "scrub completed without a valid error count", events...)
		}
		run.Status = "passed"
		if *run.Errors != "0" {
			run.Status = "failed"
			run.Error = fmt.Sprintf("scrub completed with %s errors", *run.Errors)
		}
		run.ProgressPct = 100
		run.ProgressKnown = true
	}
	events = append(events, newPoolScrubEvent(*run, "scrub_"+run.Status))
	return s.savePoolScrubRun(ctx, run, events...)
}

func (s *Service) poolScrubObservationLost(ctx context.Context, run *zfsModels.PoolScrubRun, now time.Time, message string) error {
	run.Error = message
	if run.ObservationLostAt == nil {
		run.ObservationLostAt = &now
	}
	if now.Sub(*run.ObservationLostAt) >= poolScrubCatchupWindow {
		return s.finishPoolScrubUnknown(ctx, run, now, "scrub status was unavailable for 24 hours: "+message)
	}
	return s.savePoolScrubRun(ctx, run)
}

func (s *Service) finishPoolScrubUnknown(ctx context.Context, run *zfsModels.PoolScrubRun, now time.Time, message string, events ...zfsModels.PoolScrubEvent) error {
	run.Status = "unknown"
	run.Error = message
	run.CompletedAt = &now
	events = append(events, newPoolScrubEvent(*run, "scrub_unknown"))
	return s.savePoolScrubRun(ctx, run, events...)
}

func (s *Service) finishUnstartedPoolScrub(ctx context.Context, schedule *zfsModels.PoolScrubSchedule, now time.Time, status, message string) error {
	if schedule.ScheduledFor == nil {
		return fmt.Errorf("scrub_schedule_occurrence_missing")
	}
	run := &zfsModels.PoolScrubRun{
		RunKey: scheduledPoolScrubRunKey(schedule), PoolGUID: schedule.PoolGUID, PoolName: schedule.PoolName,
		Source: "scheduled", ScheduleID: &schedule.ID, ScheduledFor: schedule.ScheduledFor,
		RequestedAt: now, CompletedAt: &now, Status: status, Error: message, ProgressPct: -1,
	}
	var events []zfsModels.PoolScrubEvent
	if status != "skipped" {
		condition := "scrub_start_failed"
		if status == "missed" {
			condition = "scrub_missed"
		}
		events = append(events, newPoolScrubEvent(*run, condition))
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		values := map[string]any{"next_run_at": schedule.NextRunAt}
		if status != "skipped" {
			values["scheduled_for"] = schedule.ScheduledFor
		}
		if err := tx.Model(&zfsModels.PoolScrubSchedule{}).Where("id = ?", schedule.ID).Updates(values).Error; err != nil {
			return err
		}
		return savePoolScrubRun(tx, run, events...)
	})
}

func (s *Service) savePoolScrubRun(ctx context.Context, run *zfsModels.PoolScrubRun, events ...zfsModels.PoolScrubEvent) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return savePoolScrubRun(tx, run, events...)
	})
}

func savePoolScrubRun(tx *gorm.DB, run *zfsModels.PoolScrubRun, events ...zfsModels.PoolScrubEvent) error {
	if err := tx.Save(run).Error; err != nil {
		return err
	}
	if run.StartedAt != nil && run.CompletedAt != nil && (run.Status == "passed" || run.Status == "failed" || run.Status == "cancelled") {
		if err := db.RecordPoolScrubCompletion(tx, run.PoolGUID, *run.CompletedAt); err != nil {
			return err
		}
	}
	if run.ScheduleID != nil {
		values := map[string]any{
			"pool_name": run.PoolName, "last_status": run.Status, "last_error": run.Error,
			"queued_at": nil, "progress_pct": run.ProgressPct, "progress_known": run.ProgressKnown,
		}
		if run.Status == "starting" || run.StartedAt != nil {
			values["last_run_at"] = run.RequestedAt
		}
		query := tx.Model(&zfsModels.PoolScrubSchedule{}).Where("id = ?", *run.ScheduleID)
		if run.Status == "skipped" {
			// A due occurrence can be skipped while this schedule's older run
			// remains active. Do not overwrite that run's visible status.
			query = query.Where("last_status NOT IN ?", poolScrubActiveStatuses)
			values["scheduled_for"] = nil
		}
		if err := query.Updates(values).Error; err != nil {
			return err
		}
	}
	for i := range events {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&events[i]).Error; err != nil {
			return err
		}
	}
	if !poolScrubStatusActive(run.Status) {
		keep := tx.Model(&zfsModels.PoolScrubRun{}).Select("id").
			Where("pool_guid = ? AND status NOT IN ?", run.PoolGUID, poolScrubActiveStatuses).
			Order("requested_at DESC, id DESC").Limit(poolScrubHistoryLimit)
		return tx.Where("pool_guid = ? AND status NOT IN ? AND id NOT IN (?)", run.PoolGUID, poolScrubActiveStatuses, keep).
			Delete(&zfsModels.PoolScrubRun{}).Error
	}
	return nil
}
