// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import "time"

type DefaultRoutes struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

type System struct {
	ID            int           `json:"id" gorm:"primaryKey"`
	Initialized   bool          `json:"initialized"`
	Hostname      string        `json:"hostname"`
	DefaultRoutes DefaultRoutes `json:"defaultRoutes" gorm:"embedded"`
	ISODir        string        `json:"isoDir"`
}

type PassedThroughIDs struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	Domain    int    `json:"domain"`
	OldDriver string `json:"oldDriver"`
	DeviceID  string `json:"deviceID" gorm:"uniqueIndex"`
}

type Triggers struct {
	ID          int       `json:"id" gorm:"primaryKey"`
	Action      string    `json:"action"`
	Data        string    `json:"data"`
	Completed   bool      `json:"completed"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
	CompletedAt time.Time `json:"completedAt" gorm:"autoUpdateTime"`
}

type ZFSCacheInvalidation struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	Kind       string `json:"kind" gorm:"uniqueIndex;not null"`
	Generation uint64 `json:"generation" gorm:"not null"`

	FirstDirtyAt time.Time `json:"firstDirtyAt" gorm:"not null"`
	LastDirtyAt  time.Time `json:"lastDirtyAt" gorm:"not null"`
}

func (ZFSCacheInvalidation) TableName() string {
	return "zfs_cache_invalidations"
}

type SystemTunable struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"uniqueIndex;not null"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
