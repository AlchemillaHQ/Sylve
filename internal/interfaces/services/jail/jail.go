// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailServiceInterfaces

import (
	"context"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
)

type HookPhase struct {
	Enabled bool   `json:"enabled"`
	Script  string `json:"script"`
}

type Hooks struct {
	Prestart  HookPhase `json:"prestart"`
	Start     HookPhase `json:"start"`
	Poststart HookPhase `json:"poststart"`
	Prestop   HookPhase `json:"prestop"`
	Stop      HookPhase `json:"stop"`
	Poststop  HookPhase `json:"poststop"`
}

type JailCreationState struct {
	CTID        uint
	DatasetName string
	JailDir     string
}

type CreateJailRequest struct {
	Name        string `json:"name" binding:"required"`
	CTID        *uint  `json:"ctId" binding:"required"`
	Hostname    string `json:"hostname"`
	Description string `json:"description"`

	Pool          string `json:"pool" binding:"required"`
	Base          string `json:"base"`
	BootstrapName string `json:"bootstrapName"`
	Fstab         string `json:"fstab"`
	ResolvConf    string `json:"resolvConf"`

	SwitchName string `json:"switchName"`

	InheritIPv4 *bool `json:"inheritIPv4"`
	InheritIPv6 *bool `json:"inheritIPv6"`

	DHCP  *bool `json:"dhcp"`
	SLAAC *bool `json:"slaac"`

	IPv4      *int   `json:"ipv4"`
	IPv4Gw    *int   `json:"ipv4Gw"`
	IPv4Raw   string `json:"ipv4Raw"`
	IPv4GwRaw string `json:"ipv4GwRaw"`

	IPv6      *int   `json:"ipv6"`
	IPv6Gw    *int   `json:"ipv6Gw"`
	IPv6Raw   string `json:"ipv6Raw"`
	IPv6GwRaw string `json:"ipv6GwRaw"`

	MAC    *int   `json:"mac"`
	MACRaw string `json:"macRaw"`

	VLAN *int `json:"vlan"`

	ResourceLimits *bool  `json:"resourceLimits"`
	Cores          *int   `json:"cores"`
	Memory         *int   `json:"memory"`
	StartAtBoot    *bool  `json:"startAtBoot"`
	StartOrder     int    `json:"startOrder"`
	DevFSRuleset   string `json:"devfsRuleset"`

	Type              jailModels.JailType `json:"type" binding:"required"`
	AllowedOptions    []string            `json:"allowedOptions"`
	CleanEnvironment  *bool               `json:"cleanEnvironment"`
	AdditionalOptions string              `json:"additionalOptions"`
	Hooks             Hooks               `json:"hooks"`

	MetadataMeta string `json:"metadataMeta"`
	MetadataEnv  string `json:"metadataEnv"`
}

type SimpleList struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	CTID           uint   `json:"ctId"`
	State          string `json:"state"`
	ResourceLimits *bool  `json:"resourceLimits"`
	Cores          int    `json:"cores"`
	Memory         int    `json:"memory"`
}

type SimpleTemplateList struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	SourceJailName string `json:"sourceJailName"`
}

type State struct {
	CTID              uint    `json:"ctId"`
	State             string  `json:"state"`
	PCPU              float64 `json:"pcpu"`
	Memory            int64   `json:"memory"`
	PendingAction     string  `json:"pendingAction,omitempty"`
	OverrideRequested bool    `json:"overrideRequested"`
}

type AddJailNetworkRequest struct {
	CTID           uint   `json:"ctId" binding:"required"`
	Name           string `json:"name" binding:"required"`
	SwitchName     string `json:"switchName" binding:"required"`
	MacID          *uint  `json:"macId"`
	MACRaw         string `json:"macRaw"`
	IP4            *uint  `json:"ip4"`
	IP4Raw         string `json:"ip4Raw"`
	IP4GW          *uint  `json:"ip4gw"`
	IP4GwRaw       string `json:"ip4gwRaw"`
	IP6            *uint  `json:"ip6"`
	IP6Raw         string `json:"ip6Raw"`
	IP6GW          *uint  `json:"ip6gw"`
	IP6GwRaw       string `json:"ip6gwRaw"`
	DHCP           *bool  `json:"dhcp"`
	SLAAC          *bool  `json:"slaac"`
	DefaultGateway *bool  `json:"defaultGateway"`
	VLAN           *int   `json:"vlan"`
}

type EditJailNetworkRequest struct {
	NetworkID      uint   `json:"networkId" binding:"required"`
	Name           string `json:"name" binding:"required"`
	SwitchName     string `json:"switchName" binding:"required"`
	MacID          *uint  `json:"macId"`
	MACRaw         string `json:"macRaw"`
	IP4            *uint  `json:"ip4"`
	IP4Raw         string `json:"ip4Raw"`
	IP4GW          *uint  `json:"ip4gw"`
	IP4GwRaw       string `json:"ip4gwRaw"`
	IP6            *uint  `json:"ip6"`
	IP6Raw         string `json:"ip6Raw"`
	IP6GW          *uint  `json:"ip6gw"`
	IP6GwRaw       string `json:"ip6gwRaw"`
	DHCP           *bool  `json:"dhcp"`
	SLAAC          *bool  `json:"slaac"`
	DefaultGateway *bool  `json:"defaultGateway"`
	VLAN           *int   `json:"vlan"`
}

type DeleteJailResult struct {
	Warnings         []string `json:"warnings"`
	RetainedDatasets []string `json:"retainedDatasets"`
}

type JailServiceInterface interface {
	JailAction(ctid int, action string) error
	ForceStopJail(ctID uint) error
	IsJailRunning(ctid uint) (bool, error)
	GetJailCTIDFromDataset(dataset string) (uint, error)
	DeleteJail(ctx context.Context, ctId uint, deleteMacs bool, deleteRootFS bool) error
	DeleteJailWithWarnings(ctx context.Context, ctId uint, deleteMacs bool, deleteRootFS bool) (DeleteJailResult, error)
	RetireJailLocalMetadata(ctx context.Context, ctId uint, deleteMacs bool) error
	StartStatsMonitoring(ctx context.Context)

	StoreJailUsage() error
	PruneOrphanedJailStats() error

	ListBootstraps(ctx context.Context, pool string) ([]BootstrapEntry, error)
	CreateBootstrap(ctx context.Context, req BootstrapRequest) error
	DeleteBootstrap(ctx context.Context, pool, name string) error
}
