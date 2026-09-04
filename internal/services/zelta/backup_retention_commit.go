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
	"github.com/alchemillahq/sylve/pkg/utils"
)

// backupRetentionProofSet is the destructive-retention authority derived from
// a fully revalidated c1 manifest. Keys are exact snapshot paths and values are
// their immutable ZFS GUIDs. A snapshot name or dataset prefix alone is never
// ownership proof.
type backupRetentionProofSet struct {
	Source map[string]string
	Target map[string]string
}

func newBackupRetentionProofSet() backupRetentionProofSet {
	return backupRetentionProofSet{
		Source: make(map[string]string),
		Target: make(map[string]string),
	}
}

func addBackupSnapshotProof(proofs map[string]string, snapshot, guid string) error {
	snapshot = strings.TrimSpace(snapshot)
	guid = strings.TrimSpace(guid)
	if !isValidZFSSnapshotName(snapshot) || guid == "" || guid == "-" {
		return fmt.Errorf("invalid_backup_retention_snapshot_proof")
	}
	if existing, ok := proofs[snapshot]; ok && existing != guid {
		return fmt.Errorf("conflicting_backup_retention_snapshot_proof: snapshot=%s", snapshot)
	}
	proofs[snapshot] = guid
	return nil
}

func datasetForBackupManifestEntry(root, suffix string) string {
	root = normalizeDatasetPath(root)
	suffix = normalizeDatasetPath(suffix)
	if suffix == "" {
		return root
	}
	return normalizeDatasetPath(root + "/" + suffix)
}

func addBackupManifestRetentionProofs(
	proofs *backupRetentionProofSet,
	job *clusterModels.BackupJob,
	manifest backupManifest,
	scopes []backupScope,
) error {
	if proofs == nil || job == nil {
		return fmt.Errorf("backup_retention_proof_context_required")
	}

	scopeByRoot := make(map[string]backupScope, len(scopes))
	for _, scope := range scopes {
		root := normalizeDatasetPath(scope.sourceDataset)
		if root == "" {
			return fmt.Errorf("backup_retention_scope_source_required")
		}
		if _, duplicate := scopeByRoot[root]; duplicate {
			return fmt.Errorf("duplicate_backup_retention_scope: source=%s", root)
		}
		scope.sourceDataset = root
		scope.destSuffix = normalizeDatasetPath(scope.destSuffix)
		scopeByRoot[root] = scope
	}

	for _, entry := range manifest.Entries {
		root := normalizeDatasetPath(entry.Root)
		scope, ok := scopeByRoot[root]
		if !ok {
			return fmt.Errorf("backup_retention_manifest_scope_missing: source=%s", root)
		}

		sourceDataset := datasetForBackupManifestEntry(root, entry.Suffix)
		targetRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		targetDataset := datasetForBackupManifestEntry(targetRoot, entry.Suffix)
		if sourceDataset == "" || targetDataset == "" || !datasetWithinRoot(targetRoot, targetDataset) {
			return fmt.Errorf("backup_retention_manifest_mapping_invalid: source=%s", root)
		}

		sourceSnapshot := sourceDataset + "@" + manifest.SnapshotName
		targetSnapshot := targetDataset + "@" + manifest.SnapshotName
		if err := addBackupSnapshotProof(proofs.Source, sourceSnapshot, entry.SnapshotGUID); err != nil {
			return err
		}
		if err := addBackupSnapshotProof(proofs.Target, targetSnapshot, entry.SnapshotGUID); err != nil {
			return err
		}
	}
	return nil
}

func filterBackupSnapshotsByProof(
	snapshots []SnapshotInfo,
	proofs map[string]string,
) []SnapshotInfo {
	filtered := make([]SnapshotInfo, 0, len(snapshots))
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		expectedGUID, ok := proofs[name]
		if !ok || strings.TrimSpace(snapshot.Guid) != expectedGUID {
			continue
		}
		filtered = append(filtered, snapshot)
	}
	return filtered
}

