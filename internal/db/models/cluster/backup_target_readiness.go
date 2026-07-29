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
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BackupTargetNodeReadinessStatus is the public effective view of one
// node-specific assessment after membership, fingerprint, and expiry checks.
type BackupTargetNodeReadinessStatus struct {
	TargetID             uint       `json:"targetId"`
	NodeID               string     `json:"nodeId"`
	ValidationSucceeded  bool       `json:"validationSucceeded"`
	LastVerifiedAt       *time.Time `json:"lastVerifiedAt"`
	ReadyUntil           *time.Time `json:"readyUntil"`
	LastError            string     `json:"lastError"`
	Revision             uint64     `json:"revision"`
	Ready                bool       `json:"ready"`
	CurrentVoter         bool       `json:"currentVoter"`
	Expired              bool       `json:"expired"`
	ConfigurationCurrent bool       `json:"configurationCurrent"`
}

// BackupTargetNodeReadiness is the replicated durable observation made by one
// exact node against one exact target configuration.
type BackupTargetNodeReadiness struct {
	TargetID             uint       `gorm:"primaryKey;autoIncrement:false;index" json:"targetId"`
	NodeID               string     `gorm:"primaryKey;size:255;index" json:"nodeId"`
	TargetFingerprint    string     `gorm:"size:64;not null;index" json:"targetFingerprint"`
	ValidationSucceeded  bool       `gorm:"not null;default:false;index" json:"validationSucceeded"`
	LastVerifiedAt       time.Time  `gorm:"not null;index" json:"lastVerifiedAt"`
	ReadyUntil           *time.Time `gorm:"index" json:"readyUntil"`
	LastError            string     `gorm:"type:text" json:"lastError"`
	Revision             uint64     `gorm:"not null;default:1" json:"revision"`
	RaftAppliedIndex     uint64     `gorm:"not null;default:0" json:"raftAppliedIndex"`
	UpdatedAt            time.Time  `gorm:"not null" json:"updatedAt"`
	Ready                bool       `gorm:"-" json:"-"`
	CurrentVoter         bool       `gorm:"-" json:"-"`
	Expired              bool       `gorm:"-" json:"-"`
	ConfigurationCurrent bool       `gorm:"-" json:"-"`
}

// BackupTargetNodeReadinessUpdate is carried in Raft commands. Every value,
// including timestamps, is supplied before apply so the FSM remains
// deterministic and never consults a local clock or remote service.
type BackupTargetNodeReadinessUpdate struct {
	TargetID            uint       `json:"targetId"`
	NodeID              string     `json:"nodeId"`
	TargetFingerprint   string     `json:"targetFingerprint"`
	ValidationSucceeded bool       `json:"validationSucceeded"`
	LastVerifiedAt      time.Time  `json:"lastVerifiedAt"`
	ReadyUntil          *time.Time `json:"readyUntil,omitempty"`
	LastError           string     `json:"lastError,omitempty"`
	RaftAppliedIndex    uint64     `json:"raftAppliedIndex,omitempty"`
}

type backupTargetConnectivityFingerprintPayload struct {
	SSHHost          string `json:"sshHost"`
	SSHPort          int    `json:"sshPort"`
	SSHKeyPath       string `json:"sshKeyPath"`
	SSHKeyHash       string `json:"sshKeyHash"`
	BackupRoot       string `json:"backupRoot"`
	CreateBackupRoot bool   `json:"createBackupRoot"`
}

// BackupTargetConnectivityFingerprint identifies only settings that can alter
// runner-to-target connectivity or validation behavior. Display-only edits do
// not invalidate an otherwise current per-node assessment.
func BackupTargetConnectivityFingerprint(target *BackupTarget) string {
	if target == nil {
		return ""
	}
	sshPort := target.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	keyHash := sha256.Sum256([]byte(strings.TrimSpace(target.SSHKey)))
	payload, _ := json.Marshal(backupTargetConnectivityFingerprintPayload{
		SSHHost:          strings.TrimSpace(target.SSHHost),
		SSHPort:          sshPort,
		SSHKeyPath:       strings.TrimSpace(target.SSHKeyPath),
		SSHKeyHash:       hex.EncodeToString(keyHash[:]),
		BackupRoot:       strings.TrimSpace(target.BackupRoot),
		CreateBackupRoot: target.CreateBackupRoot,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func normalizeBackupTargetNodeReadinessUpdate(update BackupTargetNodeReadinessUpdate) BackupTargetNodeReadinessUpdate {
	update.NodeID = strings.TrimSpace(update.NodeID)
	update.TargetFingerprint = strings.ToLower(strings.TrimSpace(update.TargetFingerprint))
	update.LastVerifiedAt = update.LastVerifiedAt.UTC()
	if update.ReadyUntil != nil {
		readyUntil := update.ReadyUntil.UTC()
		update.ReadyUntil = &readyUntil
	}
	update.LastError = strings.TrimSpace(update.LastError)
	if len(update.LastError) > 4096 {
		update.LastError = update.LastError[:4096]
	}
	return update
}

func UpsertBackupTargetNodeReadinessBackfillTxn(db *gorm.DB, row *BackupTargetNodeReadiness) error {
	if db == nil || row == nil {
		return fmt.Errorf("backup_target_readiness_backfill_invalid")
	}
	update := BackupTargetNodeReadinessUpdate{
		TargetID: row.TargetID, NodeID: row.NodeID, TargetFingerprint: row.TargetFingerprint,
		ValidationSucceeded: row.ValidationSucceeded, LastVerifiedAt: row.LastVerifiedAt,
		ReadyUntil: row.ReadyUntil, LastError: row.LastError,
		RaftAppliedIndex: row.RaftAppliedIndex,
	}
	normalized := normalizeBackupTargetNodeReadinessUpdate(update)
	if normalized.TargetID == 0 || normalized.NodeID == "" || normalized.TargetFingerprint == "" ||
		normalized.LastVerifiedAt.IsZero() {
		return fmt.Errorf("backup_target_readiness_backfill_invalid")
	}
	if normalized.ValidationSucceeded {
		if normalized.ReadyUntil == nil || !normalized.ReadyUntil.After(normalized.LastVerifiedAt) {
			return fmt.Errorf("backup_target_readiness_expiry_invalid")
		}
		normalized.LastError = ""
	} else {
		normalized.ReadyUntil = nil
		if normalized.LastError == "" {
			return fmt.Errorf("backup_target_readiness_error_required")
		}
	}
	var target BackupTarget
	result := db.Where("id = ?", normalized.TargetID).Limit(1).Find(&target)
	if result.Error != nil || result.RowsAffected == 0 {
		if result.Error != nil {
			return result.Error
		}
		return fmt.Errorf("backup_target_not_found")
	}
	if BackupTargetConnectivityFingerprint(&target) != normalized.TargetFingerprint {
		return fmt.Errorf("backup_target_readiness_fingerprint_mismatch")
	}
	revision := row.Revision
	if revision == 0 {
		revision = 1
	}
	updatedAt := row.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = normalized.LastVerifiedAt
	}
	backfill := BackupTargetNodeReadiness{
		TargetID: normalized.TargetID, NodeID: normalized.NodeID,
		TargetFingerprint: normalized.TargetFingerprint, ValidationSucceeded: normalized.ValidationSucceeded,
		LastVerifiedAt: normalized.LastVerifiedAt, ReadyUntil: normalized.ReadyUntil,
		LastError: normalized.LastError, Revision: revision,
		RaftAppliedIndex: normalized.RaftAppliedIndex, UpdatedAt: updatedAt,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "target_id"}, {Name: "node_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"target_fingerprint", "validation_succeeded", "last_verified_at",
			"ready_until", "last_error", "revision", "raft_applied_index", "updated_at",
		}),
	}).Create(&backfill).Error
}

