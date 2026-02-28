// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupJobModeDataset = "dataset"
	BackupJobModeJail    = "jail"
	BackupJobModeVM      = "vm"
)

// BackupTarget represents a remote ZFS host reachable via SSH for Zelta replication.
type BackupTarget struct {
	ID               uint        `gorm:"primaryKey" json:"id"`
	Name             string      `gorm:"uniqueIndex;not null" json:"name"`
	SSHHost          string      `gorm:"column:ssh_host;" json:"sshHost"`           // user@host
	SSHPort          int         `gorm:"column:ssh_port;default:22" json:"sshPort"` // SSH port (default 22)
	SSHKeyPath       string      `gorm:"column:ssh_key_path" json:"sshKeyPath"`     // path to private key on host filesystem
	SSHKey           string      `gorm:"column:ssh_key;type:text" json:"-"`
	BackupRoot       string      `gorm:"column:backup_root;" json:"backupRoot"` // target pool/dataset prefix (e.g., tank/Backups)
	CreateBackupRoot bool        `gorm:"column:create_backup_root;default:false" json:"createBackupRoot"`
	Description      string      `json:"description"`
	Enabled          bool        `gorm:"default:true" json:"enabled"`
	CreatedAt        time.Time   `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt        time.Time   `gorm:"autoUpdateTime" json:"updatedAt"`
	Jobs             []BackupJob `json:"jobs,omitempty" gorm:"foreignKey:TargetID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type BackupTargetReplicationPayload struct {
	ID               uint   `json:"id"`
	Name             string `json:"name"`
	SSHHost          string `json:"sshHost"`
	SSHPort          int    `json:"sshPort"`
	SSHKeyPath       string `json:"sshKeyPath"`
	SSHKey           string `json:"sshKey"`
	BackupRoot       string `json:"backupRoot"`
	CreateBackupRoot bool   `json:"createBackupRoot"`
	Description      string `json:"description"`
	Enabled          bool   `json:"enabled"`
}

func BackupTargetToReplicationPayload(target BackupTarget) BackupTargetReplicationPayload {
	return BackupTargetReplicationPayload{
		ID:               target.ID,
		Name:             target.Name,
		SSHHost:          target.SSHHost,
		SSHPort:          target.SSHPort,
		SSHKeyPath:       target.SSHKeyPath,
		SSHKey:           target.SSHKey,
		BackupRoot:       target.BackupRoot,
		CreateBackupRoot: target.CreateBackupRoot,
		Description:      target.Description,
		Enabled:          target.Enabled,
	}
}

func (p BackupTargetReplicationPayload) ToModel() BackupTarget {
	return BackupTarget{
		ID:               p.ID,
		Name:             p.Name,
		SSHHost:          p.SSHHost,
		SSHPort:          p.SSHPort,
		SSHKeyPath:       p.SSHKeyPath,
		SSHKey:           p.SSHKey,
		BackupRoot:       p.BackupRoot,
		CreateBackupRoot: p.CreateBackupRoot,
		Description:      p.Description,
		Enabled:          p.Enabled,
	}
}

// ZeltaEndpoint returns the Zelta-formatted endpoint string: user@host:pool/dataset
func (t *BackupTarget) ZeltaEndpoint(suffix string) string {
	root := t.BackupRoot
	if suffix != "" {
		root = root + "/" + suffix
	}
	return t.SSHHost + ":" + root
}

