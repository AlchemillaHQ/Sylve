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
	"gorm.io/gorm"
)

func CanNodeMutateProtectedGuest(db *gorm.DB, guestType string, guestID uint, localNodeID string) (bool, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	localNodeID = strings.TrimSpace(localNodeID)

	if guestType == "" || guestID == 0 {
		return true, nil
	}

	if localNodeID == "" {
		return false, fmt.Errorf("local_node_id_unavailable")
	}

	var policy clusterModels.ReplicationPolicy
	err := db.Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return true, nil
	}

	if err != nil {
		return false, err
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
	if err := db.Where("policy_id = ?", policy.ID).First(&lease).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}

	if lease.OwnerEpoch == 0 {
		return false, fmt.Errorf("replication_lease_owner_epoch_missing")
	}
	if time.Now().UTC().After(lease.ExpiresAt) {
		return false, nil
	}

	return strings.TrimSpace(lease.OwnerNodeID) == localNodeID &&
		lease.OwnerEpoch == policy.OwnerEpoch, nil
}

func CanNodeStartProtectedGuest(db *gorm.DB, guestType string, guestID uint, localNodeID string) (bool, error) {
	return CanNodeMutateProtectedGuest(db, guestType, guestID, localNodeID)
}
