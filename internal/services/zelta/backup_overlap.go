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
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type backupPoolIdentity struct {
	GUID    uint64
	Dataset string
}

func backupDatasetPool(dataset string) (string, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return "", fmt.Errorf("backup_dataset_required")
	}
	if idx := strings.IndexByte(dataset, '/'); idx >= 0 {
		return dataset[:idx], nil
	}
	return dataset, nil
}

func parseBackupPoolGUID(output string) (uint64, error) {
	value := strings.TrimSpace(output)
	var guid uint64
	for _, rawLine := range strings.Split(value, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "Warning: Identity file ") {
			continue
		}
		parsed, err := strconv.ParseUint(line, 10, 64)
		if err != nil || parsed == 0 || guid != 0 {
			return 0, fmt.Errorf("invalid_backup_pool_guid_output: %q", value)
		}
		guid = parsed
	}
	if guid == 0 {
		return 0, fmt.Errorf("invalid_backup_pool_guid: %q", value)
	}
	return guid, nil
}

func backupDatasetWithinSamePool(left, right backupPoolIdentity) bool {
	if left.GUID == 0 || right.GUID == 0 || left.GUID != right.GUID {
		return false
	}
	leftDataset := normalizeDatasetPath(left.Dataset)
	rightDataset := normalizeDatasetPath(right.Dataset)
	if leftDataset == "" || rightDataset == "" {
		return false
	}
	leftSlash := strings.IndexByte(leftDataset, '/')
	rightSlash := strings.IndexByte(rightDataset, '/')
	leftRelative := ""
	rightRelative := ""
	if leftSlash >= 0 {
		leftRelative = leftDataset[leftSlash+1:]
	}
	if rightSlash >= 0 {
		rightRelative = rightDataset[rightSlash+1:]
	}
	return datasetPathEqualOrAncestor(leftRelative, rightRelative) ||
		datasetPathEqualOrAncestor(rightRelative, leftRelative)
}

func datasetPathEqualOrAncestor(ancestor, dataset string) bool {
	ancestor = normalizeDatasetPath(ancestor)
	dataset = normalizeDatasetPath(dataset)
	if ancestor == "" {
		return true
	}
	return dataset == ancestor || strings.HasPrefix(dataset, ancestor+"/")
}

func (s *Service) localBackupPoolIdentity(ctx context.Context, dataset string) (backupPoolIdentity, error) {
	dataset = normalizeDatasetPath(dataset)
	pool, err := backupDatasetPool(dataset)
	if err != nil {
		return backupPoolIdentity{}, err
	}
	output, err := utils.RunCommandWithContext(ctx, "zpool", "get", "-H", "-p", "-o", "value", "guid", pool)
	if err != nil {
		return backupPoolIdentity{}, fmt.Errorf("backup_source_pool_guid_failed: %w", err)
	}
	guid, err := parseBackupPoolGUID(output)
	if err != nil {
		return backupPoolIdentity{}, err
	}
	return backupPoolIdentity{GUID: guid, Dataset: dataset}, nil
}

func (s *Service) remoteBackupPoolIdentity(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
) (backupPoolIdentity, error) {
	if target == nil {
		return backupPoolIdentity{}, fmt.Errorf("backup_target_required")
	}
	dataset = normalizeDatasetPath(dataset)
	pool, err := backupDatasetPool(dataset)
	if err != nil {
		return backupPoolIdentity{}, err
	}
	output, err := s.runTargetSSH(ctx, target, "zpool", "get", "-H", "-p", "-o", "value", "guid", pool)
	if err != nil {
		return backupPoolIdentity{}, fmt.Errorf("backup_target_pool_guid_failed: %w", err)
	}
	guid, err := parseBackupPoolGUID(output)
	if err != nil {
		return backupPoolIdentity{}, err
	}
	return backupPoolIdentity{GUID: guid, Dataset: dataset}, nil
}

func (s *Service) validateBackupScopesDoNotOverlapTarget(
	ctx context.Context,
	job *clusterModels.BackupJob,
	scopes []backupScope,
) error {
	if job == nil {
		return fmt.Errorf("backup_job_required")
	}
	if len(scopes) == 0 {
		return fmt.Errorf("backup_scopes_required")
	}
	for _, scope := range scopes {
		sourceIdentity, err := s.localBackupPoolIdentity(ctx, scope.sourceDataset)
		if err != nil {
			return err
		}
		remoteDataset := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		targetIdentity, err := s.remoteBackupPoolIdentity(ctx, &job.Target, remoteDataset)
		if err != nil {
			return err
		}
		if backupDatasetWithinSamePool(sourceIdentity, targetIdentity) {
			return fmt.Errorf(
				"backup_source_target_overlap: source=%s target=%s pool_guid=%d",
				sourceIdentity.Dataset,
				targetIdentity.Dataset,
				sourceIdentity.GUID,
			)
		}
	}
	return nil
}
