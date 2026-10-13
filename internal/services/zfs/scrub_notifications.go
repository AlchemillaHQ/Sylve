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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const poolScrubEventClaimLease = 10 * time.Minute

type poolScrubEventJob struct {
	EventID    uint   `json:"eventId"`
	ClaimToken string `json:"claimToken"`
}

func newPoolScrubEvent(run zfsModels.PoolScrubRun, condition string) zfsModels.PoolScrubEvent {
	detail, severity := "result unavailable", "warning"
	switch condition {
	case "scrub_started":
		detail, severity = "started", "info"
	case "scrub_passed":
		detail, severity = "completed without errors", "info"
	case "scrub_failed":
		detail, severity = "completed with errors", "critical"
	case "scrub_cancelled":
		detail = "was cancelled"
	case "scrub_missed":
		detail = "scheduled run was missed"
	case "scrub_start_failed":
		detail = "could not be started"
	}
	event := zfsModels.PoolScrubEvent{
		EventKey: run.RunKey + "|" + condition, RunKey: run.RunKey,
		PoolGUID: run.PoolGUID, PoolName: run.PoolName, Source: run.Source,
		Condition: condition, Severity: severity,
		Title: fmt.Sprintf("ZFS pool %s scrub %s", run.PoolName, detail), Body: run.Error,
	}
	if run.ScheduleID != nil {
		event.ScheduleID = *run.ScheduleID
	}
	if event.Body == "" {
		event.Body = fmt.Sprintf("The %s scrub of pool %s %s.", run.Source, run.PoolName, detail)
		if run.Errors != nil && run.RepairedBytes != nil && condition != "scrub_started" {
			event.Body += fmt.Sprintf(" Errors: %s; repaired bytes: %s.", *run.Errors, *run.RepairedBytes)
		}
	}
	return event
}

func (s *Service) dispatchPoolScrubEvents(ctx context.Context, now time.Time) error {
	var events []zfsModels.PoolScrubEvent
	stale := now.Add(-poolScrubEventClaimLease)
	if err := s.DB.WithContext(ctx).Where("dead_lettered_at IS NULL AND (claimed_at IS NULL OR claimed_at < ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", stale, now).
		Order("id ASC").Limit(10).Find(&events).Error; err != nil {
		return err
	}
	var dispatchErr error
	for _, event := range events {
		token := utils.GenerateRandomUUID()
		claim := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubEvent{}).
			Where("id = ? AND dead_lettered_at IS NULL AND (claimed_at IS NULL OR claimed_at < ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", event.ID, stale, now).
			Updates(map[string]any{"claim_token": token, "claimed_at": now})
		if claim.Error != nil {
			dispatchErr = errors.Join(dispatchErr, claim.Error)
			continue
		}
		if claim.RowsAffected == 0 {
			continue
		}
		job := poolScrubEventJob{EventID: event.ID, ClaimToken: token}
		var err error
		if s.scrubEventEnqueue != nil {
			err = s.scrubEventEnqueue(ctx, job)
		} else {
			err = db.EnqueueJSON(ctx, poolScrubEventJobName, job)
		}
		if err != nil {
			releaseErr := s.updatePoolScrubEvent(context.WithoutCancel(ctx), event.ID, token, map[string]any{"claim_token": "", "claimed_at": nil})
			dispatchErr = errors.Join(dispatchErr, err, releaseErr)
		}
	}
	return dispatchErr
}

func (s *Service) updatePoolScrubEvent(ctx context.Context, id uint, token string, values map[string]any) error {
	result := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubEvent{}).Where("id = ? AND claim_token = ?", id, token).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("scrub_event_claim_lost")
	}
	return nil
}

