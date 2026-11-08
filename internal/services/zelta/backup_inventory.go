// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// remoteBackupManifestInventory is one immutable view of a target root. The
// recursive ZFS lists are performed once, then manifests for all generations
// are derived from these indexes in memory.
type remoteBackupManifestInventory struct {
	root                string
	datasets            map[string]string
	snapshotGUIDsByName map[string]map[string]string
}

type backupCommitMetadataResult struct {
	Metadata backupCommitMetadata
	Err      error
}

const backupCommitMetadataBatchSize = 128

var errBackupGenerationUnprovable = errors.New("backup_generation_unprovable")

func addBackupSnapshotGUID(
	byName map[string]map[string]string,
	observedRoot string,
	fullName string,
	guid string,
) error {
	fullName = strings.TrimSpace(fullName)
	at := strings.LastIndex(fullName, "@")
	if at <= 0 || at == len(fullName)-1 {
		return fmt.Errorf("invalid_backup_manifest_snapshot_entry")
	}
	dataset := normalizeDatasetPath(fullName[:at])
	if dataset != observedRoot && !strings.HasPrefix(dataset, observedRoot+"/") {
		return fmt.Errorf("backup_manifest_snapshot_outside_root:%s", dataset)
	}
	snapshotName := strings.TrimSpace(fullName[at+1:])
	guid = strings.TrimSpace(guid)
	if snapshotName == "" || guid == "" || guid == "-" {
		return fmt.Errorf("backup_manifest_snapshot_guid_missing:%s", dataset)
	}
	guids := byName[snapshotName]
	if guids == nil {
		guids = make(map[string]string)
		byName[snapshotName] = guids
	}
	if _, exists := guids[dataset]; exists {
		return fmt.Errorf("duplicate_backup_manifest_snapshot:%s", dataset)
	}
	guids[dataset] = guid
	return nil
}

func parseBackupSnapshotGUIDInventory(output, observedRoot string) (map[string]map[string]string, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	if observedRoot == "" {
		return nil, fmt.Errorf("backup_manifest_root_required")
	}

	byName := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_backup_manifest_snapshot_entry")
		}
		if err := addBackupSnapshotGUID(byName, observedRoot, fields[0], fields[1]); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return byName, nil
}

func backupSnapshotGUIDInventoryFromSnapshots(
	snapshots []SnapshotInfo,
	observedRoot string,
) (map[string]map[string]string, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	if observedRoot == "" {
		return nil, fmt.Errorf("backup_manifest_root_required")
	}
	byName := make(map[string]map[string]string)
	for _, snapshot := range snapshots {
		if err := addBackupSnapshotGUID(byName, observedRoot, snapshot.Name, snapshot.Guid); err != nil {
			return nil, err
		}
	}
	return byName, nil
}

func newRemoteBackupManifestInventory(
	root string,
	datasets map[string]string,
	snapshotGUIDsByName map[string]map[string]string,
) (remoteBackupManifestInventory, error) {
	root = normalizeDatasetPath(root)
	if root == "" {
		return remoteBackupManifestInventory{}, fmt.Errorf("backup_manifest_root_required")
	}
	if _, exists := datasets[root]; !exists {
		return remoteBackupManifestInventory{}, fmt.Errorf("backup_manifest_root_missing:%s", root)
	}
	if snapshotGUIDsByName == nil {
		snapshotGUIDsByName = make(map[string]map[string]string)
	}
	return remoteBackupManifestInventory{
		root:                root,
		datasets:            datasets,
		snapshotGUIDsByName: snapshotGUIDsByName,
	}, nil
}

func (inventory remoteBackupManifestInventory) manifestEntries(
	canonicalRoot string,
	snapshotName string,
	recursive bool,
	requireEveryDataset bool,
) ([]backupManifestEntry, error) {
	snapshotName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil {
		return nil, err
	}
	datasets := inventory.datasets
	if !recursive {
		datasetType := strings.TrimSpace(inventory.datasets[inventory.root])
		if datasetType == "" {
			return nil, fmt.Errorf("backup_manifest_root_missing:%s", inventory.root)
		}
		datasets = map[string]string{inventory.root: datasetType}
	}
	return buildBackupSnapshotManifestEntries(
		datasets,
		inventory.snapshotGUIDsByName[snapshotName],
		inventory.root,
		canonicalRoot,
		requireEveryDataset,
	)
}

