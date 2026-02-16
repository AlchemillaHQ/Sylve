// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import "time"

type BackupReplicationEvent struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
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
