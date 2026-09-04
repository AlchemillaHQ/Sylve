// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

type backupRetentionScopeInventory struct {
	sourceRoot        string
	remoteRoot        string
	localSnapshots    []SnapshotInfo
	localSnapshotErr  error
	remoteSnapshots   []SnapshotInfo
	manifestInventory remoteBackupManifestInventory
}

func (s *Service) loadBackupRetentionScopeInventories(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) ([]backupRetentionScopeInventory, error) {
	if job == nil {
		return nil, fmt.Errorf("backup_job_required")
	}
	inventories := make([]backupRetentionScopeInventory, 0, len(scopes))
	seenRemoteRoots := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		sourceRoot := normalizeDatasetPath(scope.sourceDataset)
		if sourceRoot == "" {
			return nil, fmt.Errorf("backup_retention_scope_source_required")
		}
		remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		if _, duplicate := seenRemoteRoots[remoteRoot]; duplicate {
			return nil, fmt.Errorf("duplicate_backup_retention_scope:%s", remoteRoot)
		}
		seenRemoteRoots[remoteRoot] = struct{}{}
		remoteSnapshots, err := s.listRemoteSnapshotsForDatasetRecursive(
			ctx,
			&job.Target,
			remoteRoot,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"backup_retention_remote_inventory_failed: source=%s: %w",
				sourceRoot,
				err,
			)
		}
		manifestInventory, err := s.loadRemoteBackupManifestInventoryFromSnapshots(
			ctx,
			&job.Target,
			remoteRoot,
			remoteSnapshots,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"backup_retention_manifest_inventory_failed: source=%s: %w",
				sourceRoot,
				err,
			)
		}
		localSnapshots, localErr := s.listLocalSnapshotsForDataset(ctx, sourceRoot)
		inventories = append(inventories, backupRetentionScopeInventory{
			sourceRoot:        sourceRoot,
			remoteRoot:        remoteRoot,
			localSnapshots:    localSnapshots,
			localSnapshotErr:  localErr,
			remoteSnapshots:   remoteSnapshots,
			manifestInventory: manifestInventory,
		})
	}
	return inventories, nil
}

func backupRetentionManifestInventories(
	inventories []backupRetentionScopeInventory,
) map[string]remoteBackupManifestInventory {
	byRoot := make(map[string]remoteBackupManifestInventory, len(inventories))
	for _, inventory := range inventories {
		byRoot[inventory.remoteRoot] = inventory.manifestInventory
	}
	return byRoot
}

func backupRetentionSnapshotsForRoot(
	inventories []backupRetentionScopeInventory,
	root string,
) ([]SnapshotInfo, bool) {
	root = normalizeDatasetPath(root)
	for _, inventory := range inventories {
		if inventory.remoteRoot == root {
			return inventory.remoteSnapshots, true
		}
	}
	return nil, false
}

func (s *Service) backupRetentionEligibleSnapshotProofsFromInventories(
	ctx context.Context,
	job *clusterModels.BackupJob,
	commitRoot string,
	remoteSnapshots []SnapshotInfo,
	scopes []backupScope,
	inventories map[string]remoteBackupManifestInventory,
) (backupRetentionProofSet, error) {
	proofs := newBackupRetentionProofSet()
	if job == nil {
		return proofs, fmt.Errorf("backup_job_required")
	}
	commitRoot = normalizeDatasetPath(commitRoot)
	if commitRoot == "" {
		return proofs, fmt.Errorf("backup_retention_remote_root_required")
	}

	jobPrefix := backupSnapshotPrefixForJob(job.ID)
	shortNames := make([]string, 0, len(remoteSnapshots))
	remoteCommitSnapshots := make([]string, 0, len(remoteSnapshots))
	seen := make(map[string]struct{}, len(remoteSnapshots))
	for _, snapshot := range remoteSnapshots {
		shortName := strings.TrimPrefix(snapshotShortName(snapshot), "@")
		if !isBKSnapshotShortName(shortName, jobPrefix) {
			continue
		}
		if _, ok := seen[shortName]; ok {
			continue
		}
		seen[shortName] = struct{}{}
		if !backupSnapshotRequiresCommit(job.ID, shortName) {
			// Prefix ownership was the old contract and is not sufficient proof
			// for destructive cleanup. Keep legacy points indefinitely.
			continue
		}
		shortNames = append(shortNames, shortName)
		remoteCommitSnapshots = append(remoteCommitSnapshots, commitRoot+"@"+shortName)
	}

	metadataBySnapshot, err := s.getRemoteBackupCommitMetadataBatch(
		ctx,
		&job.Target,
		remoteCommitSnapshots,
	)
	if err != nil {
		return proofs, fmt.Errorf("backup_retention_commit_state_unavailable: %w", err)
	}
	for i, shortName := range shortNames {
		remoteSnapshot := remoteCommitSnapshots[i]
		result, ok := metadataBySnapshot[remoteSnapshot]
		if !ok {
			return proofs, fmt.Errorf(
				"backup_retention_commit_state_unavailable: snapshot=%s: metadata_missing",
				shortName,
			)
		}
		if result.Err != nil {
			if strings.Contains(result.Err.Error(), "backup_snapshot_not_committed") {
				continue
			}
			return proofs, fmt.Errorf(
				"backup_retention_commit_state_unavailable: snapshot=%s: %w",
				shortName,
				result.Err,
			)
		}
		metadata := result.Metadata
		if err := validateBackupRestoreCommit(metadata, job, shortName); err != nil {
			return proofs, fmt.Errorf(
				"backup_retention_commit_state_unavailable: snapshot=%s: %w",
				shortName,
				err,
			)
		}
		manifest, err := buildRemoteBackupManifestFromInventories(
			job,
			shortName,
			metadata.Recursive,
			scopes,
			inventories,
		)
		if err != nil {
			return proofs, fmt.Errorf(
				"backup_retention_manifest_unavailable: snapshot=%s: %w",
				shortName,
				err,
			)
		}
		if len(manifest.Entries) != metadata.EntryCount ||
			backupManifestHash(manifest) != metadata.ManifestHash {
			return proofs, fmt.Errorf("backup_retention_manifest_mismatch: snapshot=%s", shortName)
		}
		if err := addBackupManifestRetentionProofs(&proofs, job, manifest, scopes); err != nil {
			return proofs, fmt.Errorf(
				"backup_retention_manifest_proof_invalid: snapshot=%s: %w",
				shortName,
				err,
			)
		}
	}
	return proofs, nil
}

func localRetentionProtectSetFromSnapshots(
	localRootDataset string,
	remoteActiveDataset string,
	snapPrefix string,
	localSnapshots []SnapshotInfo,
	remoteSnapshots []SnapshotInfo,
) map[string]struct{} {
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

	bases := latestCommonBackupSnapshotsByDataset(
		localSnapshots,
		remoteSnapshots,
		localRootDataset,
		remoteActiveDataset,
		snapPrefix,
	)
	for _, base := range bases {
		if isValidZFSSnapshotName(base.Name) {
			protect[base.Name] = struct{}{}
		}
	}
	return protect
}
