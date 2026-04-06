// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

import "time"

type FirewallRuleDelta struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	RuleType     string    `gorm:"index;not null" json:"ruleType"` // traffic|nat
	RuleID       uint      `gorm:"index;not null" json:"ruleId"`
	PacketsDelta uint64    `gorm:"not null;default:0" json:"packetsDelta"`
	BytesDelta   uint64    `gorm:"not null;default:0" json:"bytesDelta"`
	CreatedAt    time.Time `gorm:"autoCreateTime;index" json:"createdAt"`
}
