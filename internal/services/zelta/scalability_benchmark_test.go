// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import "testing"

func BenchmarkPaginateSnapshotCandidatesLargeHistory(b *testing.B) {
	candidates := snapshotPageFixture(10_000)
	request := SnapshotPageRequest{Limit: DefaultSnapshotPageLimit}
	b.ResetTimer()
	for range b.N {
		if _, _, _, err := paginateSnapshotCandidates(candidates, request); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLimitBackupRetentionCleanupPlanLargeBacklog(b *testing.B) {
	plans := retentionBudgetFixture(10_000)
	b.ResetTimer()
	for range b.N {
		_, stats := limitBackupRetentionCleanupPlan(plans, backupRetentionGenerationBudget)
		if stats.SelectedGenerations != backupRetentionGenerationBudget {
			b.Fatalf("selected generations = %d", stats.SelectedGenerations)
		}
	}
}
