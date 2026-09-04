// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

type remoteRestoreDatasetPlan struct {
	preferredRemoteDataset string
	resolvedRemoteDataset  string
	snapshot               string
	commitMetadata         backupCommitMetadata
	restoreRecursive       bool
	expectedManifest       []restoreDatasetManifestEntry
	manifestInventory      remoteBackupManifestInventory
}

func remoteSnapshotMissingError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "snapshot does not exist") ||
		strings.Contains(lower, "dataset does not exist") ||
		(strings.Contains(lower, "cannot open") && strings.Contains(lower, "does not exist"))
}

// resolveRemoteDatasetForSnapshot checks only the selected generation in
// each lineage. Its work is independent of the number of retained snapshots.
func (s *Service) resolveRemoteDatasetForSnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	preferredDataset string,
	snapshot string,
) (string, error) {
	preferredDataset = normalizeDatasetPath(preferredDataset)
	if preferredDataset == "" {
		return "", fmt.Errorf("remote_dataset_required")
	}
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return "", err
	}
	if !datasetWithinRoot(target.BackupRoot, preferredDataset) {
		return "", fmt.Errorf("remote_dataset_outside_backup_root")
	}

	lineageDatasets, err := s.listRemoteLineageDatasets(ctx, target, preferredDataset)
	if err != nil {
		return "", err
	}
	matches := make([]SnapshotInfo, 0, 1)
	for _, dataset := range lineageDatasets {
		fullName := dataset + snapshot
		output, listErr := s.runTargetSSH(
			ctx,
			target,
			"zfs", "list", "-H", "-p", "-t", "snapshot",
			"-o", "name,creation,used,refer,guid,encryption",
			fullName,
		)
		if listErr != nil {
			if remoteSnapshotMissingError(listErr) {
				continue
			}
			return "", fmt.Errorf("failed_to_resolve_remote_snapshot: %w", listErr)
		}
		listed := parseSnapshotInfoOutput(output)
		if len(listed) != 1 || strings.TrimSpace(listed[0].Name) != fullName {
			return "", fmt.Errorf("remote_snapshot_identity_ambiguous")
		}
		if dataset == preferredDataset {
			return preferredDataset, nil
		}
		matches = append(matches, listed[0])
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("snapshot_not_found_on_target")
	}
	sort.Slice(matches, func(i, j int) bool {
		ti, okI := parseSnapshotCreationTime(matches[i].Creation)
		tj, okJ := parseSnapshotCreationTime(matches[j].Creation)
		if okI && okJ && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if okI != okJ {
			return okI
		}
		return matches[i].Name < matches[j].Name
	})
	resolved := snapshotDatasetName(matches[len(matches)-1].Name)
	if resolved == "" || !datasetWithinRoot(target.BackupRoot, resolved) {
		return "", fmt.Errorf("remote_dataset_outside_backup_root")
	}
	return resolved, nil
}

func sortedBackupInventoryDatasets(datasets map[string]string) []string {
	names := make([]string, 0, len(datasets))
	for dataset := range datasets {
		names = append(names, dataset)
	}
	sort.Strings(names)
	return names
}

// loadRemoteRestoreSnapshotInventory lists current topology once and then asks
// ZFS for only the selected snapshot on each dataset. It never enumerates the
// retained snapshot history.
func (s *Service) loadRemoteRestoreSnapshotInventory(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
	snapshot string,
	recursive bool,
) (remoteBackupManifestInventory, error) {
	parsedRoot, err := canonicalTargetDataset(target, remoteRoot)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("remote_restore_root_invalid: %w", err)
	}
	snapshot, err = normalizeSnapshotName(snapshot)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}
	remoteRoot = parsedRoot.String()
	datasetArgs := backupDatasetListArgs(remoteRoot, recursive)
	datasetOutput, err := s.runTargetSSH(ctx, target, datasetArgs...)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("list_restore_dataset_tree_failed: %w", err)
	}
	datasets, err := parseBackupDatasetTree(datasetOutput, remoteRoot, recursive)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}

	datasetNames := sortedBackupInventoryDatasets(datasets)
	guidsByName := make(map[string]map[string]string)
	for start := 0; start < len(datasetNames); start += backupCommitMetadataBatchSize {
		end := start + backupCommitMetadataBatchSize
		if end > len(datasetNames) {
			end = len(datasetNames)
		}
		exactSnapshots := make([]string, 0, end-start)
		for _, dataset := range datasetNames[start:end] {
			exactSnapshots = append(exactSnapshots, dataset+snapshot)
		}
		args := []string{"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid"}
		args = append(args, exactSnapshots...)
		snapshotOutput, listErr := s.runTargetSSH(ctx, target, args...)
		if listErr != nil {
			return remoteBackupManifestInventory{}, fmt.Errorf("list_restore_snapshot_tree_failed: %w", listErr)
		}
		batch, parseErr := parseBackupSnapshotGUIDInventory(snapshotOutput, remoteRoot)
		if parseErr != nil {
			return remoteBackupManifestInventory{}, fmt.Errorf("parse_restore_snapshot_tree_failed: %w", parseErr)
		}
		for shortName, byDataset := range batch {
			if shortName != strings.TrimPrefix(snapshot, "@") {
				return remoteBackupManifestInventory{}, fmt.Errorf("unexpected_restore_snapshot_generation")
			}
			current := guidsByName[shortName]
			if current == nil {
				current = make(map[string]string)
				guidsByName[shortName] = current
			}
			for dataset, guid := range byDataset {
				if _, duplicate := current[dataset]; duplicate {
					return remoteBackupManifestInventory{}, fmt.Errorf("duplicate_backup_manifest_snapshot:%s", dataset)
				}
				current[dataset] = guid
			}
		}
	}
	return newRemoteBackupManifestInventory(remoteRoot, datasets, guidsByName)
}

