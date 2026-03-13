// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterServiceInterfaces

type ReplicationPolicyTargetReq struct {
	NodeID string `json:"nodeId" binding:"required"`
	Weight int    `json:"weight"`
}

type ReplicationPolicyReq struct {
	Name         string                       `json:"name" binding:"required,min=2"`
	Description  string                       `json:"description"`
	GuestType    string                       `json:"guestType" binding:"required"`
	GuestID      uint                         `json:"guestId" binding:"required"`
	SourceNodeID string                       `json:"sourceNodeId"`
	ActiveNodeID string                       `json:"-"`
	OwnerEpoch   uint64                       `json:"-"`
	SourceMode   string                       `json:"sourceMode"`
	FailbackMode string                       `json:"failbackMode"`
	FailoverMode string                       `json:"failoverMode"`
	CronExpr     string                       `json:"cronExpr"`
	Enabled      *bool                        `json:"enabled"`
	Targets      []ReplicationPolicyTargetReq `json:"targets" binding:"required"`
}
