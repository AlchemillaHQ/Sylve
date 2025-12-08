// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

type ZPoolHistorical struct {
	ID            int64   `json:"id" gorm:"primaryKey"`
	Name          string  `json:"name" gorm:"index"`
	Allocated     uint64  `json:"allocated"`
	Size          uint64  `json:"size"`
	Free          uint64  `json:"free"`
	Fragmentation float64 `json:"fragmentation"`
	DedupRatio    float64 `json:"dedupRatio"`
	CreatedAt     int64   `json:"createdAt" gorm:"autoCreateTime"`
}
