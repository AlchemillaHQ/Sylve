// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtServiceInterfaces

import (
	"context"
	"encoding/xml"

	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/digitalocean/go-libvirt"
)

type LibvirtServiceInterface interface {
	ModifyCPU(rid uint, req ModifyCPURequest) error
	ModifyRAM(rid uint, ram int) error
	ModifyVNC(rid uint, req ModifyVNCRequest) error
	ModifyPassthrough(rid uint, pciDevices []int) error

	WolTasks()

	NetworkDetach(rid uint, networkId uint) error
	NetworkAttach(req NetworkAttachRequest) error
	NetworkUpdate(req NetworkUpdateRequest) error
	FindAndChangeMAC(rid uint, oldMac string, newMac string) error
	FindVmByMac(mac string) (vmModels.VM, error)

	ModifyWakeOnLan(rid uint, enabled bool) error
	ModifyBootOrder(rid uint, startAtBoot bool, bootOrder int) error
	ModifyClock(rid uint, timeOffset string) error
	ModifySerial(rid uint, enabled bool) error
	ModifyShutdownWaitTime(rid uint, waitTime int) error
	ModifyCloudInitData(rid uint, data string, metadata string, networkConfig string) error
	ModifyIgnoreUMSRs(rid uint, ignore bool) error
	ModifyQemuGuestAgent(rid uint, enabled bool) error
	GetQemuGuestAgentInfo(rid uint) (QemuGuestAgentInfo, error)

	PruneOrphanedVMStats() error
	ApplyVMStatsRetention() error
	StoreVMUsage() error
	GetVMUsage(vmId int, step db.GFSStep) ([]vmModels.VMStats, error)

	CreateVMDisk(rid uint, storage vmModels.Storage, ctx context.Context) error
	SyncVMDisks(rid uint) error
	RemoveStorageXML(rid uint, storage vmModels.Storage) error
	StorageDetach(req StorageDetachRequest) error
	GetNextBootOrderIndex(vmId int) (int, error)
	ValidateBootOrderIndex(vmId int, bootOrder int) (bool, error)
	StorageImport(req StorageAttachRequest, vm vmModels.VM, ctx context.Context) error
	StorageNew(req StorageAttachRequest, vm vmModels.VM, ctx context.Context) error
	StorageAttach(req StorageAttachRequest, ctx context.Context) error
	StorageUpdate(req StorageUpdateRequest, ctx context.Context) error
	CreateStorageParent(rid uint, poolName string, ctx context.Context) error

	FindISOByUUID(uuid string, includeImg bool) (string, error)
	GetDomainStates() ([]DomainState, error)
	IsDomainShutOff(rid uint) (bool, error)
	IsDomainShutOffByID(id uint) (bool, error)
	CreateVMDirectory(rid uint) (string, error)
	ResetUEFIVars(rid uint) error
	ValidateCPUPins(rid uint, pins []CPUPinning, hostLogicalPerSocket int) error
	GeneratePinArgs(pins []vmModels.VMCPUPinning) []string
	GetVMConfigDirectory(rid uint) (string, error)
	CreateCloudInitISO(vm vmModels.VM) error
	GetCloudInitISOPath(rid uint) (string, error)
	FlashCloudInitMediaToDisk(vm vmModels.VM) error

	CreateVmXML(vm vmModels.VM, vmPath string) (string, error)
	CreateLvVm(id int, ctx context.Context) error
	RemoveLvVm(rid uint) error
	GetLvDomain(rid uint) (*LvDomain, error)
	StartTPM() error
	StopTPM(rid uint) error
	CheckPCIDevicesInUse(vm vmModels.VM) error
	LvVMAction(vm vmModels.VM, action string) error
	SetActionDate(vm vmModels.VM, action string) error
	GetVMXML(rid uint) (string, error)
	IsDomainInactive(rid uint) (bool, error)
	GetDomainState(rid int) (libvirt.DomainState, error)

	CheckVersion() error
	IsVirtualizationEnabled() bool
}

type LvDomain struct {
	ID     int32  `json:"id"`
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type SimpleList struct {
	ID         uint                    `json:"id"`
	RID        uint                    `json:"rid"`
	Name       string                  `json:"name"`
	State      libvirt.DomainState     `json:"state"`
	VNCPort    uint                    `json:"vncPort"`
	CPUPinning []vmModels.VMCPUPinning `json:"cpuPinning"`
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

type GraphicsListen struct {
	Type    string `xml:"type,attr"`
	Address string `xml:"address,attr"`
}

type Graphics struct {
	Type     string         `xml:"type,attr"`
	Port     string         `xml:"port,attr"`
	Password string         `xml:"passwd,attr,omitempty"`
	Listen   GraphicsListen `xml:"listen"`
}

type VideoResolution struct {
	X string `xml:"x,attr"`
	Y string `xml:"y,attr"`
}

type VideoModel struct {
	Type    string           `xml:"type,attr"`
	Heads   string           `xml:"heads,attr,omitempty"`
	Primary string           `xml:"primary,attr,omitempty"`
	Res     *VideoResolution `xml:"resolution,omitempty"`
}

type Video struct {
	Model VideoModel `xml:"model"`
}

type Devices struct {
	Disks       []Disk       `xml:"disk,omitempty"`
	Interfaces  []Interface  `xml:"interface,omitempty"`
	Controllers []Controller `xml:"controller,omitempty"`
	Inputs      []Input      `xml:"input,omitempty"`
	Serials     []Serial     `xml:"serial,omitempty"`
	Graphics    *Graphics    `xml:"graphics,omitempty"`
	Video       *Video       `xml:"video,omitempty"`
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
