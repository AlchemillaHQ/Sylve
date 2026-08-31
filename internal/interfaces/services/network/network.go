// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import (
	"context"
	"errors"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

var (
	ErrEpairOwnershipConflict = errors.New("epair ownership conflict")
	ErrEpairStateConflict     = errors.New("epair state conflict")
)

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
		macSource networkModels.StandardSwitchMACSource,
		private bool,
		dhcp bool,
		disableIPv6 bool,
		slaac bool,
		defaultRoute bool,
		defaultRoute6 bool,
		disableBridgeOffloads bool,
		manual networkModels.StandardSwitchManualAddresses) (uint, error)

	EditStandardSwitch(id uint,
		mtu int,
		vlan int,
		network4Id uint,
		network6Id uint,
		gateway4Id uint,
		gateway6Id uint,
		ports []string,
		macSource networkModels.StandardSwitchMACSource,
		private bool,
		dhcp bool,
		disableIPv6 bool,
		slaac bool,
		defaultRoute bool,
		defaultRoute6 bool,
		disableBridgeOffloads bool,
		manual networkModels.StandardSwitchManualAddresses) error
	DeleteStandardSwitch(id uint) error
	IsObjectUsed(id uint) (bool, string, error)
	GetObjectEntryByID(id uint) (string, error)
	GetBridgeNameByIDType(id uint, swType string) (string, error)
	CreateEpair(name string) error
	EnsureEpair(name string) error
	DeleteEpair(name string) error
	StartFirewallMonitor(ctx context.Context)
	EnableWireGuardService(ctx context.Context) error
	DisableWireGuardService(ctx context.Context) error
	ReconcileManagedRoutes() error
	RegisterOnJailObjectUpdateCallback(cb func(jailIDs []uint))
}
