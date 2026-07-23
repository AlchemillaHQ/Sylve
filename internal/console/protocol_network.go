// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

const (
	OperationSwitchList   = "switches.list"
	OperationSwitchCreate = "switches.create"
	OperationSwitchDelete = "switches.delete"
	OperationSwitchEdit   = "switches.edit"
	OperationObjectList   = "objects.list"
	OperationObjectCreate = "objects.create"
	OperationObjectEdit   = "objects.edit"
	OperationObjectDelete = "objects.delete"
)

type SwitchListPayload struct {
	JSON bool `json:"json"`
}

type StandardSwitchCreateRequest struct {
	Name           string   `json:"name"`
	MTU            int      `json:"mtu"`
	VLAN           int      `json:"vlan"`
	Network4       uint     `json:"network4"`
	Gateway4       uint     `json:"gateway4"`
	Network6       uint     `json:"network6"`
	Gateway6       uint     `json:"gateway6"`
	Network4Manual string   `json:"network4Manual"`
	Gateway4Manual string   `json:"gateway4Manual"`
	Network6Manual string   `json:"network6Manual"`
	Gateway6Manual string   `json:"gateway6Manual"`
	DisableIPv6    bool     `json:"disableIPv6"`
	SLAAC          bool     `json:"slaac"`
	Private        bool     `json:"private"`
	DefaultRoute   bool     `json:"defaultRoute"`
	DHCP           bool     `json:"dhcp"`
	Ports          []string `json:"ports"`
}

type ManualSwitchCreateRequest struct {
	Name   string `json:"name"`
	Bridge string `json:"bridge"`
}

type SwitchCreatePayload struct {
	Type     string                       `json:"type"`
	Standard *StandardSwitchCreateRequest `json:"standard,omitempty"`
	Manual   *ManualSwitchCreateRequest   `json:"manual,omitempty"`
	JSON     bool                         `json:"json"`
}

type SwitchDeletePayload struct {
	Type string `json:"type"`
	ID   uint   `json:"id"`
	JSON bool   `json:"json"`
}

type StandardSwitchEditRequest struct {
	ID             uint      `json:"id"`
	MTU            *int      `json:"mtu,omitempty"`
	VLAN           *int      `json:"vlan,omitempty"`
	Network4       *uint     `json:"network4,omitempty"`
	Gateway4       *uint     `json:"gateway4,omitempty"`
	Network6       *uint     `json:"network6,omitempty"`
	Gateway6       *uint     `json:"gateway6,omitempty"`
	Network4Manual *string   `json:"network4Manual,omitempty"`
	Gateway4Manual *string   `json:"gateway4Manual,omitempty"`
	Network6Manual *string   `json:"network6Manual,omitempty"`
	Gateway6Manual *string   `json:"gateway6Manual,omitempty"`
	DisableIPv6    *bool     `json:"disableIPv6,omitempty"`
	SLAAC          *bool     `json:"slaac,omitempty"`
	Private        *bool     `json:"private,omitempty"`
	DefaultRoute   *bool     `json:"defaultRoute,omitempty"`
	DHCP           *bool     `json:"dhcp,omitempty"`
	Ports          *[]string `json:"ports,omitempty"`
}

type ManualSwitchEditRequest struct {
	ID     uint    `json:"id"`
	Name   *string `json:"name,omitempty"`
	Bridge *string `json:"bridge,omitempty"`
}

type SwitchEditPayload struct {
	Type     string                     `json:"type"`
	Standard *StandardSwitchEditRequest `json:"standard,omitempty"`
	Manual   *ManualSwitchEditRequest   `json:"manual,omitempty"`
	JSON     bool                       `json:"json"`
}

type NetworkObjectRequest struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

type NetworkObjectEditRequest struct {
	Name   *string   `json:"name,omitempty"`
	Type   *string   `json:"type,omitempty"`
	Values *[]string `json:"values,omitempty"`
}

type ObjectListPayload struct {
	Type string `json:"type,omitempty"`
	JSON bool   `json:"json"`
}

type ObjectCreatePayload struct {
	Request NetworkObjectRequest `json:"request"`
	JSON    bool                 `json:"json"`
}

type ObjectEditPayload struct {
	ID      uint                     `json:"id"`
	Request NetworkObjectEditRequest `json:"request"`
	JSON    bool                     `json:"json"`
}

type ObjectDeletePayload struct {
	ID   uint `json:"id"`
	JSON bool `json:"json"`
}
