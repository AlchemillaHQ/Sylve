// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupTargetProvisionStatePending   = "pending"
	BackupTargetProvisionStateCompleted = "completed"
	BackupTargetProvisionStateFailed    = "failed"
)

// BackupTargetProvisionOperation is a replicated intent record committed
// before create-time remote-root provisioning. The proposed target remains
// invisible to jobs until exact completion atomically creates it.
type BackupTargetProvisionOperation struct {
	Token               string `gorm:"primaryKey" json:"token"`
	TargetID            uint   `gorm:"index;not null" json:"targetId"`
	TargetName          string `gorm:"index;not null" json:"targetName"`
	ProposedFingerprint string `gorm:"index;not null" json:"proposedFingerprint"`
	TargetPayload       string `gorm:"type:text;not null" json:"targetPayload"`
	State               string `gorm:"index;not null" json:"state"`
	Error               string `gorm:"type:text" json:"error"`
	Revision            uint64 `gorm:"not null;default:1" json:"revision"`
}

type BackupTargetProvisionPrepare struct {
	Token               string                         `json:"token"`
	Target              BackupTargetReplicationPayload `json:"target"`
	ProposedFingerprint string                         `json:"proposedFingerprint"`
}

type BackupTargetProvisionTransition struct {
	Token               string `json:"token"`
	TargetID            uint   `json:"targetId"`
	ProposedFingerprint string `json:"proposedFingerprint"`
	Error               string `json:"error,omitempty"`
}

func normalizedBackupTargetProvisionPrepare(
	prepare BackupTargetProvisionPrepare,
) (BackupTargetProvisionPrepare, BackupTarget, string, error) {
	prepare.Token = strings.TrimSpace(prepare.Token)
	prepare.ProposedFingerprint = strings.ToLower(strings.TrimSpace(prepare.ProposedFingerprint))
	target := normalizeBackupTarget(prepare.Target.ToModel())
	prepare.Target = BackupTargetToReplicationPayload(target)
	if prepare.Token == "" {
		return prepare, target, "", fmt.Errorf("backup_target_provision_token_required")
	}
	if target.ID == 0 {
		return prepare, target, "", fmt.Errorf("backup_target_id_required")
	}
	if target.Name == "" {
		return prepare, target, "", fmt.Errorf("name_required")
	}
	if target.SSHKey == "" {
		return prepare, target, "", fmt.Errorf("managed_ssh_key_required")
	}
	if !target.CreateBackupRoot {
		return prepare, target, "", fmt.Errorf("backup_target_root_creation_not_authorized")
	}
	if prepare.ProposedFingerprint == "" ||
		BackupTargetConfigurationFingerprint(&target) != prepare.ProposedFingerprint {
		return prepare, target, "", fmt.Errorf("backup_target_provision_fingerprint_mismatch")
	}
	payload, err := json.Marshal(prepare.Target)
	if err != nil {
		return prepare, target, "", fmt.Errorf("marshal_backup_target_provision_payload: %w", err)
	}
	return prepare, target, string(payload), nil
}

func backupTargetProvisionOperationMatches(
	existing *BackupTargetProvisionOperation,
	prepare BackupTargetProvisionPrepare,
	target BackupTarget,
	payload string,
) bool {
	if existing == nil || existing.Token != prepare.Token || existing.TargetID != target.ID ||
		strings.TrimSpace(existing.TargetName) != target.Name ||
		strings.ToLower(strings.TrimSpace(existing.ProposedFingerprint)) != prepare.ProposedFingerprint {
		return false
	}
	// Terminal receipts retain the fingerprint but discard duplicate private
	// key material. The fingerprint still binds an exact replay.
	if existing.State == BackupTargetProvisionStateCompleted || existing.State == BackupTargetProvisionStateFailed {
		return true
	}
	return strings.TrimSpace(existing.TargetPayload) == strings.TrimSpace(payload)
}