func (inventory remoteBackupManifestInventory) restoreManifest(
	snapshotName string,
	recursive bool,
) ([]restoreDatasetManifestEntry, error) {
	normalizedSnapshot, err := normalizeSnapshotName(snapshotName)
	if err != nil {
		return nil, err
	}
	snapshotName = strings.TrimPrefix(normalizedSnapshot, "@")
	datasets := inventory.datasets
	if !recursive {
		datasetType := strings.TrimSpace(inventory.datasets[inventory.root])
		if datasetType == "" {
			return nil, fmt.Errorf("backup_manifest_root_missing:%s", inventory.root)
		}
		datasets = map[string]string{inventory.root: datasetType}
	}
	entries, err := buildBackupSnapshotManifestEntries(
		datasets,
		inventory.snapshotGUIDsByName[snapshotName],
		inventory.root,
		inventory.root,
		true,
	)
	if err != nil {
		return nil, err
	}
	manifest := make([]restoreDatasetManifestEntry, 0, len(entries))
	for _, entry := range entries {
		manifest = append(manifest, restoreDatasetManifestEntry{
			Suffix:       entry.Suffix,
			Type:         entry.Type,
			SnapshotGUID: entry.SnapshotGUID,
		})
	}
	return manifest, nil
}

func (s *Service) loadRemoteBackupManifestInventory(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
) (remoteBackupManifestInventory, error) {
	parsedRoot, err := canonicalTargetDataset(target, remoteRoot)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("backup_target_manifest_root_invalid: %w", err)
	}
	return loadBackupManifestInventory(ctx, s.targetZFS(target), "target", parsedRoot.String(), true)
}

func loadBackupManifestInventory(
	ctx context.Context,
	run zfsCommand,
	side, root string,
	recursive bool,
) (remoteBackupManifestInventory, error) {
	datasetOutput, err := run(ctx, backupDatasetListArgs(root, recursive)...)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("list_backup_%s_tree_failed: %w", side, err)
	}
	snapshotOutput, err := run(ctx, backupSnapshotListArgs(root, recursive)...)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("list_backup_%s_snapshots_failed: %w", side, err)
	}
	datasets, err := parseBackupDatasetTree(datasetOutput, root, recursive)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}
	guids, err := parseBackupSnapshotGUIDInventory(snapshotOutput, root)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}
	return newRemoteBackupManifestInventory(root, datasets, guids)
}

func runLocalZFS(ctx context.Context, argv ...string) (string, error) {
	return utils.RunCommandWithContext(ctx, argv[0], argv[1:]...)
}

func (s *Service) targetZFS(target *clusterModels.BackupTarget) zfsCommand {
	return func(ctx context.Context, argv ...string) (string, error) {
		return s.runTargetSSH(ctx, target, argv...)
	}
}

func (s *Service) loadRemoteBackupManifestInventoryFromSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
	snapshots []SnapshotInfo,
) (remoteBackupManifestInventory, error) {
	parsedRoot, err := canonicalTargetDataset(target, remoteRoot)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("backup_target_manifest_root_invalid: %w", err)
	}
	remoteRoot = parsedRoot.String()
	datasetArgs := backupDatasetListArgs(remoteRoot, true)
	datasetOutput, err := s.runTargetSSH(ctx, target, datasetArgs...)
	if err != nil {
		return remoteBackupManifestInventory{}, fmt.Errorf("list_backup_target_tree_failed: %w", err)
	}
	datasets, err := parseBackupDatasetTree(datasetOutput, remoteRoot, true)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}
	guids, err := backupSnapshotGUIDInventoryFromSnapshots(snapshots, remoteRoot)
	if err != nil {
		return remoteBackupManifestInventory{}, err
	}
	return newRemoteBackupManifestInventory(remoteRoot, datasets, guids)
}

