// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

const (
	OperationDatacenterNoteList              = "datacenter.notes.list"
	OperationDatacenterNoteGet               = "datacenter.notes.get"
	OperationDatacenterNoteAdd               = "datacenter.notes.add"
	OperationDatacenterNoteUpdate            = "datacenter.notes.update"
	OperationDatacenterNoteDelete            = "datacenter.notes.delete"
	OperationDatacenterClusterStatus         = "datacenter.cluster.status"
	OperationDatacenterClusterMembers        = "datacenter.cluster.members"
	OperationDatacenterClusterReaddress      = "datacenter.cluster.readdress"
	OperationDatacenterClusterRepairAddress  = "datacenter.cluster.repair-address"
	OperationDatacenterClusterGuestIDsList   = "datacenter.cluster.guest-ids.list"
	OperationDatacenterClusterGuestIDReclaim = "datacenter.cluster.guest-ids.reclaim"
)

type DatacenterNoteListPayload struct {
	JSON bool `json:"json"`
}

type DatacenterNoteGetPayload struct {
	ID   uint `json:"id"`
	JSON bool `json:"json"`
}

type DatacenterNoteMutationPayload struct {
	ID      uint   `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
	JSON    bool   `json:"json"`
}

type DatacenterNoteMutationResult struct {
	Action string `json:"action"`
	ID     uint   `json:"id,omitempty"`
}

type DatacenterClusterReadPayload struct {
	JSON bool `json:"json"`
}

type DatacenterClusterReaddressPayload struct {
	NewIP           string `json:"newIp"`
	AllowDisruption bool   `json:"allowDisruption"`
	JSON            bool   `json:"json"`
}

type DatacenterClusterRepairAddressPayload struct {
	NodeID          string `json:"nodeId"`
	NewIP           string `json:"newIp"`
	AllowDisruption bool   `json:"allowDisruption"`
	JSON            bool   `json:"json"`
}

type DatacenterClusterGuestIdentityClaim struct {
	GuestID     uint   `json:"guestId"`
	GuestKind   string `json:"guestKind"`
	OwnerNodeID string `json:"ownerNodeId"`
}

type DatacenterClusterGuestIDReclaimPayload struct {
	GuestID      uint   `json:"guestId"`
	Force        bool   `json:"force"`
	Confirmation string `json:"confirmation,omitempty"`
	JSON         bool   `json:"json"`
}

type DatacenterClusterGuestIDReclaimResult struct {
	GuestID uint `json:"guestId"`
}
