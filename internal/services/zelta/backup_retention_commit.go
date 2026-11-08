// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
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

type zfsCommand func(ctx context.Context, argv ...string) (string, error)

type backupSnapshotTree struct {
	root      string
	snapshots []SnapshotInfo
	recursive bool
}

// zfs destroy -r is not atomic, so a held or cloned generation is kept whole, and
// parents go first: an interrupted run only leaves children whose root is gone.
func destroyProvenBackupSnapshots(
	ctx context.Context,
	run zfsCommand,
	candidates []string,
	proofs map[string]string,
	tree backupSnapshotTree,
) (int, map[string]struct{}, error) {
	if len(candidates) == 0 {
		return 0, nil, nil
	}
	for _, candidate := range candidates {
		if proofs[candidate] == "" {
			return 0, nil, fmt.Errorf("backup_retention_snapshot_unproven: snapshot=%s", candidate)
		}
	}
	output, err := run(ctx, append([]string{
		"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid,userrefs,clones",
	}, candidates...)...)
	if err != nil {
		return 0, nil, fmt.Errorf("backup_retention_snapshot_recheck_failed: %w", err)
	}
	type snapshotState struct{ guid, userrefs, clones string }
	current := make(map[string]snapshotState, len(candidates))
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) == 4 {
			current[fields[0]] = snapshotState{fields[1], fields[2], fields[3]}
		}
	}
	busy := make(map[string]struct{})
	var inUse []string
	for _, candidate := range candidates {
		state, ok := current[candidate]
		if !ok {
			return 0, nil, fmt.Errorf("backup_retention_snapshot_recheck_missing: snapshot=%s", candidate)
		}
		if state.guid != proofs[candidate] {
			return 0, nil, fmt.Errorf(
				"backup_retention_snapshot_proof_changed: snapshot=%s expected_guid=%s actual_guid=%s",
				candidate, proofs[candidate], state.guid,
			)
		}
		clones := strings.FieldsFunc(state.clones, func(r rune) bool { return r == ',' })
		if state.clones == "-" {
			clones = nil
		}
		if state.userrefs != "0" {
			inUse = append(inUse, candidate+" (held)")
		} else {
			for _, clone := range clones {
				if tree.root == "" || !strings.HasPrefix(clone, tree.root+"_gen-") {
					inUse = append(inUse, candidate+" (cloned to "+clone+")")
				}
			}
		}
		if state.userrefs != "0" || len(clones) > 0 {
			busy[backupRetentionGeneration(candidate)] = struct{}{}
		}
	}
	var busyErr error
	if len(inUse) > 0 {
		busyErr = fmt.Errorf("backup_retention_snapshot_in_use: %s", strings.Join(inUse, ", "))
	}

	byDataset := make(map[string][]string)
	datasetsByGeneration := make(map[string]map[string]struct{})
	for _, candidate := range candidates {
		generation := backupRetentionGeneration(candidate)
		if _, skip := busy[generation]; skip {
			continue
		}
		dataset := snapshotDatasetName(candidate)
		byDataset[dataset] = append(byDataset[dataset], generation)
		if datasetsByGeneration[generation] == nil {
			datasetsByGeneration[generation] = make(map[string]struct{})
		}
		datasetsByGeneration[generation][dataset] = struct{}{}
	}
	if len(byDataset) == 0 {
		return 0, busy, busyErr
	}
	destroyed := 0
	for _, datasets := range datasetsByGeneration {
		destroyed += len(datasets)
	}
	if generations := recursiveDestroyGenerations(tree, datasetsByGeneration); generations != nil {
		_, err := run(ctx, "zfs", "destroy", "-r", tree.root+"@"+strings.Join(generations, ","))
		if err != nil {
			return 0, nil, err
		}
		return destroyed, busy, busyErr
	}
	datasets := make([]string, 0, len(byDataset))
	for dataset := range byDataset {
		datasets = append(datasets, dataset)
	}
	sort.Strings(datasets)
	for _, dataset := range datasets {
		if _, err := run(ctx, "zfs", "destroy", dataset+"@"+strings.Join(byDataset[dataset], ",")); err != nil {
			return 0, nil, err
		}
	}
	return destroyed, busy, busyErr
}

