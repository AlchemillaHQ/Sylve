// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	restoreAuditTypeJob    = "backup_restore"
	restoreAuditTypeTarget = "backup_target_restore"
)

type restoreExecutionContextKey struct{}

type restoreExecution struct {
	EventID     uint
	OperationID string
	Audit       db.AsyncAuditRef
}

type restoreEventSpec struct {
	JobID          *uint
	SourceDataset  string
	TargetEndpoint string
}

func withRestoreExecution(ctx context.Context, execution restoreExecution) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, restoreExecutionContextKey{}, execution)
}

func restoreExecutionFromContext(ctx context.Context) (restoreExecution, bool) {
	if ctx == nil {
		return restoreExecution{}, false
	}
	execution, ok := ctx.Value(restoreExecutionContextKey{}).(restoreExecution)
	return execution, ok && execution.EventID > 0
}

func backupEventJobIDsEqual(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func restoreEventTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "failed", "interrupted":
		return true
	default:
		return false
	}
}

func (s *Service) ensureQueuedRestoreEvent(
	operationID string,
	audit db.AsyncAuditRef,
	spec restoreEventSpec,
) (*clusterModels.BackupEvent, bool, error) {
	if s == nil || s.DB == nil {
		return nil, false, fmt.Errorf("restore_event_database_unavailable")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, false, fmt.Errorf("restore_event_operation_id_required")
	}

	loadExisting := func() (*clusterModels.BackupEvent, error) {
		var existing clusterModels.BackupEvent
		result := s.DB.Where("operation_id = ?", operationID).Limit(1).Find(&existing)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, gorm.ErrRecordNotFound
		}
		if existing.OperationID == nil || strings.TrimSpace(*existing.OperationID) != operationID ||
			!backupEventJobIDsEqual(existing.JobID, spec.JobID) ||
			strings.TrimSpace(existing.Mode) != "restore" ||
			strings.TrimSpace(existing.SourceDataset) != strings.TrimSpace(spec.SourceDataset) ||
			strings.TrimSpace(existing.TargetEndpoint) != strings.TrimSpace(spec.TargetEndpoint) {
			return nil, fmt.Errorf("restore_event_operation_mismatch")
		}
		if existing.AuditRecordID == nil && audit.RecordID > 0 {
			if err := s.DB.Model(&clusterModels.BackupEvent{}).
				Where("id = ? AND audit_record_id IS NULL", existing.ID).
				Update("audit_record_id", audit.RecordID).Error; err != nil {
				return nil, err
			}
			existing.AuditRecordID = &audit.RecordID
		}
		return &existing, nil
	}

	if existing, err := loadExisting(); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	opID := operationID
	event := clusterModels.BackupEvent{
		OperationID:    &opID,
		JobID:          spec.JobID,
		Mode:           "restore",
		Status:         "queued",
		SourceDataset:  strings.TrimSpace(spec.SourceDataset),
		TargetEndpoint: strings.TrimSpace(spec.TargetEndpoint),
		StartedAt:      time.Now().UTC(),
	}
	if audit.RecordID > 0 {
		auditRecordID := audit.RecordID
		event.AuditRecordID = &auditRecordID
	}
	if err := s.DB.Create(&event).Error; err != nil {
		// A concurrent exact retry may have won the unique operation index.
		existing, loadErr := loadExisting()
		if loadErr == nil {
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("create_restore_event_failed: %w", err)
	}
	return &event, true, nil
}

