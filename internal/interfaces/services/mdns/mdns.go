// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsInterfaces

import (
	"github.com/alchemillahq/sylve/internal/db/models/mdns"
)

type MdnsRecordWithManaged struct {
	mdnsModels.MdnsRecord
	Managed bool   `json:"managed"`
	Source  string `json:"source"`
}

type MdnsServiceInterface interface {
	Rebuild() error
	GetSettings() (mdnsModels.MdnsSettings, error)
	SetSettings(interfaces, hostname string) error
	GetRecords() ([]MdnsRecordWithManaged, error)
	CreateRecord(name, recordType string, port int, txt map[string]string, interfaces string) (mdnsModels.MdnsRecord, error)
	UpdateRecord(id uint, name, recordType string, port int, txt map[string]string, interfaces string) error
	DeleteRecord(id uint) error
}
