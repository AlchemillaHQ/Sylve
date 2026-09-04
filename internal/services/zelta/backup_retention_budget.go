// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

const backupRetentionGenerationBudget = 64

type backupRetentionCleanupScope struct {
	inventory        backupRetentionScopeInventory
	localCandidates  []string
	targetCandidates []string
}

type backupRetentionCleanupStats struct {
	CandidateGenerations int
	SelectedGenerations  int
	DeferredGenerations  int
	CandidateSnapshots   int
	SelectedSnapshots    int
}

func buildBackupRetentionCleanupPlan(
	job *clusterModels.BackupJob,
	inventories []backupRetentionScopeInventory,
	proofs backupRetentionProofSet,
) []backupRetentionCleanupScope {
	if job == nil {
		return nil
	}
	snapPrefix := backupSnapshotPrefixForJob(job.ID)
	plans := make([]backupRetentionCleanupScope, 0, len(inventories))
	for _, inventory := range inventories {
		remoteSnapshots := filterBackupSnapshotsByProof(inventory.remoteSnapshots, proofs.Target)
		localSnapshots := filterBackupSnapshotsByProof(inventory.localSnapshots, proofs.Source)
		plan := backupRetentionCleanupScope{inventory: inventory}
		if inventory.localSnapshotErr == nil {
			protect := localRetentionProtectSetFromSnapshots(
				inventory.sourceRoot,
				inventory.remoteRoot,
				snapPrefix,
				inventory.localSnapshots,
				inventory.remoteSnapshots,
			)
			plan.localCandidates = buildLocalRetentionPruneCandidates(
				localSnapshots,
				job.PruneKeepLast,
				protect,
				snapPrefix,
			)
		}
		if job.PruneTarget {
			plan.targetCandidates = buildBKRetentionPruneCandidates(
				remoteSnapshots,
				job.PruneKeepLast,
				snapshotCandidateSet(snapshotNames(remoteSnapshots)),
				snapPrefix,
			)
		}
		plans = append(plans, plan)
	}
	return plans
}

func backupRetentionGeneration(snapshot string) string {
	snapshot = strings.TrimSpace(snapshot)
	at := strings.LastIndex(snapshot, "@")
	if at <= 0 || at == len(snapshot)-1 {
		return ""
	}
	return snapshot[at+1:]
}

func backupRetentionCreationIndex(plans []backupRetentionCleanupScope) map[string]time.Time {
	index := make(map[string]time.Time)
	for _, plan := range plans {
		for _, snapshots := range [][]SnapshotInfo{
			plan.inventory.localSnapshots,
			plan.inventory.remoteSnapshots,
		} {
			for _, snapshot := range snapshots {
				created, ok := parseSnapshotCreationTime(snapshot.Creation)
				if !ok {
					continue
				}
				name := strings.TrimSpace(snapshot.Name)
				if current, exists := index[name]; !exists || created.Before(current) {
					index[name] = created
				}
			}
		}
	}
	return index
}

func filterBackupRetentionCandidatesByGeneration(
	candidates []string,
	selected map[string]struct{},
) []string {
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		generation := backupRetentionGeneration(candidate)
		if _, ok := selected[generation]; ok {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func limitBackupRetentionCleanupPlan(
	plans []backupRetentionCleanupScope,
	generationBudget int,
) ([]backupRetentionCleanupScope, backupRetentionCleanupStats) {
	type generationInfo struct {
		name      string
		created   time.Time
		knownTime bool
		firstSeen int
	}

	stats := backupRetentionCleanupStats{}
	createdBySnapshot := backupRetentionCreationIndex(plans)
	byGeneration := make(map[string]*generationInfo)
	ordered := make([]*generationInfo, 0)
	firstSeen := 0
	for _, plan := range plans {
		for _, candidates := range [][]string{plan.localCandidates, plan.targetCandidates} {
			stats.CandidateSnapshots += len(candidates)
			for _, candidate := range candidates {
				generation := backupRetentionGeneration(candidate)
				if generation == "" {
					continue
				}
				info := byGeneration[generation]
				if info == nil {
					info = &generationInfo{name: generation, firstSeen: firstSeen}
					firstSeen++
					byGeneration[generation] = info
					ordered = append(ordered, info)
				}
				if created, ok := createdBySnapshot[strings.TrimSpace(candidate)]; ok &&
					(!info.knownTime || created.Before(info.created)) {
					info.created = created
					info.knownTime = true
				}
			}
		}
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if left.knownTime && right.knownTime && !left.created.Equal(right.created) {
			return left.created.Before(right.created)
		}
		if left.knownTime != right.knownTime {
			return left.knownTime
		}
		return left.firstSeen < right.firstSeen
	})
	stats.CandidateGenerations = len(ordered)
	if generationBudget < 0 {
		generationBudget = 0
	}
	if generationBudget > len(ordered) {
		generationBudget = len(ordered)
	}
	selected := make(map[string]struct{}, generationBudget)
	for _, generation := range ordered[:generationBudget] {
		selected[generation.name] = struct{}{}
	}
	stats.SelectedGenerations = len(selected)
	stats.DeferredGenerations = stats.CandidateGenerations - stats.SelectedGenerations

	limited := make([]backupRetentionCleanupScope, 0, len(plans))
	for _, plan := range plans {
		plan.localCandidates = filterBackupRetentionCandidatesByGeneration(plan.localCandidates, selected)
		plan.targetCandidates = filterBackupRetentionCandidatesByGeneration(plan.targetCandidates, selected)
		stats.SelectedSnapshots += len(plan.localCandidates) + len(plan.targetCandidates)
		limited = append(limited, plan)
	}
	return limited, stats
}
