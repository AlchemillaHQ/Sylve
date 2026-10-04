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
	"math"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/remoteexec"

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

type BackupTargetSSHHostTrust struct {
	TargetID            uint      `gorm:"primaryKey;autoIncrement:false" json:"targetId"`
	EndpointFingerprint string    `gorm:"not null" json:"endpointFingerprint"`
	Revision            uint64    `gorm:"not null" json:"revision"`
	PublicKey           string    `gorm:"type:text" json:"publicKey"`
	UpdatedAt           time.Time `gorm:"not null" json:"updatedAt"`
}

type BackupTargetSSHHostKeyStatus struct {
	State       string `json:"state"`
	Revision    uint64 `json:"revision"`
	Fingerprint string `json:"fingerprint"`
}

func (trust BackupTargetSSHHostTrust) Status() BackupTargetSSHHostKeyStatus {
	state, fingerprint := "unlearned", ""
	if trust.PublicKey != "" {
		state = "trusted"
		fingerprint, _ = remoteexec.SSHHostKeyFingerprint(trust.PublicKey)
	}
	return BackupTargetSSHHostKeyStatus{State: state, Revision: trust.Revision, Fingerprint: fingerprint}
}

type BackupTargetSSHHostTrustChange struct {
	TargetID            uint      `json:"targetId"`
	EndpointFingerprint string    `json:"endpointFingerprint"`
	ExpectedRevision    uint64    `json:"expectedRevision"`
	PublicKey           string    `json:"publicKey,omitempty"`
	OccurredAt          time.Time `json:"occurredAt"`
}

func BackupTargetSSHEndpointFingerprint(target *BackupTarget) (string, error) {
	if target == nil {
		return "", fmt.Errorf("backup_target_required")
	}
	destination, err := remoteexec.ParseSSHDestination(target.SSHHost)
	if err != nil {
		return "", err
	}
	port := target.SSHPort
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid_ssh_port")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", destination.String(), port)))
	return fmt.Sprintf("%x", sum[:]), nil
}

func GetBackupTargetSSHHostTrust(db *gorm.DB, target *BackupTarget) (*BackupTargetSSHHostTrust, error) {
	if db == nil || target == nil || target.ID == 0 {
		return nil, fmt.Errorf("backup_target_host_key_state_unavailable")
	}
	var trust BackupTargetSSHHostTrust
	if err := db.Where("target_id = ?", target.ID).First(&trust).Error; err != nil {
		return nil, fmt.Errorf("backup_target_host_key_state_unavailable: %w", err)
	}
	endpoint, err := BackupTargetSSHEndpointFingerprint(target)
	if err != nil || trust.Revision == 0 || trust.EndpointFingerprint != endpoint {
		return nil, fmt.Errorf("backup_target_host_key_state_unavailable")
	}
	if trust.PublicKey != "" {
		canonical, err := remoteexec.CanonicalSSHHostKey(trust.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("backup_target_host_key_state_unavailable: %w", err)
		}
		trust.PublicKey = canonical
	}
	return &trust, nil
}

func RequireBackupTargetHostKeyRevision(db *gorm.DB, targetID uint, revision uint64, requireKey bool) error {
	if revision == 0 {
		return fmt.Errorf("backup_target_host_key_revision_required")
	}
	var target BackupTarget
	if err := db.First(&target, targetID).Error; err != nil {
		return fmt.Errorf("backup_target_not_found: %w", err)
	}
	trust, err := GetBackupTargetSSHHostTrust(db, &target)
	if err != nil {
		return err
	}
	if trust.Revision != revision {
		return fmt.Errorf("backup_target_host_key_revision_conflict")
	}
	if requireKey && trust.PublicKey == "" {
		return fmt.Errorf("backup_target_host_key_unlearned: select Validate before this operation")
	}
	return nil
}

