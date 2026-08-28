// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

const (
	OperationDatacenterNoteList       = "datacenter.notes.list"
	OperationDatacenterNoteGet        = "datacenter.notes.get"
	OperationDatacenterNoteAdd        = "datacenter.notes.add"
	OperationDatacenterNoteUpdate     = "datacenter.notes.update"
	OperationDatacenterNoteDelete     = "datacenter.notes.delete"
	OperationDatacenterClusterStatus  = "datacenter.cluster.status"
	OperationDatacenterClusterMembers = "datacenter.cluster.members"
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
