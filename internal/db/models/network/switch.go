// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import (
	"net/netip"
	"time"

	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

const (
	StandardSwitchMACModePort   = "port"
	StandardSwitchMACModeObject = "object"
)

// StandardSwitchMACSource is the source of a managed bridge's host
// MAC address. Exactly one of Port or MACObjectID is populated according to
// Mode. It is also used as the service-layer mutation input.
type StandardSwitchMACSource struct {
	Mode        string `json:"mode"`
	Port        string `json:"port,omitempty"`
	MACObjectID uint   `json:"macObjectId,omitempty"`
}

type StandardSwitchVLANConfig struct {
	Filtering         bool                             `json:"filtering"`
	DefaultAccessVLAN *int                             `json:"defaultAccessVlan"`
	HostVLAN          *int                             `json:"hostVlan"`
	PortPolicies      map[string]bridgevlan.PortPolicy `json:"portPolicies"`
}

type ManualSwitch struct {
	ID                 uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Name               string `json:"name" gorm:"unique;not null"`
	Bridge             string `json:"bridge" gorm:"unique;not null"`
	VLANFiltering      bool   `json:"vlanFiltering" gorm:"-"`
	DefaultAccessVLAN  *int   `json:"defaultAccessVlan" gorm:"-"`
	VLANStateAvailable bool   `json:"vlanStateAvailable" gorm:"-"`
	VLANStateError     string `json:"vlanStateError,omitempty" gorm:"-"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type StandardSwitch struct {
	ID         uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Name       string `json:"name" gorm:"unique;not null"`
	BridgeName string `json:"bridgeName" gorm:"unique;not null"`
	MTU        int    `json:"mtu" gorm:"default:1500"`
	VLAN       int    `json:"vlan" gorm:"default:0"`
	Address    string `json:"address"`
	Address6   string `json:"address6"`

	AddressID  *uint   `json:"addressId" gorm:"column:address_object_id"`
	AddressObj *Object `json:"addressObj" gorm:"foreignKey:AddressID"`

	Address6ID  *uint   `json:"address6Id" gorm:"column:address6_object_id"`
	Address6Obj *Object `json:"address6Obj" gorm:"foreignKey:Address6ID"`

	NetworkID  *uint   `json:"networkId" gorm:"column:network_object_id"`
	NetworkObj *Object `json:"networkObj" gorm:"foreignKey:NetworkID"`

	Network6ID  *uint   `json:"network6Id" gorm:"column:network6_object_id"`
	Network6Obj *Object `json:"network6Obj" gorm:"foreignKey:Network6ID"`

	GatewayAddressID  *uint   `json:"gatewayAddressId" gorm:"column:gateway_address_object_id"`
	GatewayAddressObj *Object `json:"gatewayAddressObj" gorm:"foreignKey:GatewayAddressID"`

	Gateway6AddressID  *uint   `json:"gateway6AddressId" gorm:"column:gateway6_address_object_id"`
	Gateway6AddressObj *Object `json:"gateway6AddressObj" gorm:"foreignKey:Gateway6AddressID"`

	BridgeMACMode       string  `json:"bridgeMacMode" gorm:"column:bridge_mac_mode"`
	BridgeMACSourcePort string  `json:"bridgeMacSourcePort" gorm:"column:bridge_mac_source_port"`
	BridgeMACObjectID   *uint   `json:"bridgeMacObjectId" gorm:"column:bridge_mac_object_id;index"`
	BridgeMACObject     *Object `json:"bridgeMacObject" gorm:"foreignKey:BridgeMACObjectID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`

	NetworkManual  string                  `json:"networkManual" gorm:"column:network_manual"`
	Network6Manual string                  `json:"network6Manual" gorm:"column:network6_manual"`
	GatewayManual  string                  `json:"gatewayManual" gorm:"column:gateway_manual"`
	Gateway6Manual string                  `json:"gateway6Manual" gorm:"column:gateway6_manual"`
	Addresses      []StandardSwitchAddress `json:"addresses" gorm:"foreignKey:SwitchID;constraint:OnDelete:CASCADE"`

	DisableIPv6 bool `json:"disableIPv6" gorm:"default:false"`
	Private     bool `json:"private" gorm:"default:false"`
	// DefaultRoute owns the IPv4 default route for static or DHCP addressing.
	DefaultRoute bool `json:"defaultRoute" gorm:"default:false"`
	// DefaultRoute6 owns the IPv6 default route for static or SLAAC addressing.
	DefaultRoute6 bool `json:"defaultRoute6" gorm:"default:false"`
	// DisableBridgeOffloads keeps FreeBSD from changing bridge-member
	// capabilities as transient tap and epair interfaces come and go.
	// Sorts flapping issues (Please tell @hayzam if you faced flapping before/after enabling this option).
	DisableBridgeOffloads bool `json:"disableBridgeOffloads" gorm:"default:false"`
	VLANFiltering         bool `json:"vlanFiltering" gorm:"default:false"`
	DefaultAccessVLAN     *int `json:"defaultAccessVlan"`
	HostVLAN              *int `json:"hostVlan"`

	Ports []NetworkPort `json:"ports" gorm:"foreignKey:SwitchID;constraint:OnDelete:CASCADE"`

	DHCP  bool `json:"dhcp" gorm:"default:false"`
	SLAAC bool `json:"slaac" gorm:"default:false"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type NetworkPort struct {
	ID         int                   `json:"id" gorm:"primaryKey;autoIncrement"`
	Name       string                `json:"name" gorm:"not null"`
	SwitchID   uint                  `json:"switchId" gorm:"not null"`
	Switch     StandardSwitch        `gorm:"foreignKey:SwitchID"`
	VLANPolicy bridgevlan.PortPolicy `json:"vlanPolicy" gorm:"embedded;embeddedPrefix:vlan_"`
}

// StandardSwitchManualAddresses carries raw, manually-typed address values for a
// standard switch as an alternative to selecting NetworkObjects. Each field is
// mutually exclusive with its corresponding object ID at the service layer.
type StandardSwitchManualAddresses struct {
	Network4 string
	Gateway4 string
	Network6 string
	Gateway6 string
}

// StandardSwitchAddressSource selects one Network object or a manually entered
// host CIDR. Exactly one of ObjectID and CIDR must be supplied.
type StandardSwitchAddressSource struct {
	ObjectID uint   `json:"objectId,omitempty"`
	CIDR     string `json:"cidr,omitempty"`
}

type StandardSwitchAddress struct {
	ID       uint    `json:"id" gorm:"primaryKey;autoIncrement"`
	SwitchID uint    `json:"switchId" gorm:"not null;index"`
	Family   int     `json:"family" gorm:"not null"`
	Ordering int     `json:"ordering"`
	ObjectID *uint   `json:"objectId" gorm:"index"`
	Object   *Object `json:"object" gorm:"foreignKey:ObjectID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	CIDR     string  `json:"cidr" gorm:"column:cidr"`
}

