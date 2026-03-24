// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package taskModels

import "time"

const (
	GuestTypeVM           = "vm"
	GuestTypeJail         = "jail"
	GuestTypeJailTemplate = "jail-template"
	GuestTypeVMTemplate   = "vm-template"
)

const (
	LifecycleTaskStatusQueued  = "queued"
	LifecycleTaskStatusRunning = "running"
	LifecycleTaskStatusSuccess = "success"
	LifecycleTaskStatusFailed  = "failed"
)

const (
	LifecycleTaskSourceUser    = "user"
	LifecycleTaskSourceStartup = "startup"
)

type GuestLifecycleTask struct {
	ID uint `gorm:"primaryKey" json:"id"`

	GuestType string `gorm:"index;not null" json:"guestType"`
	GuestID   uint   `gorm:"index;not null" json:"guestId"`
	Action    string `gorm:"index;not null" json:"action"`
	Source    string `gorm:"index;not null;default:user" json:"source"`

	Status string `gorm:"index;not null;default:queued" json:"status"`

	RequestedBy string `json:"requestedBy"`
	Message     string `json:"message"`
	Error       string `gorm:"type:text" json:"error"`
	Payload     string `gorm:"type:text" json:"payload"`

	OverrideRequested bool `gorm:"index;default:false" json:"overrideRequested"`

	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func (GuestLifecycleTask) TableName() string {
	return "guest_lifecycle_tasks"
}
