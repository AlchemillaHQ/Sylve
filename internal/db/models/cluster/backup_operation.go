// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupJobOperationBackup  = "backup"
	BackupJobOperationRestore = "restore"

	BackupJobOperationQueued    = "queued"
	BackupJobOperationRunning   = "running"
	BackupJobOperationFinishing = "finishing"
)

// BackupJobOperation is the replicated source of truth for mutually exclusive
// work associated with one backup job. The row is acquired before queueing, so
// deletion and execution are ordered by Raft instead of consulting node-local
// queue maps or event telemetry.
type BackupJobOperation struct {
	JobID          uint      `gorm:"primaryKey;autoIncrement:false" json:"jobId"`
	Token          string    `gorm:"uniqueIndex;not null" json:"token"`
	Operation      string    `gorm:"index;not null" json:"operation"`
	State          string    `gorm:"index;not null" json:"state"`
	HolderNodeID   string    `gorm:"index;not null" json:"holderNodeId"`
	RequestPayload string    `gorm:"type:text" json:"requestPayload"`
	Revision       uint64    `gorm:"not null" json:"revision"`
	AcquiredAt     time.Time `gorm:"index;not null" json:"acquiredAt"`
	UpdatedAt      time.Time `gorm:"index;not null" json:"updatedAt"`
}

type BackupJobOperationAcquire struct {
	JobID                uint      `json:"jobId"`
	Token                string    `json:"token"`
	Operation            string    `json:"operation"`
	HolderNodeID         string    `json:"holderNodeId"`
	RequestPayload       string    `json:"requestPayload"`
	AcquiredAt           time.Time `json:"acquiredAt"`
	RequireEnabledTarget bool      `json:"requireEnabledTarget,omitempty"`
}

type BackupJobOperationTransition struct {
	JobID                uint      `json:"jobId"`
	Token                string    `json:"token"`
	Operation            string    `json:"operation"`
	HolderNodeID         string    `json:"holderNodeId"`
	RequestPayload       string    `json:"requestPayload"`
	OccurredAt           time.Time `json:"occurredAt"`
	RequireEnabledTarget bool      `json:"requireEnabledTarget,omitempty"`
}

func normalizeBackupJobOperationIdentity(jobID uint, token, operation, holderNodeID string) (string, string, string, error) {
	token = strings.TrimSpace(token)
	operation = strings.ToLower(strings.TrimSpace(operation))
	holderNodeID = strings.TrimSpace(holderNodeID)
	if jobID == 0 {
		return "", "", "", fmt.Errorf("invalid_job_id")
	}
	if token == "" {
		return "", "", "", fmt.Errorf("backup_job_operation_token_required")
	}
	if holderNodeID == "" {
		return "", "", "", fmt.Errorf("backup_job_operation_holder_required")
	}
	if operation != BackupJobOperationBackup && operation != BackupJobOperationRestore {
		return "", "", "", fmt.Errorf("invalid_backup_job_operation")
	}
	return token, operation, holderNodeID, nil
}

