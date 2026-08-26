// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type backupTopologyEntry struct {
	Suffix string
	Type   string
}

type archivedBackupTopology struct {
	Active   string
	Archived string
}

func backupTopologyEntries(datasets map[string]string, root string) ([]backupTopologyEntry, error) {
	root = normalizeDatasetPath(root)
	if root == "" {
		return nil, fmt.Errorf("backup_topology_root_required")
	}
	entries := make([]backupTopologyEntry, 0, len(datasets))
	for dataset, datasetType := range datasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset != root && !strings.HasPrefix(dataset, root+"/") {
			return nil, fmt.Errorf("backup_topology_dataset_outside_root:%s", dataset)
		}
		entries = append(entries, backupTopologyEntry{
			Suffix: strings.TrimPrefix(strings.TrimPrefix(dataset, root), "/"),
			Type:   strings.ToLower(strings.TrimSpace(datasetType)),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Suffix < entries[j].Suffix })
	return entries, nil
}

func backupTopologiesEqual(left, right []backupTopologyEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) backupScopeTopologyChanged(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scope backupScope,
) (bool, error) {
	if job == nil {
		return false, fmt.Errorf("backup_job_required")
	}
	sourceRoot := normalizeDatasetPath(scope.sourceDataset)
	remoteRoot := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
	remoteExists, err := s.targetDatasetExists(ctx, &job.Target, remoteRoot)
	if err != nil {
		return false, fmt.Errorf("backup_topology_target_exists_failed: %w", err)
	}
	if !remoteExists {
		return false, nil
	}

	localArgs := backupDatasetListArgs(sourceRoot, job.Recursive)
	localOutput, err := utils.RunCommandWithContext(ctx, localArgs[0], localArgs[1:]...)
	if err != nil {
		return false, fmt.Errorf("backup_topology_source_list_failed: %w", err)
	}
	localDatasets, err := parseBackupDatasetTree(localOutput, sourceRoot, job.Recursive)
	if err != nil {
		return false, err
	}
	localTopology, err := backupTopologyEntries(localDatasets, sourceRoot)
	if err != nil {
		return false, err
	}

	// Always inspect the full target tree. A formerly recursive job changed to
	// nonrecursive must rotate away descendants rather than silently retaining
	// them in the active generation.
	remoteArgs := backupDatasetListArgs(remoteRoot, true)
	remoteOutput, err := s.runTargetSSH(ctx, &job.Target, remoteArgs...)
	if err != nil {
		return false, fmt.Errorf("backup_topology_target_list_failed: %w", err)
	}
	remoteDatasets, err := parseBackupDatasetTree(remoteOutput, remoteRoot, true)
	if err != nil {
		return false, err
	}
	remoteTopology, err := backupTopologyEntries(remoteDatasets, remoteRoot)
	if err != nil {
		return false, err
	}
	return !backupTopologiesEqual(localTopology, remoteTopology), nil
}

func (s *Service) archiveChangedBackupTopologies(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) ([]archivedBackupTopology, error) {
	archives := make([]archivedBackupTopology, 0)
	for _, scope := range scopes {
		changed, err := s.backupScopeTopologyChanged(ctx, job, scope)
		if err != nil {
			s.rollbackBackupTopologyArchives(ctx, &job.Target, archives)
			return nil, err
		}
		if !changed {
			continue
		}
		active, archived, err := s.archiveActiveTargetDatasetForReseed(ctx, &job.Target, scope.destSuffix)
		if err != nil {
			s.rollbackBackupTopologyArchives(ctx, &job.Target, archives)
			return nil, fmt.Errorf("backup_topology_archive_failed: %w", err)
		}
		if archived != "" {
			archives = append(archives, archivedBackupTopology{Active: active, Archived: archived})
		}
	}
	return archives, nil
}

func (s *Service) rollbackBackupTopologyArchives(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	archives []archivedBackupTopology,
) {
	for i := len(archives) - 1; i >= 0; i-- {
		activeExists, err := s.targetDatasetExists(ctx, target, archives[i].Active)
		if err != nil || activeExists {
			continue
		}
		_ = s.renameTargetDataset(ctx, target, archives[i].Archived, archives[i].Active)
	}
}
