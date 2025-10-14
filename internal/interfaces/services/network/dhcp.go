// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import networkModels "github.com/alchemillahq/sylve/internal/db/models/network"

type ModifyDHCPConfigRequest struct {
	StandardSwitches []uint   `json:"standardSwitches"`
	ManualSwitches   []uint   `json:"manualSwitches"`
	DNSServers       []string `json:"dnsServers"`
	Domain           string   `json:"domain"`
	ExpandHosts      *bool    `json:"expandHosts"`
}

type CreateDHCPRangeRequest struct {
	Type           string `json:"type" binding:"required,oneof=ipv4 ipv6"`
	StartIP        string `json:"startIp"`
	EndIP          string `json:"endIp"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
	RAOnly         *bool  `json:"raOnly"`
	SLAAC          *bool  `json:"slaac"`
}

type ModifyDHCPRangeRequest struct {
	ID             uint   `json:"id"`
	Type           string `json:"type" binding:"required,oneof=ipv4 ipv6"`
	StartIP        string `json:"startIp"`
	EndIP          string `json:"endIp"`
	StandardSwitch *uint  `json:"standardSwitch"`
	ManualSwitch   *uint  `json:"manualSwitch"`
	Expiry         *uint  `json:"expiry"`
	RAOnly         *bool  `json:"raOnly"`
	SLAAC          *bool  `json:"slaac"`
}

type FileLeases struct {
	Expiry   uint64 `json:"expiry"`
	MAC      string `json:"mac"`
	IAID     string `json:"iaid"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	ClientID string `json:"clientId"`
	DUID     string `json:"duid"`
}

type Leases struct {
	File []FileLeases                    `json:"file"`
	DB   []networkModels.DHCPStaticLease `json:"db"`
}

type CreateStaticMapRequest struct {
	Hostname     string `json:"hostname"`
	Comments     string `json:"comments"`
	IPObjectID   *uint  `json:"ipId"`
	MACObjectID  *uint  `json:"macId"`
	DUIDObjectID *uint  `json:"duidId"`
	DHCPRangeID  uint   `json:"dhcpRangeId" binding:"required"`
}

type ModifyStaticMapRequest struct {
	ID           uint   `json:"id" binding:"required"`
	Hostname     string `json:"hostname"`
	IPObjectID   *uint  `json:"ipId"`
	MACObjectID  *uint  `json:"macId"`
	DUIDObjectID *uint  `json:"duidId"`
	DHCPRangeID  uint   `json:"dhcpRangeId" binding:"required"`
	Comments     string `json:"comments"`
}
