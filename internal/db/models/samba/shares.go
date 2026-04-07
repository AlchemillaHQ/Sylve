// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaModels

import (
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
)

type SambaShare struct {
	ID                 int            `json:"id" gorm:"primaryKey"`
	Name               string         `json:"name" gorm:"uniqueIndex"`
	Dataset            string         `json:"dataset" gorm:"uniqueIndex"`
	Path               string         `json:"path"`
	ReadOnlyGroups     []models.Group `json:"readOnlyGroups" gorm:"many2many:samba_share_read_only_groups;"`
	WriteableGroups    []models.Group `json:"writeableGroups" gorm:"many2many:samba_share_writeable_groups;"`
	CreateMask         string         `json:"createMask" gorm:"default:'0664'"`
	DirectoryMask      string         `json:"directoryMask" gorm:"default:'2775'"`
	GuestOk            bool           `json:"guestOk" gorm:"default:false"`
	ReadOnly           bool           `json:"readOnly" gorm:"default:false"`
	TimeMachine        bool           `json:"timeMachine" gorm:"default:false"`
	TimeMachineMaxSize uint64         `json:"timeMachineMaxSize" gorm:"default:0"`
	CreatedAt          time.Time      `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt          time.Time      `json:"updatedAt" gorm:"autoUpdateTime"`
}

type SambaAuditLog struct {
	ID        int       `json:"id" gorm:"primaryKey"`
	Share     string    `json:"share"`
	User      string    `json:"user"`
	IP        string    `json:"ip"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Path      string    `json:"path"`
	Target    string    `json:"target"`
	Folder    string    `json:"folder"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}