func buildRemoteBackupManifestFromInventories(
	job *clusterModels.BackupJob,
	metadata backupCommitMetadata,
	scopes []backupScope,
	inventories map[string]remoteBackupManifestInventory,
) (backupManifest, error) {
	if job == nil {
		return backupManifest{}, fmt.Errorf("backup_job_required")
	}
	// A VM may have gained or lost a pool since this generation was committed.
	scopeByRoot := make(map[string]backupScope, len(scopes))
	for _, scope := range scopes {
		scopeByRoot[normalizeDatasetPath(scope.sourceDataset)] = scope
	}
	snapshotName, recursive := metadata.SnapshotName, metadata.Recursive
	entries := make([]backupManifestEntry, 0)
	for _, root := range metadata.Roots {
		scope, ok := scopeByRoot[normalizeDatasetPath(root)]
		if !ok {
			return backupManifest{}, fmt.Errorf("%w:%s", errBackupGenerationUnprovable, root)
		}
		remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		inventory, ok := inventories[remoteRoot]
		if !ok {
			return backupManifest{}, fmt.Errorf("backup_target_manifest_inventory_missing:%s", remoteRoot)
		}
		if _, ok := inventory.snapshotGUIDsByName[snapshotName][inventory.root]; !ok {
			return backupManifest{}, fmt.Errorf("%w:%s", errBackupGenerationUnprovable, remoteRoot)
		}
		part, err := inventory.manifestEntries(
			scope.sourceDataset,
			snapshotName,
			recursive,
			false,
		)
		if err != nil {
			return backupManifest{}, err
		}
		entries = append(entries, part...)
	}
	return buildBackupManifest(job.ID, snapshotName, recursive, entries)
}

func parseBackupCommitMetadataBatch(
	output string,
	expectedSnapshots []string,
) (map[string]backupCommitMetadataResult, error) {
	expected := make(map[string]struct{}, len(expectedSnapshots))
	grouped := make(map[string]*strings.Builder, len(expectedSnapshots))
	for _, snapshot := range expectedSnapshots {
		expected[snapshot] = struct{}{}
		grouped[snapshot] = &strings.Builder{}
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("invalid_backup_commit_property_entry")
		}
		snapshot := strings.TrimSpace(fields[0])
		if _, ok := expected[snapshot]; !ok {
			return nil, fmt.Errorf("unexpected_backup_commit_snapshot:%s", snapshot)
		}
		builder := grouped[snapshot]
		builder.WriteString(fields[1])
		builder.WriteByte('\t')
		builder.WriteString(fields[2])
		builder.WriteByte('\t')
		builder.WriteString(fields[3])
		builder.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	results := make(map[string]backupCommitMetadataResult, len(expectedSnapshots))
	for _, snapshot := range expectedSnapshots {
		metadata, err := parseBackupCommitMetadata(grouped[snapshot].String())
		results[snapshot] = backupCommitMetadataResult{Metadata: metadata, Err: err}
	}
	return results, nil
}

func (s *Service) getRemoteBackupCommitMetadataBatch(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteSnapshots []string,
) (map[string]backupCommitMetadataResult, error) {
	if len(remoteSnapshots) == 0 {
		return map[string]backupCommitMetadataResult{}, nil
	}
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return nil, err
	}
	canonical := make([]string, 0, len(remoteSnapshots))
	seen := make(map[string]struct{}, len(remoteSnapshots))
	for _, candidate := range remoteSnapshots {
		snapshot, parseErr := remoteexec.ParseZFSSnapshot(candidate)
		if parseErr != nil || !snapshot.Dataset().Within(root) {
			return nil, fmt.Errorf("invalid_backup_commit_snapshot")
		}
		name := snapshot.String()
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		canonical = append(canonical, name)
	}

	results := make(map[string]backupCommitMetadataResult, len(canonical))
	properties := strings.Join(backupCommitPropertyNames(), ",")
	for start := 0; start < len(canonical); start += backupCommitMetadataBatchSize {
		end := start + backupCommitMetadataBatchSize
		if end > len(canonical) {
			end = len(canonical)
		}
		batch := canonical[start:end]
		args := []string{
			"zfs", "get", "-H", "-p", "-o", "name,property,value,source",
			properties,
		}
		args = append(args, batch...)
		output, runErr := s.runTargetSSH(ctx, target, args...)
		if runErr != nil {
			return nil, fmt.Errorf("get_backup_commit_metadata_failed: %w", runErr)
		}
		parsed, parseErr := parseBackupCommitMetadataBatch(output, batch)
		if parseErr != nil {
			return nil, parseErr
		}
		for snapshot, result := range parsed {
			results[snapshot] = result
		}
	}
	return results, nil
}