func recursiveDestroyGenerations(
	tree backupSnapshotTree,
	datasetsByGeneration map[string]map[string]struct{},
) []string {
	if !tree.recursive {
		return nil
	}
	held := make(map[string]map[string]struct{}, len(datasetsByGeneration))
	for _, snapshot := range tree.snapshots {
		generation := backupRetentionGeneration(snapshot.Name)
		if _, wanted := datasetsByGeneration[generation]; !wanted {
			continue
		}
		dataset := snapshotDatasetName(snapshot.Name)
		if !datasetWithinRoot(tree.root, dataset) {
			continue
		}
		if held[generation] == nil {
			held[generation] = make(map[string]struct{})
		}
		held[generation][dataset] = struct{}{}
	}
	generations := make([]string, 0, len(datasetsByGeneration))
	for generation, candidates := range datasetsByGeneration {
		if _, ok := candidates[tree.root]; !ok || !maps.Equal(held[generation], candidates) {
			return nil
		}
		generations = append(generations, generation)
	}
	sort.Strings(generations)
	return generations
}

func (s *Service) destroyLocalBackupSnapshotsWithProof(
	ctx context.Context,
	candidates []string,
	proofs map[string]string,
	tree backupSnapshotTree,
) (int, map[string]struct{}, error) {
	for _, candidate := range candidates {
		if _, err := remoteexec.ParseZFSSnapshot(candidate); err != nil {
			return 0, nil, fmt.Errorf("backup_retention_local_snapshot_invalid: %w", err)
		}
	}
	return destroyProvenBackupSnapshots(ctx, runLocalZFS, candidates, proofs, tree)
}

func (s *Service) destroyTargetBackupSnapshotsWithProof(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	candidates []string,
	proofs map[string]string,
	tree backupSnapshotTree,
) (int, map[string]struct{}, error) {
	if target == nil {
		return 0, nil, fmt.Errorf("backup_target_required")
	}
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return 0, nil, err
	}
	for _, candidate := range candidates {
		parsed, err := remoteexec.ParseZFSSnapshot(candidate)
		if err != nil {
			return 0, nil, fmt.Errorf("backup_retention_target_snapshot_invalid: %w", err)
		}
		if !parsed.Dataset().Within(root) {
			return 0, nil, fmt.Errorf("backup_retention_target_snapshot_outside_root: snapshot=%s", candidate)
		}
	}
	return destroyProvenBackupSnapshots(ctx, func(ctx context.Context, argv ...string) (string, error) {
		output, err := s.runTargetSSH(ctx, target, argv...)
		if err != nil && isRemoteSubcommandBlocked(output) {
			logger.L.Warn().Str("ssh_host", target.SSHHost).Msg("remote_zfs_destroy_not_permitted")
			return output, fmt.Errorf("remote_zfs_destroy_not_permitted: %w", err)
		}
		return output, err
	}, candidates, proofs, tree)
}

