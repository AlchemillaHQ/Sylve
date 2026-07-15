// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	backupCommitVersion = 1

	backupCommitPropertyVersion   = "sylve:backup-commit-version"
	backupCommitPropertyJobID     = "sylve:backup-commit-job-id"
	backupCommitPropertySnapshot  = "sylve:backup-commit-snapshot"
	backupCommitPropertyManifest  = "sylve:backup-commit-manifest"
	backupCommitPropertyCount     = "sylve:backup-commit-count"
	backupCommitPropertyRecursive = "sylve:backup-commit-recursive"
	backupCommitPropertyRoots     = "sylve:backup-commit-roots"
)

// backupManifestEntry binds one source dataset to the exact snapshot GUID
// received by the target. Root is the canonical source root. Suffix is stored
// separately so a remote lineage may be renamed without changing the manifest.
type backupManifestEntry struct {
	Root         string
	Suffix       string
	Type         string
	SnapshotGUID string
}

type backupManifest struct {
	Version      int
	JobID        uint
	SnapshotName string
	Recursive    bool
	Roots        []string
	Entries      []backupManifestEntry
}

type backupCommitMetadata struct {
	Version      int
	JobID        uint
	SnapshotName string
	ManifestHash string
	EntryCount   int
	Recursive    bool
	Roots        []string
}

func normalizeBackupSnapshotName(snapshotName string) (string, error) {
	snapshotName = strings.TrimSpace(strings.TrimPrefix(snapshotName, "@"))
	if snapshotName == "" || !validReplicationZFSToken(snapshotName) {
		return "", fmt.Errorf("invalid_backup_snapshot_name")
	}
	if !strings.HasPrefix(strings.ToLower(snapshotName), "bk_") {
		return "", fmt.Errorf("backup_snapshot_name_must_use_bk_prefix")
	}
	return snapshotName, nil
}

func parseBackupDatasetTree(output, observedRoot string, recursive bool) (map[string]string, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	if observedRoot == "" {
		return nil, fmt.Errorf("backup_manifest_root_required")
	}

	datasets := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_backup_manifest_dataset_entry")
		}
		dataset := normalizeDatasetPath(fields[0])
		if dataset != observedRoot && !strings.HasPrefix(dataset, observedRoot+"/") {
			return nil, fmt.Errorf("backup_manifest_dataset_outside_root:%s", dataset)
		}
		if !recursive && dataset != observedRoot {
			return nil, fmt.Errorf("backup_manifest_nonrecursive_descendant:%s", dataset)
		}
		datasetType := strings.ToLower(strings.TrimSpace(fields[1]))
		if datasetType != "filesystem" && datasetType != "volume" {
			return nil, fmt.Errorf("invalid_backup_manifest_dataset_type:%s", datasetType)
		}
		if _, exists := datasets[dataset]; exists {
			return nil, fmt.Errorf("duplicate_backup_manifest_dataset:%s", dataset)
		}
		datasets[dataset] = datasetType
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if _, exists := datasets[observedRoot]; !exists {
		return nil, fmt.Errorf("backup_manifest_root_missing:%s", observedRoot)
	}
	return datasets, nil
}

func parseBackupSnapshotGUIDs(output, observedRoot, snapshotName string) (map[string]string, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	snapshotName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil {
		return nil, err
	}
	if observedRoot == "" {
		return nil, fmt.Errorf("backup_manifest_root_required")
	}

	guids := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_backup_manifest_snapshot_entry")
		}
		at := strings.LastIndex(fields[0], "@")
		if at <= 0 || fields[0][at+1:] != snapshotName {
			continue
		}
		dataset := normalizeDatasetPath(fields[0][:at])
		if dataset != observedRoot && !strings.HasPrefix(dataset, observedRoot+"/") {
			return nil, fmt.Errorf("backup_manifest_snapshot_outside_root:%s", dataset)
		}
		guid := strings.TrimSpace(fields[1])
		if guid == "" || guid == "-" {
			return nil, fmt.Errorf("backup_manifest_snapshot_guid_missing:%s", dataset)
		}
		if _, exists := guids[dataset]; exists {
			return nil, fmt.Errorf("duplicate_backup_manifest_snapshot:%s", dataset)
		}
		guids[dataset] = guid
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return guids, nil
}

