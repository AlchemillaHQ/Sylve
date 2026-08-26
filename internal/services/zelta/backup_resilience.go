// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

type backupScope struct {
	sourceDataset string
	destSuffix    string
}

func (s *Service) backupRunScopes(job *clusterModels.BackupJob, sourceDataset, destSuffix string, vmSourceDatasets []string) []backupScope {
	if job != nil && job.Mode == clusterModels.BackupJobModeVM {
		scopes := make([]backupScope, 0, len(vmSourceDatasets))
		for _, vmSource := range vmSourceDatasets {
			scopes = append(scopes, backupScope{
				sourceDataset: vmSource,
				destSuffix:    s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), vmSource),
			})
		}
		return scopes
	}
	return []backupScope{{sourceDataset: sourceDataset, destSuffix: destSuffix}}
}

func snapshotCreationTime(info SnapshotInfo) (time.Time, bool) {
	raw := strings.TrimSpace(info.Creation)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(epoch, 0).UTC(), true
	}
	return time.Time{}, false
}

func snapshotGUIDKey(info SnapshotInfo) string {
	return strings.TrimSpace(info.Guid)
}

func latestCommonBackupSnapshot(local, remote []SnapshotInfo, snapPrefix string) (SnapshotInfo, bool) {
	remoteGUIDs := make(map[string]struct{})
	for _, r := range remote {
		short := strings.TrimPrefix(snapshotShortName(r), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}
		if g := snapshotGUIDKey(r); g != "" {
			remoteGUIDs[g] = struct{}{}
		}
	}

	var best SnapshotInfo
	found := false
	for _, l := range local {
		short := strings.TrimPrefix(snapshotShortName(l), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}

		guid := snapshotGUIDKey(l)
		if guid == "" {
			continue
		}
		if _, common := remoteGUIDs[guid]; !common {
			continue
		}

		best = l
		found = true
	}
	return best, found
}

