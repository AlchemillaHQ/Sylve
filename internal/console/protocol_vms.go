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
	OperationVMQGASend       = "vms.qga.send"
)

type VMListPayload struct {
	JSON bool `json:"json"`
}

type VMGetPayload struct {
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
	JSON           bool `json:"json"`
}

type VMPurgePayload struct {
	RID        uint `json:"rid"`
	DeleteMACs bool `json:"deleteMacs"`
	JSON       bool `json:"json"`
}

type VMNetworksPayload struct {
	RID  uint `json:"rid"`
	JSON bool `json:"json"`
}

type VMNetworkAttachPayload struct {
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