func buildBackupSnapshotManifestEntries(
	datasets map[string]string,
	guids map[string]string,
	observedRoot string,
	canonicalRoot string,
	requireEveryDataset bool,
) ([]backupManifestEntry, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	canonicalRoot = normalizeDatasetPath(canonicalRoot)
	if observedRoot == "" || canonicalRoot == "" {
		return nil, fmt.Errorf("backup_manifest_root_required")
	}

	entries := make([]backupManifestEntry, 0, len(datasets))
	for dataset, datasetType := range datasets {
		guid := strings.TrimSpace(guids[dataset])
		if guid == "" {
			if requireEveryDataset {
				return nil, fmt.Errorf("backup_manifest_snapshot_missing:%s", dataset)
			}
			continue
		}
		relative := strings.TrimPrefix(dataset, observedRoot)
		if relative != "" && !strings.HasPrefix(relative, "/") {
			return nil, fmt.Errorf("backup_manifest_dataset_mapping_invalid:%s", dataset)
		}
		entries = append(entries, backupManifestEntry{
			Root:         canonicalRoot,
			Suffix:       strings.TrimPrefix(relative, "/"),
			Type:         datasetType,
			SnapshotGUID: guid,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("backup_manifest_snapshot_tree_empty")
	}
	if _, exists := guids[observedRoot]; !exists {
		return nil, fmt.Errorf("backup_manifest_root_snapshot_missing:%s", observedRoot)
	}
	sortBackupManifestEntries(entries)
	return entries, nil
}

func sortBackupManifestEntries(entries []backupManifestEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Root != entries[j].Root {
			return entries[i].Root < entries[j].Root
		}
		return entries[i].Suffix < entries[j].Suffix
	})
}

