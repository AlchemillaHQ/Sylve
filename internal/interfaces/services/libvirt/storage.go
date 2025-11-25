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
	StorageTypeRaw       StorageType = "raw"
	StorageTypeZVOL      StorageType = "zvol"
	StorageTypeDiskImage StorageType = "image"
	StorageTypeNone      StorageType = "none"
)

type StorageEmulationType string

const (
	VirtIOStorageEmulation StorageEmulationType = "virtio-blk"
	AHCIHDStorageEmulation StorageEmulationType = "ahci-hd"
	AHCICDStorageEmulation StorageEmulationType = "ahci-cd"
	NVMEStorageEmulation   StorageEmulationType = "nvme"
)

type StorageAttachType string

const (
	StorageAttachTypeImport StorageAttachType = "import"
	StorageAttachTypeNew    StorageAttachType = "new"
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

type StorageAttachRequest struct {
	AttachType StorageAttachType `json:"attachType" binding:"required"`
	RawPath    string            `json:"rawPath"`
	Dataset    string            `json:"dataset"`

	VMID int    `json:"vmId" binding:"required"`
	Name string `json:"name"`
	UUID string `json:"downloadUUID"`

	Pool        string               `json:"pool" binding:"required"`
	StorageType StorageType          `json:"storageType" binding:"required"`
	Emulation   StorageEmulationType `json:"emulation" binding:"required"`

	Size         *int64 `json:"size"`
	RecordSize   *int   `json:"recordSize"`
	VolBlockSize *int   `json:"volBlockSize"`
	BootOrder    *int   `json:"bootOrder"`
}

type StorageUpdateRequest struct {
	ID        int                  `json:"id" binding:"required"`
	Name      string               `json:"name" binding:"required"`
	Size      int64                `json:"size" binding:"required"`
	Emulation StorageEmulationType `json:"emulation" binding:"required"`
	BootOrder *int                 `json:"bootOrder"`
}

type StorageDetachRequest struct {
	VMID      int `json:"vmId" binding:"required"`
	StorageId int `json:"storageId" binding:"required"`
}
