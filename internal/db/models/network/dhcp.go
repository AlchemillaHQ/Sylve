// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

type DHCPConfig struct {
	ID               uint             `json:"id" gorm:"primaryKey"`
	StandardSwitches []StandardSwitch `json:"standardSwitches" gorm:"many2many:dhcp_standard_switches;joinForeignKey:DHCPConfigID;joinReferences:StandardSwitchID;constraint:OnDelete:CASCADE"`
	ManualSwitches   []ManualSwitch   `json:"manualSwitches" gorm:"many2many:dhcp_manual_switches;joinForeignKey:DHCPConfigID;joinReferences:ManualSwitchID;constraint:OnDelete:CASCADE"`
	DNSServers       []string         `json:"dnsServers" gorm:"serializer:json;type:json"`
	Domain           string           `json:"domain"`
	ExpandHosts      bool             `json:"expandHosts" gorm:"default:true"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type DHCPRange struct {
	ID      uint   `json:"id" gorm:"primaryKey"`
	Type    string `json:"type" gorm:"not null;default:'ipv4'"`
	StartIP string `json:"startIp" gorm:"not null"`
	EndIP   string `json:"endIp" gorm:"not null"`

	StandardSwitchID *uint           `json:"standardSwitchId" gorm:"index"`
	StandardSwitch   *StandardSwitch `json:"standardSwitch" gorm:"foreignKey:StandardSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

	ManualSwitchID *uint         `json:"manualSwitchId" gorm:"index"`
	ManualSwitch   *ManualSwitch `json:"manualSwitch" gorm:"foreignKey:ManualSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

	Expiry uint `json:"expiry" gorm:"default:43200"`

	RAOnly bool `json:"raOnly" gorm:"default:false"`
	SLAAC  bool `json:"slaac" gorm:"default:false"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type DHCPStaticLease struct {
	ID       uint   `json:"id" gorm:"primaryKey"`
	Hostname string `json:"hostname" gorm:"not null"`
	Comments string `json:"comments"`
	Expiry   uint   `json:"expiry" gorm:"default:0"`

	// Per-range uniqueness on each object reference (Postgres & MySQL allow multiple NULLs)
	IPObjectID   *uint `json:"ipObjectId"   gorm:"index:uniq_l_ip_per_range,unique"`
	MACObjectID  *uint `json:"macObjectId"  gorm:"index:uniq_l_mac_per_range,unique"`
	DUIDObjectID *uint `json:"duidObjectId" gorm:"index:uniq_l_duid_per_range,unique"`

	IPObject   *Object `json:"ipObject"   gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	MACObject  *Object `json:"macObject"  gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	DUIDObject *Object `json:"duidObject" gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`

	DHCPRangeID uint       `json:"dhcpRangeId" gorm:"index:uniq_l_mac_per_range,unique;index:uniq_l_ip_per_range,unique;index:uniq_l_duid_per_range,unique"`
	DHCPRange   *DHCPRange `json:"dhcpRange"   gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

// type DHCPOption struct {
// 	ID       uint   `json:"id" gorm:"primaryKey"`
// 	Option   string `json:"option" gorm:"not null"`
// 	Value    string `json:"value" gorm:"not null"`
// 	Comments string `json:"comments"`

// 	StandardSwitchID *uint           `json:"switchId" gorm:"index"`
// 	StandardSwitch   *StandardSwitch `gorm:"foreignKey:StandardSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

// 	ManualSwitchID *uint         `json:"manualSwitchId" gorm:"index"`
// 	ManualSwitch   *ManualSwitch `gorm:"foreignKey:ManualSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

// 	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
// 	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
// }
