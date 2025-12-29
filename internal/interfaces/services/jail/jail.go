// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailServiceInterfaces

import jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"

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

	Pool  string `json:"pool" binding:"required"`
	Base  string `json:"base"`
	Fstab string `json:"fstab"`

	SwitchName string `json:"switchName"`

	InheritIPv4 *bool `json:"inheritIPv4"`
	InheritIPv6 *bool `json:"inheritIPv6"`

	DHCP  *bool `json:"dhcp"`
	SLAAC *bool `json:"slaac"`

	IPv4   *int `json:"ipv4"`
	IPv4Gw *int `json:"ipv4Gw"`

	IPv6   *int `json:"ipv6"`
	IPv6Gw *int `json:"ipv6Gw"`

	MAC *int `json:"mac"`

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
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	CTID  uint   `json:"ctId"`
	State string `json:"state"`
}

type State struct {
	CTID   uint    `json:"ctId"`
	State  string  `json:"state"`
	PCPU   float64 `json:"pcpu"`
	Memory int64   `json:"memory"`
}

type AddJailNetworkRequest struct {
	CTID           uint   `json:"ctId" binding:"required"`
	Name           string `json:"name" binding:"required"`
	SwitchName     string `json:"switchName" binding:"required"`
	MacID          *uint  `json:"macId"`
	IP4            *uint  `json:"ip4"`
	IP4GW          *uint  `json:"ip4gw"`
	IP6            *uint  `json:"ip6"`
	IP6GW          *uint  `json:"ip6gw"`
	DHCP           *bool  `json:"dhcp"`
	SLAAC          *bool  `json:"slaac"`
	DefaultGateway *bool  `json:"defaultGateway"`
}

type EditJailNetworkRequest struct {
	NetworkID      uint   `json:"networkId" binding:"required"`
	Name           string `json:"name" binding:"required"`
	SwitchName     string `json:"switchName" binding:"required"`
	MacID          *uint  `json:"macId"`
	IP4            *uint  `json:"ip4"`
	IP4GW          *uint  `json:"ip4gw"`
	IP6            *uint  `json:"ip6"`
	IP6GW          *uint  `json:"ip6gw"`
	DHCP           *bool  `json:"dhcp"`
	SLAAC          *bool  `json:"slaac"`
	DefaultGateway *bool  `json:"defaultGateway"`
}

type JailServiceInterface interface {
	StoreJailUsage() error
	PruneOrphanedJailStats() error
	WatchNetworkObjectChanges() error
}
