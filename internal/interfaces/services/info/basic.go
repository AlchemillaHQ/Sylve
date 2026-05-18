// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoServiceInterfaces

type BasicInfo struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	Uptime        int64  `json:"uptime"`
	LoadAverage   string `json:"loadAverage"`
	BootMode      string `json:"bootMode"`
	SylveVersion  string `json:"sylveVersion"`
	DevFSDisabled bool   `json:"devFSDisabled"`
}
