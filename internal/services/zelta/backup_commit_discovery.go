// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/remoteexec"
)

func snapshotCommitLookupName(
	target *clusterModels.BackupTarget,
	snapshot SnapshotInfo,
) (string, error) {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return "", err
	}
	remoteDataset := snapshotDatasetName(snapshot.Name)
	if remoteDataset == "" {
		remoteDataset = normalizeDatasetPath(snapshot.Dataset)
	}
	shortName := strings.TrimPrefix(snapshotShortName(snapshot), "@")
	parsed, err := remoteexec.ParseZFSSnapshot(remoteDataset + "@" + shortName)
	if err != nil || !parsed.Dataset().Within(root) {
		return "", fmt.Errorf("invalid_backup_commit_snapshot")
	}
	return parsed.String(), nil
}

func (s *Service) filterRestorableBackupSnapshots(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshots []SnapshotInfo,
) ([]SnapshotInfo, error) {
	if job == nil {
		return nil, fmt.Errorf("backup_job_required")
	}
	lookupByIndex := make(map[int]string)
	lookups := make([]string, 0, len(snapshots))
	for i, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		if !backupSnapshotRequiresCommit(job.ID, shortName) {
			continue
		}
		lookup, err := snapshotCommitLookupName(&job.Target, snapshot)
		if err != nil {
			continue
		}
		lookupByIndex[i] = lookup
		lookups = append(lookups, lookup)
	}
	metadataBySnapshot, err := s.getRemoteBackupCommitMetadataBatch(ctx, &job.Target, lookups)
	if err != nil {
		return nil, err
	}

	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for i, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		if !backupSnapshotRequiresCommit(job.ID, shortName) {
			snapshot.Legacy = true
			filtered = append(filtered, snapshot)
			continue
		}
		lookup, ok := lookupByIndex[i]
		if !ok {
			continue
		}
		result, ok := metadataBySnapshot[lookup]
		if !ok || result.Err != nil {
			continue
		}
		if err := validateBackupRestoreCommit(result.Metadata, job, shortName); err != nil {
			continue
		}
		snapshot.Committed = true
		filtered = append(filtered, snapshot)
	}
	return filtered, nil
}

func (s *Service) filterRestorableTargetSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	datasetKind string,
	snapshots []SnapshotInfo,
) ([]SnapshotInfo, error) {
	lookupByIndex := make(map[int]string)
	jobIDByIndex := make(map[int]uint)
	lookups := make([]string, 0, len(snapshots))
	for i, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		jobID, commitRequired, parseErr := backupCommitJobIDFromSnapshot(shortName)
		if parseErr != nil || !commitRequired {
			continue
		}
		lookup, err := snapshotCommitLookupName(target, snapshot)
		if err != nil {
			continue
		}
		lookupByIndex[i] = lookup
		jobIDByIndex[i] = jobID
		lookups = append(lookups, lookup)
	}
	metadataBySnapshot, err := s.getRemoteBackupCommitMetadataBatch(ctx, target, lookups)
	if err != nil {
		return nil, err
	}

	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for i, snapshot := range snapshots {
		shortName := snapshotShortName(snapshot)
		jobID, commitRequired, parseErr := backupCommitJobIDFromSnapshot(shortName)
		if parseErr != nil {
			continue
		}
		if !commitRequired {
			if datasetKind == clusterModels.BackupJobModeVM {
				continue
			}
			snapshot.Legacy = true
			filtered = append(filtered, snapshot)
			continue
		}
		lookup, ok := lookupByIndex[i]
		if !ok || jobIDByIndex[i] != jobID {
			continue
		}
		result, ok := metadataBySnapshot[lookup]
		if !ok || result.Err != nil {
			continue
		}
		metadata := result.Metadata
		if err := metadata.validate(); err != nil ||
			metadata.JobID != jobID ||
			metadata.SnapshotName != strings.TrimPrefix(shortName, "@") {
			continue
		}
		snapshot.Committed = true
		filtered = append(filtered, snapshot)
	}
	return filtered, nil
}
