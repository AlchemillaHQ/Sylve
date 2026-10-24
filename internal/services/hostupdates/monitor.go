// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func nextMonitoringRun(state models.HostUpdateState, after time.Time) (time.Time, error) {
	if state.CronExpr == "" {
		if !slices.Contains([]int{6, 12, 24}, state.IntervalHours) {
			return time.Time{}, fmt.Errorf("invalid_update_interval")
		}
		return after.Add(time.Duration(state.IntervalHours) * time.Hour).UTC(), nil
	}
	next, err := utils.NextCronTime(state.CronExpr, after)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid_update_cron")
	}
	return next, nil
}

func monitoringResultFresh(state models.HostUpdateState, checkedAt *time.Time, now time.Time) bool {
	if checkedAt == nil || checkedAt.After(now) {
		return false
	}
	next, err := nextMonitoringRun(state, *checkedAt)
	return err == nil && now.Before(next.Add(time.Hour))
}

func (s *Service) event(tx *gorm.DB, category, title, body, severity string, operationID uint, page string) error {
	hostname, _ := os.Hostname()
	metadata := map[string]string{
		"node":        hostname,
		"operationId": strconv.FormatUint(uint64(operationID), 10),
		"path":        "/" + hostname + "/system/updates/" + page,
	}
	return tx.Create(&models.HostUpdateEvent{
		ID:            uuid.NewString(),
		Kind:          "system.updates." + category + ".host",
		Title:         title,
		Body:          body,
		Severity:      severity,
		Metadata:      metadata,
		NextAttemptAt: time.Now().UTC(),
	}).Error
}

func (s *Service) storeAudit(inventory []pkgmanager.Package, report pkgmanager.Audit, operationID uint) error {
	now := time.Now().UTC()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state models.HostUpdateState
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		previous := map[[2]string]bool{}
		for _, pkg := range state.Audit.Packages {
			for _, advisory := range pkg.Advisories {
				previous[[2]string{pkg.Name, advisory.ID}] = true
			}
		}
		added := 0
		for _, pkg := range report.Packages {
			for _, advisory := range pkg.Advisories {
				key := [2]string{pkg.Name, advisory.ID}
				if !previous[key] {
					added++
				}
				delete(previous, key)
			}
		}
		resolved := len(previous)
		if err := tx.Model(&state).Select("Inventory", "InventoryAt", "Audit", "AuditAt", "AuditInventoryHash", "AuditError").Updates(&models.HostUpdateState{Inventory: inventory, InventoryAt: &now, Audit: report, AuditAt: &now, AuditInventoryHash: pkgmanager.AuditInventoryDigest(inventory)}).Error; err != nil {
			return err
		}
		if added > 0 || resolved > 0 {
			severity := "warning"
			if len(report.Packages) == 0 {
				severity = "info"
			}
			body := fmt.Sprintf("%d affected registered packages; %d newly observed advisories, %d no longer reported. This is not a complete base/kernel security assessment and does not audit an unregistered Sylve binary.", len(report.Packages), added, resolved)
			return s.event(tx, "vulnerabilities", "Host package vulnerabilities changed", body, severity, operationID, "audit")
		}
		return nil
	})
}

func (s *Service) storeAvailable(inventory []pkgmanager.Package, plan pkgmanager.Plan, operationID uint) error {
	now := time.Now().UTC()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state models.HostUpdateState
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		oldDigest := state.Available.Digest
		if err := tx.Model(&state).Select("Inventory", "InventoryAt", "Available", "CheckedAt", "CheckInventoryHash", "CheckError").Updates(&models.HostUpdateState{Inventory: inventory, InventoryAt: &now, Available: plan, CheckedAt: &now, CheckInventoryHash: pkgmanager.InventoryDigest(inventory)}).Error; err != nil {
			return err
		}
		if len(plan.Changes) > 0 && oldDigest != plan.Digest {
			return s.event(tx, "available", "Host package updates available", fmt.Sprintf("%d package actions are available. Review the complete transaction before installation; base/kernel updates can require a reboot.", len(plan.Changes)), "info", operationID, "packages")
		}
		return nil
	})
}

func (s *Service) failedCheck(action string, cause error) error {
	if errors.Is(cause, context.Canceled) {
		return nil
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state models.HostUpdateState
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		column := "check_error"
		if action == "audit" {
			column = "audit_error"
		}
		failure := pkgmanager.Redact(cause.Error())
		state.FailureCount++
		if state.FailureCount >= 3 && !state.HealthFailed {
			if err := s.event(tx, "health", "Host update monitoring unavailable", "Three or more consecutive checks failed. Last-successful findings are retained, not a clean result. "+failure, "warning", 0, "history"); err != nil {
				return err
			}
			state.HealthFailed = true
		}
		return tx.Model(&state).Updates(map[string]any{column: failure, "failure_count": state.FailureCount, "health_failed": state.HealthFailed}).Error
	})
}