// AcquireBackupJobOperationTxn atomically proves that the job still exists and
// creates its one allowed operation reservation. Replaying the exact token is
// idempotent; a different token always conflicts.
func AcquireBackupJobOperationTxn(db *gorm.DB, payload *BackupJobOperationAcquire) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_operation_input_invalid")
	}
	token, operation, holderNodeID, err := normalizeBackupJobOperationIdentity(
		payload.JobID, payload.Token, payload.Operation, payload.HolderNodeID,
	)
	if err != nil {
		return err
	}
	if payload.AcquiredAt.IsZero() {
		return fmt.Errorf("backup_job_operation_acquired_at_required")
	}
	payload.Token = token
	payload.Operation = operation
	payload.HolderNodeID = holderNodeID
	payload.RequestPayload = strings.TrimSpace(payload.RequestPayload)
	payload.AcquiredAt = payload.AcquiredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var job BackupJob
		jobResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "runner_node_id", "target_id").
			Where("id = ?", payload.JobID).
			Limit(1).
			Find(&job)
		if jobResult.Error != nil {
			return jobResult.Error
		}
		if jobResult.RowsAffected == 0 {
			return fmt.Errorf("backup_job_not_found")
		}
		if runner := strings.TrimSpace(job.RunnerNodeID); runner != "" && runner != holderNodeID {
			return fmt.Errorf("backup_job_operation_runner_mismatch")
		}
		var existing BackupJobOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("job_id = ?", payload.JobID).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 0 {
			if existing.Token == token &&
				existing.Operation == operation &&
				strings.TrimSpace(existing.HolderNodeID) == holderNodeID &&
				strings.TrimSpace(existing.RequestPayload) == payload.RequestPayload {
				return nil
			}
			return fmt.Errorf("backup_job_running")
		}

		if payload.RequireEnabledTarget && job.TargetID != 0 {
			var target BackupTarget
			targetResult := tx.Select("id", "enabled").Where("id = ?", job.TargetID).Limit(1).Find(&target)
			if targetResult.Error != nil {
				return targetResult.Error
			}
			if targetResult.RowsAffected == 0 {
				return fmt.Errorf("backup_target_not_found")
			}
			if !target.Enabled {
				return fmt.Errorf("backup_target_disabled")
			}
		}

		rebindPending, err := BackupJobRunnerRebindPendingForJob(tx, payload.JobID)
		if err != nil {
			return err
		}
		if rebindPending {
			return fmt.Errorf("backup_job_runner_rebind_pending")
		}
		repairRequired, err := BackupJobRepairRequired(tx, payload.JobID)
		if err != nil {
			return err
		}
		if repairRequired {
			return fmt.Errorf("backup_job_repair_required")
		}

		return tx.Create(&BackupJobOperation{
			JobID:          payload.JobID,
			Token:          token,
			Operation:      operation,
			State:          BackupJobOperationQueued,
			HolderNodeID:   holderNodeID,
			RequestPayload: payload.RequestPayload,
			Revision:       1,
			AcquiredAt:     payload.AcquiredAt,
			UpdatedAt:      payload.AcquiredAt,
		}).Error
	})
}

func transitionBackupJobOperation(db *gorm.DB, payload *BackupJobOperationTransition, targetState string) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_operation_input_invalid")
	}
	token, operation, holderNodeID, err := normalizeBackupJobOperationIdentity(
		payload.JobID, payload.Token, payload.Operation, payload.HolderNodeID,
	)
	if err != nil {
		return err
	}
	if payload.OccurredAt.IsZero() {
		return fmt.Errorf("backup_job_operation_occurred_at_required")
	}
	payload.Token = token
	payload.Operation = operation
	payload.HolderNodeID = holderNodeID
	payload.RequestPayload = strings.TrimSpace(payload.RequestPayload)
	payload.OccurredAt = payload.OccurredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var existing BackupJobOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("job_id = ?", payload.JobID).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("backup_job_operation_not_found")
		}
		if existing.Token != token || existing.Operation != operation ||
			strings.TrimSpace(existing.HolderNodeID) != holderNodeID ||
			strings.TrimSpace(existing.RequestPayload) != payload.RequestPayload {
			return fmt.Errorf("backup_job_operation_token_mismatch")
		}

		switch targetState {
		case BackupJobOperationRunning:
			switch existing.State {
			case BackupJobOperationQueued:
				if !payload.RequireEnabledTarget {
					break
				}
				var job BackupJob
				jobResult := tx.Select("id", "target_id").Where("id = ?", payload.JobID).Limit(1).Find(&job)
				if jobResult.Error != nil {
					return jobResult.Error
				}
				if jobResult.RowsAffected == 0 {
					return fmt.Errorf("backup_job_not_found")
				}
				if job.TargetID != 0 {
					var target BackupTarget
					targetResult := tx.Select("id", "enabled").Where("id = ?", job.TargetID).Limit(1).Find(&target)
					if targetResult.Error != nil {
						return targetResult.Error
					}
					if targetResult.RowsAffected == 0 {
						return fmt.Errorf("backup_target_not_found")
					}
					if !target.Enabled {
						return fmt.Errorf("backup_target_disabled")
					}
				}
			case BackupJobOperationRunning:
				return nil
			case BackupJobOperationFinishing:
				return fmt.Errorf("backup_job_operation_finishing")
			default:
				return fmt.Errorf("invalid_backup_job_operation_state")
			}
		case BackupJobOperationFinishing:
			switch existing.State {
			case BackupJobOperationQueued, BackupJobOperationRunning:
			case BackupJobOperationFinishing:
				return nil
			default:
				return fmt.Errorf("invalid_backup_job_operation_state")
			}
		default:
			return fmt.Errorf("invalid_backup_job_operation_state")
		}

		updated := tx.Model(&BackupJobOperation{}).
			Where("job_id = ? AND token = ? AND revision = ?", payload.JobID, token, existing.Revision).
			Updates(map[string]any{
				"state":      targetState,
				"revision":   existing.Revision + 1,
				"updated_at": payload.OccurredAt,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_job_operation_revision_conflict")
		}
		return nil
	})
}

