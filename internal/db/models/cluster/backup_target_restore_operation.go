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
	BackupTargetRestoreOperationQueued    = "queued"
	BackupTargetRestoreOperationRunning   = "running"
	BackupTargetRestoreOperationFinishing = "finishing"
	BackupTargetRestoreOperationCompleted = "completed"
)

// BackupTargetRestoreOperation is the replicated outbox, exclusion record, and
// completed idempotency receipt for an out-of-band restore. Destinations are
// local to HolderNodeID, so equal dataset names on different nodes do not
// conflict. Active ancestor and descendant datasets on the same node do.
type BackupTargetRestoreOperation struct {
	Token              string    `gorm:"primaryKey" json:"token"`
	TargetID           uint      `gorm:"index;not null" json:"targetId"`
	HolderNodeID       string    `gorm:"index:idx_backup_target_restore_destination;not null" json:"holderNodeId"`
	DestinationDataset string    `gorm:"index:idx_backup_target_restore_destination;not null" json:"destinationDataset"`
	RequestPayload     string    `gorm:"type:text;not null" json:"requestPayload"`
	State              string    `gorm:"index;not null" json:"state"`
	Revision           uint64    `gorm:"not null" json:"revision"`
	AcquiredAt         time.Time `gorm:"index;not null" json:"acquiredAt"`
	UpdatedAt          time.Time `gorm:"index;not null" json:"updatedAt"`
}

type BackupTargetRestoreOperationAcquire struct {
	Token              string    `json:"token"`
	TargetID           uint      `json:"targetId"`
	HolderNodeID       string    `json:"holderNodeId"`
	DestinationDataset string    `json:"destinationDataset"`
	RequestPayload     string    `json:"requestPayload"`
	AcquiredAt         time.Time `json:"acquiredAt"`
}

type BackupTargetRestoreOperationTransition struct {
	Token              string    `json:"token"`
	TargetID           uint      `json:"targetId"`
	HolderNodeID       string    `json:"holderNodeId"`
	DestinationDataset string    `json:"destinationDataset"`
	RequestPayload     string    `json:"requestPayload"`
	OccurredAt         time.Time `json:"occurredAt"`
}

// NormalizeBackupTargetRestoreDestination mirrors the public restore input
// normalization without consulting ZFS or any node-local state.
func NormalizeBackupTargetRestoreDestination(dataset string) string {
	dataset = strings.TrimLeft(strings.TrimSpace(dataset), "/")
	for strings.HasSuffix(dataset, "/") {
		dataset = strings.TrimSuffix(dataset, "/")
	}
	return dataset
}

