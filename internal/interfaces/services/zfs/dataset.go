// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

// type zDataset struct {
// 	Dataset zfs.Dataset
// }

type Dataset struct {
	Name          string `json:"name"`
	Origin        string `json:"origin"`
	GUID          string `json:"guid"`
	Used          uint64 `json:"used"`
	Avail         uint64 `json:"avail"`
	Recordsize    uint64 `json:"recordsize"`
	Mountpoint    string `json:"mountpoint"`
	Compression   string `json:"compression"`
	Type          string `json:"type"`
	Written       uint64 `json:"written"`
	Volsize       uint64 `json:"volsize"`
	VolBlockSize  uint64 `json:"volblocksize"`
	Logicalused   uint64 `json:"logicalused"`
	Usedbydataset uint64 `json:"usedbydataset"`
	Quota         uint64 `json:"quota"`
	Referenced    uint64 `json:"referenced"`
	Mounted       string `json:"mounted"`
	Checksum      string `json:"checksum"`
	Dedup         string `json:"dedup"`
	ACLInherit    string `json:"aclinherit"`
	ACLMode       string `json:"aclmode"`
	PrimaryCache  string `json:"primarycache"`
	VolMode       string `json:"volmode"`
}

type CreatePeriodicSnapshotJobRequest struct {
	GUID      string `json:"guid" binding:"required"`
	Prefix    string `json:"prefix" binding:"required"`
	Recursive *bool  `json:"recursive"`
	Interval  *int   `json:"interval" binding:"required"`
	CronExpr  string `json:"cronExpr"`

	KeepLast   *int `json:"keepLast"`
	MaxAgeDays *int `json:"maxAgeDays"`

	KeepHourly  *int `json:"keepHourly"`
	KeepDaily   *int `json:"keepDaily"`
	KeepWeekly  *int `json:"keepWeekly"`
	KeepMonthly *int `json:"keepMonthly"`
	KeepYearly  *int `json:"keepYearly"`
}

type ModifyPeriodicSnapshotRetentionRequest struct {
	ID int `json:"id" binding:"required"`

	KeepLast   *int `json:"keepLast"`
	MaxAgeDays *int `json:"maxAgeDays"`

	KeepHourly  *int `json:"keepHourly"`
	KeepDaily   *int `json:"keepDaily"`
	KeepWeekly  *int `json:"keepWeekly"`
	KeepMonthly *int `json:"keepMonthly"`
	KeepYearly  *int `json:"keepYearly"`
}