func ApplyBackupTargetNodeReadinessForJobTxn(
	db *gorm.DB,
	job *BackupJob,
	update *BackupTargetNodeReadinessUpdate,
) error {
	if job == nil || update == nil {
		return fmt.Errorf("backup_target_readiness_job_receipt_required")
	}
	if update.TargetID != job.TargetID || strings.TrimSpace(update.NodeID) != strings.TrimSpace(job.RunnerNodeID) {
		return fmt.Errorf("backup_target_readiness_job_scope_mismatch")
	}
	if !update.ValidationSucceeded {
		return fmt.Errorf("backup_target_readiness_job_validation_failed")
	}
	return ApplyBackupTargetNodeReadinessUpdateTxn(db, update)
}

// ApplyBackupTargetNodeReadinessUpdateTxn validates the receipt against the
// current replicated target and upserts it. It is safe to call from a larger
// FSM transaction that also mutates a backup job.
func ApplyBackupTargetNodeReadinessUpdateTxn(db *gorm.DB, update *BackupTargetNodeReadinessUpdate) error {
	if db == nil || update == nil {
		return fmt.Errorf("backup_target_readiness_update_invalid")
	}
	normalized := normalizeBackupTargetNodeReadinessUpdate(*update)
	*update = normalized
	if update.TargetID == 0 {
		return fmt.Errorf("backup_target_readiness_target_id_required")
	}
	if update.NodeID == "" {
		return fmt.Errorf("backup_target_readiness_node_id_required")
	}
	if update.TargetFingerprint == "" {
		return fmt.Errorf("backup_target_readiness_fingerprint_required")
	}
	if update.LastVerifiedAt.IsZero() {
		return fmt.Errorf("backup_target_readiness_verified_at_required")
	}
	if update.ValidationSucceeded {
		if update.ReadyUntil == nil || !update.ReadyUntil.After(update.LastVerifiedAt) {
			return fmt.Errorf("backup_target_readiness_expiry_invalid")
		}
		update.LastError = ""
	} else {
		update.ReadyUntil = nil
		if update.LastError == "" {
			return fmt.Errorf("backup_target_readiness_error_required")
		}
	}

	var target BackupTarget
	result := db.Where("id = ?", update.TargetID).Limit(1).Find(&target)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("backup_target_not_found")
	}
	if current := BackupTargetConnectivityFingerprint(&target); current != update.TargetFingerprint {
		return fmt.Errorf("backup_target_readiness_fingerprint_mismatch")
	}

	var existing BackupTargetNodeReadiness
	existingResult := db.
		Where("target_id = ? AND node_id = ?", update.TargetID, update.NodeID).
		Limit(1).Find(&existing)
	if existingResult.Error != nil {
		return existingResult.Error
	}
	revision := uint64(1)
	if existingResult.RowsAffected > 0 {
		revision = existing.Revision + 1
	}
	row := BackupTargetNodeReadiness{
		TargetID:            update.TargetID,
		NodeID:              update.NodeID,
		TargetFingerprint:   update.TargetFingerprint,
		ValidationSucceeded: update.ValidationSucceeded,
		LastVerifiedAt:      update.LastVerifiedAt,
		ReadyUntil:          update.ReadyUntil,
		LastError:           update.LastError,
		Revision:            revision,
		RaftAppliedIndex:    update.RaftAppliedIndex,
		UpdatedAt:           update.LastVerifiedAt,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "target_id"}, {Name: "node_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"target_fingerprint", "validation_succeeded", "last_verified_at",
			"ready_until", "last_error", "revision", "raft_applied_index", "updated_at",
		}),
	}).Create(&row).Error
}