func buildBackupManifest(
	jobID uint,
	snapshotName string,
	recursive bool,
	entries []backupManifestEntry,
) (backupManifest, error) {
	if jobID == 0 {
		return backupManifest{}, fmt.Errorf("backup_manifest_job_id_required")
	}
	snapshotName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil {
		return backupManifest{}, err
	}
	if len(entries) == 0 {
		return backupManifest{}, fmt.Errorf("backup_manifest_entries_required")
	}

	entries = append([]backupManifestEntry(nil), entries...)
	rootSet := make(map[string]struct{})
	seen := make(map[string]struct{})
	for i := range entries {
		entries[i].Root = normalizeDatasetPath(entries[i].Root)
		entries[i].Suffix = normalizeDatasetPath(entries[i].Suffix)
		entries[i].Type = strings.ToLower(strings.TrimSpace(entries[i].Type))
		entries[i].SnapshotGUID = strings.TrimSpace(entries[i].SnapshotGUID)
		if entries[i].Root == "" || entries[i].SnapshotGUID == "" {
			return backupManifest{}, fmt.Errorf("invalid_backup_manifest_entry")
		}
		if entries[i].Type != "filesystem" && entries[i].Type != "volume" {
			return backupManifest{}, fmt.Errorf("invalid_backup_manifest_dataset_type:%s", entries[i].Type)
		}
		key := entries[i].Root + "\x00" + entries[i].Suffix
		if _, exists := seen[key]; exists {
			return backupManifest{}, fmt.Errorf("duplicate_backup_manifest_entry:%s", entries[i].Root)
		}
		seen[key] = struct{}{}
		rootSet[entries[i].Root] = struct{}{}
	}
	sortBackupManifestEntries(entries)

	roots := make([]string, 0, len(rootSet))
	for root := range rootSet {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	return backupManifest{
		Version:      backupCommitVersion,
		JobID:        jobID,
		SnapshotName: snapshotName,
		Recursive:    recursive,
		Roots:        roots,
		Entries:      entries,
	}, nil
}

func backupManifestHash(manifest backupManifest) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(
		h,
		"version=%d\njob=%d\nsnapshot=%s\nrecursive=%t\n",
		manifest.Version,
		manifest.JobID,
		manifest.SnapshotName,
		manifest.Recursive,
	)
	for _, root := range manifest.Roots {
		_, _ = fmt.Fprintf(h, "root=%s\n", normalizeDatasetPath(root))
	}
	for _, entry := range manifest.Entries {
		_, _ = fmt.Fprintf(
			h,
			"dataset=%s/%s\ntype=%s\nguid=%s\n",
			normalizeDatasetPath(entry.Root),
			normalizeDatasetPath(entry.Suffix),
			strings.ToLower(strings.TrimSpace(entry.Type)),
			strings.TrimSpace(entry.SnapshotGUID),
		)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func backupManifestRootsValue(roots []string) (string, error) {
	normalized := make([]string, 0, len(roots))
	seen := make(map[string]struct{})
	for _, root := range roots {
		root = normalizeDatasetPath(root)
		if root == "" {
			return "", fmt.Errorf("backup_manifest_root_required")
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		normalized = append(normalized, root)
	}
	if len(normalized) == 0 {
		return "", fmt.Errorf("backup_manifest_roots_required")
	}
	sort.Strings(normalized)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func parseBackupManifestRootsValue(value string) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("invalid_backup_commit_roots: %w", err)
	}
	var roots []string
	if err := json.Unmarshal(raw, &roots); err != nil {
		return nil, fmt.Errorf("invalid_backup_commit_roots: %w", err)
	}
	normalized := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = normalizeDatasetPath(root)
		if root == "" {
			return nil, fmt.Errorf("backup_manifest_root_required")
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		normalized = append(normalized, root)
	}
	sort.Strings(normalized)
	encoded, err := backupManifestRootsValue(normalized)
	if err != nil {
		return nil, err
	}
	if encoded != strings.TrimSpace(value) {
		return nil, fmt.Errorf("noncanonical_backup_commit_roots")
	}
	return normalized, nil
}

func backupDatasetListArgs(root string, recursive bool) []string {
	args := []string{"zfs", "list", "-H", "-p", "-t", "filesystem,volume", "-o", "name,type"}
	if recursive {
		args = append(args, "-r")
	}
	return append(args, root)
}

func backupSnapshotListArgs(root string, recursive bool) []string {
	args := []string{"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid"}
	if recursive {
		args = append(args, "-r")
	}
	return append(args, root)
}

func (s *Service) localBackupManifestEntries(
	ctx context.Context,
	root string,
	snapshotName string,
	recursive bool,
) ([]backupManifestEntry, error) {
	root = normalizeDatasetPath(root)
	datasetArgs := backupDatasetListArgs(root, recursive)
	datasetOutput, err := utils.RunCommandWithContext(ctx, datasetArgs[0], datasetArgs[1:]...)
	if err != nil {
		return nil, fmt.Errorf("list_backup_source_tree_failed: %w", err)
	}
	snapshotArgs := backupSnapshotListArgs(root, recursive)
	snapshotOutput, err := utils.RunCommandWithContext(ctx, snapshotArgs[0], snapshotArgs[1:]...)
	if err != nil {
		return nil, fmt.Errorf("list_backup_source_snapshots_failed: %w", err)
	}
	datasets, err := parseBackupDatasetTree(datasetOutput, root, recursive)
	if err != nil {
		return nil, err
	}
	guids, err := parseBackupSnapshotGUIDs(snapshotOutput, root, snapshotName)
	if err != nil {
		return nil, err
	}
	return buildBackupSnapshotManifestEntries(datasets, guids, root, root, true)
}

func (s *Service) remoteBackupManifestEntries(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteRoot string,
	canonicalSourceRoot string,
	snapshotName string,
	recursive bool,
) ([]backupManifestEntry, error) {
	if target == nil {
		return nil, fmt.Errorf("backup_target_required")
	}
	remoteRoot = normalizeDatasetPath(remoteRoot)
	datasetArgs := backupDatasetListArgs(remoteRoot, recursive)
	datasetOutput, err := s.runTargetSSH(ctx, target, datasetArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_backup_target_tree_failed: %w", err)
	}
	snapshotArgs := backupSnapshotListArgs(remoteRoot, recursive)
	snapshotOutput, err := s.runTargetSSH(ctx, target, snapshotArgs...)
	if err != nil {
		return nil, fmt.Errorf("list_backup_target_snapshots_failed: %w", err)
	}
	datasets, err := parseBackupDatasetTree(datasetOutput, remoteRoot, recursive)
	if err != nil {
		return nil, err
	}
	guids, err := parseBackupSnapshotGUIDs(snapshotOutput, remoteRoot, snapshotName)
	if err != nil {
		return nil, err
	}
	return buildBackupSnapshotManifestEntries(datasets, guids, remoteRoot, canonicalSourceRoot, false)
}

func (s *Service) buildLocalBackupManifest(
	ctx context.Context,
	jobID uint,
	snapshotName string,
	recursive bool,
	scopes []backupScope,
) (backupManifest, error) {
	entries := make([]backupManifestEntry, 0)
	for _, scope := range scopes {
		part, err := s.localBackupManifestEntries(ctx, scope.sourceDataset, snapshotName, recursive)
		if err != nil {
			return backupManifest{}, err
		}
		entries = append(entries, part...)
	}
	return buildBackupManifest(jobID, snapshotName, recursive, entries)
}

func (s *Service) buildRemoteBackupManifest(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshotName string,
	scopes []backupScope,
) (backupManifest, error) {
	if job == nil {
		return backupManifest{}, fmt.Errorf("backup_job_required")
	}
	entries := make([]backupManifestEntry, 0)
	for _, scope := range scopes {
		remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		part, err := s.remoteBackupManifestEntries(
			ctx,
			&job.Target,
			remoteRoot,
			scope.sourceDataset,
			snapshotName,
			job.Recursive,
		)
		if err != nil {
			return backupManifest{}, err
		}
		entries = append(entries, part...)
	}
	return buildBackupManifest(job.ID, snapshotName, job.Recursive, entries)
}

func newBackupCommitMetadata(manifest backupManifest) backupCommitMetadata {
	return backupCommitMetadata{
		Version:      manifest.Version,
		JobID:        manifest.JobID,
		SnapshotName: manifest.SnapshotName,
		ManifestHash: backupManifestHash(manifest),
		EntryCount:   len(manifest.Entries),
		Recursive:    manifest.Recursive,
		Roots:        append([]string(nil), manifest.Roots...),
	}
}

func (metadata backupCommitMetadata) validate() error {
	if metadata.Version != backupCommitVersion {
		return fmt.Errorf("unsupported_backup_commit_version")
	}
	if metadata.JobID == 0 {
		return fmt.Errorf("backup_commit_job_id_required")
	}
	if _, err := normalizeBackupSnapshotName(metadata.SnapshotName); err != nil {
		return err
	}
	if len(metadata.ManifestHash) != sha256.Size*2 {
		return fmt.Errorf("invalid_backup_commit_manifest_hash")
	}
	if _, err := hex.DecodeString(metadata.ManifestHash); err != nil {
		return fmt.Errorf("invalid_backup_commit_manifest_hash")
	}
	if metadata.EntryCount < 1 || len(metadata.Roots) < 1 {
		return fmt.Errorf("invalid_backup_commit_manifest_size")
	}
	return nil
}

func backupCommitProperties(metadata backupCommitMetadata) ([]string, error) {
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	roots, err := backupManifestRootsValue(metadata.Roots)
	if err != nil {
		return nil, err
	}
	return []string{
		backupCommitPropertyVersion + "=" + strconv.Itoa(metadata.Version),
		backupCommitPropertyJobID + "=" + strconv.FormatUint(uint64(metadata.JobID), 10),
		backupCommitPropertySnapshot + "=" + metadata.SnapshotName,
		backupCommitPropertyManifest + "=" + metadata.ManifestHash,
		backupCommitPropertyCount + "=" + strconv.Itoa(metadata.EntryCount),
		backupCommitPropertyRecursive + "=" + strconv.FormatBool(metadata.Recursive),
		backupCommitPropertyRoots + "=" + roots,
	}, nil
}

func compareBackupManifests(expected, actual backupManifest) error {
	expectedHash := backupManifestHash(expected)
	actualHash := backupManifestHash(actual)
	if expectedHash == actualHash && len(expected.Entries) == len(actual.Entries) {
		return nil
	}
	return fmt.Errorf(
		"backup_manifest_mismatch: expected_hash=%s actual_hash=%s expected_count=%d actual_count=%d",
		expectedHash,
		actualHash,
		len(expected.Entries),
		len(actual.Entries),
	)
}

func backupCommitCoordinatorScope(job *clusterModels.BackupJob, scopes []backupScope) (int, error) {
	if job == nil || len(scopes) == 0 {
		return -1, fmt.Errorf("backup_commit_scopes_required")
	}
	preferred := normalizeDatasetPath(job.SourceDataset)
	if job.Mode == clusterModels.BackupJobModeJail {
		preferred = normalizeDatasetPath(job.JailRootDataset)
	}
	for i := range scopes {
		if normalizeDatasetPath(scopes[i].sourceDataset) == preferred {
			return i, nil
		}
	}
	return 0, nil
}

func (s *Service) setRemoteBackupCommitMetadata(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteSnapshot string,
	metadata backupCommitMetadata,
) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}
	if !isValidZFSSnapshotName(remoteSnapshot) {
		return fmt.Errorf("invalid_backup_commit_snapshot")
	}
	properties, err := backupCommitProperties(metadata)
	if err != nil {
		return err
	}
	args := []string{"zfs", "set"}
	args = append(args, properties...)
	args = append(args, remoteSnapshot)
	output, err := s.runTargetSSH(ctx, target, args...)
	if err != nil {
		return fmt.Errorf(
			"set_backup_commit_metadata_failed: snapshot=%s error=%w output=%s",
			remoteSnapshot,
			err,
			strings.TrimSpace(output),
		)
	}
	return nil
}