// BackupJob represents a scheduled Zelta replication job.
type BackupJob struct {
	ID               uint         `gorm:"primaryKey" json:"id"`
	Name             string       `gorm:"not null" json:"name"`
	TargetID         uint         `gorm:"index;not null" json:"targetId"`
	Target           BackupTarget `json:"target" gorm:"foreignKey:TargetID;references:ID"`
	RunnerNodeID     string       `gorm:"index" json:"runnerNodeId"`
	Mode             string       `gorm:"default:dataset;index" json:"mode"` // "dataset" or "jail"
	SourceDataset    string       `json:"sourceDataset"`                     // for mode=dataset
	JailRootDataset  string       `json:"jailRootDataset"`                   // for mode=jail
	FriendlySrc      string       `gorm:"column:friendly_src" json:"friendlySrc"`
	DestSuffix       string       `gorm:"column:dest_suffix" json:"destSuffix"` // appended to target's BackupRoot
	PruneKeepLast    int          `gorm:"column:prune_keep_last;default:0" json:"pruneKeepLast"`
	PruneTarget      bool         `gorm:"column:prune_target;default:false" json:"pruneTarget"`
	StopBeforeBackup bool         `gorm:"column:stop_before_backup;default:false" json:"stopBeforeBackup"`
	CronExpr         string       `gorm:"not null" json:"cronExpr"`
	Enabled          bool         `gorm:"default:true;index" json:"enabled"`
	LastRunAt        *time.Time   `json:"lastRunAt"`
	NextRunAt        *time.Time   `gorm:"index" json:"nextRunAt"`
	LastStatus       string       `gorm:"index" json:"lastStatus"`
	LastError        string       `gorm:"type:text" json:"lastError"`
	CreatedAt        time.Time    `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt        time.Time    `gorm:"autoUpdateTime" json:"updatedAt"`
}

// BackupEvent records the result of a Zelta backup run.
type BackupEvent struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	JobID          *uint      `gorm:"index" json:"jobId"`
	SourceDataset  string     `json:"sourceDataset"`
	TargetEndpoint string     `json:"targetEndpoint"`
	Mode           string     `json:"mode"`
	Status         string     `gorm:"index" json:"status"` // "running", "success", "failed"
	Error          string     `gorm:"type:text" json:"error"`
	Output         string     `gorm:"type:text" json:"output"` // zelta output
	StartedAt      time.Time  `gorm:"index" json:"startedAt"`
	CompletedAt    *time.Time `json:"completedAt"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

func upsertBackupTarget(db *gorm.DB, target *BackupTarget) error {
	if target.ID == 0 {
		return fmt.Errorf("backup_target_id_required")
	}

	normalized := normalizeBackupTarget(*target)
	*target = normalized

	return db.Transaction(func(tx *gorm.DB) error {
		var existingByID BackupTarget
		err := tx.Where("id = ?", target.ID).First(&existingByID).Error
		hasByID := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var existingByName BackupTarget
		err = tx.Where("name = ?", target.Name).First(&existingByName).Error
		hasByName := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		now := time.Now()
		updates := map[string]any{
			"name":               target.Name,
			"ssh_host":           target.SSHHost,
			"ssh_port":           target.SSHPort,
			"ssh_key_path":       target.SSHKeyPath,
			"ssh_key":            target.SSHKey,
			"backup_root":        target.BackupRoot,
			"create_backup_root": target.CreateBackupRoot,
			"description":        target.Description,
			"enabled":            target.Enabled,
			"updated_at":         now,
		}

		switch {
		case hasByID:
			if hasByName && existingByName.ID != existingByID.ID {
				return fmt.Errorf("backup_target_name_conflict_id_mismatch")
			}

			if backupTargetsEquivalent(existingByID, *target) {
				return nil
			}

			return tx.Model(&BackupTarget{}).Where("id = ?", target.ID).Updates(updates).Error

		case hasByName:
			if existingByName.ID == target.ID {
				if backupTargetsEquivalent(existingByName, *target) {
					return nil
				}
				return tx.Model(&BackupTarget{}).Where("id = ?", target.ID).Updates(updates).Error
			}

			updatesWithID := make(map[string]any, len(updates)+1)
			for key, value := range updates {
				updatesWithID[key] = value
			}
			updatesWithID["id"] = target.ID

			return tx.Model(&BackupTarget{}).Where("id = ?", existingByName.ID).Updates(updatesWithID).Error

		default:
			return tx.Create(target).Error
		}
	})
}

func normalizeBackupTarget(target BackupTarget) BackupTarget {
	target.Name = strings.TrimSpace(target.Name)
	target.SSHHost = strings.TrimSpace(target.SSHHost)
	target.SSHKeyPath = strings.TrimSpace(target.SSHKeyPath)
	target.BackupRoot = strings.TrimSpace(target.BackupRoot)
	target.Description = strings.TrimSpace(target.Description)

	if target.SSHPort == 0 {
		target.SSHPort = 22
	}

	return target
}

func backupTargetsEquivalent(existing BackupTarget, incoming BackupTarget) bool {
	return existing.ID == incoming.ID &&
		existing.Name == incoming.Name &&
		existing.SSHHost == incoming.SSHHost &&
		existing.SSHPort == incoming.SSHPort &&
		existing.SSHKeyPath == incoming.SSHKeyPath &&
		existing.SSHKey == incoming.SSHKey &&
		existing.BackupRoot == incoming.BackupRoot &&
		existing.CreateBackupRoot == incoming.CreateBackupRoot &&
		existing.Description == incoming.Description &&
		existing.Enabled == incoming.Enabled
}

func upsertBackupJob(db *gorm.DB, job *BackupJob) error {
	if job.ID == 0 {
		return fmt.Errorf("backup_job_id_required")
	}

	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name",
			"target_id",
			"runner_node_id",
			"mode",
			"source_dataset",
			"jail_root_dataset",
			"friendly_src",
			"dest_suffix",
			"prune_keep_last",
			"prune_target",
			"stop_before_backup",
			"cron_expr",
			"enabled",
			"next_run_at",
			"updated_at",
		}),
	}).Create(job).Error
}
