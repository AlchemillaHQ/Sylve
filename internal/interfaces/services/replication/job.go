// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package replicationServiceInterfaces

// BackupJobRunPayload is the payload for a queued backup job execution.
type BackupJobRunPayload struct {
	JobID  uint `json:"job_id"`
	Manual bool `json:"manual"`
}
