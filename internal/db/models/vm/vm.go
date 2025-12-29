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
	"github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

func (Storage) TableName() string {
	return "vm_storages"
}

type VMStorageType string

const (
	VMStorageTypeRaw       VMStorageType = "raw"
	VMStorageTypeZVol      VMStorageType = "zvol"
	VMStorageTypeDiskImage VMStorageType = "image"
)

type VMStorageEmulationType string

const (
	VirtIOStorageEmulation VMStorageEmulationType = "virtio-blk"
	AHCIHDStorageEmulation VMStorageEmulationType = "ahci-hd"
	AHCICDStorageEmulation VMStorageEmulationType = "ahci-cd"
	NVMEStorageEmulation   VMStorageEmulationType = "nvme"
)

type TimeOffset string

const (
	TimeOffsetUTC   TimeOffset = "utc"
	TimeOffsetLocal TimeOffset = "localtime"
)

type VMStorageDataset struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Pool string `json:"pool"`
	Name string `json:"name"`
	GUID string `json:"guid"`
}

func (VMStorageDataset) TableName() string {
	return "vm_storage_datasets"
}

type Storage struct {
	ID   uint          `gorm:"primaryKey" json:"id"`
	Type VMStorageType `json:"type"`

	Name         string `json:"name"`
	DownloadUUID string `json:"uuid"`

	Pool string `json:"pool"`

	DatasetID *uint            `json:"datasetId" gorm:"column:dataset_id"`
	Dataset   VMStorageDataset `json:"dataset" gorm:"foreignKey:DatasetID;references:ID"`

	Size      int64                  `json:"size"`
	Emulation VMStorageEmulationType `json:"emulation"`

	RecordSize   int `json:"recordSize"`
	VolBlockSize int `json:"volBlockSize"`

	BootOrder int  `json:"bootOrder"`
	VMID      uint `json:"vmId" gorm:"index"`
}

func (Network) TableName() string {
	return "vm_networks"
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

func (VMStats) TableName() string {
	return "vm_stats"
}

func (s VMStats) GetID() uint {
	return s.ID
}

func (s VMStats) GetCreatedAt() time.Time {
	return s.CreatedAt
}

type VMCPUPinning struct {
	ID   uint `gorm:"primaryKey" json:"id"`
	VMID uint `json:"vmId" gorm:"index"`

	HostSocket int   `json:"hostSocket"`
	HostCPU    []int `json:"hostCpu" gorm:"serializer:json;type:json"`
}

type VM struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RID         uint   `json:"rid" gorm:"column:rid;not null;uniqueIndex;"`

	CPUSockets int `json:"cpuSockets"`
	CPUCores   int `json:"cpuCores"`
	CPUThreads int `json:"cpuThreads"`

	CPUPinning []VMCPUPinning `json:"cpuPinning" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	RAM int `json:"ram"`

	TPMEmulation     bool `json:"tpmEmulation"`
	ShutdownWaitTime int  `json:"shutdownWaitTime" gorm:"default:10"`

	Serial bool `json:"serial" gorm:"default:false"`

	VNCEnabled    bool   `json:"vncEnabled" gorm:"default:true"`
	VNCPort       int    `json:"vncPort"`
	VNCPassword   string `json:"vncPassword"`
	VNCResolution string `json:"vncResolution"`
	VNCWait       bool   `json:"vncWait"`

	StartAtBoot bool       `json:"startAtBoot"`
	StartOrder  int        `json:"startOrder"`
	WoL         bool       `json:"wol" gorm:"default:false"`
	TimeOffset  TimeOffset `json:"timeOffset" gorm:"default:'utc'"`

	Storages   []Storage `json:"storages" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Networks   []Network `json:"networks" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	PCIDevices []int     `json:"pciDevices" gorm:"serializer:json;type:json"`

	ACPI bool `json:"acpi"`
	APIC bool `json:"apic"`

	Stats []VMStats           `json:"-" gorm:"foreignKey:VMID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	State libvirt.DomainState `json:"state" gorm:"-"`

	CloudInitData     string `json:"cloudInitData" gorm:"type:text"`
	CloudInitMetaData string `json:"cloudInitMetaData" gorm:"type:text"`
	IgnoreUMSR        bool   `json:"ignoreUMSR" gorm:"default:false"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`

	StartedAt *time.Time `json:"startedAt" gorm:"default:null"`
	StoppedAt *time.Time `json:"stoppedAt" gorm:"default:null"`
}
