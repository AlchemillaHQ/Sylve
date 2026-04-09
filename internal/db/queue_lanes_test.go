// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import "testing"

func TestResolveQueueLane(t *testing.T) {
	tests := []struct {
		name     string
		job      string
		expected string
	}{
		{name: "lifecycle exec", job: "guest-lifecycle-exec", expected: queueLaneLifecycleID},
		{name: "autostart", job: "guest-autostart-sequence", expected: queueLaneLifecycleID},
		{name: "wol", job: "utils-wol-process", expected: queueLaneLifecycleID},
		{name: "download start", job: "utils-download-start", expected: queueLaneDownloadsID},
		{name: "download postproc", job: "utils-download-postproc", expected: queueLaneDownloadsID},
		{name: "zelta backup", job: "zelta-backup-run", expected: queueLaneZeltaID},
		{name: "zelta restore", job: "zelta-restore-run", expected: queueLaneZeltaID},
		{name: "zelta replication", job: "zelta-replication-run", expected: queueLaneZeltaID},
		{name: "zfs maintenance", job: "zfs_history_batch", expected: queueLaneMaintenanceID},
		{name: "fallback", job: "some-custom-job", expected: queueLaneDefaultID},
		{name: "normalized", job: "  UTILS-DOWNLOAD-SYNC  ", expected: queueLaneDownloadsID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveQueueLane(tt.job)
			if got != tt.expected {
				t.Fatalf("resolveQueueLane(%q) = %q, want %q", tt.job, got, tt.expected)
			}
		})
	}
}