// commitBackupSnapshot verifies the exact source generation against every
// target root before publishing a commit marker. For multi-root jobs the
// coordinator root is written last, so the restore-listing root cannot appear
// committed while another root is still unmarked.
func (s *Service) commitBackupSnapshot(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshotName string,
	scopes []backupScope,
) (backupCommitMetadata, error) {
	if job == nil {
		return backupCommitMetadata{}, fmt.Errorf("backup_job_required")
	}
	snapshotName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil {
		return backupCommitMetadata{}, err
	}
	expected, err := s.buildLocalBackupManifest(ctx, job.ID, snapshotName, job.Recursive, scopes)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("build_backup_source_manifest_failed: %w", err)
	}
	actual, err := s.buildRemoteBackupManifest(ctx, job, snapshotName, scopes)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("build_backup_target_manifest_failed: %w", err)
	}
	if err := compareBackupManifests(expected, actual); err != nil {
		return backupCommitMetadata{}, err
	}

	metadata := newBackupCommitMetadata(expected)
	coordinator, err := backupCommitCoordinatorScope(job, scopes)
	if err != nil {
		return backupCommitMetadata{}, err
	}
	order := make([]int, 0, len(scopes))
	for i := range scopes {
		if i != coordinator {
			order = append(order, i)
		}
	}
	order = append(order, coordinator)
	for _, i := range order {
		remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scopes[i].destSuffix)
		remoteSnapshot := remoteRoot + "@" + snapshotName
		if err := s.setRemoteBackupCommitMetadata(ctx, &job.Target, remoteSnapshot, metadata); err != nil {
			return backupCommitMetadata{}, err
		}
	}
	return metadata, nil
}