func PrepareBackupTargetProvisionOperationTxn(db *gorm.DB, prepare *BackupTargetProvisionPrepare) error {
	if db == nil || prepare == nil {
		return fmt.Errorf("backup_target_provision_input_invalid")
	}
	normalized, target, targetPayload, err := normalizedBackupTargetProvisionPrepare(*prepare)
	if err != nil {
		return err
	}
	*prepare = normalized

	return db.Transaction(func(tx *gorm.DB) error {
		var existingToken BackupTargetProvisionOperation
		tokenResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token = ?", normalized.Token).Limit(1).Find(&existingToken)
		if tokenResult.Error != nil {
			return tokenResult.Error
		}
		if tokenResult.RowsAffected != 0 {
			if backupTargetProvisionOperationMatches(&existingToken, normalized, target, targetPayload) {
				return nil
			}
			return fmt.Errorf("backup_target_provision_token_mismatch")
		}

		var existingTarget BackupTarget
		idResult := tx.Where("id = ?", target.ID).Limit(1).Find(&existingTarget)
		if idResult.Error != nil {
			return idResult.Error
		}
		if idResult.RowsAffected != 0 {
			return fmt.Errorf("backup_target_id_conflict")
		}
		nameResult := tx.Where("name = ?", target.Name).Limit(1).Find(&existingTarget)
		if nameResult.Error != nil {
			return nameResult.Error
		}
		if nameResult.RowsAffected != 0 {
			return fmt.Errorf("backup_target_name_conflict")
		}

		var activeCount int64
		if err := tx.Model(&BackupTargetProvisionOperation{}).
			Where("state = ? AND (target_id = ? OR target_name = ?)", BackupTargetProvisionStatePending, target.ID, target.Name).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount != 0 {
			return fmt.Errorf("backup_target_provision_pending")
		}

		return tx.Create(&BackupTargetProvisionOperation{
			Token:               normalized.Token,
			TargetID:            target.ID,
			TargetName:          target.Name,
			ProposedFingerprint: normalized.ProposedFingerprint,
			TargetPayload:       targetPayload,
			State:               BackupTargetProvisionStatePending,
			Revision:            1,
		}).Error
	})
}

func DecodeBackupTargetProvisionTarget(operation *BackupTargetProvisionOperation) (BackupTarget, error) {
	if operation == nil {
		return BackupTarget{}, fmt.Errorf("backup_target_provision_operation_required")
	}
	var payload BackupTargetReplicationPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(operation.TargetPayload)), &payload); err != nil {
		return BackupTarget{}, fmt.Errorf("decode_backup_target_provision_payload: %w", err)
	}
	target := normalizeBackupTarget(payload.ToModel())
	if target.ID != operation.TargetID || target.Name != strings.TrimSpace(operation.TargetName) ||
		BackupTargetConfigurationFingerprint(&target) != strings.ToLower(strings.TrimSpace(operation.ProposedFingerprint)) {
		return BackupTarget{}, fmt.Errorf("backup_target_provision_payload_mismatch")
	}
	return target, nil
}

func normalizeBackupTargetProvisionTransition(
	transition BackupTargetProvisionTransition,
) (BackupTargetProvisionTransition, error) {
	transition.Token = strings.TrimSpace(transition.Token)
	transition.ProposedFingerprint = strings.ToLower(strings.TrimSpace(transition.ProposedFingerprint))
	transition.Error = strings.TrimSpace(transition.Error)
	if transition.Token == "" {
		return transition, fmt.Errorf("backup_target_provision_token_required")
	}
	if transition.TargetID == 0 || transition.ProposedFingerprint == "" {
		return transition, fmt.Errorf("backup_target_provision_identity_required")
	}
	return transition, nil
}

