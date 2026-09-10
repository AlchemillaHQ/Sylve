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

type StandardSwitchConfig struct {
	MTU                   int
	VLAN                  int
	Network4ID            uint
	Network6ID            uint
	Gateway4ID            uint
	Gateway6ID            uint
	Ports                 []string
	MACSource             networkModels.StandardSwitchMACSource
	Private               bool
	DHCP                  bool
	DisableIPv6           bool
	SLAAC                 bool
	DefaultRoute          bool
	DefaultRoute6         bool
	DisableBridgeOffloads bool
	Manual                networkModels.StandardSwitchManualAddresses
	VLANConfig            networkModels.StandardSwitchVLANConfig
}

type CreateStandardSwitchRequest struct {
	Name string
	StandardSwitchConfig
}

type UpdateStandardSwitchRequest struct {
	ID uint
	StandardSwitchConfig
}

type NetworkServiceInterface interface {
	SyncStandardSwitches() error
	GetStandardSwitches() ([]networkModels.StandardSwitch, error)
	NewStandardSwitch(request CreateStandardSwitchRequest) (uint, error)
	EditStandardSwitch(request UpdateStandardSwitchRequest) error
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
