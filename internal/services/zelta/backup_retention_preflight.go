// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

// backupRetentionEligibleSnapshotProofsForPreflight validates target-only
// generations from one immutable inventory per scope. The caller may seed the
// inventory it already listed while finding foreign snapshots.
func (s *Service) backupRetentionEligibleSnapshotProofsForPreflight(
	ctx context.Context,
	job *clusterModels.BackupJob,
	commitRoot string,
	candidates []SnapshotInfo,
	scopes []backupScope,
	seedRoot string,
	seedSnapshots []SnapshotInfo,
) (backupRetentionProofSet, error) {
	proofs := newBackupRetentionProofSet()
	if job == nil {
		return proofs, fmt.Errorf("backup_job_required")
	}
	commitRoot = normalizeDatasetPath(commitRoot)
	seedRoot = normalizeDatasetPath(seedRoot)
	inventories := make(map[string]remoteBackupManifestInventory, len(scopes))
	for _, scope := range scopes {
		remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		if _, duplicate := inventories[remoteRoot]; duplicate {
			return proofs, fmt.Errorf("duplicate_backup_retention_scope:%s", remoteRoot)
		}
		var inventory remoteBackupManifestInventory
		var err error
		if remoteRoot == seedRoot {
			inventory, err = s.loadRemoteBackupManifestInventoryFromSnapshots(
				ctx,
				&job.Target,
				remoteRoot,
				seedSnapshots,
			)
		} else {
			inventory, err = s.loadRemoteBackupManifestInventory(
				ctx,
				&job.Target,
				remoteRoot,
			)
		}
		if err != nil {
			return proofs, fmt.Errorf(
				"backup_retention_manifest_inventory_failed: target=%s: %w",
				remoteRoot,
				err,
			)
		}
		inventories[remoteRoot] = inventory
	}
	return s.backupRetentionEligibleSnapshotProofsFromInventories(
		ctx,
		job,
		commitRoot,
		candidates,
		scopes,
		inventories,
	)
}
