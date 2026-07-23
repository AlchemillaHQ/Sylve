// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"

const (
	OperationJailList          = "jails.list"
	OperationJailGet           = "jails.get"
	OperationJailCreate        = "jails.create"
	OperationJailAction        = "jails.action"
	OperationJailDelete        = "jails.delete"
	OperationJailNetworks      = "jails.networks"
	OperationJailRemoveNetwork = "jails.remove-network"
	OperationBootstrapList     = "jails.bootstrap.list"
	OperationBootstrapCreate   = "jails.bootstrap.create"
	OperationBootstrapDelete   = "jails.bootstrap.delete"
)

type JailCreatePayload struct {
	Request jailServiceInterfaces.CreateJailRequest `json:"request"`
	JSON    bool                                    `json:"json"`
}

type JailListPayload struct {
	JSON bool `json:"json"`
}

type JailGetPayload struct {
	CTID uint `json:"ctId"`
	JSON bool `json:"json"`
}

type JailActionPayload struct {
	CTID   uint   `json:"ctId"`
	Action string `json:"action"`
	All    bool   `json:"all"`
	JSON   bool   `json:"json"`
}

type JailDeletePayload struct {
	CTID  uint `json:"ctId"`
	Purge bool `json:"purge"`
	JSON  bool `json:"json"`
}

type JailNetworksPayload struct {
	CTID uint `json:"ctId"`
	JSON bool `json:"json"`
}

type JailRemoveNetworkPayload struct {
	CTID      uint `json:"ctId"`
	NetworkID uint `json:"networkId"`
	JSON      bool `json:"json"`
}

type BootstrapCreatePayload struct {
	Request jailServiceInterfaces.BootstrapRequest `json:"request"`
	Wait    bool                                   `json:"wait"`
	JSON    bool                                   `json:"json"`
}

type BootstrapListPayload struct {
	Pool string `json:"pool"`
	JSON bool   `json:"json"`
}

type BootstrapDeletePayload struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	JSON bool   `json:"json"`
}
