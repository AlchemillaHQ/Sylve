// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailTypes "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *Service) GetPackageMonitoring(ctx context.Context, ctID uint) (jailTypes.PackageMonitoring, error) {
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailTypes.PackageMonitoring{}, err
	}
	state, err := s.packageState(jail, guid)
	return monitorView(jail, state), err
}

func (s *Service) SetPackageMonitoring(ctx context.Context, ctID uint, monitor jailTypes.PackageMonitoring) (jailTypes.PackageMonitoring, error) {
	if monitor.IntervalHours != 6 && monitor.IntervalHours != 12 && monitor.IntervalHours != 24 {
		return jailTypes.PackageMonitoring{}, fmt.Errorf("invalid_package_audit_interval")
	}
	jail, guid, err := s.packageJail(ctx, ctID)
	if err != nil {
		return jailTypes.PackageMonitoring{}, err
	}
	state, err := s.packageState(jail, guid)
	if err != nil {
		return jailTypes.PackageMonitoring{}, err
	}
	if err = s.savePackageInventory(state); err != nil {
		return jailTypes.PackageMonitoring{}, err
	}
	if monitor.Enabled && (!jail.PackageMonitoringEnabled || monitor.IntervalHours != jail.PackageAuditIntervalHours) {
		now := time.Now().UTC()
		state.NextAuditAt = &now
	}
	if !monitor.Enabled {
		state.NextAuditAt = nil
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&jailModels.Jail{}).Where("id = ? AND type = ?", jail.ID, jailModels.JailTypeFreeBSD).Updates(map[string]any{"package_monitoring_enabled": monitor.Enabled, "package_audit_interval_hours": monitor.IntervalHours}).Error; err != nil {
			return err
		}
		return tx.Model(&state).Select("NextAuditAt").Updates(&state).Error
	})
	if err != nil {
		return jailTypes.PackageMonitoring{}, err
	}
	jail.PackageMonitoringEnabled, jail.PackageAuditIntervalHours = monitor.Enabled, monitor.IntervalHours
	if s.GZFS != nil {
		if writeErr := s.WriteJailJSON(ctID); writeErr != nil {
			return monitorView(jail, state), fmt.Errorf("package_monitor_metadata_write_failed: %w", writeErr)
		}
	}
	return monitorView(jail, state), nil
}

func (s *Service) persistPackageAudit(jail *jailModels.Jail, guid string, inventory []pkgmanager.Package, report pkgmanager.Audit, op jailModels.JailPackageOperation) error {
	now := time.Now().UTC()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state jailModels.JailPackageState
		if err := tx.First(&state, "jail_id = ? AND guid = ?", jail.ID, guid).Error; err != nil {
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
		state.Inventory, state.InventoryAt, state.Audit, state.AuditAt = inventory, &now, report, &now
		state.AuditInventoryHash = pkgmanager.AuditInventoryDigest(inventory)
		state.AuditError = ""
		state.FailureCount = 0
		state.SkipReason = ""
		if state.HealthFailed {
			if err := s.newPackageEvent(tx, jail, guid, notifier.JailPackageHealthKindPrefix, "Package audit recovered", "The package vulnerability audit completed successfully.", "info", op); err != nil {
				return err
			}
			state.HealthFailed = false
		}
		if err := tx.Model(&state).Select("Inventory", "InventoryAt", "Audit", "AuditAt", "AuditInventoryHash", "AuditError", "FailureCount", "SkipReason", "HealthFailed").Updates(&state).Error; err != nil {
			return err
		}
		if op.ID != "" {
			record := jailModels.JailPackageOperation{ID: op.ID, Audit: &report}
			if err := tx.Model(&record).Select("Audit").Updates(&record).Error; err != nil {
				return err
			}
		}
		if added > 0 || resolved > 0 {
			severity, title := "warning", "Package vulnerabilities changed"
			if len(report.Packages) == 0 {
				severity, title = "info", "Package vulnerabilities no longer reported"
			}
			body := fmt.Sprintf("%s (%d): %s. %d newly observed, %d no longer reported. Known registered-package findings only; not a full base/kernel assessment.", jail.Name, jail.CTID, auditSummary(report), added, resolved)
			for i, pkg := range report.Packages {
				if i >= 8 {
					body += "\nAdditional findings are available in View Audit."
					break
				}
				ids := []string{}
				for j, advisory := range pkg.Advisories {
					if j >= 4 {
						ids = append(ids, "…")
						break
					}
					ids = append(ids, advisory.ID)
				}
				body += "\n" + pkg.Name + " " + pkg.Version + ": " + strings.Join(ids, ", ")
			}
			return s.newPackageEvent(tx, jail, guid, notifier.JailPackageVulnerabilitiesKindPrefix, title, body, severity, op)
		}
		return nil
	})
}

