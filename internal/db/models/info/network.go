// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

import "time"

type NetworkInterface struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	IsDelta       bool      `json:"isDelta"`
	ReceivedBytes int64     `gorm:"default:0" json:"receivedBytes"`
	SentBytes     int64     `gorm:"default:0" json:"sentBytes"`
	CreatedAt     time.Time `gorm:"autoCreateTime;index" json:"createdAt"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func (n NetworkInterface) GetID() uint             { return n.ID }
func (n NetworkInterface) GetCreatedAt() time.Time { return n.CreatedAt }
