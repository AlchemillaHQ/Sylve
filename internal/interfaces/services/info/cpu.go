// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoServiceInterfaces

type CPUInfo struct {
	Name           string   `json:"name"`
	Sockets        int16    `json:"sockets"`
	PhysicalCores  int16    `json:"physicalCores"`
	ThreadsPerCore int16    `json:"threadsPerCore"`
	LogicalCores   int16    `json:"logicalCores"`
	Family         int16    `json:"family"`
	Model          int16    `json:"model"`
	Features       []string `json:"features"`
	CacheLine      int16    `json:"cacheLine"`
	Cache          struct {
		L1D int16 `json:"l1d"`
		L1I int16 `json:"l1i"`
		L2  int16 `json:"l2"`
		L3  int16 `json:"l3"`
	} `json:"cache"`
	Frequency int64   `json:"frequency"`
	Usage     float64 `json:"usage"`
}
