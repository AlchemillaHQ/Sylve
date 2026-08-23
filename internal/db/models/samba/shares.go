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
	Enabled            bool           `json:"enabled" gorm:"not null;default:true"`
	ReadOnlyUsers      []models.User  `json:"readOnlyUsers" gorm:"many2many:samba_share_read_only_users;"`
	WriteableUsers     []models.User  `json:"writeableUsers" gorm:"many2many:samba_share_writeable_users;"`
	ReadOnlyGroups     []models.Group `json:"readOnlyGroups" gorm:"many2many:samba_share_read_only_groups;"`
	WriteableGroups    []models.Group `json:"writeableGroups" gorm:"many2many:samba_share_writeable_groups;"`
	CreateMask         string         `json:"createMask" gorm:"default:'0664'"`
	DirectoryMask      string         `json:"directoryMask" gorm:"default:'2775'"`
	GuestOk            bool           `json:"guestOk" gorm:"default:false"`
	ReadOnly           bool           `json:"readOnly" gorm:"default:false"`
	TimeMachine        bool           `json:"timeMachine" gorm:"default:false"`
	TimeMachineMaxSize uint64         `json:"timeMachineMaxSize" gorm:"default:0"`
	AuditEnabled       bool           `json:"auditEnabled" gorm:"default:false"`
	AuditRetentionDays *uint32        `json:"auditRetentionDays" gorm:"not null;default:70"`
	AuditedOperations  []string       `json:"auditedOperations" gorm:"serializer:json;default:'[]'"`
	CreatedAt          time.Time      `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt          time.Time      `json:"updatedAt" gorm:"autoUpdateTime"`
}

type SambaAuditLog struct {
	ID            int       `json:"id" gorm:"primaryKey"`
	ShareID       uint      `json:"shareId" gorm:"index:idx_samba_audit_share_created,priority:1"`
	Share         string    `json:"share"`
	User          string    `json:"user" gorm:"index:idx_samba_audit_user_created,priority:1"`
	Client        string    `json:"client"`
	IP            string    `json:"ip"`
	Action        string    `json:"action" gorm:"index:idx_samba_audit_action_created,priority:1"`
	Result        string    `json:"result"`
	Path          string    `json:"path"`
	Target        string    `json:"target"`
	Folder        string    `json:"folder"`
	ObjectType    string    `json:"-" gorm:"-"`
	Disposition   string    `json:"-" gorm:"-"`
	Occurrences   uint32    `json:"occurrences" gorm:"not null;default:1"`
	RetentionDays *uint32   `json:"retentionDays" gorm:"not null;default:70;index:idx_samba_audit_retention_created,priority:1"`
	CreatedAt     time.Time `json:"createdAt" gorm:"autoCreateTime;index:idx_samba_audit_created;index:idx_samba_audit_share_created,priority:2;index:idx_samba_audit_user_created,priority:2;index:idx_samba_audit_action_created,priority:2;index:idx_samba_audit_retention_created,priority:2"`
}

const DefaultAuditRetentionDays uint32 = 70

func AuditRetentionDaysPointer(days uint32) *uint32 {
	return &days
}

func AuditRetentionDaysValue(days *uint32) uint32 {
	if days == nil {
		return DefaultAuditRetentionDays
	}
	return *days
}
