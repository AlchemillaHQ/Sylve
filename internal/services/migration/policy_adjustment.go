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

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) phasePolicyAdjustment(ctx context.Context, mp *migrationPayload, task taskModels.GuestLifecycleTask) error {
	if s.WorkloadGuard == nil {
		logger.L.Warn().
			Str("guest_type", task.GuestType).
			Uint("guest_id", task.GuestID).
			Msg("migration_ownership_reassignment_skipped_no_guard")
		mp.Warnings = append(mp.Warnings, "warning_ownership_reassignment_skipped_no_guard")
		return nil
	}

	if err := s.WorkloadGuard.MigrateGuestOwnership(ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID); err != nil {
		logger.L.Warn().Err(err).
			Str("guest_type", task.GuestType).
			Uint("guest_id", task.GuestID).
			Str("new_owner", mp.TargetNodeUUID).
			Msg("migration_ownership_reassignment_failed")
		mp.Warnings = append(mp.Warnings, "warning_ownership_reassignment_failed")
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
