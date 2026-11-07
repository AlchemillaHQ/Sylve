// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"gorm.io/gorm"
)

// normalizeTargetPools drops blank and same-name entries, so an empty map
// means every dataset keeps its pool.
func normalizeTargetPools(pools map[string]string) map[string]string {
	var out map[string]string
	for source, target := range pools {
		source, target = strings.TrimSpace(source), strings.TrimSpace(target)
		if source == "" || target == "" || source == target {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[source] = target
	}
	return out
}

func migrationTargetRoot(root string, pools map[string]string) string {
	pool, rest, ok := strings.Cut(root, "/")
	if target, mapped := pools[pool]; ok && mapped {
		return target + "/" + rest
	}
	return root
}

// TargetRoot is where one source root goes on the target node. Root is the
// guest root that ends up holding its datasets. When several source pools map
// to one target pool, all but the first are received into a staging dataset
// beside Root (a stream received into Root itself with -F would destroy them),
// and the target moves their datasets into Root at import.
type TargetRoot struct {
	Source  string
	Root    string
	Receive string
}

func TargetDatasetRoots(roots []string, pools map[string]string) []TargetRoot {
	targets := make([]TargetRoot, 0, len(roots))
	merged := make(map[string]int)
	for _, root := range normalizedMigrationDatasetRootManifest(roots) {
		target := TargetRoot{Source: root, Root: migrationTargetRoot(root, pools)}
		target.Receive = target.Root
		if n := merged[target.Root]; n > 0 {
			target.Receive = fmt.Sprintf("%s.merge-%d", target.Root, n)
		}
		merged[target.Root]++
		targets = append(targets, target)
	}
	return targets
}

func (s *Service) remoteTargetPools(ctx context.Context, targetNodeID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return cluster.CallNode[struct{}, []string](ctx, s.Cluster, targetNodeID, "migration-target-pools", struct{}{})
}

// targetPoolPreflight checks that each pool holding the guest's datasets maps
// to a usable pool on the target node. It also returns both pool lists, so
// the UI can offer the target's pools for a missing one.
func (s *Service) targetPoolPreflight(
	ctx context.Context,
	guestType string,
	guestID uint,
	targetNode clusterModels.ClusterNode,
	pools map[string]string,
) (reasons, guestPools, nodePools []string) {
	roots, err := s.resolveGuestDatasets(ctx, guestType, guestID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return []string{fmt.Sprintf("guest_dataset_resolution_failed: %v", err)}, nil, nil
	}
	rootByPool := make(map[string]string)
	for _, root := range filterParentDatasets(normalizedMigrationDatasetRootManifest(roots)) {
		pool, _, _ := strings.Cut(root, "/")
		rootByPool[pool] = root
	}
	guestPools = slices.Sorted(maps.Keys(rootByPool))

	nodePools, err = s.remoteTargetPools(ctx, targetNode.NodeUUID)
	if err != nil {
		return []string{fmt.Sprintf("target_pool_check_failed: %v", err)}, guestPools, nil
	}
	if len(nodePools) == 0 && len(guestPools) > 0 {
		return []string{"target_has_no_usable_pools"}, guestPools, nil
	}

	for _, source := range slices.Sorted(maps.Keys(pools)) {
		if _, ok := rootByPool[source]; !ok {
			reasons = append(reasons, fmt.Sprintf("target_pool_mapping_unknown: %s", source))
		}
	}
	if len(pools) > 0 {
		hasJobs, err := s.guestHasBackupJobs(ctx, guestType, guestID)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("backup_job_lookup_failed: %v", err))
		} else if hasJobs {
			reasons = append(reasons, "target_pool_change_with_backup_jobs")
		}
	}

	for _, root := range TargetDatasetRoots(slices.Collect(maps.Values(rootByPool)), pools) {
		pool, _, _ := strings.Cut(root.Root, "/")
		if !slices.Contains(nodePools, pool) {
			if reason := fmt.Sprintf("target_missing_pool: %s", pool); !slices.Contains(reasons, reason) {
				reasons = append(reasons, reason)
			}
			continue
		}
		info, err := s.targetDataset(ctx, targetNode.NodeUUID, TargetDatasetRequest{
			GuestType: guestType, GuestID: guestID, Dataset: root.Receive,
		})
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("target_guest_check_failed_%s: %v", pool, err))
		} else if info.Exists {
			reasons = append(reasons, fmt.Sprintf("warning_stale_dataset_on_target: %s", root.Receive))
		}
	}

	return reasons, guestPools, nodePools
}

// A backup job names the guest's datasets with their pool, so moving the
// guest to another pool would leave the job pointing at the old one.
func (s *Service) guestHasBackupJobs(ctx context.Context, guestType string, guestID uint) (bool, error) {
	var jobs []clusterModels.BackupJob
	if err := s.DB.WithContext(ctx).Find(&jobs).Error; err != nil {
		return false, err
	}
	for i := range jobs {
		jobGuestType, jobGuestID := clusterModels.BackupJobGuestIdentity(&jobs[i])
		if jobGuestType == guestType && jobGuestID == guestID {
			return true, nil
		}
	}
	return false, nil
}
