// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func (s *Service) MoveGuestIdentityOwner(
	ctx context.Context,
	guestKind string,
	guestID uint,
	newOwnerNodeID string,
	operationToken string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.DB == nil || s.Raft() == nil || s.Raft().State() != raft.Leader {
		return fmt.Errorf("guest_identity_move_requires_leader")
	}
	guestKind = strings.ToLower(strings.TrimSpace(guestKind))
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	var operation clusterModels.ReplicationGuestOperation
	if err := s.DB.Where(
		"guest_type = ? AND guest_id = ? AND operation = ? AND token = ?",
		guestKind,
		guestID,
		clusterModels.ReplicationGuestOperationMigration,
		strings.TrimSpace(operationToken),
	).First(&operation).Error; err != nil {
		return fmt.Errorf("guest_identity_migration_operation_lookup_failed: %w", err)
	}
	if operation.State != clusterModels.ReplicationGuestOperationCutover ||
		strings.TrimSpace(operation.OwnerNodeID) == "" ||
		strings.TrimSpace(operation.TargetNodeID) != newOwnerNodeID {
		return fmt.Errorf("guest_identity_migration_operation_mismatch")
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if err := s.RequireCurrentRaftVoter(newOwnerNodeID); err != nil {
		return fmt.Errorf("guest_identity_move_target_not_current_voter: %w", err)
	}
	return s.apply("guest_identity", "move", clusterModels.GuestIdentityMoveOwner{
		GuestKind:      guestKind,
		GuestID:        guestID,
		OldOwnerNodeID: strings.TrimSpace(operation.OwnerNodeID),
		NewOwnerNodeID: newOwnerNodeID,
	})
}