func latestCommonBackupSnapshotsByDataset(
	local []SnapshotInfo,
	remote []SnapshotInfo,
	localRoot string,
	remoteRoot string,
	snapPrefix string,
) map[string]SnapshotInfo {
	localRoot = normalizeDatasetPath(localRoot)
	remoteRoot = normalizeDatasetPath(remoteRoot)
	if localRoot == "" || remoteRoot == "" {
		return map[string]SnapshotInfo{}
	}

	remoteGUIDsBySuffix := make(map[string]map[string]struct{})
	for _, snapshot := range remote {
		short := strings.TrimPrefix(snapshotShortName(snapshot), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}
		guid := snapshotGUIDKey(snapshot)
		if guid == "" {
			continue
		}
		dataset := snapshotDatasetName(snapshot.Name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		if !datasetWithinRoot(remoteRoot, dataset) {
			continue
		}
		suffix := relativeDatasetSuffix(remoteRoot, dataset)
		guids := remoteGUIDsBySuffix[suffix]
		if guids == nil {
			guids = make(map[string]struct{})
			remoteGUIDsBySuffix[suffix] = guids
		}
		guids[guid] = struct{}{}
	}

	bestByDataset := make(map[string]SnapshotInfo)
	for _, snapshot := range local {
		short := strings.TrimPrefix(snapshotShortName(snapshot), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}
		guid := snapshotGUIDKey(snapshot)
		if guid == "" {
			continue
		}
		dataset := snapshotDatasetName(snapshot.Name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		if !datasetWithinRoot(localRoot, dataset) {
			continue
		}
		suffix := relativeDatasetSuffix(localRoot, dataset)
		remoteGUIDs := remoteGUIDsBySuffix[suffix]
		if remoteGUIDs == nil {
			continue
		}
		if _, common := remoteGUIDs[guid]; common {
			bestByDataset[dataset] = snapshot
		}
	}

	return bestByDataset
}

func foreignTargetSnapshots(
	local, remote []SnapshotInfo,
	sourceRoot, targetRoot string,
	provenTargetSnapshots map[string]string,
) []string {
	sourceRoot = normalizeDatasetPath(sourceRoot)
	targetRoot = normalizeDatasetPath(targetRoot)

	// A GUID match is meaningful only at the corresponding dataset suffix. A
	// global GUID set would allow a snapshot from one child to vouch for a
	// different child, while a name match is not ownership proof at all.
	sourceBySuffixAndGUID := make(map[string]struct{})
	for _, snapshot := range local {
		dataset := snapshotDatasetName(snapshot.Name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		guid := snapshotGUIDKey(snapshot)
		if guid == "" || !datasetWithinRoot(sourceRoot, dataset) {
			continue
		}
		suffix := relativeDatasetSuffix(sourceRoot, dataset)
		sourceBySuffixAndGUID[suffix+"\x00"+guid] = struct{}{}
	}

	foreign := make([]string, 0)
	for _, snapshot := range remote {
		name := strings.TrimSpace(snapshot.Name)
		dataset := snapshotDatasetName(name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		guid := snapshotGUIDKey(snapshot)
		owned := false
		if guid != "" && datasetWithinRoot(targetRoot, dataset) {
			suffix := relativeDatasetSuffix(targetRoot, dataset)
			_, owned = sourceBySuffixAndGUID[suffix+"\x00"+guid]
		}
		if !owned && guid != "" {
			expectedGUID, proven := provenTargetSnapshots[name]
			owned = proven && expectedGUID == guid
		}
		if owned {
			continue
		}
		if name == "" {
			name = "<unnamed-target-snapshot>"
		}
		foreign = append(foreign, name)
	}
	return foreign
}

// filterToleratedLegacyTargetSnapshots removes pre-c1 snapshots that the old
// backup protocol associated with this exact job scope by name from a
// non-destructive foreign-snapshot preflight result. It does not produce
// ownership proof and must never authorize retention cleanup or deletion.
func filterToleratedLegacyTargetSnapshots(
	job *clusterModels.BackupJob,
	sourceRoot string,
	targetRoot string,
	scopes []backupScope,
	foreign []string,
) []string {
	if job == nil || job.ID == 0 {
		return foreign
	}

	sourceRoot = normalizeDatasetPath(sourceRoot)
	targetRoot = normalizeDatasetPath(targetRoot)
	backupRoot := normalizeDatasetPath(job.Target.BackupRoot)
	if !datasetWithinRoot(backupRoot, targetRoot) {
		return foreign
	}

	canonicalScope := false
	for _, scope := range scopes {
		if normalizeDatasetPath(scope.sourceDataset) != sourceRoot {
			continue
		}
		expectedTarget := remoteActiveDatasetForSuffix(backupRoot, scope.destSuffix)
		if expectedTarget == targetRoot {
			canonicalScope = true
			break
		}
	}
	if !canonicalScope {
		return foreign
	}

	jobPrefix := backupSnapshotPrefixForJob(job.ID)
	remaining := make([]string, 0, len(foreign))
	for _, candidate := range foreign {
		name := strings.TrimSpace(candidate)
		dataset := snapshotDatasetName(name)
		shortName := strings.TrimPrefix(snapshotShortName(SnapshotInfo{Name: name}), "@")
		_, c1, err := backupCommitJobIDFromSnapshot(shortName)
		if !datasetWithinRoot(targetRoot, dataset) ||
			!isBKSnapshotShortName(shortName, jobPrefix) || err != nil || c1 {
			remaining = append(remaining, candidate)
		}
	}
	return remaining
}

func buildLocalRetentionPruneCandidates(snapshots []SnapshotInfo, keepCount int, protect map[string]struct{}, snapPrefix string) []string {
	if keepCount < 0 {
		keepCount = 0
	}

	grouped := make(map[string][]string)
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if !isValidZFSSnapshotName(name) {
			continue
		}
		if !isBKSnapshotShortName(snapshotShortName(snapshot), snapPrefix) {
			continue
		}
		dataset := snapshotDatasetName(name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		grouped[dataset] = append(grouped[dataset], name)
	}

	if len(grouped) == 0 {
		return []string{}
	}

	datasets := make([]string, 0, len(grouped))
	for dataset := range grouped {
		datasets = append(datasets, dataset)
	}
	sort.Strings(datasets)

	candidates := make([]string, 0)
	for _, dataset := range datasets {
		names := grouped[dataset]
		if len(names) <= keepCount {
			continue
		}
		deleteUpTo := len(names) - keepCount
		for i := 0; i < deleteUpTo; i++ {
			if protect != nil {
				if _, ok := protect[names[i]]; ok {
					continue
				}
			}
			candidates = append(candidates, names[i])
		}
	}

	return candidates
}

func (s *Service) localRetentionProtectSet(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	localRootDataset string,
	remoteActiveDataset string,
	snapPrefix string,
	localSnapshots []SnapshotInfo,
) (map[string]struct{}, error) {
	protect := make(map[string]struct{})

	newestPerDataset := make(map[string]SnapshotInfo)
	for _, snap := range localSnapshots {
		if !isBKSnapshotShortName(snapshotShortName(snap), snapPrefix) {
			continue
		}
		dataset := snapshotDatasetName(snap.Name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snap.Dataset)
		}
		newestPerDataset[dataset] = snap
	}
	for _, snap := range newestPerDataset {
		if isValidZFSSnapshotName(snap.Name) {
			protect[snap.Name] = struct{}{}
		}
	}

	remoteActiveDataset = normalizeDatasetPath(remoteActiveDataset)
	if target != nil && remoteActiveDataset != "" {
		remoteSnaps, err := s.listRemoteSnapshotsForDatasetRecursive(ctx, target, remoteActiveDataset)
		if err != nil {
			return protect, fmt.Errorf("list_recursive_remote_retention_snapshots_failed: %w", err)
		}
		bases := latestCommonBackupSnapshotsByDataset(
			localSnapshots,
			remoteSnaps,
			localRootDataset,
			remoteActiveDataset,
			snapPrefix,
		)
		for _, base := range bases {
			if isValidZFSSnapshotName(base.Name) {
				protect[base.Name] = struct{}{}
			}
		}
	}

	return protect, nil
}

func (s *Service) findForeignTargetSnapshots(
	ctx context.Context,
	job *clusterModels.BackupJob,
	sourceDataset string,
	activeDataset string,
	scopes []backupScope,
) ([]string, error) {
	sourceDataset = normalizeDatasetPath(sourceDataset)
	activeDataset = normalizeDatasetPath(activeDataset)
	if job == nil || sourceDataset == "" || activeDataset == "" {
		return nil, nil
	}
	target := &job.Target
	if strings.TrimSpace(target.SSHHost) == "" {
		return nil, nil
	}
	if !datasetWithinRoot(target.BackupRoot, activeDataset) {
		return nil, fmt.Errorf("active_dataset_outside_backup_root")
	}

	exists, err := s.targetDatasetExists(ctx, target, activeDataset)
	if err != nil || !exists {
		return nil, err
	}

	remoteSnaps, err := s.listRemoteSnapshotsForDatasetRecursive(ctx, target, activeDataset)
	if err != nil {
		return nil, err
	}
	localSnaps, err := s.listLocalSnapshotsForDataset(ctx, sourceDataset)
	if err != nil {
		return nil, err
	}
	foreign := foreignTargetSnapshots(localSnaps, remoteSnaps, sourceDataset, activeDataset, nil)
	if len(foreign) == 0 {
		return nil, nil
	}

	// A source-retention policy may have removed an old local c1 snapshot while
	// the target intentionally retained it. Permit that target-only point only
	// after revalidating its exact commit and complete manifest; never infer
	// ownership from the bk_ prefix.
	foreignSet := make(map[string]struct{}, len(foreign))
	for _, name := range foreign {
		foreignSet[name] = struct{}{}
	}
	unmatched := make([]SnapshotInfo, 0, len(foreign))
	for _, snapshot := range remoteSnaps {
		if _, ok := foreignSet[strings.TrimSpace(snapshot.Name)]; ok {
			unmatched = append(unmatched, snapshot)
		}
	}
	proofs, err := s.backupRetentionEligibleSnapshotProofs(
		ctx,
		job,
		activeDataset,
		unmatched,
		scopes,
	)
	if err != nil {
		return nil, fmt.Errorf("prove_target_only_backup_snapshots_failed: %w", err)
	}

	foreign = foreignTargetSnapshots(
		localSnaps,
		remoteSnaps,
		sourceDataset,
		activeDataset,
		proofs.Target,
	)
	return filterToleratedLegacyTargetSnapshots(
		job,
		sourceDataset,
		activeDataset,
		scopes,
		foreign,
	), nil
}

func remoteActiveDatasetForSuffix(backupRoot, destSuffix string) string {
	backupRoot = normalizeDatasetPath(strings.TrimSpace(backupRoot))
	destSuffix = normalizeDatasetPath(destSuffix)
	if backupRoot == "" {
		return destSuffix
	}
	if destSuffix == "" {
		return backupRoot
	}
	return normalizeDatasetPath(backupRoot + "/" + destSuffix)
}