func (s *Service) pruneBackupJob(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) ([]string, error) {
	var output []string
	fail := func(err error) ([]string, error) {
		logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("backup_prune_failed")
		return append(output, err.Error()), err
	}

	coordinator, err := backupCommitCoordinatorScope(job, scopes)
	if err != nil {
		return fail(fmt.Errorf("backup_prune_commit_coordinator_failed: %w", err))
	}
	inventories, err := s.loadBackupRetentionScopeInventories(ctx, job, scopes)
	if err != nil {
		return fail(fmt.Errorf("backup_prune_commit_inventory_failed: %w", err))
	}
	for _, inventory := range inventories {
		if inventory.localSnapshotErr != nil {
			return fail(fmt.Errorf(
				"backup_prune_local_snapshot_list_failed: source=%s: %w",
				inventory.sourceRoot,
				inventory.localSnapshotErr,
			))
		}
	}
	commitRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scopes[coordinator].destSuffix)
	commitSnapshots, ok := backupRetentionSnapshotsForRoot(inventories, commitRoot)
	if !ok {
		return fail(fmt.Errorf("backup_prune_commit_validation_failed: backup_retention_commit_inventory_missing"))
	}
	proofs, err := s.backupRetentionEligibleSnapshotProofsFromInventories(
		ctx,
		job,
		commitRoot,
		commitSnapshots,
		scopes,
		backupRetentionManifestInventories(inventories),
	)
	if err != nil {
		return fail(fmt.Errorf("backup_prune_commit_validation_failed: %w", err))
	}

	started := time.Now()
	plans, stats := limitBackupRetentionCleanupPlan(
		buildBackupRetentionCleanupPlan(job, inventories, proofs),
		backupRetentionGenerationBudget,
	)
	if stats.DeferredGenerations > 0 {
		output = append(output, fmt.Sprintf(
			"backup_prune_deferred: generation_budget=%d selected_generations=%d deferred_generations=%d",
			backupRetentionGenerationBudget,
			stats.SelectedGenerations,
			stats.DeferredGenerations,
		))
	}

	// A generation kept locally stays on the target too, so it remains provable.
	var pruneErr error
	prunedLocal, prunedTarget := 0, 0
	keptLocally := make(map[string]struct{})
	for _, plan := range plans {
		tree := backupSnapshotTree{plan.inventory.sourceRoot, plan.inventory.localSnapshots, job.Recursive}
		destroyed, kept, err := s.destroyLocalBackupSnapshotsWithProof(ctx, plan.localCandidates, proofs.Source, tree)
		prunedLocal += destroyed
		maps.Copy(keptLocally, kept)
		if err != nil && kept == nil {
			for _, candidate := range plan.localCandidates {
				keptLocally[backupRetentionGeneration(candidate)] = struct{}{}
			}
		}
		if err != nil {
			err = fmt.Errorf("backup_prune_destroy_failed: source=%s: %w", plan.inventory.sourceRoot, err)
			output = append(output, err.Error())
			pruneErr = errors.Join(pruneErr, err)
		}
	}
	if job.PruneTarget {
		for _, plan := range plans {
			candidates := make([]string, 0, len(plan.targetCandidates))
			for _, candidate := range plan.targetCandidates {
				if _, kept := keptLocally[backupRetentionGeneration(candidate)]; !kept {
					candidates = append(candidates, candidate)
				}
			}
			tree := backupSnapshotTree{plan.inventory.remoteRoot, plan.inventory.remoteSnapshots, job.Recursive}
			destroyed, _, err := s.destroyTargetBackupSnapshotsWithProof(ctx, &job.Target, candidates, proofs.Target, tree)
			prunedTarget += destroyed
			if err != nil {
				err = fmt.Errorf("backup_prune_target_destroy_failed: source=%s: %w", plan.inventory.sourceRoot, err)
				output = append(output, err.Error())
				pruneErr = errors.Join(pruneErr, err)
			}
		}
	}

	logger.L.Info().
		Uint("job_id", job.ID).
		Int("candidate_generations", stats.CandidateGenerations).
		Int("selected_generations", stats.SelectedGenerations).
		Int("deferred_generations", stats.DeferredGenerations).
		Int("pruned_local", prunedLocal).
		Int("pruned_target", prunedTarget).
		Err(pruneErr).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("backup_prune_pass_completed")
	return output, pruneErr
}

// errBackupGenerationKept marks an archive that is not provably this job's; it is kept, not an error.
var errBackupGenerationKept = errors.New("backup_generation_not_provably_owned")

func archivedGenerationTime(name string) (int64, int) {
	token := name[strings.LastIndex(name, "_gen-")+len("_gen-"):]
	attempt := 0
	if base, suffix, ok := strings.Cut(token, "-"); ok {
		token = base
		attempt, _ = strconv.Atoi(suffix)
	}
	ms, err := strconv.ParseInt(token, 36, 64)
	if err != nil {
		return 0, attempt
	}
	return ms, attempt
}