func (s *Service) recordPackageAuditFailure(jail *jailModels.Jail, guid string, cause error) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var state jailModels.JailPackageState
		if err := tx.First(&state, "jail_id = ? AND guid = ?", jail.ID, guid).Error; err != nil {
			return err
		}
		state.AuditError = pkgmanager.Redact(cause.Error())
		state.FailureCount++
		if state.FailureCount >= 3 && !state.HealthFailed {
			state.HealthFailed = true
			if err := s.newPackageEvent(tx, jail, guid, notifier.JailPackageHealthKindPrefix, "Package audit unavailable", fmt.Sprintf("%s (%d): %d consecutive audit failures. Last successful findings are retained; this is not a clean result. %s", jail.Name, jail.CTID, state.FailureCount, state.AuditError), "warning", jailModels.JailPackageOperation{}); err != nil {
				return err
			}
		}
		return tx.Model(&state).Select("AuditError", "FailureCount", "HealthFailed").Updates(&state).Error
	})
}

func (s *Service) newPackageEvent(tx *gorm.DB, jail *jailModels.Jail, guid, prefix, title, body, severity string, op jailModels.JailPackageOperation) error {
	hostname, _ := os.Hostname()
	metadata := map[string]string{"ctId": strconv.FormatUint(uint64(jail.CTID), 10), "jailId": strconv.FormatUint(uint64(jail.ID), 10), "guid": guid, "node": hostname, "operationId": op.ID, "auditedAt": time.Now().UTC().Format(time.RFC3339)}
	if op.TaskID != nil {
		metadata["taskId"] = strconv.FormatUint(uint64(*op.TaskID), 10)
	}
	event := jailModels.JailPackageNotificationEvent{ID: uuid.NewString(), JailID: jail.ID, GUID: guid, Kind: prefix + notifier.JailPackageTarget(jail.ID, guid), Title: title, Body: body, Severity: severity, Metadata: metadata, NextAttemptAt: time.Now().UTC()}
	return tx.Create(&event).Error
}

func (s *Service) dispatchPackageEvents(ctx context.Context) error {
	var events []jailModels.JailPackageNotificationEvent
	if err := s.DB.WithContext(ctx).Where("finished_at IS NULL AND next_attempt_at <= ?", time.Now().UTC()).Order("created_at ASC").Limit(20).Find(&events).Error; err != nil {
		return err
	}
	var dispatchErr error
	for _, event := range events {
		if err := s.dispatchPackageEvent(ctx, &event); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			dispatchErr = errors.Join(dispatchErr, err)
		}
		event.Attempts++
		event.NextAttemptAt = time.Now().UTC().Add(time.Duration(min(event.Attempts*event.Attempts, 360)) * time.Minute)
		if event.FinishedAt == nil && ((event.TargetsLoaded && len(event.Delivered) == len(event.Targets)) || event.Attempts >= 10) {
			now := time.Now().UTC()
			event.FinishedAt = &now
		}
		if err := s.DB.WithContext(ctx).Model(&event).Select("Attempts", "NextAttemptAt", "FinishedAt").Updates(&event).Error; err != nil {
			dispatchErr = errors.Join(dispatchErr, err)
		}
	}
	return dispatchErr
}

