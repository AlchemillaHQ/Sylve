// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupTargetUpdateKindMetadata  = "metadata"
	BackupTargetUpdateKindDisable   = "disable"
	BackupTargetUpdateKindEnable    = "enable"
	BackupTargetUpdateKindRotateKey = "rotate_key"
)

type backupTargetConfigurationFingerprintPayload struct {
	ID               uint   `json:"id"`
	Name             string `json:"name"`
	SSHHost          string `json:"sshHost"`
	SSHPort          int    `json:"sshPort"`
	SSHKeyHash       string `json:"sshKeyHash"`
	BackupRoot       string `json:"backupRoot"`
	CreateBackupRoot bool   `json:"createBackupRoot"`
	Description      string `json:"description"`
	Enabled          bool   `json:"enabled"`
}

// BackupTargetConfigurationFingerprint identifies replicated target state
// while deliberately excluding node-local key paths and database timestamps.
func BackupTargetConfigurationFingerprint(target *BackupTarget) string {
	if target == nil {
		return ""
	}
	normalized := normalizeBackupTarget(*target)
	keyHash := sha256.Sum256([]byte(normalized.SSHKey))
	payload, _ := json.Marshal(backupTargetConfigurationFingerprintPayload{
		ID:               normalized.ID,
		Name:             normalized.Name,
		SSHHost:          normalized.SSHHost,
		SSHPort:          normalized.SSHPort,
		SSHKeyHash:       hex.EncodeToString(keyHash[:]),
		BackupRoot:       normalized.BackupRoot,
		CreateBackupRoot: normalized.CreateBackupRoot,
		Description:      normalized.Description,
		Enabled:          normalized.Enabled,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// BackupTargetCreateV2 is the immutable-identity create command. Legacy raw
// backup_target/create payloads remain accepted solely for Raft-log replay.
type BackupTargetCreateV2 struct {
	Target              BackupTargetReplicationPayload `json:"target"`
	ProposedFingerprint string                         `json:"proposedFingerprint"`
}

// BackupTargetUpdateV2 carries only fields authorized by one explicit update
// class. Endpoint identity is absent by construction and therefore immutable.
type BackupTargetUpdateV2 struct {
	TargetID            uint   `json:"targetId"`
	Kind                string `json:"kind"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
	ProposedFingerprint string `json:"proposedFingerprint"`
	Name                string `json:"name,omitempty"`
	Description         string `json:"description,omitempty"`
	Enabled             bool   `json:"enabled,omitempty"`
	SSHKey              string `json:"sshKey,omitempty"`
}

func normalizeBackupTargetUpdateV2(update BackupTargetUpdateV2) BackupTargetUpdateV2 {
	update.Kind = strings.ToLower(strings.TrimSpace(update.Kind))
	update.ExpectedFingerprint = strings.ToLower(strings.TrimSpace(update.ExpectedFingerprint))
	update.ProposedFingerprint = strings.ToLower(strings.TrimSpace(update.ProposedFingerprint))
	update.Name = strings.TrimSpace(update.Name)
	update.Description = strings.TrimSpace(update.Description)
	update.SSHKey = strings.TrimSpace(update.SSHKey)
	return update
}

func backupTargetProvisionPendingForIdentity(tx *gorm.DB, targetID uint, name string) (bool, error) {
	if tx == nil || !tx.Migrator().HasTable(&BackupTargetProvisionOperation{}) {
		return false, nil
	}
	var count int64
	if err := tx.Model(&BackupTargetProvisionOperation{}).
		Where("state = ? AND (target_id = ? OR target_name = ?)", BackupTargetProvisionStatePending, targetID, strings.TrimSpace(name)).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count != 0, nil
}

func ApplyBackupTargetCreateV2Txn(db *gorm.DB, command *BackupTargetCreateV2) error {
	if db == nil || command == nil {
		return fmt.Errorf("backup_target_create_input_invalid")
	}
	target := normalizeBackupTarget(command.Target.ToModel())
	command.ProposedFingerprint = strings.ToLower(strings.TrimSpace(command.ProposedFingerprint))
	if target.ID == 0 {
		return fmt.Errorf("backup_target_id_required")
	}
	if target.Name == "" {
		return fmt.Errorf("name_required")
	}
	if target.SSHKey == "" {
		return fmt.Errorf("managed_ssh_key_required")
	}
	if command.ProposedFingerprint == "" || BackupTargetConfigurationFingerprint(&target) != command.ProposedFingerprint {
		return fmt.Errorf("backup_target_create_fingerprint_mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var byID BackupTarget
		idResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", target.ID).Limit(1).Find(&byID)
		if idResult.Error != nil {
			return idResult.Error
		}
		if idResult.RowsAffected != 0 {
			if BackupTargetConfigurationFingerprint(&byID) == command.ProposedFingerprint {
				return nil
			}
			return fmt.Errorf("backup_target_id_conflict")
		}

		var byName BackupTarget
		nameResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", target.Name).Limit(1).Find(&byName)
		if nameResult.Error != nil {
			return nameResult.Error
		}
		if nameResult.RowsAffected != 0 {
			return fmt.Errorf("backup_target_name_conflict")
		}

		pending, err := backupTargetProvisionPendingForIdentity(tx, target.ID, target.Name)
		if err != nil {
			return err
		}
		if pending {
			return fmt.Errorf("backup_target_provision_pending")
		}
		return tx.Create(&target).Error
	})
}

func backupTargetActiveOperationCountTxn(tx *gorm.DB, targetID uint) (int64, error) {
	if tx == nil || targetID == 0 {
		return 0, nil
	}
	var total int64
	if tx.Migrator().HasTable(&BackupJobOperation{}) && tx.Migrator().HasTable(&BackupJob{}) {
		var count int64
		if err := tx.Table("backup_job_operations AS operations").
			Joins("JOIN backup_jobs AS jobs ON jobs.id = operations.job_id").
			Where("jobs.target_id = ?", targetID).
			Count(&count).Error; err != nil {
			return 0, err
		}
		total += count
	}
	if tx.Migrator().HasTable(&BackupTargetRestoreOperation{}) {
		var count int64
		if err := tx.Model(&BackupTargetRestoreOperation{}).
			Where("target_id = ? AND state <> ?", targetID, BackupTargetRestoreOperationCompleted).
			Count(&count).Error; err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func BackupTargetActiveOperationCount(db *gorm.DB, targetID uint) (int64, error) {
	return backupTargetActiveOperationCountTxn(db, targetID)
}

func proposedBackupTargetForUpdate(existing BackupTarget, update BackupTargetUpdateV2) (BackupTarget, error) {
	proposed := normalizeBackupTarget(existing)
	switch update.Kind {
	case BackupTargetUpdateKindMetadata:
		if update.Name == "" {
			return BackupTarget{}, fmt.Errorf("name_required")
		}
		proposed.Name = update.Name
		proposed.Description = update.Description
	case BackupTargetUpdateKindDisable:
		proposed.Enabled = false
	case BackupTargetUpdateKindEnable:
		proposed.Enabled = true
	case BackupTargetUpdateKindRotateKey:
		if update.SSHKey == "" {
			return BackupTarget{}, fmt.Errorf("managed_ssh_key_required")
		}
		proposed.SSHKey = update.SSHKey
		proposed.SSHKeyPath = ""
		proposed.Enabled = false
	default:
		return BackupTarget{}, fmt.Errorf("invalid_backup_target_update_kind")
	}
	return normalizeBackupTarget(proposed), nil
}

func ApplyBackupTargetUpdateV2Txn(db *gorm.DB, command *BackupTargetUpdateV2) error {
	if db == nil || command == nil {
		return fmt.Errorf("backup_target_update_input_invalid")
	}
	update := normalizeBackupTargetUpdateV2(*command)
	*command = update
	if update.TargetID == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	if update.ExpectedFingerprint == "" || update.ProposedFingerprint == "" {
		return fmt.Errorf("backup_target_update_fingerprint_required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existing BackupTarget
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", update.TargetID).Limit(1).Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("backup_target_not_found")
		}
		currentFingerprint := BackupTargetConfigurationFingerprint(&existing)
		if currentFingerprint == update.ProposedFingerprint {
			return nil
		}
		if currentFingerprint != update.ExpectedFingerprint {
			return fmt.Errorf("backup_target_update_stale")
		}

		proposed, err := proposedBackupTargetForUpdate(existing, update)
		if err != nil {
			return err
		}
		if BackupTargetConfigurationFingerprint(&proposed) != update.ProposedFingerprint {
			return fmt.Errorf("backup_target_update_proposed_fingerprint_mismatch")
		}

		updates := map[string]any{}
		switch update.Kind {
		case BackupTargetUpdateKindMetadata:
			var nameOwner BackupTarget
			nameResult := tx.Where("name = ?", proposed.Name).Limit(1).Find(&nameOwner)
			if nameResult.Error != nil {
				return nameResult.Error
			}
			if nameResult.RowsAffected != 0 && nameOwner.ID != existing.ID {
				return fmt.Errorf("backup_target_name_conflict")
			}
			updates["name"] = proposed.Name
			updates["description"] = proposed.Description
		case BackupTargetUpdateKindDisable:
			updates["enabled"] = false
		case BackupTargetUpdateKindEnable:
			if strings.TrimSpace(existing.SSHKey) == "" {
				return fmt.Errorf("managed_ssh_key_required")
			}
			updates["enabled"] = true
		case BackupTargetUpdateKindRotateKey:
			if existing.Enabled {
				return fmt.Errorf("backup_target_must_be_disabled_for_key_rotation")
			}
			active, err := backupTargetActiveOperationCountTxn(tx, existing.ID)
			if err != nil {
				return err
			}
			if active != 0 {
				return fmt.Errorf("backup_target_has_active_operations: %d", active)
			}
			updates["ssh_key"] = proposed.SSHKey
			updates["ssh_key_path"] = ""
		default:
			return fmt.Errorf("invalid_backup_target_update_kind")
		}

		if len(updates) == 0 {
			return nil
		}
		updated := tx.Model(&BackupTarget{}).Where("id = ?", existing.ID).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_target_update_conflict")
		}
		if update.Kind == BackupTargetUpdateKindRotateKey && tx.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
			if err := tx.Where("target_id = ?", existing.ID).Delete(&BackupTargetNodeReadiness{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
