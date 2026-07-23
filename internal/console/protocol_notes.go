// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

const (
	OperationNoteList   = "notes.list"
	OperationNoteGet    = "notes.get"
	OperationNoteAdd    = "notes.add"
	OperationNoteDelete = "notes.delete"
)

type NoteAddPayload struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	JSON    bool   `json:"json"`
}

type NoteListPayload struct {
	JSON bool `json:"json"`
}

type NoteGetPayload struct {
	ID   uint `json:"id"`
	JSON bool `json:"json"`
}

type NoteDeletePayload struct {
	ID   uint `json:"id"`
	JSON bool `json:"json"`
}
