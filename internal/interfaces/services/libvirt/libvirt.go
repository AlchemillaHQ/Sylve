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

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/digitalocean/go-libvirt"
)

type LibvirtServiceInterface interface {
	CheckVersion() error
	StartTPM() error

	ListStoragePools() ([]StoragePool, error)
	CreateStoragePool(name string) error
	DeleteStoragePool(name string) error
	RescanStoragePools() error

	NetworkDetach(vmId int, networkId int) error
	NetworkAttach(vmId int, switchName string, emulation string, macObjId uint) error
	FindAndChangeMAC(vmId int, oldMac string, newMac string) error

	StoreVMUsage() error

	FindISOByUUID(uuid string, includeImg bool) (string, error)

	GetLvDomain(vmId int) (*LvDomain, error)
	IsDomainInactive(vmId int) (bool, error)

	FindVmByMac(mac string) (vmModels.VM, error)
	WolTasks()
}

type LvDomain struct {
	ID     int32  `json:"id"`
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type SimpleList struct {
	ID    uint                `json:"id"`
	Name  string              `json:"name"`
	VMID  int                 `json:"vmId"`
	State libvirt.DomainState `json:"state"`
}

type DomainStateReason string

const (
	DomainReasonUnknown DomainStateReason = "unknown"

	// --- Running state reasons ---
	DomainReasonRunningBooted            DomainStateReason = "booted"
	DomainReasonRunningMigrated          DomainStateReason = "migrated"
	DomainReasonRunningRestored          DomainStateReason = "restored"
	DomainReasonRunningFromSnapshot      DomainStateReason = "from_snapshot"
	DomainReasonRunningUnpaused          DomainStateReason = "unpaused"
	DomainReasonRunningMigrationCanceled DomainStateReason = "migration_canceled"
	DomainReasonRunningSaveCanceled      DomainStateReason = "save_canceled"
	DomainReasonRunningWakeup            DomainStateReason = "wakeup"
	DomainReasonRunningCrashed           DomainStateReason = "crashed"

	// --- Shutoff state reasons ---
	DomainReasonShutoffShutdown     DomainStateReason = "shutdown"
	DomainReasonShutoffDestroyed    DomainStateReason = "destroyed"
	DomainReasonShutoffCrashed      DomainStateReason = "crashed"
	DomainReasonShutoffSaved        DomainStateReason = "saved"
	DomainReasonShutoffFailed       DomainStateReason = "failed"
	DomainReasonShutoffFromSnapshot DomainStateReason = "from_snapshot"

	// --- Paused state reasons ---
	DomainReasonPausedUser         DomainStateReason = "user"
	DomainReasonPausedMigration    DomainStateReason = "migration"
	DomainReasonPausedSave         DomainStateReason = "save"
	DomainReasonPausedDump         DomainStateReason = "dump"
	DomainReasonPausedIOError      DomainStateReason = "io_error"
	DomainReasonPausedWatchdog     DomainStateReason = "watchdog"
	DomainReasonPausedFromSnapshot DomainStateReason = "from_snapshot"
	DomainReasonPausedShuttingDown DomainStateReason = "shutting_down"
	DomainReasonPausedSnapshot     DomainStateReason = "snapshot"

	DomainReasonBlockedUnknown DomainStateReason = "blocked_unknown"
	DomainReasonCrashedUnknown DomainStateReason = "crashed_unknown"
	DomainReasonPMSuspended    DomainStateReason = "pm_suspended"
)

type DomainState struct {
	Domain string              `json:"domain"`
	State  libvirt.DomainState `json:"state"`
	Reason DomainStateReason   `json:"reason"`
}

type Memory struct {
	Unit string `xml:"unit,attr"`
	Text string `xml:",chardata"`
}

type MemoryBacking struct {
	Locked struct{} `xml:"locked"`
}

type Topology struct {
	Sockets string `xml:"sockets,attr"`
	Cores   string `xml:"cores,attr"`
	Threads string `xml:"threads,attr"`
}

type CPU struct {
	Topology Topology `xml:"topology"`
}

type Loader struct {
	ReadOnly string `xml:"readonly,attr"`
	Type     string `xml:"type,attr"`
	Path     string `xml:",chardata"`
}

type OS struct {
	Type   string `xml:"type"`
	Loader Loader `xml:"loader"`
}

type Features struct {
	APIC struct{} `xml:"apic"`
	ACPI struct{} `xml:"acpi"`
}

type Clock struct {
	Offset string `xml:"offset,attr"`
}

type Driver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type Target struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

type Source struct {
	File string `xml:"file,attr"`
}

type Volume struct {
	Pool   string `xml:"pool,attr"`
	Volume string `xml:"volume,attr"`
}

type Disk struct {
	Type     string    `xml:"type,attr"`
	Device   string    `xml:"device,attr"`
	Driver   *Driver   `xml:"driver,omitempty"`
	Source   any       `xml:"source"`
	Target   Target    `xml:"target"`
	ReadOnly *struct{} `xml:"readonly,omitempty"`
}

type MACAddress struct {
	Address string `xml:"address,attr"`
}

type BridgeSource struct {
	Bridge string `xml:"bridge,attr"`
}

type Model struct {
	Type string `xml:"type,attr"`
}

type Interface struct {
	Type   string       `xml:"type,attr"`
	MAC    *MACAddress  `xml:"mac,omitempty"`
	Source BridgeSource `xml:"source"`
	Model  Model        `xml:"model"`
}

type Input struct {
	Type string `xml:"type,attr"`
	Bus  string `xml:"bus,attr"`
}

type SerialSource struct {
	Master string `xml:"master,attr"`
	Slave  string `xml:"slave,attr"`
}

type Serial struct {
	Type   string       `xml:"type,attr"`
	Source SerialSource `xml:"source"`
}

type Address struct {
	Type     string `xml:"type,attr,omitempty"`
	Domain   string `xml:"domain,attr,omitempty"`
	Bus      string `xml:"bus,attr,omitempty"`
	Slot     string `xml:"slot,attr,omitempty"`
	Function string `xml:"function,attr,omitempty"`
}

type Controller struct {
	Type    string   `xml:"type,attr"`
	Index   *int     `xml:"index,attr,omitempty"`
	Model   string   `xml:"model,attr,omitempty"`
	Address *Address `xml:"address,omitempty"`
}

type Devices struct {
	Disks       []Disk       `xml:"disk,omitempty"`
	Interfaces  []Interface  `xml:"interface,omitempty"`
	Controllers []Controller `xml:"controller,omitempty"`
	Inputs      []Input      `xml:"input,omitempty"`
	Serials     []Serial     `xml:"serial,omitempty"`
}

type BhyveArg struct {
	Value string `xml:"value,attr"`
}

type BhyveCommandline struct {
	Args []BhyveArg `xml:"bhyve:arg"`
}

type Domain struct {
	XMLName       xml.Name       `xml:"domain"`
	Type          string         `xml:"type,attr"`
	XMLNSBhyve    string         `xml:"xmlns:bhyve,attr"`
	Name          string         `xml:"name"`
	Memory        Memory         `xml:"memory"`
	MemoryBacking *MemoryBacking `xml:"memoryBacking,omitempty"`
	CPU           CPU            `xml:"cpu"`
	VCPU          int            `xml:"vcpu"`
	OS            OS             `xml:"os"`
	Features      Features       `xml:"features"`
	Clock         Clock          `xml:"clock"`

	OnPoweroff string `xml:"on_poweroff,omitempty"`
	OnReboot   string `xml:"on_reboot,omitempty"`
	OnCrash    string `xml:"on_crash,omitempty"`

	Devices Devices `xml:"devices"`

	BhyveCommandline *BhyveCommandline `xml:"bhyve:commandline,omitempty"`
}
