// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsServiceInterfaces

type ZFSHistoryBatchJob struct {
	Pool     string   `json:"pool"`
	Kind     string   `json:"kind"`
	EventIDs []uint   `json:"event_ids"`
	Datasets []string `json:"datasets"`
	Actions  []string `json:"actions"`
	MinTXG   string   `json:"min_txg,omitempty"`
	MaxTXG   string   `json:"max_txg,omitempty"`
}