func backupTargetRestoreDatasetsOverlap(left, right string) bool {
	left = NormalizeBackupTargetRestoreDestination(left)
	right = NormalizeBackupTargetRestoreDestination(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func normalizeBackupTargetRestoreOperationIdentity(
	token string,
	targetID uint,
	holderNodeID string,
	destinationDataset string,
	requestPayload string,
) (string, string, string, string, error) {
	token = strings.TrimSpace(token)
	holderNodeID = strings.TrimSpace(holderNodeID)
	destinationDataset = NormalizeBackupTargetRestoreDestination(destinationDataset)
	requestPayload = strings.TrimSpace(requestPayload)

	switch {
	case token == "":
		return "", "", "", "", fmt.Errorf("backup_target_restore_operation_token_required")
	case targetID == 0:
		return "", "", "", "", fmt.Errorf("invalid_target_id")
	case holderNodeID == "":
		return "", "", "", "", fmt.Errorf("backup_target_restore_operation_holder_required")
	case destinationDataset == "":
		return "", "", "", "", fmt.Errorf("destination_dataset_required")
	case !strings.Contains(destinationDataset, "/") || strings.Contains(destinationDataset, "@"):
		return "", "", "", "", fmt.Errorf("destination_dataset_invalid")
	case requestPayload == "":
		return "", "", "", "", fmt.Errorf("backup_target_restore_operation_request_required")
	}

	return token, holderNodeID, destinationDataset, requestPayload, nil
}

func backupTargetRestoreOperationMatches(
	existing *BackupTargetRestoreOperation,
	token string,
	targetID uint,
	holderNodeID string,
	destinationDataset string,
	requestPayload string,
) bool {
	return existing != nil &&
		existing.Token == token &&
		existing.TargetID == targetID &&
		strings.TrimSpace(existing.HolderNodeID) == holderNodeID &&
		NormalizeBackupTargetRestoreDestination(existing.DestinationDataset) == destinationDataset &&
		strings.TrimSpace(existing.RequestPayload) == requestPayload
}

// AcquireBackupTargetRestoreOperationTxn atomically reserves one local dataset
// tree before queue insertion. Exact-token replay is idempotent; every other
// overlapping operation on the same holder conflicts.
func AcquireBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationAcquire) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_target_restore_operation_input_invalid")
	}
	token, holderNodeID, destinationDataset, requestPayload, err := normalizeBackupTargetRestoreOperationIdentity(
		payload.Token,
		payload.TargetID,
		payload.HolderNodeID,
		payload.DestinationDataset,
		payload.RequestPayload,
	)
	if err != nil {
		return err
	}
	if payload.AcquiredAt.IsZero() {
		return fmt.Errorf("backup_target_restore_operation_acquired_at_required")
	}
	payload.Token = token
	payload.HolderNodeID = holderNodeID
	payload.DestinationDataset = destinationDataset
	payload.RequestPayload = requestPayload
	payload.AcquiredAt = payload.AcquiredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var existingToken BackupTargetRestoreOperation
		tokenResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token = ?", token).
			Limit(1).
			Find(&existingToken)
		if tokenResult.Error != nil {
			return tokenResult.Error
		}
		if tokenResult.RowsAffected != 0 {
			if backupTargetRestoreOperationMatches(
				&existingToken,
				token,
				payload.TargetID,
				holderNodeID,
				destinationDataset,
				requestPayload,
			) {
				return nil
			}
			return fmt.Errorf("backup_target_restore_operation_token_mismatch")
		}

		var targetCount int64
		if err := tx.Model(&BackupTarget{}).Where("id = ?", payload.TargetID).Count(&targetCount).Error; err != nil {
			return err
		}
		if targetCount == 0 {
			return fmt.Errorf("backup_target_not_found")
		}

		var holderOperations []BackupTargetRestoreOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("holder_node_id = ? AND state <> ?", holderNodeID, BackupTargetRestoreOperationCompleted).
			Order("destination_dataset ASC, token ASC").
			Find(&holderOperations).Error; err != nil {
			return err
		}
		for _, existing := range holderOperations {
			if backupTargetRestoreDatasetsOverlap(existing.DestinationDataset, destinationDataset) {
				return fmt.Errorf(
					"restore_destination_reserved: dataset=%s holder=%s token=%s",
					existing.DestinationDataset,
					holderNodeID,
					existing.Token,
				)
			}
		}

		return tx.Create(&BackupTargetRestoreOperation{
			Token:              token,
			TargetID:           payload.TargetID,
			HolderNodeID:       holderNodeID,
			DestinationDataset: destinationDataset,
			RequestPayload:     requestPayload,
			State:              BackupTargetRestoreOperationQueued,
			Revision:           1,
			AcquiredAt:         payload.AcquiredAt,
			UpdatedAt:          payload.AcquiredAt,
		}).Error
	})
}