func (s *Service) prepareRestoreObservability(
	ctx context.Context,
	auditType string,
	auditSubjectID uint,
	operationID string,
	spec restoreEventSpec,
) (restoreExecution, *clusterModels.BackupEvent, error) {
	operationID = strings.TrimSpace(operationID)
	audit, err := db.PrepareAsyncAuditRecord(
		s.TelemetryDB,
		ctx,
		auditType,
		auditSubjectID,
		operationID,
	)
	if err != nil {
		return restoreExecution{}, nil, err
	}

	event, _, err := s.ensureQueuedRestoreEvent(operationID, audit, spec)
	if err != nil {
		_ = db.FinalizeAsyncAuditOperation(s.TelemetryDB, audit, "failed", err.Error(), map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return restoreExecution{}, nil, err
	}
	execution := restoreExecution{
		EventID:     event.ID,
		OperationID: operationID,
		Audit:       audit,
	}
	if restoreEventTerminal(event.Status) {
		s.finalizeRestoreAuditForEvent(event)
	}
	return execution, event, nil
}

func (s *Service) restoreEventForExecution(
	execution restoreExecution,
	spec restoreEventSpec,
) (*clusterModels.BackupEvent, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("restore_event_database_unavailable")
	}
	operationID := strings.TrimSpace(execution.OperationID)
	if operationID == "" {
		return nil, fmt.Errorf("restore_event_operation_id_required")
	}

	// The queue carries EventID for recovery convenience, but the exact durable
	// operation token is authoritative. Never let a corrupted local message
	// redirect finalization to another operation's event.
	var canonical clusterModels.BackupEvent
	result := s.DB.Where("operation_id = ?", operationID).Limit(1).Find(&canonical)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected > 0 {
		return &canonical, nil
	}
	event, _, err := s.ensureQueuedRestoreEvent(operationID, execution.Audit, spec)
	return event, err
}

func (s *Service) restoreExecutionForOperation(operationID string) (restoreExecution, error) {
	execution := restoreExecution{OperationID: strings.TrimSpace(operationID)}
	if s == nil || s.DB == nil || execution.OperationID == "" {
		return execution, nil
	}
	var event clusterModels.BackupEvent
	result := s.DB.Where("operation_id = ?", execution.OperationID).Limit(1).Find(&event)
	if result.Error != nil {
		return execution, result.Error
	}
	if result.RowsAffected == 0 {
		return execution, nil
	}
	execution.EventID = event.ID
	execution.Audit.OperationID = execution.OperationID
	if event.AuditRecordID != nil {
		execution.Audit.RecordID = *event.AuditRecordID
	}
	return execution, nil
}

func (s *Service) beginRestoreEvent(eventID uint, operationID string) (bool, error) {
	if s == nil || s.DB == nil || eventID == 0 {
		return false, fmt.Errorf("restore_event_required")
	}
	operationID = strings.TrimSpace(operationID)
	result := s.DB.Model(&clusterModels.BackupEvent{}).
		Where("id = ? AND operation_id = ? AND status = ?", eventID, operationID, "queued").
		Updates(map[string]any{
			"status":     "running",
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}

	var event clusterModels.BackupEvent
	if err := s.DB.First(&event, eventID).Error; err != nil {
		return false, err
	}
	if event.OperationID == nil || strings.TrimSpace(*event.OperationID) != operationID {
		return false, fmt.Errorf("restore_event_operation_mismatch")
	}
	if event.Status == "running" {
		return true, nil
	}
	if restoreEventTerminal(event.Status) {
		return false, nil
	}
	return false, fmt.Errorf("invalid_restore_event_state: %s", event.Status)
}

func restoreAuditOutcome(event *clusterModels.BackupEvent) (string, string) {
	if event == nil {
		return "failed", "restore_event_unavailable"
	}
	if event.Status == "success" {
		return "success", ""
	}
	errMsg := strings.TrimSpace(event.Error)
	if errMsg == "" {
		errMsg = "restore_failed"
	}
	return "failed", errMsg
}

func (s *Service) finalizeRestoreAuditForEvent(event *clusterModels.BackupEvent) {
	if s == nil || event == nil || event.OperationID == nil {
		return
	}
	ref := db.AsyncAuditRef{OperationID: strings.TrimSpace(*event.OperationID)}
	if event.AuditRecordID != nil {
		ref.RecordID = *event.AuditRecordID
	}
	auditStatus, errMsg := restoreAuditOutcome(event)
	if err := db.FinalizeAsyncAuditOperation(s.TelemetryDB, ref, auditStatus, errMsg, map[string]any{
		"eventId": event.ID,
		"status":  event.Status,
		"error":   errMsg,
	}); err != nil {
		logger.L.Warn().Err(err).Uint("event_id", event.ID).Msg("failed_to_finalize_restore_audit")
	}
}

func (s *Service) finalizeRestoreEventByID(eventID uint, runErr error, output string) error {
	if s == nil || s.DB == nil || eventID == 0 {
		return nil
	}

	status := "success"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status":       status,
		"error":        errMsg,
		"completed_at": now,
		"updated_at":   now,
	}
	if strings.TrimSpace(output) != "" {
		updates["output"] = output
	}
	result := s.DB.Model(&clusterModels.BackupEvent{}).
		Where("id = ? AND status IN ?", eventID, []string{"queued", "running"}).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("finalize_restore_event_%d: %w", eventID, result.Error)
	}

	var event clusterModels.BackupEvent
	if err := s.DB.First(&event, eventID).Error; err != nil {
		return err
	}
	if !restoreEventTerminal(event.Status) {
		return fmt.Errorf("restore_event_terminal_transition_conflict")
	}
	s.finalizeRestoreAuditForEvent(&event)
	s.emitLeftPanelRefresh(fmt.Sprintf("restore_event_finalized_%d", event.ID))
	return nil
}