func (sw *StandardSwitch) AddressSources(v int) []StandardSwitchAddressSource {
	sources := make([]StandardSwitchAddressSource, 0)
	for _, address := range sw.Addresses {
		if address.Family != v {
			continue
		}
		source := StandardSwitchAddressSource{CIDR: address.CIDR}
		if address.ObjectID != nil {
			source.ObjectID = *address.ObjectID
		}
		sources = append(sources, source)
	}
	if len(sources) > 0 {
		return sources
	}
	var id *uint
	var manual string
	switch v {
	case 4:
		id, manual = sw.NetworkID, sw.NetworkManual
	case 6:
		id, manual = sw.Network6ID, sw.Network6Manual
	default:
		return sources
	}
	if id != nil && *id != 0 {
		return []StandardSwitchAddressSource{{ObjectID: *id}}
	}
	if manual != "" {
		sources = append(sources, StandardSwitchAddressSource{CIDR: manual})
	}
	return sources
}

// Network returns the primary CIDR used by the existing route configuration.
func (sw *StandardSwitch) Network(v int) string {
	if addresses := sw.Networks(v); len(addresses) > 0 {
		return addresses[0]
	}
	return ""
}

// Networks resolves all static host CIDRs, preserving the primary address and
// removing duplicates between selected objects and manually configured addresses.
func (sw *StandardSwitch) Networks(v int) []string {
	var object *Object
	var manual string
	switch v {
	case 4:
		object, manual = sw.NetworkObj, sw.NetworkManual
	case 6:
		object, manual = sw.Network6Obj, sw.Network6Manual
	default:
		return nil
	}

	values := make([]string, 0)
	configured := false
	for _, address := range sw.Addresses {
		if address.Family != v {
			continue
		}
		configured = true
		if address.ObjectID != nil {
			if address.Object != nil {
				for _, entry := range address.Object.Entries {
					values = append(values, entry.Value)
				}
			}
		} else {
			values = append(values, address.CIDR)
		}
	}
	if !configured && object != nil && len(object.Entries) > 0 {
		for _, entry := range object.Entries {
			values = append(values, entry.Value)
		}
	} else if !configured && manual != "" {
		values = append(values, manual)
	}
	addresses := values[:0]
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			value = prefix.String()
		}
		if !seen[value] {
			seen[value] = true
			addresses = append(addresses, value)
		}
	}
	return addresses
}

func (sw *StandardSwitch) Gateway(v int) string {
	if v == 4 {
		if sw.GatewayAddressObj != nil && len(sw.GatewayAddressObj.Entries) > 0 {
			return sw.GatewayAddressObj.Entries[0].Value
		}
		return sw.GatewayManual
	}

	if v == 6 {
		if sw.Gateway6AddressObj != nil && len(sw.Gateway6AddressObj.Entries) > 0 {
			return sw.Gateway6AddressObj.Entries[0].Value
		}
		return sw.Gateway6Manual
	}

	return ""
}

func (sw *StandardSwitch) IPv4() string {
	if sw.AddressObj != nil && len(sw.AddressObj.Entries) > 0 {
		return sw.AddressObj.Entries[0].Value
	}
	return ""
}

func (sw *StandardSwitch) IPv6() string {
	if sw.Address6Obj != nil && len(sw.Address6Obj.Entries) > 0 {
		return sw.Address6Obj.Entries[0].Value
	}
	return ""
}
