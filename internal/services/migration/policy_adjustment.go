// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) phasePolicyAdjustment(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	if err := s.adjustReplicationPolicies(ctx, mp, task); err != nil {
		logger.L.Warn().Err(err).Msg("migration_replication_policy_adjustment_failed")
	}

	if err := s.adjustBackupJobs(ctx, mp, task); err != nil {
		logger.L.Warn().Err(err).Msg("migration_backup_job_adjustment_failed")
	}

	return nil
}

func (s *Service) adjustReplicationPolicies(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", task.GuestType, task.GuestID, true).
		Find(&policies).Error; err != nil {
		return fmt.Errorf("lookup_replication_policies_failed: %w", err)
	}

	if len(policies) == 0 {
		return nil
	}

	detail := s.Cluster.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}
	localNodeID := strings.TrimSpace(detail.NodeID)

	now := time.Now().UTC()
	leaseTTL := 20 * time.Second

	for _, policy := range policies {
		newEpoch := policy.OwnerEpoch + 1

		updates := map[string]any{
			"active_node_id":   mp.TargetNodeUUID,
			"source_node_id":   mp.TargetNodeUUID,
			"owner_epoch":      newEpoch,
			"transition_state": "none",
			"updated_at":       now,
		}

		if err := s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", policy.ID).Updates(updates).Error; err != nil {
			logger.L.Warn().Err(err).Uint("policy_id", policy.ID).Msg("migration_replication_policy_update_failed")
			continue
		}

		var existingLease clusterModels.ReplicationLease
		res := s.DB.Where("policy_id = ?", policy.ID).Limit(1).Find(&existingLease)
		if res.Error != nil {
			logger.L.Warn().Err(res.Error).Uint("policy_id", policy.ID).Msg("migration_lease_lookup_failed")
			continue
		}

		if res.RowsAffected > 0 {
			if err := s.DB.Model(&clusterModels.ReplicationLease{}).Where("id = ?", existingLease.ID).Updates(map[string]any{
				"owner_node_id": mp.TargetNodeUUID,
				"owner_epoch":   newEpoch,
				"expires_at":    now.Add(leaseTTL),
				"updated_at":    now,
			}).Error; err != nil {
				logger.L.Warn().Err(err).Uint("lease_id", existingLease.ID).Msg("migration_lease_update_failed")
			}
		}

		logger.L.Info().
			Uint("policy_id", policy.ID).
			Str("guest_type", task.GuestType).
			Uint("guest_id", task.GuestID).
			Str("new_node", mp.TargetNodeUUID).
			Str("old_node", localNodeID).
			Uint64("new_epoch", newEpoch).
			Msg("migration_replication_policy_updated")
	}

	return nil
}

func (s *Service) adjustBackupJobs(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	detail := s.Cluster.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}
	localNodeID := strings.TrimSpace(detail.NodeID)

	var jobs []clusterModels.BackupJob
	if err := s.DB.
		Where("runner_node_id = ? AND enabled = ?", localNodeID, true).
		Find(&jobs).Error; err != nil {
		return fmt.Errorf("lookup_backup_jobs_failed: %w", err)
	}

	if len(jobs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	updated := 0

	for _, job := range jobs {
		if !s.backupJobReferencesGuest(job, task.GuestType, task.GuestID) {
			continue
		}

		if err := s.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Updates(map[string]any{
			"runner_node_id": mp.TargetNodeUUID,
			"updated_at":     now,
		}).Error; err != nil {
			logger.L.Warn().Err(err).Uint("job_id", job.ID).Msg("migration_backup_job_update_failed")
			continue
		}
		updated++

		logger.L.Info().
			Uint("job_id", job.ID).
			Str("job_name", job.Name).
			Str("old_runner", localNodeID).
			Str("new_runner", mp.TargetNodeUUID).
			Msg("migration_backup_job_runner_updated")
	}

	if updated > 0 {
		logger.L.Info().
			Int("updated_count", updated).
			Str("guest_type", task.GuestType).
			Uint("guest_id", task.GuestID).
			Msg("migration_backup_jobs_adjusted")
	}

	return nil
}

func (s *Service) backupJobReferencesGuest(job clusterModels.BackupJob, guestType string, guestID uint) bool {
	if guestType == taskModels.GuestTypeVM && job.Mode == clusterModels.BackupJobModeVM {
		sourceDataset := strings.TrimSpace(job.SourceDataset)
		vmSuffix := fmt.Sprintf("virtual-machines/%d", guestID)
		if strings.Contains(sourceDataset, vmSuffix) {
			return true
		}
	}

	if guestType == taskModels.GuestTypeJail && job.Mode == clusterModels.BackupJobModeJail {
		jailDataset := strings.TrimSpace(job.JailRootDataset)
		jailSuffix := fmt.Sprintf("jails/%d", guestID)
		if strings.Contains(jailDataset, jailSuffix) {
			return true
		}
	}

	return false
}
