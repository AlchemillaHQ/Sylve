// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterServiceInterfaces

type NodeHealthSync struct {
	NodeUUID    string  `json:"nodeUuid"`
	Hostname    string  `json:"hostname"`
	API         string  `json:"api"`
	Status      string  `json:"status"`
	CPU         int     `json:"cpu"`
	CPUUsage    float64 `json:"cpuUsage"`
	Memory      uint64  `json:"memory"`
	MemoryUsage float64 `json:"memoryUsage"`
	Disk        uint64  `json:"disk"`
	DiskUsage   float64 `json:"diskUsage"`
	GuestIDs    []uint  `json:"guestIds"`
}