func (s *Service) handlePoolScrubEventJob(ctx context.Context, job poolScrubEventJob) error {
	if job.EventID == 0 || job.ClaimToken == "" {
		return nil
	}
	if !s.scrubJobsReady.Load() {
		return s.updatePoolScrubEvent(ctx, job.EventID, job.ClaimToken, map[string]any{"claim_token": "", "claimed_at": nil})
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	token := utils.GenerateRandomUUID()
	claim := s.DB.WithContext(ctx).Model(&zfsModels.PoolScrubEvent{}).
		Where("id = ? AND claim_token = ? AND claimed_at > ? AND dead_lettered_at IS NULL", job.EventID, job.ClaimToken, now.Add(-poolScrubEventClaimLease)).
		Updates(map[string]any{"claim_token": token, "claimed_at": now})
	if claim.Error != nil || claim.RowsAffected == 0 {
		return claim.Error
	}
	var event zfsModels.PoolScrubEvent
	if err := s.DB.WithContext(ctx).First(&event, job.EventID).Error; err != nil {
		return err
	}
	if err := s.deliverPoolScrubEvent(ctx, &event, token, now); err != nil {
		logger.L.Error().Err(err).Str("event_key", event.EventKey).Msg("zfs_scrub_event_delivery_failed")
	}
	return nil
}

func decodePoolScrubTargets(value string) ([]string, error) {
	if value == "" {
		return []string{}, nil
	}
	var targets []string
	if err := json.Unmarshal([]byte(value), &targets); err != nil {
		return nil, err
	}
	for _, target := range targets {
		if target == "" {
			return nil, fmt.Errorf("invalid_scrub_notification_target")
		}
	}
	return targets, nil
}

func encodePoolScrubTargets(targets []string) string {
	if targets == nil {
		targets = []string{}
	}
	encoded, _ := json.Marshal(targets)
	return string(encoded)
}

func (s *Service) deliverPoolScrubEvent(ctx context.Context, event *zfsModels.PoolScrubEvent, token string, now time.Time) error {
	// Ordering includes manual runs and later occurrences for the same GUID.
	var earlier []zfsModels.PoolScrubEvent
	if err := s.DB.WithContext(ctx).Select("id", "next_attempt_at").
		Where("pool_guid = ? AND id < ? AND dead_lettered_at IS NULL", event.PoolGUID, event.ID).
		Order("id ASC").Limit(1).Find(&earlier).Error; err != nil {
		return err
	}
	if len(earlier) > 0 {
		retryAt := now.Add(5 * time.Second)
		if earlier[0].NextAttemptAt != nil && earlier[0].NextAttemptAt.After(retryAt) {
			retryAt = *earlier[0].NextAttemptAt
		}
		return s.updatePoolScrubEvent(ctx, event.ID, token, map[string]any{"claim_token": "", "claimed_at": nil, "next_attempt_at": retryAt})
	}
	input := notifier.EventInput{
		Kind: notifier.KindForZFSScrub(event.PoolGUID), Title: event.Title, Body: event.Body,
		Severity: event.Severity, Source: "system.zfs.scrub", Fingerprint: event.PoolGUID + "|scrub",
		Metadata: map[string]string{
			"condition": event.Condition, "pool": event.PoolName, "pool_guid": event.PoolGUID,
			"run_key": event.RunKey, "source": event.Source,
			"schedule_id": strconv.FormatUint(uint64(event.ScheduleID), 10),
		},
	}
	plan, err := decodePoolScrubTargets(event.DeliveryPlan)
	if err == nil && event.DeliveryPlan == "" {
		plan, err = notifier.DeliveryTargets(ctx, input)
		if err == nil {
			event.DeliveryPlan = encodePoolScrubTargets(plan)
			err = s.updatePoolScrubEvent(ctx, event.ID, token, map[string]any{"delivery_plan": event.DeliveryPlan})
		}
	}
	delivered, decodeErr := decodePoolScrubTargets(event.DeliveredTargets)
	err = errors.Join(err, decodeErr)
	failed, decodeErr := decodePoolScrubTargets(event.FailedTargets)
	err = errors.Join(err, decodeErr)
	var deliveryErr error
	for _, target := range plan {
		if err != nil {
			break
		}
		if slices.Contains(delivered, target) || slices.Contains(failed, target) {
			continue
		}
		if err = s.updatePoolScrubEvent(ctx, event.ID, token, map[string]any{"claimed_at": time.Now().UTC()}); err != nil {
			break
		}
		_, emitErr := notifier.EmitTarget(ctx, input, target)
		if emitErr != nil && !notifier.IsPermanentDeliveryError(emitErr) {
			deliveryErr = errors.Join(deliveryErr, emitErr)
			continue
		}
		if emitErr != nil {
			failed = append(failed, target)
			event.FailedTargets = encodePoolScrubTargets(failed)
			err = s.updatePoolScrubEvent(ctx, event.ID, token, map[string]any{"failed_targets": event.FailedTargets})
			logger.L.Error().Err(emitErr).Str("event_key", event.EventKey).Str("target", target).Msg("zfs_scrub_event_permanently_failed")
		} else {
			delivered = append(delivered, target)
			event.DeliveredTargets = encodePoolScrubTargets(delivered)
			err = s.updatePoolScrubEvent(ctx, event.ID, token, map[string]any{"delivered_targets": event.DeliveredTargets})
		}
	}
	err = errors.Join(err, deliveryErr)
	complete := err == nil
	if complete && len(failed) == 0 {
		return s.DB.WithContext(ctx).Where("id = ? AND claim_token = ?", event.ID, token).Delete(&zfsModels.PoolScrubEvent{}).Error
	}
	event.AttemptCount++
	dead := complete || event.AttemptCount >= 48 || now.Sub(event.CreatedAt) >= 7*24*time.Hour
	var deadAt *time.Time
	if dead {
		deadAt = &now
	}
	delay := min(time.Hour, 30*time.Second*time.Duration(uint64(1)<<min(event.AttemptCount-1, 7)))
	delay = min(time.Hour, delay+time.Duration(event.ID%30)*time.Second)
	message := "notification delivery incomplete"
	if err != nil {
		message = err.Error()
	} else if len(failed) > 0 {
		message = "notification delivery permanently failed for one or more targets"
	}
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer releaseCancel()
	releaseErr := s.updatePoolScrubEvent(releaseCtx, event.ID, token, map[string]any{
		"claim_token": "", "claimed_at": nil, "attempt_count": event.AttemptCount,
		"next_attempt_at": now.Add(delay), "delivery_error": message, "dead_lettered_at": deadAt,
	})
	return errors.Join(err, releaseErr)
}
