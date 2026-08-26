// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"

const (
	OperationVMList          = "vms.list"
	OperationVMGet           = "vms.get"
	OperationVMCreate        = "vms.create"
	OperationVMAction        = "vms.action"
	OperationVMDelete        = "vms.delete"
	OperationVMPurge         = "vms.purge"
	OperationVMNetworks      = "vms.networks"
	OperationVMNetworkAttach = "vms.network.attach"
	OperationVMNetworkDetach = "vms.network.detach"
	OperationVMQGAInfo       = "vms.qga.info"
	OperationVMQGASend       = "vms.qga.send"
)

type JSONPayload struct {
	JSON bool `json:"json"`
}

type VMRIDPayload struct {
	RID  uint `json:"rid"`
	JSON bool `json:"json"`
}

type VMCreatePayload struct {
	Request libvirtServiceInterfaces.CreateVMRequest `json:"request"`
	JSON    bool                                     `json:"json"`
}

type VMActionPayload struct {
	RID    uint   `json:"rid"`
	Action string `json:"action"`
	JSON   bool   `json:"json"`
}

type VMDeletePayload struct {
	RID            uint `json:"rid"`
	DeleteMACs     bool `json:"deleteMacs"`
	DeleteRawDisks bool `json:"deleteRawDisks"`
	DeleteVolumes  bool `json:"deleteVolumes"`
	DryRun         bool `json:"dryRun"`
	JSON           bool `json:"json"`
}

type VMPurgePayload struct {
	RID        uint `json:"rid"`
	DeleteMACs bool `json:"deleteMacs"`
	JSON       bool `json:"json"`
}

type VMNetworkAttachPayload struct {
	RID     uint                                          `json:"rid"`
	Request libvirtServiceInterfaces.NetworkAttachRequest `json:"request"`
	JSON    bool                                          `json:"json"`
}

type VMNetworkDetachPayload struct {
	RID       uint `json:"rid"`
	NetworkID uint `json:"networkId"`
	JSON      bool `json:"json"`
}

type VMQGASendPayload struct {
	RID     uint   `json:"rid"`
	Command string `json:"command"`
	JSON    bool   `json:"json"`
}

const (
	OperationVMConfigCPU          = "vms.config.cpu"
	OperationVMConfigName         = "vms.config.name"
	OperationVMConfigDescription  = "vms.config.description"
	OperationVMConfigMemory       = "vms.config.memory"
	OperationVMConfigVNC          = "vms.config.vnc"
	OperationVMConfigSerial       = "vms.config.serial"
	OperationVMConfigPCI          = "vms.config.pci"
	OperationVMConfigAutostart    = "vms.config.autostart"
	OperationVMConfigClock        = "vms.config.clock"
	OperationVMConfigShutdown     = "vms.config.shutdown"
	OperationVMConfigBootROM      = "vms.config.boot-rom"
	OperationVMConfigCloudInit    = "vms.config.cloud-init"
	OperationVMConfigBhyveOptions = "vms.config.bhyve-options"
	OperationVMConfigUnknownMSR   = "vms.config.unknown-msr"
	OperationVMConfigQGA          = "vms.config.qga"
	OperationVMConfigWOL          = "vms.config.wol"
	OperationVMConfigTPM          = "vms.config.tpm"
	OperationVMAccessVNC          = "vms.access.vnc"
)

type VMConfigTextPayload struct {
	RID   uint   `json:"rid"`
	Value string `json:"value"`
	JSON  bool   `json:"json"`
}

type VMConfigCPUPayload struct {
	RID     uint                                      `json:"rid"`
	Request libvirtServiceInterfaces.ModifyCPURequest `json:"request"`
	JSON    bool                                      `json:"json"`
}

type VMConfigMemoryPayload struct {
	RID  uint `json:"rid"`
	RAM  int  `json:"ram"`
	JSON bool `json:"json"`
}

type VMVNCChanges struct {
	Enabled    *bool   `json:"enabled"`
	Port       *int    `json:"port"`
	Bind       *string `json:"bind"`
	Resolution *string `json:"resolution"`
	Wait       *bool   `json:"wait"`
	Password   *string `json:"password"`
}

type VMConfigVNCPayload struct {
	RID     uint         `json:"rid"`
	Changes VMVNCChanges `json:"changes"`
	JSON    bool         `json:"json"`
}

type VMConfigBoolPayload struct {
	RID     uint `json:"rid"`
	Enabled bool `json:"enabled"`
	JSON    bool `json:"json"`
}

type VMConfigPCIPayload struct {
	RID       uint  `json:"rid"`
	DeviceIDs []int `json:"deviceIds"`
	JSON      bool  `json:"json"`
}

type VMConfigAutostartPayload struct {
	RID     uint `json:"rid"`
	Enabled bool `json:"enabled"`
	Order   int  `json:"order"`
	JSON    bool `json:"json"`
}

type VMConfigClockPayload struct {
	RID        uint   `json:"rid"`
	TimeOffset string `json:"timeOffset"`
	JSON       bool   `json:"json"`
}

type VMConfigShutdownPayload struct {
	RID         uint `json:"rid"`
	WaitSeconds int  `json:"waitSeconds"`
	JSON        bool `json:"json"`
}

type VMConfigBootROMPayload struct {
	RID     uint   `json:"rid"`
	BootROM string `json:"bootRom"`
	JSON    bool   `json:"json"`
}

type VMConfigCloudInitPayload struct {
	RID           uint   `json:"rid"`
	Data          string `json:"data"`
	Metadata      string `json:"metadata"`
	NetworkConfig string `json:"networkConfig"`
	JSON          bool   `json:"json"`
}

