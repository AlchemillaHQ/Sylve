// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoServiceInterfaces

import infoModels "github.com/alchemillahq/sylve/internal/db/models/info"

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
	Cron()
}
