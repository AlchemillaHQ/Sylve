// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtServiceInterfaces

type TimeOffset string

const (
	TimeOffsetUTC   TimeOffset = "utc"
	TimeOffsetLocal TimeOffset = "localtime"
)

type CPUPinning struct {
	Socket int   `json:"socket" binding:"required,min=0"`
	Cores  []int `json:"cores"  binding:"required,min=1"`
}

type CreateVMRequest struct {
	Name        string `json:"name" binding:"required"`
	RID         *uint  `json:"rid" binding:"required"`
	Description string `json:"description"`

	ISO string `json:"iso"`

	StoragePool          string               `json:"storagePool"`
	StorageType          StorageType          `json:"storageType"`
	StorageSize          *uint64              `json:"storageSize"`
	StorageEmulationType StorageEmulationType `json:"storageEmulationType"`

	SwitchName          string `json:"switchName"`
	SwitchEmulationType string `json:"switchEmulationType"`
	MacId               *uint  `json:"macId"`

	CPUSockets int `json:"cpuSockets" binding:"required"`
	CPUCores   int `json:"cpuCores" binding:"required"`
	CPUThreads int `json:"cpuThreads" binding:"required"`

	CPUPinning []CPUPinning `json:"cpuPinning"`

	RAM          int   `json:"ram" binding:"required"`
	TPMEmulation *bool `json:"tpmEmulation"`

	PCIDevices []int `json:"pciDevices"`

	Serial        *bool  `json:"serial"`
	VNCPort       int    `json:"vncPort" binding:"required"`
	VNCPassword   string `json:"vncPassword"`
	VNCResolution string `json:"vncResolution"`
	VNCWait       *bool  `json:"vncWait"`

	CloudInit         *bool  `json:"cloudInit"`
	CloudInitData     string `json:"cloudInitData"`
	CloudInitMetaData string `json:"cloudInitMetaData"`

	APIC        *bool `json:"apic"`
	ACPI        *bool `json:"acpi"`
	IgnoreUMSRs *bool `json:"ignoreUMSR"`

	StartAtBoot *bool      `json:"startAtBoot"`
	StartOrder  int        `json:"startOrder"`
	TimeOffset  TimeOffset `json:"timeOffset" binding:"required"`
}

type ModifyCPURequest struct {
	CPUSockets int `json:"cpuSockets" binding:"required"`
	CPUCores   int `json:"cpuCores" binding:"required"`
	CPUThreads int `json:"cpuThreads" binding:"required"`

	CPUPinning []CPUPinning `json:"cpuPinning"`
}