func StartBackupJobOperationTxn(db *gorm.DB, payload *BackupJobOperationTransition) error {
	return transitionBackupJobOperation(db, payload, BackupJobOperationRunning)
}

func FinishBackupJobOperationTxn(db *gorm.DB, payload *BackupJobOperationTransition) error {
	return transitionBackupJobOperation(db, payload, BackupJobOperationFinishing)
}

func releaseBackupJobOperation(db *gorm.DB, payload *BackupJobOperationTransition, allowQueued bool) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_job_operation_input_invalid")
	}
	token, operation, holderNodeID, err := normalizeBackupJobOperationIdentity(
		payload.JobID, payload.Token, payload.Operation, payload.HolderNodeID,
	)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing BackupJobOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("job_id = ?", payload.JobID).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if existing.Token != token || existing.Operation != operation ||
			strings.TrimSpace(existing.HolderNodeID) != holderNodeID ||
			strings.TrimSpace(existing.RequestPayload) != strings.TrimSpace(payload.RequestPayload) {
			return fmt.Errorf("backup_job_operation_token_mismatch")
		}
		if existing.State != BackupJobOperationFinishing && !(allowQueued && existing.State == BackupJobOperationQueued) {
			return fmt.Errorf("backup_job_operation_not_releasable")
		}
		deleted := tx.Where("job_id = ? AND token = ?", payload.JobID, token).Delete(&BackupJobOperation{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return fmt.Errorf("backup_job_operation_release_conflict")
		}
		return nil
	})
}

func AbortBackupJobOperationTxn(db *gorm.DB, payload *BackupJobOperationTransition) error {
	return releaseBackupJobOperation(db, payload, true)
}

func ReleaseBackupJobOperationTxn(db *gorm.DB, payload *BackupJobOperationTransition) error {
	return releaseBackupJobOperation(db, payload, false)
}

// DeleteBackupJobTxn checks only replicated operation state. Local event rows
// are telemetry and deliberately remain outside this deterministic transaction.
func DeleteBackupJobTxn(db *gorm.DB, jobID uint) error {
	if db == nil {
		return fmt.Errorf("backup_job_database_unavailable")
	}
	if jobID == 0 {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var operationCount int64
		if err := tx.Model(&BackupJobOperation{}).
			Where("job_id = ?", jobID).
			Count(&operationCount).Error; err != nil {
			return err
		}
		if operationCount != 0 {
			return fmt.Errorf("backup_job_running")
		}

		deleted := tx.Delete(&BackupJob{}, jobID)
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected == 0 {
			return fmt.Errorf("backup_job_not_found")
		}
		return MarkDeletedBackupJobRunnerRebindItemsTxn(tx, jobID)
	})
}
