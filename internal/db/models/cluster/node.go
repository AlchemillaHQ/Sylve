// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import "time"

type ClusterNode struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	NodeUUID    string    `json:"nodeUUID" gorm:"column:node_uuid;uniqueIndex;default:'';not null"`
	Status      string    `json:"status"`
	Hostname    string    `json:"hostname"`
	API         string    `json:"api"`
	CPU         int       `json:"cpu"`
	CPUUsage    float64   `json:"cpuUsage"`
	Memory      uint64    `json:"memory"`
	MemoryUsage float64   `json:"memoryUsage"`
	Disk        uint64    `json:"disk"`
	DiskUsage   float64   `json:"diskUsage"`
	GuestIDs    []uint    `json:"guestIDs" gorm:"serializer:json;type:json"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