func snapshotNames(snapshots []SnapshotInfo) []string {
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if isValidZFSSnapshotName(snapshot.Name) {
			names = append(names, snapshot.Name)
		}
	}
	return names
}

func parseExactSnapshotGUID(output, expectedSnapshot string) (string, error) {
	expectedSnapshot = strings.TrimSpace(expectedSnapshot)
	lines := make([]string, 0, 1)
	for _, rawLine := range strings.Split(strings.TrimSpace(output), "\n") {
		line := strings.TrimSpace(rawLine)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("backup_retention_snapshot_identity_ambiguous")
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 2 || fields[0] != expectedSnapshot {
		return "", fmt.Errorf("backup_retention_snapshot_identity_mismatch")
	}
	guid := strings.TrimSpace(fields[1])
	if guid == "" || guid == "-" {
		return "", fmt.Errorf("backup_retention_snapshot_guid_missing")
	}
	return guid, nil
}

func (s *Service) destroyLocalBackupSnapshotsWithProof(
	ctx context.Context,
	snapshots []string,
	proofs map[string]string,
) error {
	for _, candidate := range snapshots {
		parsed, err := remoteexec.ParseZFSSnapshot(candidate)
		if err != nil {
			return fmt.Errorf("backup_retention_local_snapshot_invalid: %w", err)
		}
		snapshot := parsed.String()
		expectedGUID, ok := proofs[snapshot]
		if !ok || expectedGUID == "" {
			return fmt.Errorf("backup_retention_local_snapshot_unproven: snapshot=%s", snapshot)
		}
		output, err := utils.RunCommandWithContext(
			ctx,
			"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", snapshot,
		)
		if err != nil {
			return fmt.Errorf("backup_retention_local_snapshot_recheck_failed: snapshot=%s: %w", snapshot, err)
		}
		guid, err := parseExactSnapshotGUID(output, snapshot)
		if err != nil {
			return fmt.Errorf("backup_retention_local_snapshot_recheck_invalid: snapshot=%s: %w", snapshot, err)
		}
		if guid != expectedGUID {
			return fmt.Errorf(
				"backup_retention_local_snapshot_proof_changed: snapshot=%s expected_guid=%s actual_guid=%s",
				snapshot,
				expectedGUID,
				guid,
			)
		}
		if err := s.DestroySnapshots(ctx, []string{snapshot}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) destroyTargetBackupSnapshotsWithProof(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	snapshots []string,
	proofs map[string]string,
) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return err
	}
	for _, candidate := range snapshots {
		parsed, err := remoteexec.ParseZFSSnapshot(candidate)
		if err != nil {
			return fmt.Errorf("backup_retention_target_snapshot_invalid: %w", err)
		}
		snapshot := parsed.String()
		expectedGUID, ok := proofs[snapshot]
		if !ok || expectedGUID == "" {
			return fmt.Errorf("backup_retention_target_snapshot_unproven: snapshot=%s", snapshot)
		}
		if !parsed.Dataset().Within(root) {
			return fmt.Errorf("backup_retention_target_snapshot_outside_root: snapshot=%s", snapshot)
		}
		output, err := s.runTargetSSH(
			ctx,
			target,
			"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", snapshot,
		)
		if err != nil {
			return fmt.Errorf("backup_retention_target_snapshot_recheck_failed: snapshot=%s: %w", snapshot, err)
		}
		guid, err := parseExactSnapshotGUID(output, snapshot)
		if err != nil {
			return fmt.Errorf("backup_retention_target_snapshot_recheck_invalid: snapshot=%s: %w", snapshot, err)
		}
		if guid != expectedGUID {
			return fmt.Errorf(
				"backup_retention_target_snapshot_proof_changed: snapshot=%s expected_guid=%s actual_guid=%s",
				snapshot,
				expectedGUID,
				guid,
			)
		}
		if err := s.DestroyTargetSnapshotsByName(ctx, target, []string{snapshot}); err != nil {
			return err
		}
	}
	return nil
}
