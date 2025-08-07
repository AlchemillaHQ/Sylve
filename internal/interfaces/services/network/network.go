// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import networkModels "sylve/internal/db/models/network"

type NetworkServiceInterface interface {
	SyncStandardSwitches(previous *networkModels.StandardSwitch, action string) error
	GetStandardSwitches() ([]networkModels.StandardSwitch, error)
	NewStandardSwitch(name string, mtu int, vlan int, address uint, address6 uint, ports []string, private bool, dhcp bool, disableIPv6 bool, slaac bool) error
	EditStandardSwitch(id int, mtu int, vlan int, address uint, address6 uint, ports []string, private bool, dhcp bool, disableIPv6 bool, slaac bool) error
	DeleteStandardSwitch(id int) error
	IsObjectUsed(id uint) (bool, error)
	GetObjectEntryByID(id uint) (string, error)
	GetBridgeNameByID(id uint) (string, error)
	CreateEpair(name string) error
	SyncEpairs() error
}
