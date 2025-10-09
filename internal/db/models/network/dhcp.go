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

type DHCPRanges struct {
	ID      uint   `json:"id" gorm:"primaryKey"`
	StartIP string `json:"startIp" gorm:"not null"`
	EndIP   string `json:"endIp" gorm:"not null"`

	StandardSwitchID *uint           `json:"switchId" gorm:"index"`
	StandardSwitch   *StandardSwitch `json:"standardSwitch" gorm:"foreignKey:StandardSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

	ManualSwitchID *uint         `json:"manualSwitchId" gorm:"index"`
	ManualSwitch   *ManualSwitch `json:"manualSwitch" gorm:"foreignKey:ManualSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

	Expiry uint `json:"expiry" gorm:"default:43200"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

// type DHCPStaticMapping struct {
// 	ID       uint   `json:"id" gorm:"primaryKey"`
// 	Hostname string `json:"hostname" gorm:"not null"`
// 	MAC      string `json:"mac" gorm:"not null;uniqueIndex:idx_mac_switch"`
// 	IP       string `json:"ip" gorm:"not null;index:idx_ip_switch,unique"`
// 	Comments string `json:"comments"`
// 	Expiry   int    `json:"expiry" gorm:"default:0"`

// 	StandardSwitchID *uint           `json:"switchId" gorm:"index:idx_mac_switch;index:idx_ip_switch,unique"`
// 	StandardSwitch   *StandardSwitch `gorm:"foreignKey:StandardSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

// 	ManualSwitchID *uint         `json:"manualSwitchId" gorm:"index:idx_mac_switch;index:idx_ip_switch,unique"`
// 	ManualSwitch   *ManualSwitch `gorm:"foreignKey:ManualSwitchID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`

// 	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
// 	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
// }

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
