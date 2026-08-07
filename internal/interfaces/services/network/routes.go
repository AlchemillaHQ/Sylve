// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

type UpsertStaticRouteRequest struct {
	Name             string `json:"name" binding:"required,max=128"`
	Description      string `json:"description" binding:"max=2048"`
	Enabled          *bool  `json:"enabled"`
	FIB              *uint  `json:"fib"`
	DestinationType  string `json:"destinationType" binding:"required,oneof=host network"`
	Destination      string `json:"destination" binding:"max=256"`
	DestinationRaw   string `json:"destinationRaw" binding:"max=256"`
	DestinationObjID *uint  `json:"destinationObjId" binding:"omitempty,gt=0"`
	Family           string `json:"family" binding:"required,oneof=inet inet6"`
	NextHopMode      string `json:"nextHopMode" binding:"required,oneof=gateway interface"`
	Gateway          string `json:"gateway" binding:"max=256"`
	GatewayRaw       string `json:"gatewayRaw" binding:"max=256"`
	GatewayObjID     *uint  `json:"gatewayObjId" binding:"omitempty,gt=0"`
	GatewayZone      string `json:"gatewayZone" binding:"max=64"`
	Interface        string `json:"interface" binding:"max=64"`
}

type StaticRouteSuggestion struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Enabled         bool   `json:"enabled"`
	FIB             uint   `json:"fib"`
	DestinationType string `json:"destinationType"`
	Destination     string `json:"destination"`
	Family          string `json:"family"`
	NextHopMode     string `json:"nextHopMode"`
	Gateway         string `json:"gateway"`
	GatewayZone     string `json:"gatewayZone"`
	Interface       string `json:"interface"`
	SourceHint      string `json:"sourceHint"`
}
