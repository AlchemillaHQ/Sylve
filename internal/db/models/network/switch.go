// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import (
	"time"
)

type ManualSwitch struct {
	ID     uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Name   string `json:"name" gorm:"unique;not null"`
	Bridge string `json:"bridge" gorm:"unique;not null"`

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

	NetworkManual  string `json:"networkManual" gorm:"column:network_manual"`
	Network6Manual string `json:"network6Manual" gorm:"column:network6_manual"`
	GatewayManual  string `json:"gatewayManual" gorm:"column:gateway_manual"`
	Gateway6Manual string `json:"gateway6Manual" gorm:"column:gateway6_manual"`

	DisableIPv6  bool `json:"disableIPv6" gorm:"default:false"`
	Private      bool `json:"private" gorm:"default:false"`
	DefaultRoute bool `json:"defaultRoute" gorm:"default:false"`

	Ports []NetworkPort `json:"ports" gorm:"foreignKey:SwitchID;constraint:OnDelete:CASCADE"`

	DHCP  bool `json:"dhcp" gorm:"default:false"`
	SLAAC bool `json:"slaac" gorm:"default:false"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type NetworkPort struct {
	ID       int            `json:"id" gorm:"primaryKey;autoIncrement"`
	Name     string         `json:"name" gorm:"not null"`
	SwitchID uint           `json:"switchId" gorm:"not null"`
	Switch   StandardSwitch `gorm:"foreignKey:SwitchID"`
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

func (sw *StandardSwitch) Network(v int) string {
	if v == 4 {
		if sw.NetworkObj != nil && len(sw.NetworkObj.Entries) > 0 {
			return sw.NetworkObj.Entries[0].Value
		}
		return sw.NetworkManual
	}

	if v == 6 {
		if sw.Network6Obj != nil && len(sw.Network6Obj.Entries) > 0 {
			return sw.Network6Obj.Entries[0].Value
		}
		return sw.Network6Manual
	}

	return ""
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
