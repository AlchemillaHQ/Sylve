// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

// The protocol token distinguishes snapshots created after commit manifests
// became mandatory from legacy restore points that predate the feature. It is
// kept inside the existing per-job prefix so ownership filters and retention
// continue to treat both generations as belonging to the same job.
const backupCommitProtocolToken = "c1"

func backupSnapshotNameForJob(jobID uint) string {
	return fmt.Sprintf(
		"%s_%s_%s",
		backupSnapshotPrefixForJob(jobID),
		backupCommitProtocolToken,
		compactNowToken(),
	)
}

func backupSnapshotRequiresCommit(jobID uint, snapshotName string) bool {
	snapshotName = strings.TrimPrefix(strings.TrimSpace(snapshotName), "@")
	if jobID == 0 || snapshotName == "" {
		return false
	}
	return strings.HasPrefix(
		snapshotName,
		backupSnapshotPrefixForJob(jobID)+"_"+backupCommitProtocolToken+"_",
	)
}

func backupCommitJobIDFromSnapshot(snapshotName string) (uint, bool, error) {
	snapshotName = strings.TrimPrefix(strings.TrimSpace(snapshotName), "@")
	marker := "_" + backupCommitProtocolToken + "_"
	markerIndex := strings.Index(snapshotName, marker)
	if markerIndex < 0 {
		return 0, false, nil
	}
	if !strings.HasPrefix(snapshotName, "bk_j") {
		return 0, false, nil
	}
	jobToken := strings.TrimPrefix(snapshotName[:markerIndex], "bk_j")
	jobID, err := strconv.ParseUint(jobToken, 36, 64)
	if err != nil || jobID == 0 {
		return 0, false, fmt.Errorf("invalid_backup_commit_job_token")
	}
	if uint64(uint(jobID)) != jobID || !backupSnapshotRequiresCommit(uint(jobID), snapshotName) {
		return 0, false, fmt.Errorf("invalid_backup_commit_snapshot_name")
	}
	return uint(jobID), true, nil
}

func validateBackupRestoreCommit(metadata backupCommitMetadata, jobID uint, snapshotName string) error {
	if err := validateBackupCommitForJob(metadata, jobID, snapshotName); err != nil {
		return fmt.Errorf("restore_backup_commit_invalid: %w", err)
	}
	return nil
}

func backupCommitMetadataEquivalent(left, right backupCommitMetadata) bool {
	return left.Version == right.Version &&
		left.JobID == right.JobID &&
		left.SnapshotName == right.SnapshotName &&
		left.ManifestHash == right.ManifestHash &&
		left.EntryCount == right.EntryCount &&
		left.Recursive == right.Recursive &&
		slices.Equal(left.Roots, right.Roots)
}

func (s *Service) requireRemoteBackupRestoreCommit(
	ctx context.Context,
	job *clusterModels.BackupJob,
	remoteDataset string,
	snapshotName string,
) (backupCommitMetadata, error) {
	if !backupSnapshotRequiresCommit(job.ID, snapshotName) {
		// Restore points created before the commit protocol remain usable. New
		// snapshots carry the c1 token and can never take this compatibility path.
		return backupCommitMetadata{}, nil
	}
	return s.requireRemoteBackupCommitForJob(ctx, &job.Target, job.ID, remoteDataset, snapshotName)
}

func (s *Service) requireRemoteBackupRestoreCommitBySnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset string,
	snapshotName string,
) (backupCommitMetadata, error) {
	jobID, required, err := backupCommitJobIDFromSnapshot(snapshotName)
	if err != nil || !required {
		return backupCommitMetadata{}, err
	}
	return s.requireRemoteBackupCommitForJob(ctx, target, jobID, remoteDataset, snapshotName)
}

func (s *Service) requireRemoteBackupCommitForJob(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	jobID uint,
	remoteDataset string,
	snapshotName string,
) (backupCommitMetadata, error) {
	remoteDataset = normalizeDatasetPath(remoteDataset)
	if remoteDataset == "" {
		return backupCommitMetadata{}, fmt.Errorf("restore_remote_dataset_required")
	}
	snapshotName = strings.TrimPrefix(strings.TrimSpace(snapshotName), "@")
	metadata, err := s.getRemoteBackupCommitMetadata(ctx, target, remoteDataset+"@"+snapshotName)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_snapshot_not_committed: %w", err)
	}
	if err := validateBackupRestoreCommit(metadata, jobID, snapshotName); err != nil {
		return backupCommitMetadata{}, err
	}
	return metadata, nil
}
