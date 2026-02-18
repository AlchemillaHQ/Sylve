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
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BackupJobModeDataset = "dataset"
	BackupJobModeJail    = "jail"
)

type BackupTarget struct {
	ID          uint        `gorm:"primaryKey" json:"id"`
	Name        string      `gorm:"uniqueIndex;not null" json:"name"`
	Endpoint    string      `gorm:"not null" json:"endpoint"`
	Description string      `json:"description"`
	Enabled     bool        `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time   `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt   time.Time   `gorm:"autoUpdateTime" json:"updatedAt"`
	Jobs        []BackupJob `json:"jobs,omitempty" gorm:"foreignKey:TargetID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type BackupJob struct {
	ID                 uint         `gorm:"primaryKey" json:"id"`
	Name               string       `gorm:"not null" json:"name"`
	TargetID           uint         `gorm:"index;not null" json:"targetId"`
	Target             BackupTarget `json:"target" gorm:"foreignKey:TargetID;references:ID"`
	RunnerNodeID       string       `gorm:"index" json:"runnerNodeId"`
	Mode               string       `gorm:"default:dataset;index" json:"mode"`
	SourceDataset      string       `json:"sourceDataset"`
	JailRootDataset    string       `json:"jailRootDataset"`
	DestinationDataset string       `gorm:"not null" json:"destinationDataset"`
	CronExpr           string       `gorm:"not null" json:"cronExpr"`
	Force              bool         `json:"force"`
	WithIntermediates  bool         `json:"withIntermediates"`
	Enabled            bool         `gorm:"default:true;index" json:"enabled"`
	LastRunAt          *time.Time   `json:"lastRunAt"`
	NextRunAt          *time.Time   `gorm:"index" json:"nextRunAt"`
	LastStatus         string       `gorm:"index" json:"lastStatus"`
	LastError          string       `gorm:"type:text" json:"lastError"`
	CreatedAt          time.Time    `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt          time.Time    `gorm:"autoUpdateTime" json:"updatedAt"`
}

type BackupReplicationEvent struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	JobID              *uint      `gorm:"index" json:"jobId"`
	Direction          string     `gorm:"index" json:"direction"`
	RemoteAddress      string     `json:"remoteAddress"`
	SourceDataset      string     `json:"sourceDataset"`
	DestinationDataset string     `json:"destinationDataset"`
	BaseSnapshot       string     `json:"baseSnapshot"`
	TargetSnapshot     string     `json:"targetSnapshot"`
	Mode               string     `json:"mode"`
	Status             string     `gorm:"index" json:"status"`
	Error              string     `gorm:"type:text" json:"error"`
	StartedAt          time.Time  `gorm:"index" json:"startedAt"`
	CompletedAt        *time.Time `json:"completedAt"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updatedAt"`
}

func upsertBackupTarget(db *gorm.DB, target *BackupTarget) error {
	if target.ID == 0 {
		return fmt.Errorf("backup_target_id_required")
	}

	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "endpoint", "description", "enabled", "updated_at",
		}),
	}).Create(target).Error
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
			"destination_dataset",
			"cron_expr",
			"force",
			"with_intermediates",
			"enabled",
			"next_run_at",
			"updated_at",
		}),
	}).Create(job).Error
}
