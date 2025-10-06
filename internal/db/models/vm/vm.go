// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vmModels

import (
	"fmt"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"gorm.io/gorm"
)

type Storage struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `json:"name" gorm:"default:''"`
	Type      string `json:"type"`
	Dataset   string `json:"dataset"`
	Size      int64  `json:"size"`
	Emulation string `json:"emulation"`

	VMID uint `json:"vmId" gorm:"index"`
}

type Network struct {
	ID  uint   `gorm:"primaryKey" json:"id"`
	MAC string `json:"mac"`

	MacID      *uint                 `json:"macId" gorm:"column:mac_id"`
	AddressObj *networkModels.Object `json:"macObj" gorm:"foreignKey:MacID"`

	SwitchID   uint   `json:"switchId" gorm:"index;not null"`
	SwitchType string `json:"switchType" gorm:"index;not null;default:standard"`

	StandardSwitch *networkModels.StandardSwitch `gorm:"-" json:"-"`
	ManualSwitch   *networkModels.ManualSwitch   `gorm:"-" json:"-"`

	Emulation string `json:"emulation"`
	VMID      uint   `json:"vmId" gorm:"index"`
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

type VMStats struct {
	ID          uint    `gorm:"primaryKey" json:"id"`
	VMID        uint    `json:"vmId" gorm:"index"`
	CPUUsage    float64 `json:"cpuUsage"`
	MemoryUsage float64 `json:"memoryUsage"`
	MemoryUsed  float64 `json:"memoryUsed"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

type VM struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	VmID          int    `json:"vmId"`
	CPUSockets    int    `json:"cpuSockets"`
	CPUCores      int    `json:"cpuCores"`
	CPUsThreads   int    `json:"cpuThreads"`
	RAM           int    `json:"ram"`
	Serial        bool   `json:"serial" gorm:"default:false"`
	VNCEnabled    bool   `json:"vncEnabled" gorm:"default:true"`
	VNCPort       int    `json:"vncPort"`
	VNCPassword   string `json:"vncPassword"`
	VNCResolution string `json:"vncResolution"`
	VNCWait       bool   `json:"vncWait"`
	StartAtBoot   bool   `json:"startAtBoot"`
	TPMEmulation  bool   `json:"tpmEmulation"`
	StartOrder    int    `json:"startOrder"`
	WoL           bool   `json:"wol" gorm:"default:false"`
	TimeOffset    string `json:"timeOffset" gorm:"default:'utc'"`

	ISO        string    `json:"iso"`
	Storages   []Storage `json:"storages" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Networks   []Network `json:"networks" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	PCIDevices []int     `json:"pciDevices" gorm:"serializer:json;type:json"`
	CPUPinning []int     `json:"cpuPinning" gorm:"serializer:json;type:json"`

	Stats []VMStats `json:"-" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	State string    `json:"state" gorm:"-"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`

	StartedAt *time.Time `json:"startedAt" gorm:"default:null"`
	StoppedAt *time.Time `json:"stoppedAt" gorm:"default:null"`
}