func CompleteBackupTargetProvisionOperationTxn(db *gorm.DB, transition *BackupTargetProvisionTransition) error {
	if db == nil || transition == nil {
		return fmt.Errorf("backup_target_provision_input_invalid")
	}
	normalized, err := normalizeBackupTargetProvisionTransition(*transition)
	if err != nil {
		return err
	}
	*transition = normalized

	return db.Transaction(func(tx *gorm.DB) error {
		var operation BackupTargetProvisionOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token = ?", normalized.Token).Limit(1).Find(&operation)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("backup_target_provision_operation_not_found")
		}
		if operation.TargetID != normalized.TargetID ||
			strings.ToLower(strings.TrimSpace(operation.ProposedFingerprint)) != normalized.ProposedFingerprint {
			return fmt.Errorf("backup_target_provision_token_mismatch")
		}
		if operation.State == BackupTargetProvisionStateCompleted {
			var committed BackupTarget
			if err := tx.Where("id = ?", operation.TargetID).First(&committed).Error; err != nil {
				return err
			}
			if BackupTargetConfigurationFingerprint(&committed) != normalized.ProposedFingerprint {
				return fmt.Errorf("backup_target_provision_completed_target_mismatch")
			}
			return nil
		}
		if operation.State != BackupTargetProvisionStatePending {
			return fmt.Errorf("backup_target_provision_not_completable")
		}
		target, err := DecodeBackupTargetProvisionTarget(&operation)
		if err != nil {
			return err
		}

		var existing BackupTarget
		idResult := tx.Where("id = ?", target.ID).Limit(1).Find(&existing)
		if idResult.Error != nil {
			return idResult.Error
		}
		if idResult.RowsAffected != 0 && BackupTargetConfigurationFingerprint(&existing) != normalized.ProposedFingerprint {
			return fmt.Errorf("backup_target_id_conflict")
		}
		nameResult := tx.Where("name = ?", target.Name).Limit(1).Find(&existing)
		if nameResult.Error != nil {
			return nameResult.Error
		}
		if nameResult.RowsAffected != 0 && BackupTargetConfigurationFingerprint(&existing) != normalized.ProposedFingerprint {
			return fmt.Errorf("backup_target_name_conflict")
		}
		if idResult.RowsAffected == 0 && nameResult.RowsAffected == 0 {
			if err := tx.Create(&target).Error; err != nil {
				return err
			}
		}

		updated := tx.Model(&BackupTargetProvisionOperation{}).
			Where("token = ? AND state = ? AND revision = ?", operation.Token, BackupTargetProvisionStatePending, operation.Revision).
			Updates(map[string]any{
				"state":          BackupTargetProvisionStateCompleted,
				"error":          "",
				"target_payload": "",
				"revision":       operation.Revision + 1,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_target_provision_revision_conflict")
		}
		return nil
	})
}

func FailBackupTargetProvisionOperationTxn(db *gorm.DB, transition *BackupTargetProvisionTransition) error {
	if db == nil || transition == nil {
		return fmt.Errorf("backup_target_provision_input_invalid")
	}
	normalized, err := normalizeBackupTargetProvisionTransition(*transition)
	if err != nil {
		return err
	}
	if normalized.Error == "" {
		return fmt.Errorf("backup_target_provision_error_required")
	}
	*transition = normalized

	return db.Transaction(func(tx *gorm.DB) error {
		var operation BackupTargetProvisionOperation
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token = ?", normalized.Token).Limit(1).Find(&operation)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("backup_target_provision_operation_not_found")
		}
		if operation.TargetID != normalized.TargetID ||
			strings.ToLower(strings.TrimSpace(operation.ProposedFingerprint)) != normalized.ProposedFingerprint {
			return fmt.Errorf("backup_target_provision_token_mismatch")
		}
		if operation.State == BackupTargetProvisionStateFailed {
			if strings.TrimSpace(operation.Error) == normalized.Error {
				return nil
			}
			return fmt.Errorf("backup_target_provision_terminal_mismatch")
		}
		if operation.State != BackupTargetProvisionStatePending {
			return fmt.Errorf("backup_target_provision_not_failable")
		}
		updated := tx.Model(&BackupTargetProvisionOperation{}).
			Where("token = ? AND state = ? AND revision = ?", operation.Token, BackupTargetProvisionStatePending, operation.Revision).
			Updates(map[string]any{
				"state":          BackupTargetProvisionStateFailed,
				"error":          normalized.Error,
				"target_payload": "",
				"revision":       operation.Revision + 1,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_target_provision_revision_conflict")
		}
		return nil
	})
}
