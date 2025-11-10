// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtServiceInterfaces

import (
	"encoding/xml"
)

type StorageType string

const (
	StorageTypeRaw  StorageType = "raw"
	StorageTypeZVOL StorageType = "zvol"
	StorageTypeISO  StorageType = "iso"
	StorageTypeNone StorageType = "none"
)

type StorageEmulationType string

const (
	VirtIOStorageEmulation StorageEmulationType = "virtio-blk"
	AHCIHDStorageEmulation StorageEmulationType = "ahci-hd"
	AHCICDStorageEmulation StorageEmulationType = "ahci-cd"
	NVMEStorageEmulation   StorageEmulationType = "nvme"
)

type StoragePoolXML struct {
	XMLName xml.Name `xml:"pool"`
	Text    string   `xml:",chardata"`
	Type    string   `xml:"type,attr"`
	Name    string   `xml:"name"`
	UUID    string   `xml:"uuid"`
	Source  struct {
		Text string `xml:",chardata"`
		Name string `xml:"name"`
	} `xml:"source"`
}

type StoragePool struct {
	Name   string
	Source string
	UUID   string
}

/*
type VMStorageDataset struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Pool string `json:"pool"`
	Name string `json:"name"`
	GUID string `json:"guid"`

	VMID uint `json:"vmId" gorm:"index"`
}

func (VMStorageDataset) TableName() string {
	return "vm_storage_datasets"
}

type Storage struct {
	ID   uint          `gorm:"primaryKey" json:"id"`
	Type VMStorageType `json:"type"`

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
*/

type StorageAttachRequest struct {
	VMID         int                  `json:"vmId" binding:"required"`
	Name         string               `json:"name"`
	UUID         string               `json:"uuid"`
	Pool         string               `json:"pool" binding:"required"`
	StorageType  StorageType          `json:"storageType" binding:"required"`
	Emulation    StorageEmulationType `json:"emulation" binding:"required"`
	Size         *int64               `json:"size"`
	RecordSize   *int                 `json:"recordSize"`
	VolBlockSize *int                 `json:"volBlockSize"`
	BootOrder    *int                 `json:"bootOrder"`
}

type StorageDetachRequest struct {
	VMID      int `json:"vmId" binding:"required"`
	StorageId int `json:"storageId" binding:"required"`
}
