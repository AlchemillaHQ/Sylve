// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import networkModels "github.com/alchemillahq/sylve/internal/db/models/network"

type NetworkServiceInterface interface {
	SyncStandardSwitches(previous *networkModels.StandardSwitch, action string) error
	GetStandardSwitches() ([]networkModels.StandardSwitch, error)
	NewStandardSwitch(name string,
		mtu int,
		vlan int,
		network4Id uint,
		network6Id uint,
		gateway4Id uint,
		gateway6Id uint,
		ports []string,
		private bool,
		dhcp bool,
		disableIPv6 bool,
		slaac bool,
		defaultRoute bool) error

	EditStandardSwitch(id uint,
		mtu int,
		vlan int,
		network4Id uint,
		network6Id uint,
		gateway4Id uint,
		gateway6Id uint,
		ports []string,
		private bool,
		dhcp bool,
		disableIPv6 bool,
		slaac bool,
		defaultRoute bool) error
	DeleteStandardSwitch(id int) error
	IsObjectUsed(id uint) (bool, string, error)
	GetObjectEntryByID(id uint) (string, error)
	GetBridgeNameByIDType(id uint, swType string) (string, error)
	CreateEpair(name string) error
	SyncEpairs(forceStart bool) error
	DeleteEpair(name string) error
}
