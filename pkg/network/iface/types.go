// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iface

import "net"

type IPv4 struct {
	IP        net.IP `json:"ip"`
	Netmask   string `json:"netmask"`
	Broadcast net.IP `json:"broadcast"`
}

type IPv6 struct {
	IP           net.IP `json:"ip"`
	PrefixLength int    `json:"prefixLength"`
	ScopeID      uint32 `json:"scopeId"`
	AutoConf     bool   `json:"autoConf"`
	Detached     bool   `json:"detached"`
	Deprecated   bool   `json:"deprecated"`
	LifeTimes    struct {
		Preferred uint32 `json:"preferred"`
		Valid     uint32 `json:"valid"`
	} `json:"lifeTimes"`
}

type Flags struct {
	Raw  uint32   `json:"raw"`
	Desc []string `json:"desc"`
}

type Capabilities struct {
	Enabled   Flags `json:"enabled"`
	Supported Flags `json:"supported"`
}

type Media struct {
	Type       string   `json:"type"`
	Subtype    string   `json:"subtype"`
	Options    []string `json:"options"`
	Mode       string   `json:"mode"`
	RawCurrent int      `json:"rawCurrent"`
	RawActive  int      `json:"rawActive"`
	Status     string   `json:"status"`
}

type ND6 struct {
	Flags
}

type STP struct {
	Priority     int    `json:"priority"`
	HelloTime    int    `json:"hellotime"`
	FwdDelay     int    `json:"fwddelay"`
	MaxAge       int    `json:"maxage"`
	HoldCnt      int    `json:"holdcnt"`
	Proto        string `json:"proto"`
	RootID       string `json:"rootId"`
	RootPriority int    `json:"rootPriority"`
	RootPathCost int    `json:"ifcost"`
	RootPort     int    `json:"port"`
}

type BridgeMember struct {
	Name      string `json:"name"`
	Flags     Flags  `json:"flags"`
	IfMaxAddr int    `json:"ifmaxaddr"`
	State     int    `json:"state"`
	Priority  int    `json:"priority"`
	Port      int    `json:"port"`
	PathCost  int    `json:"pathCost"`
}

type Interface struct {
	Name          string         `json:"name"`
	Ether         string         `json:"ether"`
	HWAddr        string         `json:"hwaddr"`
	Flags         Flags          `json:"flags"`
	MTU           int            `json:"mtu"`
	Metric        int            `json:"metric"`
	Capabilities  Capabilities   `json:"capabilities"`
	Driver        string         `json:"driver"`
	Model         string         `json:"model"`
	Description   string         `json:"description"`
	BridgeID      string         `json:"bridgeId"`
	STP           *STP           `json:"stp"`
	MaxAddr       int            `json:"maxaddr"`
	Timeout       int            `json:"timeout"`
	BridgeMembers []BridgeMember `json:"bridgeMembers"`
	Groups        []string       `json:"groups"`

	IPv4 []IPv4 `json:"ipv4"`
	IPv6 []IPv6 `json:"ipv6"`

	Media *Media `json:"media"`

	ND6 ND6 `json:"nd6"`
}

type FlagDescriptor struct {
	Mask uint32
	Name string
}
