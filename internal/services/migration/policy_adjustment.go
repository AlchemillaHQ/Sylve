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

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
)

func (s *Service) phasePolicyAdjustment(
	ctx context.Context,
	mp *migrationPayload,
	task taskModels.GuestLifecycleTask,
	operationToken string,
) error {
	if s.WorkloadGuard == nil {
		return fmt.Errorf("migration_ownership_reassignment_guard_unavailable")
	}

	if err := s.WorkloadGuard.MigrateGuestOwnership(
		ctx, task.GuestType, task.GuestID, mp.TargetNodeUUID, operationToken,
	); err != nil {
		return fmt.Errorf("migration_ownership_reassignment_failed: %w", err)
	}

	return nil
}
