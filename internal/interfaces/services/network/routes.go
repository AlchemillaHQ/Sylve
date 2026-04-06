// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

type UpsertStaticRouteRequest struct {
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Enabled         *bool  `json:"enabled"`
	FIB             *uint  `json:"fib"`
	DestinationType string `json:"destinationType" binding:"required,oneof=host network"`
	Destination     string `json:"destination" binding:"required"`
	Family          string `json:"family" binding:"required,oneof=inet inet6"`
	NextHopMode     string `json:"nextHopMode" binding:"required,oneof=gateway interface"`
	Gateway         string `json:"gateway"`
	Interface       string `json:"interface"`
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
	Interface       string `json:"interface"`
	SourceHint      string `json:"sourceHint"`
}