// pruneArchivedGenerations keeps the newest job.PruneKeepGenerations archived
// generations of each target dataset and destroys the older ones.
func (s *Service) pruneArchivedGenerations(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) ([]string, error) {
	if job.PruneKeepGenerations <= 0 {
		return nil, nil
	}
	var output []string
	var pruneErr error
	run := s.targetZFS(&job.Target)
	for _, scope := range scopes {
		active := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		parent := active[:strings.LastIndex(active, "/")]
		listing, err := run(ctx, "zfs", "list", "-H", "-o", "name", "-t", "filesystem,volume", "-d", "1", parent)
		if err != nil {
			pruneErr = errors.Join(pruneErr, fmt.Errorf("backup_generation_list_failed: %s: %w", active, err))
			continue
		}
		var archives []string
		for _, name := range strings.Fields(listing) {
			if strings.HasPrefix(name, active+"_gen-") {
				archives = append(archives, name)
			}
		}
		sort.Slice(archives, func(i, j int) bool {
			ti, ai := archivedGenerationTime(archives[i])
			tj, aj := archivedGenerationTime(archives[j])
			if ti != tj {
				return ti > tj
			}
			return ai > aj
		})
		for _, archive := range archives[min(job.PruneKeepGenerations, len(archives)):] {
			err := s.destroyArchivedGeneration(ctx, job, run, archive)
			if errors.Is(err, errBackupGenerationKept) {
				output = append(output, fmt.Sprintf("backup_generation_kept: %s: %v", archive, err))
				continue
			}
			if err != nil {
				err = fmt.Errorf("backup_generation_prune_failed: %s: %w", archive, err)
				output = append(output, err.Error())
				pruneErr = errors.Join(pruneErr, err)
				continue
			}
			output = append(output, "backup_generation_pruned: "+archive)
		}
	}
	return output, pruneErr
}

// destroyArchivedGeneration removes one archive of this job. Datasets cloned
// from it are promoted first: that moves the shared history into them, so only
// the archive's own snapshots are destroyed.
func (s *Service) destroyArchivedGeneration(
	ctx context.Context,
	job *clusterModels.BackupJob,
	run zfsCommand,
	archive string,
) error {
	owner, err := run(ctx, "zfs", "get", "-H", "-o", "value", "-s", "local", "sylve:backup_job_id", archive)
	if err != nil {
		return err
	}
	if strings.TrimSpace(owner) != strconv.FormatUint(uint64(job.ID), 10) {
		return fmt.Errorf("%w: not_owned", errBackupGenerationKept)
	}
	jobPrefix := backupSnapshotPrefixForJob(job.ID)
	for attempt := 0; ; attempt++ {
		listing, err := run(ctx, "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,clones", "-s", "createtxg", "-r", archive)
		if err != nil {
			return err
		}
		// Promote the clone of the newest dependent snapshot; one promote can
		// free several older ones, so list again afterwards.
		dependent := ""
		for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
			name, clones, _ := strings.Cut(line, "\t")
			if name == "" {
				continue
			}
			if !isBKSnapshotShortName(strings.TrimPrefix(name[strings.LastIndex(name, "@"):], "@"), jobPrefix) {
				return fmt.Errorf("%w: foreign_snapshot %s", errBackupGenerationKept, name)
			}
			for _, clone := range strings.Split(clones, ",") {
				if clone != "" && clone != "-" && !datasetWithinRoot(archive, clone) {
					dependent = clone
				}
			}
		}
		if dependent == "" {
			break
		}
		if attempt >= 64 {
			return fmt.Errorf("backup_generation_dependents_remain")
		}
		if _, err := run(ctx, "zfs", "promote", dependent); err != nil {
			return err
		}
	}
	_, err = run(ctx, "zfs", "destroy", "-r", archive)
	return err
}