type VMConfigBhyveOptionsPayload struct {
	RID     uint     `json:"rid"`
	Options []string `json:"options"`
	JSON    bool     `json:"json"`
}

type VMVNCAccessInfo struct {
	RID                uint   `json:"rid"`
	Name               string `json:"name"`
	Enabled            bool   `json:"enabled"`
	Available          bool   `json:"available"`
	DomainState        string `json:"domainState"`
	BindAddress        string `json:"bindAddress"`
	Port               int    `json:"port"`
	Endpoint           string `json:"endpoint"`
	Resolution         string `json:"resolution"`
	Wait               bool   `json:"wait"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	UnavailableReason  string `json:"unavailableReason"`
}

const (
	OperationVMSnapshotList     = "vms.snapshot.list"
	OperationVMSnapshotCreate   = "vms.snapshot.create"
	OperationVMSnapshotRollback = "vms.snapshot.rollback"
	OperationVMSnapshotDelete   = "vms.snapshot.delete"

	OperationVMTemplateList    = "vms.template.list"
	OperationVMTemplateGet     = "vms.template.get"
	OperationVMTemplateConvert = "vms.template.convert"
	OperationVMTemplateCreate  = "vms.template.create"
	OperationVMTemplateDelete  = "vms.template.delete"

	OperationVMAccessSerial = "vms.access.serial"
)

type VMSnapshotCreatePayload struct {
	RID         uint   `json:"rid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	JSON        bool   `json:"json"`
}

type VMSnapshotRollbackPayload struct {
	RID          uint `json:"rid"`
	SnapshotID   uint `json:"snapshotId"`
	DestroyNewer bool `json:"destroyNewer"`
	JSON         bool `json:"json"`
}

type VMSnapshotDeletePayload struct {
	RID        uint `json:"rid"`
	SnapshotID uint `json:"snapshotId"`
	JSON       bool `json:"json"`
}

type VMSnapshotRollbackOutput struct {
	RolledBack              bool     `json:"rolledBack"`
	RID                     uint     `json:"rid"`
	SnapshotID              uint     `json:"snapshotId"`
	WasRunning              bool     `json:"wasRunning"`
	Restarted               bool     `json:"restarted"`
	NewerSnapshotsDestroyed int64    `json:"newerSnapshotsDestroyed"`
	Warnings                []string `json:"warnings"`
}

type VMSnapshotDeleteOutput struct {
	Deleted    bool `json:"deleted"`
	RID        uint `json:"rid"`
	SnapshotID uint `json:"snapshotId"`
}

type VMTemplateConvertPayload struct {
	RID     uint                                              `json:"rid"`
	Request libvirtServiceInterfaces.ConvertToTemplateRequest `json:"request"`
	JSON    bool                                              `json:"json"`
}

type VMTemplateGetPayload struct {
	TemplateID uint `json:"templateId"`
	JSON       bool `json:"json"`
}

type VMTemplateCreatePayload struct {
	TemplateID uint                                               `json:"templateId"`
	Request    libvirtServiceInterfaces.CreateFromTemplateRequest `json:"request"`
	JSON       bool                                               `json:"json"`
}

type VMTemplateDeletePayload struct {
	TemplateID uint `json:"templateId"`
	JSON       bool `json:"json"`
}

type VMTemplateStorageInfo struct {
	SourceStorageID uint   `json:"sourceStorageId"`
	Type            string `json:"type"`
	Pool            string `json:"pool"`
}

type VMTemplateInfo struct {
	ID           uint                    `json:"id"`
	Name         string                  `json:"name"`
	SourceVMName string                  `json:"sourceVmName"`
	SourceVMRID  uint                    `json:"sourceVmRid"`
	Storages     []VMTemplateStorageInfo `json:"storages"`
}

type VMTemplateTaskOutput struct {
	TaskID     uint   `json:"taskId"`
	SourceRID  uint   `json:"sourceRid,omitempty"`
	TemplateID uint   `json:"templateId,omitempty"`
	Action     string `json:"action"`
	Outcome    string `json:"outcome"`
}

type VMTemplateDeleteOutput struct {
	Deleted    bool `json:"deleted"`
	TemplateID uint `json:"templateId"`
}

type VMAccessSerialPayload struct {
	RID      uint   `json:"rid"`
	BaudRate string `json:"baudRate"`
	JSON     bool   `json:"json"`
}

type VMSerialConsoleLaunch struct {
	RID        uint   `json:"rid"`
	BaudRate   string `json:"baudRate"`
	DevicePath string `json:"devicePath"`
}

const (
	OperationVMStorageList   = "vms.storage.list"
	OperationVMStorageAttach = "vms.storage.attach"
	OperationVMStorageUpdate = "vms.storage.update"
	OperationVMStorageDetach = "vms.storage.detach"
	OperationVMNetworkUpdate = "vms.network.update"
)

type VMStorageAttachPayload struct {
	RID     uint                                          `json:"rid"`
	Request libvirtServiceInterfaces.StorageAttachRequest `json:"request"`
	JSON    bool                                          `json:"json"`
}

type VMStorageUpdatePayload struct {
	RID       uint                                          `json:"rid"`
	StorageID uint                                          `json:"storageId"`
	Request   libvirtServiceInterfaces.StorageUpdateRequest `json:"request"`
	JSON      bool                                          `json:"json"`
}

type VMStorageDetachPayload struct {
	RID       uint `json:"rid"`
	StorageID uint `json:"storageId"`
	JSON      bool `json:"json"`
}

type VMNetworkUpdatePayload struct {
	RID       uint                                          `json:"rid"`
	NetworkID uint                                          `json:"networkId"`
	Request   libvirtServiceInterfaces.NetworkUpdateRequest `json:"request"`
	JSON      bool                                          `json:"json"`
}