func ApplyBackupTargetSSHHostTrustTxn(db *gorm.DB, action string, change *BackupTargetSSHHostTrustChange) error {
	if db == nil || change == nil || change.TargetID == 0 || change.OccurredAt.IsZero() || change.ExpectedRevision == math.MaxUint64 {
		return fmt.Errorf("backup_target_host_key_change_invalid")
	}
	change.OccurredAt = NormalizeCommandTime(change.OccurredAt)
	change.PublicKey = strings.TrimSpace(change.PublicKey)
	if action != "initialize_v1" && action != "install_v1" && action != "reset_v1" {
		return fmt.Errorf("backup_target_host_key_action_invalid")
	}
	if action != "initialize_v1" && change.ExpectedRevision == 0 {
		return fmt.Errorf("backup_target_host_key_revision_required")
	}
	if change.PublicKey != "" {
		canonical, err := remoteexec.CanonicalSSHHostKey(change.PublicKey)
		if err != nil {
			return err
		}
		change.PublicKey = canonical
	}
	if action == "install_v1" && change.PublicKey == "" || action == "reset_v1" && change.PublicKey != "" {
		return fmt.Errorf("backup_target_host_key_change_invalid")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var target BackupTarget
		if err := tx.First(&target, change.TargetID).Error; err != nil {
			return fmt.Errorf("backup_target_not_found: %w", err)
		}
		endpoint, err := BackupTargetSSHEndpointFingerprint(&target)
		if err != nil || endpoint != change.EndpointFingerprint {
			return fmt.Errorf("backup_target_host_key_endpoint_conflict")
		}
		var current BackupTargetSSHHostTrust
		result := tx.Where("target_id = ?", target.ID).Limit(1).Find(&current)
		if result.Error != nil {
			return result.Error
		}
		if action == "initialize_v1" {
			if change.ExpectedRevision != 0 {
				return fmt.Errorf("backup_target_host_key_change_invalid")
			}
			if result.RowsAffected != 0 {
				if current.EndpointFingerprint == endpoint {
					if change.PublicKey != "" && current.Revision == 1 && current.PublicKey != change.PublicKey {
						return fmt.Errorf("backup_target_host_key_install_conflict")
					}
					return nil
				}
				return fmt.Errorf("backup_target_host_key_endpoint_conflict")
			}
			return tx.Create(&BackupTargetSSHHostTrust{TargetID: target.ID, EndpointFingerprint: endpoint,
				Revision: 1, PublicKey: change.PublicKey, UpdatedAt: change.OccurredAt}).Error
		}
		if result.RowsAffected == 0 || current.EndpointFingerprint != endpoint {
			return fmt.Errorf("backup_target_host_key_state_unavailable")
		}
		if current.Revision == change.ExpectedRevision+1 && current.PublicKey == change.PublicKey && current.UpdatedAt.Equal(change.OccurredAt) {
			return nil
		}
		if current.Revision != change.ExpectedRevision {
			return fmt.Errorf("backup_target_host_key_reset_stale")
		}
		if action == "install_v1" && current.PublicKey != "" {
			return fmt.Errorf("backup_target_host_key_already_trusted")
		}
		if action == "reset_v1" {
			active, err := backupTargetActiveOperationCountTxn(tx, target.ID)
			if err != nil {
				return err
			}
			if active != 0 {
				return fmt.Errorf("backup_target_host_key_reset_busy: wait for active backups and restores to stop")
			}
		}
		updated := tx.Model(&BackupTargetSSHHostTrust{}).Where("target_id = ? AND revision = ?", target.ID, current.Revision).
			Updates(map[string]any{"public_key": change.PublicKey, "revision": current.Revision + 1, "updated_at": change.OccurredAt})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("backup_target_host_key_revision_conflict")
		}
		if tx.Migrator().HasTable(&BackupTargetNodeReadiness{}) {
			return tx.Where("target_id = ?", target.ID).Delete(&BackupTargetNodeReadiness{}).Error
		}
		return nil
	})
}

type BackupTargetCreateV3 struct {
	BackupTargetCreateV2
	HostKey    string    `json:"hostKey"`
	OccurredAt time.Time `json:"occurredAt"`
}

func InitializeBackupTargetSSHHostTrustTxn(db *gorm.DB, occurredAt time.Time) error {
	if db == nil || occurredAt.IsZero() {
		return fmt.Errorf("backup_target_host_key_change_invalid")
	}
	occurredAt = NormalizeCommandTime(occurredAt)
	return db.Transaction(func(tx *gorm.DB) error {
		var options ClusterOption
		if err := tx.Where("id = ?", 1).Limit(1).Find(&options).Error; err != nil {
			return err
		}
		if options.SSHHostTrustInitialized {
			return nil
		}
		var targets []BackupTarget
		if err := tx.Order("id ASC").Find(&targets).Error; err != nil {
			return err
		}
		for i := range targets {
			endpoint, err := BackupTargetSSHEndpointFingerprint(&targets[i])
			if err != nil {
				return err
			}
			if err := ApplyBackupTargetSSHHostTrustTxn(tx, "initialize_v1", &BackupTargetSSHHostTrustChange{
				TargetID: targets[i].ID, EndpointFingerprint: endpoint, OccurredAt: occurredAt,
			}); err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(map[string]any{"ssh_host_trust_initialized": true, "updated_at": occurredAt}),
		}).Create(&ClusterOption{ID: 1, SSHHostTrustInitialized: true, CreatedAt: occurredAt, UpdatedAt: occurredAt}).Error
	})
}

func ApplyBackupTargetCreateV3Txn(db *gorm.DB, command *BackupTargetCreateV3) error {
	if db == nil || command == nil || command.HostKey == "" {
		return fmt.Errorf("backup_target_host_key_required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := ApplyBackupTargetCreateV2Txn(tx, &command.BackupTargetCreateV2); err != nil {
			return err
		}
		target := command.Target.ToModel()
		endpoint, err := BackupTargetSSHEndpointFingerprint(&target)
		if err != nil {
			return err
		}
		return ApplyBackupTargetSSHHostTrustTxn(tx, "initialize_v1", &BackupTargetSSHHostTrustChange{
			TargetID: target.ID, EndpointFingerprint: endpoint, PublicKey: command.HostKey, OccurredAt: command.OccurredAt,
		})
	})
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
