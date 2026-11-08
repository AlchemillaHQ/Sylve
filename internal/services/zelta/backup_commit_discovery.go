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
	return s.filterCommittedSnapshots(ctx, &job.Target, snapshots, func(shortName string) (uint, bool) {
		if backupSnapshotRequiresCommit(job.ID, shortName) {
			return job.ID, true
		}
		return 0, true
	})
}

func (s *Service) filterRestorableTargetSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	datasetKind string,
	snapshots []SnapshotInfo,
) ([]SnapshotInfo, error) {
	return s.filterCommittedSnapshots(ctx, target, snapshots, func(shortName string) (uint, bool) {
		jobID, required, err := backupCommitJobIDFromSnapshot(shortName)
		if err != nil {
			return 0, false
		}
		return jobID, required || datasetKind != clusterModels.BackupJobModeVM
	})
}

// commitJob names the job a snapshot's commit must belong to, or 0 for a legacy
// restore point that has none; false drops the snapshot.
func (s *Service) filterCommittedSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	snapshots []SnapshotInfo,
	commitJob func(shortName string) (uint, bool),
) ([]SnapshotInfo, error) {
	type candidate struct {
		jobID  uint
		keep   bool
		lookup string
	}
	candidates := make([]candidate, len(snapshots))
	lookups := make([]string, 0, len(snapshots))
	for i, snapshot := range snapshots {
		jobID, keep := commitJob(snapshotShortName(snapshot))
		candidates[i] = candidate{jobID: jobID, keep: keep}
		if !keep || jobID == 0 {
			continue
		}
		if lookup, err := snapshotCommitLookupName(target, snapshot); err == nil {
			candidates[i].lookup = lookup
			lookups = append(lookups, lookup)
		}
	}
	metadataBySnapshot, err := s.getRemoteBackupCommitMetadataBatch(ctx, target, lookups)
	if err != nil {
		return nil, err
	}

	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for i, snapshot := range snapshots {
		c := candidates[i]
		switch {
		case !c.keep:
			continue
		case c.jobID == 0:
			snapshot.Legacy = true
		default:
			result, ok := metadataBySnapshot[c.lookup]
			if c.lookup == "" || !ok || result.Err != nil ||
				validateBackupRestoreCommit(result.Metadata, c.jobID, snapshotShortName(snapshot)) != nil {
				continue
			}
			snapshot.Committed = true
		}
		filtered = append(filtered, snapshot)
	}
	return filtered, nil
}
