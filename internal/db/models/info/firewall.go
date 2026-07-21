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

// FirewallRuleCounterTotal preserves a rule's cumulative counters across PF reloads.
// LastPF* values are the raw PF baseline used to calculate the next delta.
type FirewallRuleCounterTotal struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	RuleType      string    `gorm:"not null;uniqueIndex:idx_firewall_rule_counter_totals_rule" json:"ruleType"` // traffic|nat
	RuleID        uint      `gorm:"not null;uniqueIndex:idx_firewall_rule_counter_totals_rule" json:"ruleId"`
	Packets       uint64    `gorm:"not null;default:0" json:"packets"`
	Bytes         uint64    `gorm:"not null;default:0" json:"bytes"`
	LastPFPackets uint64    `gorm:"not null;default:0" json:"lastPfPackets"`
	LastPFBytes   uint64    `gorm:"not null;default:0" json:"lastPfBytes"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}