func (s *Service) interruptRestoreEvent(eventID uint, reason string) error {
	if s == nil || s.DB == nil || eventID == 0 {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "process_crashed_or_restarted"
	}
	now := time.Now().UTC()
	result := s.DB.Model(&clusterModels.BackupEvent{}).
		Where("id = ? AND status IN ?", eventID, []string{"queued", "running"}).
		Updates(map[string]any{
			"status":       "interrupted",
			"error":        reason,
			"completed_at": now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	var event clusterModels.BackupEvent
	if err := s.DB.First(&event, eventID).Error; err != nil {
		return err
	}
	if restoreEventTerminal(event.Status) {
		s.finalizeRestoreAuditForEvent(&event)
	}
	return nil
}

func (s *Service) finalizeRestoreEvent(event *clusterModels.BackupEvent, runErr error, output string) {
	if event == nil || event.ID == 0 {
		return
	}
	// Queue-managed operations have one outer finalizer that runs after every
	// restore-specific defer. Deep helpers must not publish an early terminal
	// result that could omit a later rollback/restart failure.
	if event.OperationID != nil && strings.TrimSpace(*event.OperationID) != "" {
		if strings.TrimSpace(output) != "" && s != nil && s.DB != nil {
			if updateErr := s.DB.Model(&clusterModels.BackupEvent{}).
				Where("id = ? AND status IN ?", event.ID, []string{"queued", "running"}).
				Update("output", output).Error; updateErr != nil {
				logger.L.Warn().Err(updateErr).Uint("event_id", event.ID).Msg("failed_to_stage_restore_event_output")
			}
		}
		return
	}
	if err := s.finalizeRestoreEventByID(event.ID, runErr, output); err != nil {
		logger.L.Warn().Err(err).Uint("event_id", event.ID).Msg("failed_to_finalize_restore_event")
		return
	}
	if current, err := s.GetLocalBackupEvent(event.ID); err == nil && current != nil {
		*event = *current
	}
}

func (s *Service) restoreOperationState(operationID string) (active bool, completed bool, err error) {
	if s == nil || s.DB == nil {
		return false, false, nil
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return false, false, nil
	}

	if s.DB.Migrator().HasTable(&clusterModels.BackupJobOperation{}) {
		var count int64
		if err := s.DB.Model(&clusterModels.BackupJobOperation{}).
			Where("token = ? AND operation = ?", operationID, clusterModels.BackupJobOperationRestore).
			Count(&count).Error; err != nil {
			return false, false, err
		}
		if count > 0 {
			return true, false, nil
		}
	}
	if s.DB.Migrator().HasTable(&clusterModels.BackupTargetRestoreOperation{}) {
		var operation clusterModels.BackupTargetRestoreOperation
		result := s.DB.Where("token = ?", operationID).Limit(1).Find(&operation)
		if result.Error != nil {
			return false, false, result.Error
		}
		if result.RowsAffected > 0 {
			if operation.State == clusterModels.BackupTargetRestoreOperationCompleted {
				return false, true, nil
			}
			return true, false, nil
		}
	}
	return false, false, nil
}

func (s *Service) reconcileRestoreEventWithoutExecution(operationID string) error {
	operationID = strings.TrimSpace(operationID)
	if s == nil || s.DB == nil || operationID == "" {
		return nil
	}
	var event clusterModels.BackupEvent
	result := s.DB.Where("operation_id = ?", operationID).Limit(1).Find(&event)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	if restoreEventTerminal(event.Status) {
		s.finalizeRestoreAuditForEvent(&event)
		return nil
	}
	active, completed, err := s.restoreOperationState(operationID)
	if err != nil || active {
		return err
	}
	reason := "restore_operation_disappeared_before_observability_finalization"
	if completed {
		reason = "restore_completed_without_a_durable_observability_outcome"
	}
	return s.interruptRestoreEvent(event.ID, reason)
}

func auditActionIsRestore(actionJSON string) bool {
	var action struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(actionJSON), &action) != nil {
		return false
	}
	path := strings.TrimSpace(action.Path)
	if !strings.HasPrefix(path, "/api/cluster/backups/") {
		return false
	}
	return strings.HasSuffix(path, "/restore")
}

// ReconcileRestoreObservabilityAfterRestart closes every crash window between
// request auditing, durable reservation, event creation, queue execution, and
// terminal audit publication. Active durable operations remain pending and are
// left for their outbox reconcilers.
func (s *Service) ReconcileRestoreObservabilityAfterRestart() error {
	if s == nil || s.DB == nil {
		return nil
	}

	var result error
	var events []clusterModels.BackupEvent
	if err := s.DB.Where("mode = ? AND operation_id IS NOT NULL", "restore").
		Order("id ASC").Find(&events).Error; err != nil {
		return err
	}
	for i := range events {
		event := &events[i]
		if restoreEventTerminal(event.Status) {
			s.finalizeRestoreAuditForEvent(event)
			continue
		}
		operationID := ""
		if event.OperationID != nil {
			operationID = strings.TrimSpace(*event.OperationID)
		}
		active, completed, err := s.restoreOperationState(operationID)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if active {
			continue
		}
		reason := "process_crashed_or_restarted"
		if completed {
			reason = "restore_completed_without_a_durable_observability_outcome"
		}
		if err := s.interruptRestoreEvent(event.ID, reason); err != nil {
			result = errors.Join(result, err)
		}
	}

	if s.TelemetryDB == nil {
		return result
	}
	var pending []infoModels.AuditRecord
	if err := s.TelemetryDB.
		Where("status = ? AND async_job_type IN ?", "pending", []string{restoreAuditTypeJob, restoreAuditTypeTarget}).
		Order("id ASC").Find(&pending).Error; err != nil {
		return errors.Join(result, err)
	}
	for i := range pending {
		audit := &pending[i]
		operationID := strings.TrimSpace(audit.AsyncOperationID)
		ref := db.AsyncAuditRef{RecordID: audit.ID, OperationID: operationID}
		if operationID == "" {
			if !audit.CreatedAt.Before(s.startedAt) {
				continue
			}
			errMsg := "legacy_restore_audit_correlation_unavailable_after_restart"
			result = errors.Join(result, db.FinalizeAsyncAuditOperation(
				s.TelemetryDB, ref, "failed", errMsg, map[string]any{"status": "failed", "error": errMsg},
			))
			continue
		}

		var event clusterModels.BackupEvent
		eventResult := s.DB.Where("operation_id = ?", operationID).Limit(1).Find(&event)
		if eventResult.Error != nil {
			result = errors.Join(result, eventResult.Error)
			continue
		}
		if eventResult.RowsAffected > 0 {
			if restoreEventTerminal(event.Status) {
				s.finalizeRestoreAuditForEvent(&event)
			}
			continue
		}
		active, _, err := s.restoreOperationState(operationID)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if active {
			continue
		}
		errMsg := "restore_audit_operation_missing_after_restart"
		result = errors.Join(result, db.FinalizeAsyncAuditOperation(
			s.TelemetryDB, ref, "failed", errMsg, map[string]any{"status": "failed", "error": errMsg},
		))
	}

	var started []infoModels.AuditRecord
	if err := s.TelemetryDB.Where("status = ?", "started").
		Order("id ASC").Find(&started).Error; err != nil {
		return errors.Join(result, err)
	}
	now := time.Now().UTC()
	for i := range started {
		if !started[i].CreatedAt.Before(s.startedAt) || !auditActionIsRestore(started[i].Action) {
			continue
		}
		update := s.TelemetryDB.Model(&infoModels.AuditRecord{}).
			Where("id = ? AND status = ?", started[i].ID, "started").
			Updates(map[string]any{
				"status":   "failed",
				"error":    "restore_request_interrupted_before_queue_publication",
				"ended":    now,
				"duration": now.Sub(started[i].Started),
			})
		if update.Error != nil {
			result = errors.Join(result, update.Error)
		}
	}
	return result
}
