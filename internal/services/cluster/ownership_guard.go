// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"gorm.io/gorm"
)

func CanNodeMutateProtectedGuest(db *gorm.DB, guestType string, guestID uint, localNodeID string) (bool, error) {
	return canNodeMutateProtectedGuest(db, guestType, guestID, localNodeID, "", "")
}

// CanMutateProtectedGuestStorageTopology fails closed while replication is
// enabled or a durable guest operation is active. Storage changes require
// disabling protection, changing topology, then re-enabling/reseeding.
func CanMutateProtectedGuestStorageTopology(db *gorm.DB, guestType string, guestID uint) (bool, error) {
	if db == nil || guestID == 0 {
		return false, fmt.Errorf("replication_topology_guard_input_invalid")
	}
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail {
		return false, fmt.Errorf("invalid_guest_type")
	}
	if replicationguard.GuestOperationSchemaReady(db) {
		var operation clusterModels.ReplicationGuestOperation
		result := db.Where("guest_type = ? AND guest_id = ?", guestType, guestID).Limit(1).Find(&operation)
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected != 0 {
			return false, nil
		}
	}
	var count int64
	if err := db.Model(&clusterModels.ReplicationPolicy{}).
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

// CanNodeMutateProtectedGuestForTransition is the narrowly scoped bypass for
// the transition engine. The caller must propagate the persisted run ID all
// the way to the guest operation; merely running on the owner node is not
// enough to bypass the durable policy lock.
func CanNodeMutateProtectedGuestForTransition(
	db *gorm.DB,
	guestType string,
	guestID uint,
	localNodeID string,
	transitionRunID string,
) (bool, error) {
	return canNodeMutateProtectedGuest(db, guestType, guestID, localNodeID, transitionRunID, "")
}

func canNodeMutateProtectedGuest(
	db *gorm.DB,
	guestType string,
	guestID uint,
	localNodeID string,
	transitionRunID string,
	action string,
) (bool, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	localNodeID = strings.TrimSpace(localNodeID)
	transitionRunID = strings.TrimSpace(transitionRunID)

	if (guestType == "" || guestID == 0) && action == "" {
		return true, nil
	}
	if replicationguard.GuestOperationSchemaReady(db) {
		var operation clusterModels.ReplicationGuestOperation
		result := db.Where("guest_type = ? AND guest_id = ?", guestType, guestID).Limit(1).Find(&operation)
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected != 0 {
			if operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
				operation.State != clusterModels.ReplicationGuestOperationCutover ||
				localNodeID == "" {
				return false, nil
			}
			switch action {
			case "stop":
				return localNodeID == strings.TrimSpace(operation.OwnerNodeID), nil
			case "start":
				return localNodeID == strings.TrimSpace(operation.TargetNodeID), nil
			default:
				return false, nil
			}
		}
	}
	if guestType == "" || guestID == 0 {
		return true, nil
	}
	var policy clusterModels.ReplicationPolicy
	res := db.
		Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).
		Limit(1).
		Find(&policy)

	if res.Error != nil {
		return false, res.Error
	}

	if res.RowsAffected == 0 {
		return true, nil
	}

	if localNodeID == "" {
		return false, fmt.Errorf("local_node_id_unavailable")
	}

	if replicationPolicyTransitionInProgress(policy.TransitionState) {
		if transitionRunID == "" || transitionRunID != strings.TrimSpace(policy.TransitionRunID) {
			return false, nil
		}
	} else if transitionRunID != "" {
		// A stale internal request must not gain privileges after its run has
		// completed and a later operation may have taken ownership.
		return false, nil
	}

	expectedOwner := strings.TrimSpace(policy.ActiveNodeID)
	if expectedOwner == "" {
		expectedOwner = strings.TrimSpace(policy.SourceNodeID)
	}

	if expectedOwner == "" || expectedOwner != localNodeID {
		return false, nil
	}

	if policy.OwnerEpoch == 0 {
		return false, fmt.Errorf("replication_policy_owner_epoch_missing")
	}

	var lease clusterModels.ReplicationLease
	res = db.
		Where("policy_id = ?", policy.ID).
		Limit(1).
		Find(&lease)

	if res.Error != nil {
		return false, res.Error
	}

	if res.RowsAffected == 0 {
		return false, nil
	}

	if lease.OwnerEpoch == 0 {
		return false, fmt.Errorf("replication_lease_owner_epoch_missing")
	}
	if strings.TrimSpace(strings.ToLower(lease.GuestType)) != policy.GuestType || lease.GuestID != policy.GuestID {
		return false, fmt.Errorf("replication_lease_guest_mismatch")
	}

	if time.Now().UTC().After(lease.ExpiresAt) {
		return false, nil
	}

	return strings.TrimSpace(lease.OwnerNodeID) == localNodeID &&
		lease.OwnerEpoch == policy.OwnerEpoch, nil
}

// CanNodeStopGuestForMigration permits only a stop on the exact source of a
// sealed migration guard. It exists so cutover can quiesce the source while
// every other ordinary source mutation remains blocked by the durable guard.
func CanNodeStopGuestForMigration(db *gorm.DB, guestType string, guestID uint, localNodeID string) (bool, error) {
	return canNodeMutateProtectedGuest(db, guestType, guestID, localNodeID, "", "stop")
}

func replicationPolicyTransitionInProgress(state string) bool {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case clusterModels.ReplicationTransitionStateDemoting,
		clusterModels.ReplicationTransitionStateCatchup,
		clusterModels.ReplicationTransitionStatePromoting,
		clusterModels.ReplicationTransitionStateRollingBack:
		return true
	default:
		return false
	}
}

func CanNodeStartProtectedGuest(db *gorm.DB, guestType string, guestID uint, localNodeID string) (bool, error) {
	return canNodeMutateProtectedGuest(db, guestType, guestID, localNodeID, "", "start")
}

func CanNodeStartProtectedGuestForTransition(
	db *gorm.DB,
	guestType string,
	guestID uint,
	localNodeID string,
	transitionRunID string,
) (bool, error) {
	return CanNodeMutateProtectedGuestForTransition(db, guestType, guestID, localNodeID, transitionRunID)
}
