// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func moveReplicationPolicyGuestIdentityClaimTxn(
	tx *gorm.DB,
	policy *ReplicationPolicy,
	expectedOwnerNodeID string,
	newOwnerNodeID string,
	move *GuestIdentityMoveOwner,
) error {
	if tx == nil || policy == nil {
		return fmt.Errorf("replication_guest_identity_policy_required")
	}
	if !tx.Migrator().HasTable(&GuestIdentityRegistry{}) {
		return nil
	}
	registry, err := loadGuestIdentityRegistry(tx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrGuestIdentityRegistryInitializing
	}
	if err != nil {
		return err
	}
	if registry.Phase != GuestIdentityRegistryPhaseActive {
		return ErrGuestIdentityRegistryInitializing
	}
	if move == nil {
		return fmt.Errorf("replication_guest_identity_move_required")
	}
	if normalizeGuestIdentityKind(move.GuestKind) != normalizeGuestIdentityKind(policy.GuestType) ||
		move.GuestID != policy.GuestID ||
		strings.TrimSpace(move.OldOwnerNodeID) != strings.TrimSpace(expectedOwnerNodeID) ||
		strings.TrimSpace(move.NewOwnerNodeID) != strings.TrimSpace(newOwnerNodeID) {
		return fmt.Errorf("replication_guest_identity_move_mismatch")
	}
	return MoveGuestIdentityClaimTxn(tx, move)
}
