// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoServiceInterfaces

import (
	"context"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
)

type NodeInfo struct {
	Hostname     string  `json:"hostname"`
	LogicalCores int16   `json:"logicalCores"`
	CPUUsage     float64 `json:"cpuUsage"`

	RAMTotal uint64  `json:"ramTotal"`
	RAMUsage float64 `json:"ramUsage"`

	DiskTotal uint64  `json:"diskTotal"`
	DiskUsage float64 `json:"diskUsage"`

	Guests []uint `json:"guestIds"`
}

type InfoServiceInterface interface {
	GetAuditRecords(limit int) ([]infoModels.AuditRecord, error)

	GetBasicInfo() (basicInfo BasicInfo, err error)

	GetCPUInfo(usageOnly bool) (CPUInfo, error)
	GetCPUUsageHistorical() ([]infoModels.CPU, error)

	GetNetworkInterfacesInfo() ([]NetworkInterface, error)
	GetNetworkInterfacesHistorical() ([]HistoricalNetworkInterface, error)

	GetRAMInfo() (RAMInfo, error)
	GetSwapInfo() (SwapInfo, error)

	GetNoteByID(id int) (infoModels.Note, error)
	GetNotes() ([]infoModels.Note, error)
	AddNote(title, note string) (infoModels.Note, error)
	DeleteNoteByID(id int) error
	BulkDeleteNotes(ids []int) error
	UpdateNoteByID(id int, title, note string) error

	StoreStats()
	StoreNetworkInterfaceStats()
	Cron(ctx context.Context)
}
