// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

import (
	"time"
)

type AuditRecord struct {
	ID        uint          `gorm:"primaryKey" json:"id"`
	UserID    *uint         `json:"userId" gorm:"index"`
	User      string        `json:"user"`
	AuthType  string        `json:"authType"`
	Node      string        `json:"node"`
	Started   time.Time     `json:"started"`
	Ended     time.Time     `json:"ended"`
	Action    string        `json:"action"`
	Duration  time.Duration `json:"duration"`
	Status    string        `json:"status"`
	CreatedAt time.Time     `json:"createdAt" gorm:"autoCreateTime;index"`
	UpdatedAt time.Time     `json:"updatedAt" gorm:"autoUpdateTime"`

	AsyncJobID   *uint  `json:"asyncJobId" gorm:"index"`
	AsyncJobType string `json:"asyncJobType"`
	Error        string `json:"error,omitempty" gorm:"type:text"`

	Version int `json:"version"`
}
