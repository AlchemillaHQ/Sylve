// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

type ReaddressIdentity struct {
	NodeID       string `json:"nodeId"`
	Enabled      bool   `json:"enabled"`
	OldIP        string `json:"oldIp"`
	NewIP        string `json:"newIp"`
	RaftIP       string `json:"raftIp"`
	Phase        string `json:"phase"`
	SylveVersion string `json:"sylveVersion"`
}
