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
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

var (
	ErrEpairOwnershipConflict = errors.New("epair ownership conflict")
	ErrEpairStateConflict     = errors.New("epair state conflict")
)

const (
	HostInterfaceL3ConflictMissingInterface   = "host_interface_l3_missing_interface"
	HostInterfaceL3ConflictIdentityMismatch   = "host_interface_l3_identity_mismatch"
	HostInterfaceL3ConflictStandardSwitchPort = "host_interface_l3_standard_switch_port"
	HostInterfaceL3ConflictBridgeMember       = "host_interface_l3_bridge_member"
	HostInterfaceL3ConflictVLANParentMissing  = "host_interface_l3_vlan_parent_missing"
	HostInterfaceL3ConflictParentHasHostIP    = "host_interface_l3_parent_has_host_ip"
	HostInterfaceL3ConflictParentHasChildren  = "host_interface_l3_parent_has_vlan_children"
	HostInterfaceL3ConflictPrefixOwnerChanged = "host_interface_l3_prefix_owner_changed"
	HostInterfaceL3ConflictVLANIdentity       = "host_interface_l3_vlan_identity_mismatch"
	HostInterfaceL3ConflictPending            = "host_interface_l3_pending_conflict"
)

type HostInterfaceL3Entry struct {
	Interface      string `json:"interface"`
	VLANParent     string `json:"vlanParent"`
	VLANTag        uint16 `json:"vlanTag"`
	IPv6Mode       string `json:"ipv6Mode"`
	MTU            *uint  `json:"mtu"`
	MTUBaseline    *uint  `json:"mtuBaseline"`
	Metric         *uint  `json:"metric"`
	MetricBaseline *uint  `json:"metricBaseline"`
	IdentityMAC    string `json:"identityMac"`
	Revision       uint64 `json:"revision"`

	Addresses        []HostInterfaceL3AddressEntry                 `json:"addresses"`
	ManagedAddresses []networkModels.HostInterfaceL3AppliedAddress `json:"managedAddresses"`

	Present   bool     `json:"present"`
	LiveMAC   string   `json:"liveMac"`
	Conflicts []string `json:"conflicts"`
}

type HostInterfaceL3AddressEntry struct {
	Family       string `json:"family"`
	Address      string `json:"address"`
	PrefixLength uint8  `json:"prefixLength"`
}

type HostInterfaceL3TargetEntry struct {
	Interface string `json:"interface"`
	Eligible  bool   `json:"eligible"`
	Reason    string `json:"reason"`
}

type HostInterfaceL3List struct {
	Rows    []HostInterfaceL3Entry       `json:"rows"`
	Targets []HostInterfaceL3TargetEntry `json:"targets"`
}

type HostInterfaceL3AddressInput struct {
	Address string
}

type HostInterfaceL3UpdateRequest struct {
	IPv6Mode         *string
	MTU              *uint
	Metric           *uint
	Addresses        []HostInterfaceL3AddressInput
	ExpectedRevision uint64
}

type HostInterfaceL3PendingEntry struct {
	ID        string    `json:"id"`
	Interface string    `json:"interface"`
	Kind      string    `json:"kind"`
	Phase     string    `json:"phase"`
	Deadline  time.Time `json:"deadline"`
}

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
	ID                       uint
	ConfirmHostLayer3Removal bool
	PreserveVLANConfig       bool
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

type StartupNetworkServiceInterface interface {
	NetworkServiceInterface
	RecoverHostInterfaceL3() error
	ReconcileHostInterfaceL3() error
	StartHostInterfaceL3Sweeper(ctx context.Context)
}