func backupCommitPropertyNames() []string {
	return []string{
		backupCommitPropertyVersion,
		backupCommitPropertyJobID,
		backupCommitPropertySnapshot,
		backupCommitPropertyManifest,
		backupCommitPropertyCount,
		backupCommitPropertyRecursive,
		backupCommitPropertyRoots,
	}
}

func parseBackupCommitMetadata(output string) (backupCommitMetadata, error) {
	values := make(map[string]string)
	knownProperties := make(map[string]struct{}, len(backupCommitPropertyNames()))
	for _, property := range backupCommitPropertyNames() {
		knownProperties[property] = struct{}{}
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_property_entry")
		}
		property := strings.TrimSpace(fields[0])
		if _, known := knownProperties[property]; !known {
			return backupCommitMetadata{}, fmt.Errorf("unexpected_backup_commit_property:%s", property)
		}
		if fields[2] != "local" {
			return backupCommitMetadata{}, fmt.Errorf("backup_commit_property_not_local:%s", property)
		}
		if _, duplicate := values[property]; duplicate {
			return backupCommitMetadata{}, fmt.Errorf("duplicate_backup_commit_property:%s", property)
		}
		values[property] = fields[1]
	}
	if err := scanner.Err(); err != nil {
		return backupCommitMetadata{}, err
	}
	if len(values) != len(knownProperties) {
		return backupCommitMetadata{}, fmt.Errorf("backup_snapshot_not_committed")
	}
	for _, property := range backupCommitPropertyNames() {
		value := strings.TrimSpace(values[property])
		if value == "" || value == "-" {
			return backupCommitMetadata{}, fmt.Errorf("backup_snapshot_not_committed")
		}
	}

	version, err := strconv.Atoi(values[backupCommitPropertyVersion])
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_version")
	}
	jobID, err := strconv.ParseUint(values[backupCommitPropertyJobID], 10, 64)
	if err != nil || jobID == 0 {
		return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_job_id")
	}
	entryCount, err := strconv.Atoi(values[backupCommitPropertyCount])
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_count")
	}
	recursive, err := strconv.ParseBool(values[backupCommitPropertyRecursive])
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_recursive")
	}
	roots, err := parseBackupManifestRootsValue(values[backupCommitPropertyRoots])
	if err != nil {
		return backupCommitMetadata{}, err
	}
	metadata := backupCommitMetadata{
		Version:      version,
		JobID:        uint(jobID),
		SnapshotName: values[backupCommitPropertySnapshot],
		ManifestHash: values[backupCommitPropertyManifest],
		EntryCount:   entryCount,
		Recursive:    recursive,
		Roots:        roots,
	}
	if err := metadata.validate(); err != nil {
		return backupCommitMetadata{}, err
	}
	return metadata, nil
}

