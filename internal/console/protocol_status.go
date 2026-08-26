// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import "time"

const OperationStatus = "console.status"

type StatusPayload struct{}

// StatusSnapshot contains raw values so every console mode can use the same
// width-aware presentation. Nil values mean that a metric is unavailable.
type StatusSnapshot struct {
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	CPUUsage    *float64  `json:"cpuUsage,omitempty"`
	RAMUsed     *uint64   `json:"ramUsed,omitempty"`
	RAMTotal    *uint64   `json:"ramTotal,omitempty"`
	Uptime      *int64    `json:"uptime,omitempty"`
	ZFSHealth   *string   `json:"zfsHealth,omitempty"`
	VMRunning   *int      `json:"vmRunning,omitempty"`
	VMTotal     *int      `json:"vmTotal,omitempty"`
	JailRunning *int      `json:"jailRunning,omitempty"`
	JailTotal   *int      `json:"jailTotal,omitempty"`
	ActiveTasks *int64    `json:"activeTasks,omitempty"`
	CollectedAt time.Time `json:"collectedAt"`
	Stale       bool      `json:"stale,omitempty"`
}