func (s *Service) dispatchPackageEvent(ctx context.Context, event *jailModels.JailPackageNotificationEvent) error {
	ctID, _ := strconv.ParseUint(event.Metadata["ctId"], 10, 32)
	var jail jailModels.Jail
	err := s.DB.WithContext(ctx).Preload("Storages").First(&jail, "id = ? AND ct_id = ?", event.JailID, ctID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err != nil || jail.Type != jailModels.JailTypeFreeBSD || jail.CreationPending {
		now := time.Now().UTC()
		event.FinishedAt = &now
		return nil
	}
	_, root, err := jailRootDatasetIdentity(&jail)
	if err != nil {
		return err
	}
	if root.GUID == "" {
		return fmt.Errorf("jail_dataset_identity_unavailable")
	}
	if root.GUID != event.GUID {
		now := time.Now().UTC()
		event.FinishedAt = &now
		return nil
	}
	input := notifier.EventInput{Kind: event.Kind, Title: event.Title, Body: event.Body, Severity: event.Severity, Source: "jail.packages", Fingerprint: event.ID, Metadata: event.Metadata}
	if !event.TargetsLoaded {
		event.Targets, err = notifier.DeliveryTargets(ctx, input)
		if err != nil {
			return err
		}
		event.TargetsLoaded = true
		if err = s.DB.WithContext(ctx).Model(event).Select("Targets", "TargetsLoaded").Updates(event).Error; err != nil {
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
		_, err = notifier.EmitTarget(ctx, input, target)
		if err == nil || notifier.IsPermanentDeliveryError(err) {
			event.Delivered = append(event.Delivered, target)
			if err = s.DB.WithContext(ctx).Model(event).Select("Delivered").Updates(event).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) runPackageSchedules(ctx context.Context) error {
	var jails []jailModels.Jail
	now := time.Now().UTC()
	if err := s.DB.WithContext(ctx).Select("jails.*").Preload("Storages").
		Joins("JOIN jail_storages AS root ON root.jid = jails.id AND root.is_base = ?", true).
		Joins("LEFT JOIN jail_package_states AS state ON state.jail_id = jails.id AND state.guid = root.guid").
		Where("jails.type = ? AND jails.creation_pending = ? AND jails.package_monitoring_enabled = ?", jailModels.JailTypeFreeBSD, false, true).
		Where("root.guid <> '' AND trim(root.pool) <> '' AND instr(root.pool, '/') = 0").
		Where("(SELECT COUNT(*) FROM jail_storages WHERE jid = jails.id AND is_base = ?) = 1", true).
		Where("state.next_audit_at IS NULL OR state.next_audit_at <= ?", now).
		Order("state.next_audit_at ASC").Limit(2).Find(&jails).Error; err != nil {
		return err
	}
	for _, jail := range jails {
		_, storage, err := jailRootDatasetIdentity(&jail)
		if err != nil {
			continue
		}
		state, err := s.packageState(&jail, storage.GUID)
		if err != nil {
			return err
		}
		if state.CreatedAt.IsZero() {
			if err = s.savePackageInventory(state); err != nil {
				return err
			}
		}
		next := now.Add(time.Duration(monitorView(&jail, state).IntervalHours)*time.Hour + time.Duration(state.JailID%300)*time.Second)
		claim := s.DB.WithContext(ctx).Model(&state).Where("guid = ? AND (next_audit_at IS NULL OR next_audit_at <= ?)", storage.GUID, now).
			Where("EXISTS (SELECT 1 FROM jails WHERE id = ? AND package_monitoring_enabled = ? AND package_audit_interval_hours = ?)", jail.ID, true, jail.PackageAuditIntervalHours).
			Updates(map[string]any{"next_audit_at": next, "last_attempt_at": now})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			continue
		}
		err = s.queueScheduledPackageAudit(ctx, state.CTID)
		if err != nil {
			if !expectedPackageSkip(err) {
				_ = s.recordPackageAuditFailure(&jail, storage.GUID, err)
			}
			code := packageErrorCode(err)
			retry := now.Add(15*time.Minute + time.Duration(state.JailID%60)*time.Second)
			if err = s.DB.Model(&state).Updates(map[string]any{"skip_reason": code, "next_audit_at": retry}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) queueScheduledPackageAudit(ctx context.Context, ctID uint) error {
	var record infoModels.AuditRecord
	if s.packages.telemetry != nil {
		hostname, _ := os.Hostname()
		action, _ := json.Marshal(map[string]any{"method": "POST", "path": fmt.Sprintf("/api/jail/%d/packages/audit", ctID), "body": map[string]any{"scheduled": true}})
		record = infoModels.AuditRecord{User: "package-monitor", AuthType: "system", Node: hostname, Started: time.Now().UTC(), Action: string(action), Status: "started", Version: 2}
		if err := s.packages.telemetry.Create(&record).Error; err != nil {
			return err
		}
		ctx = db.ContextWithAuditRecordID(ctx, record.ID)
	}
	_, err := s.StartPackageOperation(ctx, ctID, "pkg-audit", jailTypes.PackageRequest{}, "scheduled", "package-monitor")
	if err != nil && record.ID != 0 {
		_ = s.packages.telemetry.Model(&record).Where("status IN ?", []string{"started", "pending"}).Updates(map[string]any{"status": "failed", "error": pkgmanager.Redact(err.Error()), "ended": time.Now().UTC()}).Error
	}
	return err
}

func expectedPackageSkip(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	switch packageErrorCode(err) {
	case "jail_not_running", "package_root_not_mounted", "jail_operation_in_progress", "package_operation_in_progress", "lifecycle_task_in_progress", "replication_lease_not_owned", "replication_operation_in_progress", "migration_in_progress":
		return true
	}
	return false
}

func (s *Service) StartPackageMonitor(ctx context.Context) {
	if s.packages == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runPackageSchedules(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Warn().Err(err).Msg("package_schedule_failed")
			}
			if err := s.dispatchPackageEvents(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Warn().Err(err).Msg("package_notification_delivery_failed")
			}
			s.prunePackageOperations()
		}
	}
}

func (s *Service) prunePackageOperations() {
	_ = s.DB.Where("NOT EXISTS (SELECT 1 FROM jails WHERE jails.id = jail_package_states.jail_id)").Delete(&jailModels.JailPackageState{}).Error
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	var operations []jailModels.JailPackageOperation
	if s.DB.Where("active_jail_id IS NULL AND finished_at < ?", cutoff).Limit(25).Find(&operations).Error != nil {
		return
	}
	for _, op := range operations {
		if _, err := uuid.Parse(op.ID); err != nil {
			continue
		}
		dir := filepath.Join(s.packages.dataPath, op.ID)
		if err := os.RemoveAll(dir); err != nil {
			continue
		}
		_ = s.DB.Delete(&op).Error
	}
	_ = s.DB.Where("expires_at < ?", cutoff).Delete(&jailModels.JailPackagePlan{}).Error
	_ = s.DB.Where("finished_at < ?", cutoff).Delete(&jailModels.JailPackageNotificationEvent{}).Error
}
