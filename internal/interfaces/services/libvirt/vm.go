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

	CloudInit              *bool  `json:"cloudInit"`
	CloudInitData          string `json:"cloudInitData"`
	CloudInitMetaData      string `json:"cloudInitMetaData"`
	CloudInitNetworkConfig string `json:"cloudInitNetworkConfig"`

	APIC           *bool `json:"apic"`
	ACPI           *bool `json:"acpi"`
	IgnoreUMSRs    *bool `json:"ignoreUMSR"`
	QemuGuestAgent *bool `json:"qemuGuestAgent"`

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

type ModifyVNCRequest struct {
	VNCEnabled    *bool  `json:"vncEnabled"`
	VNCPort       int    `json:"vncPort" binding:"required"`
	VNCResolution string `json:"vncResolution" binding:"required"`
	VNCPassword   string `json:"vncPassword" binding:"required"`
	VNCWait       *bool  `json:"vncWait"`
}

type QemuGuestAgentInfo struct {
	OSInfo     QGAOSInfo             `json:"osInfo"`
	Interfaces []QGANetworkInterface `json:"interfaces"`
}

type QGAOSInfo struct {
	Name          string `json:"name"`
	KernelRelease string `json:"kernel-release"`
	Version       string `json:"version"`
	PrettyName    string `json:"pretty-name"`
	VersionID     string `json:"version-id"`
	KernelVersion string `json:"kernel-version"`
	Machine       string `json:"machine"`
	ID            string `json:"id"`
}

type QGANetworkInterface struct {
	Name            string                `json:"name"`
	IPAddresses     []QGANetworkIPAddress `json:"ip-addresses"`
	Statistics      QGANetworkStatistics  `json:"statistics"`
	HardwareAddress string                `json:"hardware-address"`
}

type QGANetworkIPAddress struct {
	IPType  string `json:"ip-address-type"`
	Address string `json:"ip-address"`
	Prefix  int    `json:"prefix"`
}

type QGANetworkStatistics struct {
	TxPackets uint64 `json:"tx-packets"`
	TxErrs    uint64 `json:"tx-errs"`
	RxBytes   uint64 `json:"rx-bytes"`
	RxDropped uint64 `json:"rx-dropped"`
	RxPackets uint64 `json:"rx-packets"`
	RxErrs    uint64 `json:"rx-errs"`
	TxBytes   uint64 `json:"tx-bytes"`
	TxDropped uint64 `json:"tx-dropped"`
}

type NetworkAttachRequest struct {
	RID        uint   `json:"rid" binding:"required"`
	SwitchName string `json:"switchName" binding:"required"`
	Emulation  string `json:"emulation" binding:"required"`
	MacId      *uint  `json:"macId"`
}

type NetworkUpdateRequest struct {
	NetworkID  uint   `json:"networkId" binding:"required"`
	SwitchName string `json:"switchName" binding:"required"`
	Emulation  string `json:"emulation" binding:"required"`
	MacId      *uint  `json:"macId"`
}
