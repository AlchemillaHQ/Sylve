// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import "strings"

type backupOutputKind string

const (
	backupOutputUnknown                   backupOutputKind = "unknown"
	backupOutputUpToDate                  backupOutputKind = "up_to_date"
	backupOutputBlockedNoSource           backupOutputKind = "blocked_no_source"
	backupOutputBlockedNoSourceSnapshot   backupOutputKind = "blocked_no_source_snapshot"
	backupOutputBlockedNoSnapshotDiverged backupOutputKind = "blocked_no_snapshot_diverged"
	backupOutputBlockedNoCommonSnapshot   backupOutputKind = "blocked_no_common_snapshot"
	backupOutputBlockedTargetLocalWrites  backupOutputKind = "blocked_target_local_writes"
	backupOutputBlockedTargetDiverged     backupOutputKind = "blocked_target_diverged"
)

func classifyBackupOutput(output string) backupOutputKind {
	lower := strings.ToLower(output)

	switch {
	case strings.Contains(lower, "no common snapshot (diverged)"):
		return backupOutputBlockedNoCommonSnapshot
	case strings.Contains(lower, "no snapshot; target diverged"):
		return backupOutputBlockedNoSnapshotDiverged
	case strings.Contains(lower, "target has local writes"):
		return backupOutputBlockedTargetLocalWrites
	case strings.Contains(lower, "target has diverged"), strings.Contains(lower, "target diverged"):
		return backupOutputBlockedTargetDiverged
	case strings.Contains(lower, "no source snapshot"):
		return backupOutputBlockedNoSourceSnapshot
	case strings.Contains(lower, "no source:"):
		return backupOutputBlockedNoSource
	case strings.Contains(lower, "up-to-date"):
		return backupOutputUpToDate
	default:
		return backupOutputUnknown
	}
}

func (k backupOutputKind) errorCode() string {
	switch k {
	case backupOutputBlockedNoSource:
		return "backup_source_missing"
	case backupOutputBlockedNoSourceSnapshot:
		return "backup_source_snapshot_missing"
	case backupOutputBlockedTargetLocalWrites:
		return "backup_target_has_local_writes"
	case backupOutputBlockedNoSnapshotDiverged,
		backupOutputBlockedNoCommonSnapshot,
		backupOutputBlockedTargetDiverged:
		return "backup_target_diverged"
	default:
		return ""
	}
}
