// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"gorm.io/gorm"
)

type auditRecordContextKey struct{}

// AsyncAuditRef is the exact, node-local audit identity carried by asynchronous
// queue messages. OperationID prevents a reused numeric job/target ID from
// finalizing an unrelated request.
type AsyncAuditRef struct {
	RecordID    uint   `json:"audit_record_id,omitempty"`
	OperationID string `json:"audit_operation_id,omitempty"`
}

func ContextWithAuditRecordID(ctx context.Context, recordID uint) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if recordID == 0 {
		return ctx
	}
	return context.WithValue(ctx, auditRecordContextKey{}, recordID)
}

func AuditRecordIDFromContext(ctx context.Context) uint {
	if ctx == nil {
		return 0
	}
	recordID, _ := ctx.Value(auditRecordContextKey{}).(uint)
	return recordID
}

// PrepareAsyncAuditRecord binds the request audit row to an exact asynchronous
// operation before its queue message can become visible to a worker. Calls
// without request-logger context remain valid for schedulers and direct tests.
func PrepareAsyncAuditRecord(
	telemetryDB *gorm.DB,
	ctx context.Context,
	jobType string,
	jobID uint,
	operationID string,
) (AsyncAuditRef, error) {
	operationID = strings.TrimSpace(operationID)
	ref := AsyncAuditRef{
		RecordID:    AuditRecordIDFromContext(ctx),
		OperationID: operationID,
	}
	if ref.RecordID == 0 {
		return ref, nil
	}
	if telemetryDB == nil {
		return AsyncAuditRef{}, fmt.Errorf("async_audit_database_unavailable")
	}
	jobType = strings.TrimSpace(jobType)
	if jobType == "" || jobID == 0 || operationID == "" {
		return AsyncAuditRef{}, fmt.Errorf("async_audit_identity_invalid")
	}

	result := telemetryDB.Model(&infoModels.AuditRecord{}).
		Where(
			"id = ? AND status IN ? AND (COALESCE(async_operation_id, '') = '' OR async_operation_id = ?)",
			ref.RecordID,
			[]string{"started", "pending"},
			operationID,
		).
		Updates(map[string]any{
			"status":             "pending",
			"async_job_id":       jobID,
			"async_job_type":     jobType,
			"async_operation_id": operationID,
		})
	if result.Error != nil {
		return AsyncAuditRef{}, fmt.Errorf("prepare_async_audit_record: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return ref, nil
	}

	var existing infoModels.AuditRecord
	if err := telemetryDB.First(&existing, ref.RecordID).Error; err != nil {
		return AsyncAuditRef{}, fmt.Errorf("load_async_audit_record: %w", err)
	}
	if strings.TrimSpace(existing.AsyncOperationID) == operationID &&
		existing.AsyncJobID != nil && *existing.AsyncJobID == jobID &&
		strings.TrimSpace(existing.AsyncJobType) == jobType &&
		existing.Status != "started" {
		// Exact replay is harmless even if a very fast worker already made the
		// row terminal.
		return ref, nil
	}
	return AsyncAuditRef{}, fmt.Errorf("async_audit_record_transition_conflict")
}

func FinalizeAsyncAuditOperation(
	telemetryDB *gorm.DB,
	ref AsyncAuditRef,
	status string,
	errMsg string,
	response interface{},
) error {
	if telemetryDB == nil {
		return nil
	}
	ref.OperationID = strings.TrimSpace(ref.OperationID)
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("async_audit_terminal_status_required")
	}
	if ref.OperationID == "" && ref.RecordID == 0 {
		return nil
	}

	query := telemetryDB.Where("status = ?", "pending")
	if ref.OperationID != "" {
		query = query.Where("async_operation_id = ?", ref.OperationID)
	} else {
		query = query.Where("id = ?", ref.RecordID)
	}
	var records []infoModels.AuditRecord
	if err := query.Order("id ASC").Find(&records).Error; err != nil {
		return fmt.Errorf("load_pending_async_audits: %w", err)
	}

	var result error
	for i := range records {
		if err := finalizeAsyncAuditRecordByID(telemetryDB, &records[i], status, errMsg, response); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func FinalizeAsyncAuditRecord(telemetryDB *gorm.DB, jobType string, jobID uint, status string, errMsg string, response interface{}) {
	finalizeAsyncAuditRecords(telemetryDB, jobType, jobID, status, errMsg, response, nil)
}

func FinalizeAsyncAuditRecordsBefore(
	telemetryDB *gorm.DB,
	jobType string,
	jobID uint,
	status string,
	errMsg string,
	response interface{},
	createdBefore time.Time,
) {
	createdBefore = createdBefore.UTC()
	finalizeAsyncAuditRecords(telemetryDB, jobType, jobID, status, errMsg, response, &createdBefore)
}

func finalizeAsyncAuditRecords(
	telemetryDB *gorm.DB,
	jobType string,
	jobID uint,
	status string,
	errMsg string,
	response interface{},
	createdBefore *time.Time,
) {
	if telemetryDB == nil {
		return
	}

	query := telemetryDB.Where("async_job_id = ? AND status = ?", jobID, "pending")
	if jobType != "" {
		query = query.Where("async_job_type = ?", jobType)
	}
	var records []infoModels.AuditRecord
	if err := query.Order("created_at DESC").Find(&records).Error; err != nil {
		return
	}
	if createdBefore != nil {
		filtered := records[:0]
		for i := range records {
			if records[i].CreatedAt.UTC().Before(createdBefore.UTC()) {
				filtered = append(filtered, records[i])
			}
		}
		records = filtered
	}

	for i := range records {
		_ = finalizeAsyncAuditRecordByID(telemetryDB, &records[i], status, errMsg, response)
	}
}

func finalizeAsyncAuditRecordByID(
	telemetryDB *gorm.DB,
	record *infoModels.AuditRecord,
	status string,
	errMsg string,
	response interface{},
) error {
	if telemetryDB == nil || record == nil || record.ID == 0 {
		return nil
	}

	actionJSON := record.Action
	if response != nil {
		var action map[string]any
		if err := json.Unmarshal([]byte(record.Action), &action); err == nil {
			action["response"] = response
			if updated, err := json.Marshal(action); err == nil {
				actionJSON = string(updated)
			}
		}
	}

	now := time.Now().UTC()
	duration := now.Sub(record.Started)
	if record.Started.IsZero() || duration < 0 {
		duration = 0
	}
	updates := map[string]any{
		"status":   status,
		"error":    errMsg,
		"ended":    now,
		"duration": duration,
	}
	if actionJSON != record.Action {
		updates["action"] = actionJSON
	}

	result := telemetryDB.Model(&infoModels.AuditRecord{}).
		Where("id = ? AND status = ?", record.ID, "pending").
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("finalize_async_audit_record_%d: %w", record.ID, result.Error)
	}
	return nil
}
