// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

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

type StandardSwitchMACSourceRequest struct {
	Mode        string `json:"mode"`
	Port        string `json:"port,omitempty"`
	MACObjectID uint   `json:"macObjectId,omitempty"`
}

type StandardSwitchCreateRequest struct {
	Name                  string                           `json:"name"`
	MTU                   int                              `json:"mtu"`
	VLAN                  int                              `json:"vlan"`
	Network4              uint                             `json:"network4"`
	Gateway4              uint                             `json:"gateway4"`
	Network6              uint                             `json:"network6"`
	Gateway6              uint                             `json:"gateway6"`
	Network4Manual        string                           `json:"network4Manual"`
	Gateway4Manual        string                           `json:"gateway4Manual"`
	Network6Manual        string                           `json:"network6Manual"`
	Gateway6Manual        string                           `json:"gateway6Manual"`
	DisableIPv6           bool                             `json:"disableIPv6"`
	SLAAC                 bool                             `json:"slaac"`
	Private               bool                             `json:"private"`
	DefaultRoute          bool                             `json:"defaultRoute"`
	DefaultRoute6         bool                             `json:"defaultRoute6"`
	DisableBridgeOffloads bool                             `json:"disableBridgeOffloads"`
	DHCP                  bool                             `json:"dhcp"`
	Ports                 []string                         `json:"ports"`
	BridgeMAC             StandardSwitchMACSourceRequest   `json:"bridgeMac"`
	VLANFiltering         bool                             `json:"vlanFiltering"`
	DefaultAccessVLAN     *int                             `json:"defaultAccessVlan"`
	HostVLAN              *int                             `json:"hostVlan"`
	PortPolicies          map[string]bridgevlan.PortPolicy `json:"portPolicies"`
}

func StandardSwitchServiceRequest(request StandardSwitchCreateRequest) networkServiceInterfaces.CreateStandardSwitchRequest {
	return networkServiceInterfaces.CreateStandardSwitchRequest{
		Name: request.Name,
		StandardSwitchConfig: networkServiceInterfaces.StandardSwitchConfig{
			MTU:        request.MTU,
			VLAN:       request.VLAN,
			Network4ID: request.Network4,
			Network6ID: request.Network6,
			Gateway4ID: request.Gateway4,
			Gateway6ID: request.Gateway6,
			Ports:      request.Ports,
			MACSource: networkModels.StandardSwitchMACSource{
				Mode:        request.BridgeMAC.Mode,
				Port:        request.BridgeMAC.Port,
				MACObjectID: request.BridgeMAC.MACObjectID,
			},
			Private:               request.Private,
			DHCP:                  request.DHCP,
			DisableIPv6:           request.DisableIPv6,
			SLAAC:                 request.SLAAC,
			DefaultRoute:          request.DefaultRoute,
			DefaultRoute6:         request.DefaultRoute6,
			DisableBridgeOffloads: request.DisableBridgeOffloads,
			Manual: networkModels.StandardSwitchManualAddresses{
				Network4: request.Network4Manual,
				Gateway4: request.Gateway4Manual,
				Network6: request.Network6Manual,
				Gateway6: request.Gateway6Manual,
			},
			VLANConfig: networkModels.StandardSwitchVLANConfig{
				Filtering:         request.VLANFiltering,
				DefaultAccessVLAN: request.DefaultAccessVLAN,
				HostVLAN:          request.HostVLAN,
				PortPolicies:      request.PortPolicies,
			},
		},
	}
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
	ID                    uint                              `json:"id"`
	MTU                   *int                              `json:"mtu,omitempty"`
	VLAN                  *int                              `json:"vlan,omitempty"`
	Network4              *uint                             `json:"network4,omitempty"`
	Gateway4              *uint                             `json:"gateway4,omitempty"`
	Network6              *uint                             `json:"network6,omitempty"`
	Gateway6              *uint                             `json:"gateway6,omitempty"`
	Network4Manual        *string                           `json:"network4Manual,omitempty"`
	Gateway4Manual        *string                           `json:"gateway4Manual,omitempty"`
	Network6Manual        *string                           `json:"network6Manual,omitempty"`
	Gateway6Manual        *string                           `json:"gateway6Manual,omitempty"`
	DisableIPv6           *bool                             `json:"disableIPv6,omitempty"`
	SLAAC                 *bool                             `json:"slaac,omitempty"`
	Private               *bool                             `json:"private,omitempty"`
	DefaultRoute          *bool                             `json:"defaultRoute,omitempty"`
	DefaultRoute6         *bool                             `json:"defaultRoute6,omitempty"`
	DisableBridgeOffloads *bool                             `json:"disableBridgeOffloads,omitempty"`
	DHCP                  *bool                             `json:"dhcp,omitempty"`
	Ports                 *[]string                         `json:"ports,omitempty"`
	BridgeMAC             *StandardSwitchMACSourceRequest   `json:"bridgeMac,omitempty"`
	VLANFiltering         *bool                             `json:"vlanFiltering,omitempty"`
	DefaultAccessVLAN     *int                              `json:"defaultAccessVlan,omitempty"`
	HostVLAN              *int                              `json:"hostVlan,omitempty"`
	PortPolicies          *map[string]bridgevlan.PortPolicy `json:"portPolicies,omitempty"`
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