func transitionBackupTargetRestoreOperation(
	db *gorm.DB,
	payload *BackupTargetRestoreOperationTransition,
	action string,
) error {
	if db == nil || payload == nil {
		return fmt.Errorf("backup_target_restore_operation_input_invalid")
	}
	token, holderNodeID, destinationDataset, requestPayload, err := normalizeBackupTargetRestoreOperationIdentity(
		payload.Token,
		payload.TargetID,
		payload.HolderNodeID,
		payload.DestinationDataset,
		payload.RequestPayload,
	)
	if err != nil {
		return err
	}
	if payload.OccurredAt.IsZero() {
		return fmt.Errorf("backup_target_restore_operation_occurred_at_required")
	}
	payload.Token = token
	payload.HolderNodeID = holderNodeID
	payload.DestinationDataset = destinationDataset
	payload.RequestPayload = requestPayload
	payload.OccurredAt = payload.OccurredAt.UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var existing BackupTargetRestoreOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token = ?", token).
			Limit(1).
			Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if action == "abort" || action == "release" {
				return nil
			}
			return fmt.Errorf("backup_target_restore_operation_not_found")
		}
		if !backupTargetRestoreOperationMatches(
			&existing,
			token,
			payload.TargetID,
			holderNodeID,
			destinationDataset,
			requestPayload,
		) {
			return fmt.Errorf("backup_target_restore_operation_token_mismatch")
		}

		targetState := ""
		switch action {
		case "start":
			switch existing.State {
			case BackupTargetRestoreOperationQueued:
				targetState = BackupTargetRestoreOperationRunning
			case BackupTargetRestoreOperationRunning:
				return fmt.Errorf("backup_target_restore_operation_already_started")
			case BackupTargetRestoreOperationFinishing:
				return fmt.Errorf("backup_target_restore_operation_finishing")
			case BackupTargetRestoreOperationCompleted:
				return fmt.Errorf("backup_target_restore_operation_already_completed")
			default:
				return fmt.Errorf("invalid_backup_target_restore_operation_state")
			}
		case "finish":
			switch existing.State {
			case BackupTargetRestoreOperationRunning:
				targetState = BackupTargetRestoreOperationFinishing
			case BackupTargetRestoreOperationFinishing, BackupTargetRestoreOperationCompleted:
				return nil
			default:
				return fmt.Errorf("backup_target_restore_operation_not_finishable")
			}
		case "requeue":
			switch existing.State {
			case BackupTargetRestoreOperationQueued:
				return nil
			case BackupTargetRestoreOperationRunning:
				targetState = BackupTargetRestoreOperationQueued
			case BackupTargetRestoreOperationFinishing:
				return fmt.Errorf("backup_target_restore_operation_finishing")
			default:
				return fmt.Errorf("invalid_backup_target_restore_operation_state")
			}
		case "abort":
			if existing.State == BackupTargetRestoreOperationCompleted {
				return nil
			}
			if existing.State != BackupTargetRestoreOperationQueued {
				return fmt.Errorf("backup_target_restore_operation_not_abortable")
			}
		case "release":
			if existing.State == BackupTargetRestoreOperationCompleted {
				return nil
			}
			if existing.State != BackupTargetRestoreOperationFinishing {
				return fmt.Errorf("backup_target_restore_operation_not_releasable")
			}
			targetState = BackupTargetRestoreOperationCompleted
		default:
			return fmt.Errorf("invalid_backup_target_restore_operation_action")
		}

		if action == "abort" {
			deleted := tx.Where("token = ? AND revision = ?", token, existing.Revision).
				Delete(&BackupTargetRestoreOperation{})
			if deleted.Error != nil {
				return deleted.Error
			}
			if deleted.RowsAffected != 1 {
				return fmt.Errorf("backup_target_restore_operation_release_conflict")
			}
			return nil
		}

		updated := tx.Model(&BackupTargetRestoreOperation{}).
			Where("token = ? AND revision = ?", token, existing.Revision).
			Updates(map[string]any{
				"state":      targetState,
				"revision":   existing.Revision + 1,
				"updated_at": payload.OccurredAt,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_target_restore_operation_revision_conflict")
		}
		return nil
	})
}

func StartBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationTransition) error {
	return transitionBackupTargetRestoreOperation(db, payload, "start")
}

func FinishBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationTransition) error {
	return transitionBackupTargetRestoreOperation(db, payload, "finish")
}

func RequeueBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationTransition) error {
	return transitionBackupTargetRestoreOperation(db, payload, "requeue")
}

func AbortBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationTransition) error {
	return transitionBackupTargetRestoreOperation(db, payload, "abort")
}

// ReleaseBackupTargetRestoreOperationTxn retains a completed receipt. A new
// operation ID may reuse the destination, while retrying the completed ID is a
// no-op even after the original HTTP response was lost.
func ReleaseBackupTargetRestoreOperationTxn(db *gorm.DB, payload *BackupTargetRestoreOperationTransition) error {
	return transitionBackupTargetRestoreOperation(db, payload, "release")
}

// DeleteBackupTargetTxn keeps target deletion ordered with both backup jobs and
// queued/running out-of-band restores. HasTable preserves replay and tests made
// from schemas that predate durable target-restore reservations.
func DeleteBackupTargetTxn(db *gorm.DB, targetID uint) error {
	if db == nil {
		return fmt.Errorf("backup_target_database_unavailable")
	}
	if targetID == 0 {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var jobCount int64
		if err := tx.Model(&BackupJob{}).Where("target_id = ?", targetID).Count(&jobCount).Error; err != nil {
			return err
		}
		if jobCount > 0 {
			return fmt.Errorf("target_in_use_by_backup_jobs: %d", jobCount)
		}

		if tx.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
			var restoreCount int64
			if err := tx.Model(&BackupTargetRestoreOperation{}).
				Where("target_id = ? AND state <> ?", targetID, BackupTargetRestoreOperationCompleted).
				Count(&restoreCount).Error; err != nil {
				return err
			}
			if restoreCount > 0 {
				return fmt.Errorf("target_in_use_by_restore_operations: %d", restoreCount)
			}
			if err := tx.Where("target_id = ? AND state = ?", targetID, BackupTargetRestoreOperationCompleted).
				Delete(&BackupTargetRestoreOperation{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
			if err := tx.Where("target_id = ?", targetID).
				Delete(&BackupTargetNodeReadiness{}).Error; err != nil {
				return err
			}
		}

		return tx.Delete(&BackupTarget{}, targetID).Error
	})
}
