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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func guestIdentityOwnershipMoveToken(operationKey string) (string, error) {
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return "", fmt.Errorf("guest_identity_move_operation_key_required")
	}
	sum := sha256.Sum256([]byte("sylve-guest-identity-owner-move-v1\x00" + operationKey))
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) authoritativeGuestIdentityMove(
	guestKind string,
	guestID uint,
	expectedOwnerNodeID string,
	newOwnerNodeID string,
	operationKey string,
) (clusterModels.GuestIdentityMoveOwner, error) {
	var move clusterModels.GuestIdentityMoveOwner
	guestKind = strings.ToLower(strings.TrimSpace(guestKind))
	expectedOwnerNodeID = strings.TrimSpace(expectedOwnerNodeID)
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	if !clusterModels.ValidGuestIdentityKind(guestKind) || guestID == 0 ||
		guestID > clusterModels.GuestIdentityMaxID || expectedOwnerNodeID == "" ||
		newOwnerNodeID == "" || expectedOwnerNodeID == newOwnerNodeID {
		return move, fmt.Errorf("guest_identity_move_invalid")
	}
	newToken, err := guestIdentityOwnershipMoveToken(operationKey)
	if err != nil {
		return move, err
	}
	claims, err := s.authoritativeGuestIdentityClaims()
	if err != nil {
		return move, err
	}
	for _, claim := range claims {
		if claim.GuestID != guestID {
			continue
		}
		if claim.GuestKind != guestKind {
			return move, fmt.Errorf("%w: guest_id=%d", clusterModels.ErrGuestIdentityClaimConflict, guestID)
		}
		oldToken := claim.Token
		if claim.OwnerNodeID == newOwnerNodeID && claim.Token == newToken {
			oldToken = "already-moved-previous-token"
		} else if claim.OwnerNodeID != expectedOwnerNodeID {
			return move, fmt.Errorf(
				"%w: guest_id=%d expected_owner=%s actual_owner=%s",
				clusterModels.ErrGuestIdentityClaimConflict,
				guestID,
				expectedOwnerNodeID,
				claim.OwnerNodeID,
			)
		}
		if oldToken == newToken {
			return move, fmt.Errorf("guest_identity_move_token_collision")
		}
		return clusterModels.GuestIdentityMoveOwner{
			GuestKind:      guestKind,
			GuestID:        guestID,
			OldOwnerNodeID: expectedOwnerNodeID,
			NewOwnerNodeID: newOwnerNodeID,
			OldToken:       oldToken,
			NewToken:       newToken,
		}, nil
	}
	return move, fmt.Errorf("%w: guest_id=%d missing", clusterModels.ErrGuestIdentityClaimConflict, guestID)
}

func (s *Service) replicationPolicyGuestIdentityMove(
	policyID uint,
	expectedOwnerNodeID string,
	newOwnerNodeID string,
	operationKey string,
) (*clusterModels.GuestIdentityMoveOwner, error) {
	if policyID == 0 {
		return nil, fmt.Errorf("replication_policy_id_required")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("cluster_service_not_initialized")
	}
	if !s.DB.Migrator().HasTable(&clusterModels.GuestIdentityRegistry{}) {
		return nil, nil
	}
	var policy clusterModels.ReplicationPolicy
	if err := s.DB.First(&policy, policyID).Error; err != nil {
		return nil, fmt.Errorf("replication_policy_guest_identity_lookup_failed: %w", err)
	}
	move, err := s.authoritativeGuestIdentityMove(
		policy.GuestType,
		policy.GuestID,
		expectedOwnerNodeID,
		newOwnerNodeID,
		operationKey,
	)
	if err != nil {
		return nil, err
	}
	return &move, nil
}

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
	if s == nil || s.DB == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return fmt.Errorf("guest_identity_move_requires_leader")
	}
	guestKind = strings.ToLower(strings.TrimSpace(guestKind))
	newOwnerNodeID = strings.TrimSpace(newOwnerNodeID)
	operationToken = strings.TrimSpace(operationToken)
	var operation clusterModels.ReplicationGuestOperation
	if err := s.DB.Where(
		"guest_type = ? AND guest_id = ? AND operation = ? AND token = ?",
		guestKind,
		guestID,
		clusterModels.ReplicationGuestOperationMigration,
		operationToken,
	).First(&operation).Error; err != nil {
		return fmt.Errorf("guest_identity_migration_operation_lookup_failed: %w", err)
	}
	if operation.State != clusterModels.ReplicationGuestOperationCutover ||
		strings.TrimSpace(operation.OwnerNodeID) == "" ||
		strings.TrimSpace(operation.TargetNodeID) != newOwnerNodeID {
		return fmt.Errorf("guest_identity_migration_operation_mismatch")
	}
	move, err := s.authoritativeGuestIdentityMove(
		guestKind,
		guestID,
		operation.OwnerNodeID,
		newOwnerNodeID,
		operationToken,
	)
	if err != nil {
		return err
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if err := s.RequireCurrentRaftVoter(newOwnerNodeID); err != nil {
		return fmt.Errorf("guest_identity_move_target_not_current_voter: %w", err)
	}
	return s.applyGuestIdentityRaftAction("move_id_owner", move)
}
