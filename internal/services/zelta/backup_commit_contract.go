// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
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

func validateBackupRestoreCommit(
	metadata backupCommitMetadata,
	job *clusterModels.BackupJob,
	snapshotName string,
) error {
	if err := validateBackupCommitForJob(metadata, job, snapshotName); err != nil {
		return fmt.Errorf("restore_backup_commit_invalid: %w", err)
	}
	return nil
}

func backupCommitMetadataEquivalent(left, right backupCommitMetadata) bool {
	if left.Version != right.Version ||
		left.JobID != right.JobID ||
		left.SnapshotName != right.SnapshotName ||
		left.ManifestHash != right.ManifestHash ||
		left.EntryCount != right.EntryCount ||
		left.Recursive != right.Recursive ||
		len(left.Roots) != len(right.Roots) {
		return false
	}
	for i := range left.Roots {
		if left.Roots[i] != right.Roots[i] {
			return false
		}
	}
	return true
}

func (s *Service) requireRemoteBackupRestoreCommit(
	ctx context.Context,
	job *clusterModels.BackupJob,
	remoteDataset string,
	snapshotName string,
) (backupCommitMetadata, error) {
	if job == nil {
		return backupCommitMetadata{}, fmt.Errorf("backup_job_required")
	}
	if !backupSnapshotRequiresCommit(job.ID, snapshotName) {
		// Restore points created before the commit protocol remain usable. New
		// snapshots carry the c1 token and can never take this compatibility path.
		return backupCommitMetadata{}, nil
	}
	remoteDataset = normalizeDatasetPath(remoteDataset)
	if remoteDataset == "" {
		return backupCommitMetadata{}, fmt.Errorf("restore_remote_dataset_required")
	}
	snapshotName = strings.TrimPrefix(strings.TrimSpace(snapshotName), "@")
	metadata, err := s.getRemoteBackupCommitMetadata(
		ctx,
		&job.Target,
		remoteDataset+"@"+snapshotName,
	)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_snapshot_not_committed: %w", err)
	}
	if err := validateBackupRestoreCommit(metadata, job, snapshotName); err != nil {
		return backupCommitMetadata{}, err
	}
	return metadata, nil
}

func (s *Service) requireRemoteBackupRestoreCommitBySnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteDataset string,
	snapshotName string,
) (backupCommitMetadata, error) {
	jobID, required, err := backupCommitJobIDFromSnapshot(snapshotName)
	if err != nil {
		return backupCommitMetadata{}, err
	}
	if !required {
		return backupCommitMetadata{}, nil
	}
	if target == nil {
		return backupCommitMetadata{}, fmt.Errorf("backup_target_required")
	}
	remoteDataset = normalizeDatasetPath(remoteDataset)
	if remoteDataset == "" {
		return backupCommitMetadata{}, fmt.Errorf("restore_remote_dataset_required")
	}
	snapshotName = strings.TrimPrefix(strings.TrimSpace(snapshotName), "@")
	metadata, err := s.getRemoteBackupCommitMetadata(
		ctx,
		target,
		remoteDataset+"@"+snapshotName,
	)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_snapshot_not_committed: %w", err)
	}
	if err := metadata.validate(); err != nil {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_commit_invalid: %w", err)
	}
	if metadata.JobID != jobID {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_commit_job_mismatch")
	}
	if metadata.SnapshotName != snapshotName {
		return backupCommitMetadata{}, fmt.Errorf("restore_backup_commit_snapshot_mismatch")
	}
	return metadata, nil
}