func verifySingleRootBackupCommitFromInventory(
	metadata backupCommitMetadata,
	snapshot string,
	inventory remoteBackupManifestInventory,
) error {
	if metadata.Version != backupCommitVersion {
		return nil
	}
	if len(metadata.Roots) != 1 {
		return fmt.Errorf("restore_backup_commit_root_count_invalid")
	}
	entries, err := inventory.manifestEntries(
		metadata.Roots[0],
		snapshot,
		metadata.Recursive,
		false,
	)
	if err != nil {
		return fmt.Errorf("restore_backup_manifest_read_failed: %w", err)
	}
	manifest, err := buildBackupManifest(
		metadata.JobID,
		snapshot,
		metadata.Recursive,
		entries,
	)
	if err != nil {
		return fmt.Errorf("restore_backup_manifest_invalid: %w", err)
	}
	if len(manifest.Entries) != metadata.EntryCount ||
		backupManifestHash(manifest) != metadata.ManifestHash {
		return fmt.Errorf("restore_backup_manifest_mismatch")
	}
	return nil
}

func (s *Service) prepareJobRestoreDatasetPlan(
	ctx context.Context,
	job *clusterModels.BackupJob,
	preferredRemoteDataset string,
	snapshot string,
) (remoteRestoreDatasetPlan, error) {
	if job == nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("backup_job_required")
	}
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return remoteRestoreDatasetPlan{}, err
	}
	resolved, err := s.resolveRemoteDatasetForSnapshot(
		ctx,
		&job.Target,
		preferredRemoteDataset,
		snapshot,
	)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("resolve_restore_snapshot_dataset_failed: %w", err)
	}
	metadata, err := s.requireRemoteBackupRestoreCommit(ctx, job, resolved, snapshot)
	if err != nil {
		return remoteRestoreDatasetPlan{}, err
	}
	recursive := job.Recursive
	if backupSnapshotRequiresCommit(job.ID, snapshot) {
		recursive = metadata.Recursive
	}
	inventory, err := s.loadRemoteRestoreSnapshotInventory(ctx, &job.Target, resolved, snapshot, recursive)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("restore_preflight_snapshot_failed: %w", err)
	}
	if err := verifySingleRootBackupCommitFromInventory(metadata, snapshot, inventory); err != nil {
		return remoteRestoreDatasetPlan{}, err
	}
	expected, err := inventory.restoreManifest(snapshot, recursive)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("restore_preflight_snapshot_failed: %w", err)
	}
	return remoteRestoreDatasetPlan{
		preferredRemoteDataset: preferredRemoteDataset,
		resolvedRemoteDataset:  resolved,
		snapshot:               snapshot,
		commitMetadata:         metadata,
		restoreRecursive:       recursive,
		expectedManifest:       expected,
		manifestInventory:      inventory,
	}, nil
}

func (s *Service) prepareTargetRestoreDatasetPlan(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	preferredRemoteDataset string,
	snapshot string,
	restoreRecursive bool,
) (remoteRestoreDatasetPlan, error) {
	snapshot, err := normalizeSnapshotName(snapshot)
	if err != nil {
		return remoteRestoreDatasetPlan{}, err
	}
	resolved, err := s.resolveRemoteDatasetForSnapshot(
		ctx,
		target,
		preferredRemoteDataset,
		snapshot,
	)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("resolve_restore_snapshot_dataset_failed: %w", err)
	}
	metadata, err := s.requireRemoteBackupRestoreCommitBySnapshot(ctx, target, resolved, snapshot)
	if err != nil {
		return remoteRestoreDatasetPlan{}, err
	}
	inventory, err := s.loadRemoteRestoreSnapshotInventory(
		ctx,
		target,
		resolved,
		snapshot,
		restoreRecursive,
	)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("restore_preflight_recursive_snapshot_failed: %w", err)
	}
	if metadata.Version == backupCommitVersion && len(metadata.Roots) == 1 {
		if err := verifySingleRootBackupCommitFromInventory(metadata, snapshot, inventory); err != nil {
			return remoteRestoreDatasetPlan{}, err
		}
	}
	expected, err := inventory.restoreManifest(snapshot, restoreRecursive)
	if err != nil {
		return remoteRestoreDatasetPlan{}, fmt.Errorf("restore_preflight_recursive_snapshot_failed: %w", err)
	}
	return remoteRestoreDatasetPlan{
		preferredRemoteDataset: preferredRemoteDataset,
		resolvedRemoteDataset:  resolved,
		snapshot:               snapshot,
		commitMetadata:         metadata,
		restoreRecursive:       restoreRecursive,
		expectedManifest:       expected,
		manifestInventory:      inventory,
	}, nil
}

func (s *Service) recheckRemoteRestoreDatasetPlan(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	plan remoteRestoreDatasetPlan,
) error {
	current, err := s.loadRemoteRestoreSnapshotInventory(
		ctx,
		target,
		plan.resolvedRemoteDataset,
		plan.snapshot,
		plan.restoreRecursive,
	)
	if err != nil {
		return err
	}
	actual, err := current.restoreManifest(plan.snapshot, plan.restoreRecursive)
	if err != nil {
		return err
	}
	problems := compareRestoreDatasetManifests(plan.expectedManifest, actual)
	if len(problems) > 0 {
		return fmt.Errorf("restore_snapshot_changed_after_preflight: %s", strings.Join(problems, ","))
	}
	return nil
}
