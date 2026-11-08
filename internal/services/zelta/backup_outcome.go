// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import "strings"

const (
	backupErrorSourceMissing         = "backup_source_missing"
	backupErrorSourceSnapshotMissing = "backup_source_snapshot_missing"
	backupErrorSourceSnapshotFailed  = "backup_source_snapshot_failed"
	backupErrorTargetLocalWrites     = "backup_target_has_local_writes"
	backupErrorTargetDiverged        = "backup_target_diverged"
)

func backupOutputErrorCode(output string) string {
	lower := strings.ToLower(output)

	switch {
	case strings.Contains(lower, "no common snapshot (diverged)"),
		strings.Contains(lower, "no snapshot; target diverged"):
		return backupErrorTargetDiverged
	case strings.Contains(lower, "target has local writes"):
		return backupErrorTargetLocalWrites
	case strings.Contains(lower, "target has diverged"), strings.Contains(lower, "target diverged"):
		return backupErrorTargetDiverged
	case strings.Contains(lower, "no source snapshot"):
		return backupErrorSourceSnapshotMissing
	case strings.Contains(lower, "no source:"):
		return backupErrorSourceMissing
	default:
		return ""
	}
}

func shouldAutoRotateBackupErrorCode(code string) bool {
	switch strings.TrimSpace(code) {
	case backupErrorTargetDiverged, backupErrorTargetLocalWrites:
		return true
	default:
		return false
	}
}
