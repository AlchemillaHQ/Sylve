// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailModels

import (
	"fmt"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"gorm.io/gorm"
)

func (Network) TableName() string {
	return "jail_networks"
}

type Network struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	JailID uint `json:"jid" gorm:"column:jid;index"`

	SwitchID   uint   `json:"switchId" gorm:"not null;index"`
	SwitchType string `json:"switchType" gorm:"index;not null;default:standard"`

	StandardSwitch *networkModels.StandardSwitch `gorm:"-" json:"-"`
	ManualSwitch   *networkModels.ManualSwitch   `gorm:"-" json:"-"`

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
}

func (n *Network) AfterFind(tx *gorm.DB) error {
	switch n.SwitchType {
	case "standard":
		var s networkModels.StandardSwitch
		if err := tx.First(&s, n.SwitchID).Error; err != nil {
			return fmt.Errorf("load standard switch %d: %w", n.SwitchID, err)
		}
		n.StandardSwitch = &s
	case "manual":
		var m networkModels.ManualSwitch
		if err := tx.First(&m, n.SwitchID).Error; err != nil {
			return fmt.Errorf("load manual switch %d: %w", n.SwitchID, err)
		}
		n.ManualSwitch = &m
	default:
		return fmt.Errorf("unknown switch type: %s", n.SwitchType)
	}
	return nil
}

type JailStats struct {
	ID          uint    `json:"id" gorm:"primaryKey"`
	JailID      uint    `json:"jid" gorm:"column:jid;index"`
	CPUUsage    float64 `json:"cpuUsage"`
	MemoryUsage float64 `json:"memoryUsage"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

type Storage struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	JailID uint   `json:"jid" gorm:"column:jid;index"`
	Pool   string `json:"pool" gorm:"not null"`
	GUID   string `json:"guid" gorm:"uniqueIndex;not null"`
	Name   string `json:"name"`
	IsBase bool   `json:"isBase" gorm:"default:false"`
}

func (Storage) TableName() string {
	return "jail_storages"
}

type JailHookPhase string

const (
	JailHookPhasePreStart  JailHookPhase = "prestart"
	JailHookPhaseStart     JailHookPhase = "start"
	JailHookPhasePostStart JailHookPhase = "poststart"
	JailHookPhasePreStop   JailHookPhase = "prestop"
	JailHookPhaseStop      JailHookPhase = "stop"
	JailHookPhasePostStop  JailHookPhase = "poststop"
)

type JailHooks struct {
	JailID  uint          `json:"jid" gorm:"column:jid;index"`
	Phase   JailHookPhase `json:"phase" gorm:"column:phase;index"`
	Enabled bool          `json:"enabled"`
	Script  string        `json:"script"`
}

type JailType string

const (
	JailTypeFreeBSD JailType = "freebsd"
	JailTypeLinux   JailType = "linux"
)

type Jail struct {
	ID          uint     `json:"id" gorm:"primaryKey"`
	CTID        uint     `json:"ctId" gorm:"unique;not null;uniqueIndex"`
	Name        string   `json:"name" gorm:"not null;unique"`
	Hostname    string   `json:"hostname"`
	Description string   `json:"description"`
	Type        JailType `json:"type"`

	StartAtBoot *bool `json:"startAtBoot" gorm:"default:false"`
	StartOrder  int   `json:"startOrder"`

	InheritIPv4 bool `json:"inheritIPv4"`
	InheritIPv6 bool `json:"inheritIPv6"`

	ResourceLimits *bool  `json:"resourceLimits" gorm:"default:true"`
	Cores          int    `json:"cores"`
	CPUSet         []int  `json:"cpuSet" gorm:"serializer:json;type:json"`
	Memory         int    `json:"memory"`
	DevFSRuleset   string `json:"devfsRuleset"`

	Fstab             string      `json:"fstab"`
	CleanEnvironment  bool        `json:"cleanEnvironment"`
	AdditionalOptions string      `json:"additionalOptions"`
	AllowedOptions    []string    `json:"allowedOptions" gorm:"serializer:json;type:json"`
	JailHooks         []JailHooks `gorm:"foreignKey:JailID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`

	Storages []Storage   `gorm:"foreignKey:JailID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Networks []Network   `gorm:"foreignKey:JailID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Stats    []JailStats `gorm:"foreignKey:JailID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`

	MetadataMeta string `json:"metadataMeta"`
	MetadataEnv  string `json:"metadataEnv"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`

	StartLogs string     `json:"startLogs" gorm:"default:''"`
	StopLogs  string     `json:"stopLogs" gorm:"default:''"`
	StartedAt *time.Time `json:"startedAt" gorm:"default:null"`
	StoppedAt *time.Time `json:"stoppedAt" gorm:"default:null"`
}
