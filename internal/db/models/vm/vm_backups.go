// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vmModels

import "time"

type VMBackupStatus string

const (
	StatusPending    VMBackupStatus = "pending"
	StatusInProgress VMBackupStatus = "in_progress"
	StatusCompleted  VMBackupStatus = "completed"
	StatusFailed     VMBackupStatus = "failed"
)

type VMBackupJob struct {
	VmID             int `json:"vmId"`
	ClusterStorageID int `json:"clusterStorageId"`

	/* Simple Retention */
	KeepLast   int `json:"keepLast" gorm:"default:0"`   // e.g., keep last 5 snapshots
	MaxAgeDays int `json:"maxAgeDays" gorm:"default:0"` // e.g., 30 (delete older than 30 days)

	/* GFS Retention */
	KeepHourly  int `json:"keepHourly" gorm:"default:0"`  // e.g., keep 24 hourly
	KeepDaily   int `json:"keepDaily" gorm:"default:0"`   // e.g., keep 7 daily
	KeepWeekly  int `json:"keepWeekly" gorm:"default:0"`  // e.g., keep 4 weekly
	KeepMonthly int `json:"keepMonthly" gorm:"default:0"` // e.g., keep 12 monthly
	KeepYearly  int `json:"keepYearly" gorm:"default:0"`  // e.g., keep 3 yearly

	Runs []VMBackupRun `json:"runs"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	LastRunAt time.Time `json:"lastRunAt"`
}

type VMBackupRun struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	VMBackupJobID uint           `json:"vmBackupJobId"`
	StartedAt     time.Time      `json:"startedAt"`
	CompletedAt   time.Time      `json:"completedAt"`
	Status        VMBackupStatus `json:"status"`
}
