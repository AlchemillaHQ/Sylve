// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsModels

import "time"

type PeriodicSnapshot struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	GUID      string `gorm:"uniqueIndex:uniq_guid_interval_prefix,priority:1" json:"guid"`
	Interval  int    `gorm:"uniqueIndex:uniq_guid_interval_prefix,priority:2" json:"interval"`
	Prefix    string `gorm:"uniqueIndex:uniq_guid_interval_prefix,priority:3" json:"prefix"`
	Recursive bool   `json:"recursive"`
	CronExpr  string `json:"cronExpr"`

	/* Simple Retention */
	KeepLast   int `json:"keepLast" gorm:"default:0"`   // e.g., keep last 5 snapshots
	MaxAgeDays int `json:"maxAgeDays" gorm:"default:0"` // e.g., 30 (delete older than 30 days)

	/* GFS Retention */
	KeepHourly  int `json:"keepHourly" gorm:"default:0"`  // e.g., keep 24 hourly
	KeepDaily   int `json:"keepDaily" gorm:"default:0"`   // e.g., keep 7 daily
	KeepWeekly  int `json:"keepWeekly" gorm:"default:0"`  // e.g., keep 4 weekly
	KeepMonthly int `json:"keepMonthly" gorm:"default:0"` // e.g., keep 12 monthly
	KeepYearly  int `json:"keepYearly" gorm:"default:0"`  // e.g., keep 3 yearly

	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	LastRunAt time.Time `json:"lastRunAt"`
}
