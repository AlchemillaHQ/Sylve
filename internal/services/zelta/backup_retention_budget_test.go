// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"fmt"
	"testing"
	"time"
)

func retentionBudgetFixture(generations int) []backupRetentionCleanupScope {
	plans := make([]backupRetentionCleanupScope, 2)
	for scopeIndex := range plans {
		for generationIndex := 0; generationIndex < generations; generationIndex++ {
			generation := fmt.Sprintf("bk_j1_c1_generation-%03d", generationIndex)
			creation := time.Unix(int64(1700000000+generationIndex), 0).UTC().Format(time.RFC3339)
			local := fmt.Sprintf("source/%d@%s", scopeIndex, generation)
			target := fmt.Sprintf("backup/%d@%s", scopeIndex, generation)
			plans[scopeIndex].localCandidates = append(plans[scopeIndex].localCandidates, local)
			plans[scopeIndex].targetCandidates = append(plans[scopeIndex].targetCandidates, target)
			plans[scopeIndex].inventory.localSnapshots = append(
				plans[scopeIndex].inventory.localSnapshots,
				SnapshotInfo{Name: local, Creation: creation},
			)
			plans[scopeIndex].inventory.remoteSnapshots = append(
				plans[scopeIndex].inventory.remoteSnapshots,
				SnapshotInfo{Name: target, Creation: creation},
			)
		}
	}
	return plans
}

func cleanupPlanGenerationCounts(plans []backupRetentionCleanupScope) map[string]int {
	counts := make(map[string]int)
	for _, plan := range plans {
		for _, candidates := range [][]string{plan.localCandidates, plan.targetCandidates} {
			for _, candidate := range candidates {
				counts[backupRetentionGeneration(candidate)]++
			}
		}
	}
	return counts
}

func TestLimitBackupRetentionCleanupPlanKeepsGenerationsWhole(t *testing.T) {
	plans := retentionBudgetFixture(130)
	limited, stats := limitBackupRetentionCleanupPlan(plans, backupRetentionGenerationBudget)
	if stats.CandidateGenerations != 130 || stats.SelectedGenerations != 64 || stats.DeferredGenerations != 66 {
		t.Fatalf("unexpected cleanup stats: %#v", stats)
	}
	if stats.CandidateSnapshots != 520 || stats.SelectedSnapshots != 256 {
		t.Fatalf("unexpected snapshot stats: %#v", stats)
	}
	for generation, count := range cleanupPlanGenerationCounts(limited) {
		if count != 4 {
			t.Fatalf("generation %q was split: selected %d of 4 snapshots", generation, count)
		}
	}
}

func TestLimitBackupRetentionCleanupPlanContinuesBacklogOnNextPass(t *testing.T) {
	plans := retentionBudgetFixture(130)
	first, _ := limitBackupRetentionCleanupPlan(plans, 64)
	firstGenerations := cleanupPlanGenerationCounts(first)

	remaining := retentionBudgetFixture(130)
	for index := range remaining {
		filterRemaining := func(candidates []string) []string {
			out := candidates[:0]
			for _, candidate := range candidates {
				if _, removed := firstGenerations[backupRetentionGeneration(candidate)]; !removed {
					out = append(out, candidate)
				}
			}
			return out
		}
		remaining[index].localCandidates = filterRemaining(remaining[index].localCandidates)
		remaining[index].targetCandidates = filterRemaining(remaining[index].targetCandidates)
	}

	second, stats := limitBackupRetentionCleanupPlan(remaining, 64)
	if stats.CandidateGenerations != 66 || stats.SelectedGenerations != 64 || stats.DeferredGenerations != 2 {
		t.Fatalf("unexpected second-pass stats: %#v", stats)
	}
	for generation := range cleanupPlanGenerationCounts(second) {
		if _, repeated := firstGenerations[generation]; repeated {
			t.Fatalf("generation %q repeated instead of advancing", generation)
		}
	}
}