func (s *Service) successfulCheck() error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state models.HostUpdateState
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		if state.HealthFailed {
			if err := s.event(tx, "health", "Host update monitoring recovered", "Scheduled checks are available again. Review the latest findings and available updates.", "info", 0, "history"); err != nil {
				return err
			}
		}
		return tx.Model(&state).Updates(map[string]any{"failure_count": 0, "health_failed": false}).Error
	})
}

func (s *Service) StartMonitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.context.Done():
			return
		case <-ticker.C:
		}
		if err := s.reconcileAudits(); err != nil {
			logger.L.Warn().Err(err).Msg("host_update_audit_reconciliation_failed")
		}
		if err := s.schedule(); err != nil && !errors.Is(err, context.Canceled) {
			logger.L.Warn().Err(err).Msg("host_update_schedule_failed")
		}
		if err := s.dispatchEvents(s.context); err != nil && !errors.Is(err, context.Canceled) {
			logger.L.Warn().Err(err).Msg("host_update_notification_failed")
		}
		s.prune()
	}
}

func (s *Service) schedule() error {
	state, err := s.state()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !state.Monitoring || (state.NextCheckAt != nil && state.NextCheckAt.After(now)) {
		return nil
	}
	hostname, _ := os.Hostname()
	next, err := nextMonitoringRun(state, now)
	if err != nil {
		return err
	}
	if state.CronExpr == "" {
		sum := sha256.Sum256([]byte(hostname))
		next = next.Add(time.Duration(sum[0]) * time.Second)
	}
	claim := s.DB.Model(&state).Where("monitoring = ? AND (next_check_at IS NULL OR next_check_at <= ?)", true, now).
		Where("interval_hours = ? AND COALESCE(cron_expr, '') = ?", state.IntervalHours, state.CronExpr).
		Updates(map[string]any{"next_check_at": next, "last_attempt_at": now})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil
	}
	_, err = s.Start(s.context, "check", Request{}, "host-update-monitor")
	if err != nil && code(err) == "host_update_in_progress" {
		if state.CronExpr == "" {
			return s.DB.Model(&state).
				Where("monitoring = ? AND next_check_at = ?", true, next).
				Where("interval_hours = ? AND COALESCE(cron_expr, '') = ?", state.IntervalHours, state.CronExpr).
				Update("next_check_at", now.Add(15*time.Minute)).Error
		}
		return nil
	}
	return err
}

func (s *Service) dispatchEvents(ctx context.Context) error {
	var events []models.HostUpdateEvent
	if err := s.DB.WithContext(ctx).Where("finished_at IS NULL AND next_attempt_at <= ?", time.Now().UTC()).Order("created_at ASC").Limit(20).Find(&events).Error; err != nil {
		return err
	}
	for _, event := range events {
		input := notifier.EventInput{Kind: event.Kind, Title: event.Title, Body: event.Body, Severity: event.Severity, Source: "system.updates", Fingerprint: event.ID, Metadata: event.Metadata}
		if !event.TargetsLoaded {
			targets, err := notifier.DeliveryTargets(ctx, input)
			if err != nil {
				return err
			}
			event.Targets, event.TargetsLoaded = targets, true
			if err = s.DB.Model(&event).Select("Targets", "TargetsLoaded").Updates(&event).Error; err != nil {
				return err
			}
		}
		completed := map[string]bool{}
		for _, target := range event.Delivered {
			completed[target] = true
		}
		for _, target := range event.Targets {
			if completed[target] {
				continue
			}
			_, err := notifier.EmitTarget(ctx, input, target)
			if err == nil || notifier.IsPermanentDeliveryError(err) {
				event.Delivered = append(event.Delivered, target)
				if err = s.DB.Model(&event).Select("Delivered").Updates(&event).Error; err != nil {
					return err
				}
			}
		}
		event.Attempts++
		event.NextAttemptAt = time.Now().UTC().Add(time.Duration(min(event.Attempts*event.Attempts, 360)) * time.Minute)
		if len(event.Targets) == len(event.Delivered) || event.Attempts >= 10 {
			now := time.Now().UTC()
			event.FinishedAt = &now
		}
		if err := s.DB.Model(&event).Select("Attempts", "NextAttemptAt", "FinishedAt").Updates(&event).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) prune() {
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	var operations []models.HostUpdateOperation
	if s.DB.Where("active IS NULL AND finished_at < ?", cutoff).Limit(20).Find(&operations).Error != nil {
		return
	}
	for _, op := range operations {
		if _, err := uuid.Parse(op.UUID); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.directory, op.UUID)); err != nil {
			continue
		}
		_ = s.DB.Delete(&op).Error
	}
	_ = s.DB.Where("expires_at < ? AND NOT EXISTS (SELECT 1 FROM host_update_operations WHERE host_update_operations.plan_id = host_update_plans.id)", cutoff).Delete(&models.HostUpdatePlan{}).Error
	_ = s.DB.Where("finished_at < ?", cutoff).Delete(&models.HostUpdateEvent{}).Error
}
