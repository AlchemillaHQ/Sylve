// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

type AvailableService string

const (
	DHCPServer     AvailableService = "dhcp-server"
	Jails          AvailableService = "jails"
	SambaServer    AvailableService = "samba-server"
	Virtualization AvailableService = "virtualization"
	WoLServer      AvailableService = "wol-server"
)

type BasicSettings struct {
	ID          uint               `json:"id" gorm:"primaryKey;autoIncrement"`
	Pools       []string           `json:"pools" gorm:"serializer:json;type:json"`
	Services    []AvailableService `json:"services" gorm:"serializer:json;type:json"`
	Initialized bool               `json:"initialized"`
	Restarted   bool               `json:"restarted"`
}