func (s *Service) getRemoteBackupCommitMetadata(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteSnapshot string,
) (backupCommitMetadata, error) {
	if target == nil {
		return backupCommitMetadata{}, fmt.Errorf("backup_target_required")
	}
	if !isValidZFSSnapshotName(remoteSnapshot) {
		return backupCommitMetadata{}, fmt.Errorf("invalid_backup_commit_snapshot")
	}
	output, err := s.runTargetSSH(
		ctx,
		target,
		"zfs", "get", "-H", "-p", "-o", "property,value,source",
		strings.Join(backupCommitPropertyNames(), ","),
		remoteSnapshot,
	)
	if err != nil {
		return backupCommitMetadata{}, fmt.Errorf("get_backup_commit_metadata_failed: %w", err)
	}
	return parseBackupCommitMetadata(output)
}

func validateBackupCommitForJob(
	metadata backupCommitMetadata,
	job *clusterModels.BackupJob,
	snapshotName string,
) error {
	if job == nil {
		return fmt.Errorf("backup_job_required")
	}
	snapshotName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil {
		return err
	}
	if err := metadata.validate(); err != nil {
		return err
	}
	if metadata.JobID != job.ID {
		return fmt.Errorf("backup_commit_job_mismatch")
	}
	if metadata.SnapshotName != snapshotName {
		return fmt.Errorf("backup_commit_snapshot_mismatch")
	}
	return nil
}
