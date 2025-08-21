// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailModels

import (
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

func (Network) TableName() string {
	return "jail_networks"
}

type Network struct {
	ID uint `gorm:"primaryKey" json:"id"`

	SwitchID uint                         `json:"switchId" gorm:"not null;index"`
	Switch   networkModels.StandardSwitch `gorm:"foreignKey:SwitchID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`

	MacID         *uint                 `json:"macId" gorm:"column:mac_id"`
	MacAddressObj *networkModels.Object `json:"macObj" gorm:"foreignKey:MacID"`

	IPv4ID    *uint                 `json:"ipv4Id" gorm:"column:ipv4_id"`
	IPv4Obj   *networkModels.Object `json:"ipv4Obj" gorm:"foreignKey:IPv4ID"`
	IPv4GwID  *uint                 `json:"ipv4GwId" gorm:"column:ipv4_gw_id"`
	IPv4GwObj *networkModels.Object `json:"ipv4GwObj" gorm:"foreignKey:IPv4GwID"`

	IPv6ID    *uint                 `json:"ipv6Id" gorm:"column:ipv6_id"`
	IPv6Obj   *networkModels.Object `json:"ipv6Obj" gorm:"foreignKey:IPv6ID"`
	IPv6GwID  *uint                 `json:"ipv6GwId" gorm:"column:ipv6_gw_id"`
	IPv6GwObj *networkModels.Object `json:"ipv6GwObj" gorm:"foreignKey:IPv6GwID"`

	DHCP  bool `json:"dhcp" gorm:"default:false"`
	SLAAC bool `json:"slaac" gorm:"default:false"`

	CTID uint `json:"ctId" gorm:"index"`
}

type JailStats struct {
	ID          uint    `json:"id" gorm:"primaryKey"`
	CTID        int     `json:"ctId"`
	CPUUsage    float64 `json:"cpuUsage"`
	MemoryUsage float64 `json:"memoryUsage"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

type Jail struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	CTID        int    `json:"ctId" gorm:"unique;not null;uniqueIndex"`
	Name        string `json:"name" gorm:"not null;unique"`
	Description string `json:"description"`
	Dataset     string `json:"dataset"`
	Base        string `json:"base"`
	StartAtBoot *bool  `json:"startAtBoot" gorm:"default:false"`
	StartOrder  int    `json:"startOrder"`

	InheritIPv4 bool `json:"inheritIPv4"`
	InheritIPv6 bool `json:"inheritIPv6"`

	ResourceLimits *bool `json:"resourceLimits" gorm:"default:true"`
	Cores          int   `json:"cores"`
	CPUSet         []int `json:"cpuSet" gorm:"serializer:json;type:json"`
	Memory         int   `json:"memory"`

	Networks []Network   `json:"networks" gorm:"foreignKey:CTID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Stats    []JailStats `json:"-" gorm:"foreignKey:CTID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`

	StartLogs string     `json:"startLogs" gorm:"default:''"`
	StopLogs  string     `json:"stopLogs" gorm:"default:''"`
	StartedAt *time.Time `json:"startedAt" gorm:"default:null"`
	StoppedAt *time.Time `json:"stoppedAt" gorm:"default:null"`
}
