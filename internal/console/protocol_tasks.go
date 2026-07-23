// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

const (
	OperationTaskListActive = "tasks.active"
	OperationTaskListRecent = "tasks.recent"
	OperationTaskGet        = "tasks.get"
)

type TaskActivePayload struct {
	GuestType string `json:"guestType,omitempty"`
	GuestID   uint   `json:"guestId,omitempty"`
	JSON      bool   `json:"json"`
}

type TaskRecentPayload struct {
	GuestType string `json:"guestType,omitempty"`
	GuestID   uint   `json:"guestId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	JSON      bool   `json:"json"`
}

type TaskGetPayload struct {
	TaskID uint `json:"taskId"`
	JSON   bool `json:"json"`
}
