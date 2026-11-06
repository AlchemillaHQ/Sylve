// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"strings"

	"gorm.io/gorm"
)

func moveReplicationPolicyGuestIdentityClaimTxn(tx *gorm.DB, policy *ReplicationPolicy, oldOwnerNodeID, newOwnerNodeID string) error {
	if strings.TrimSpace(oldOwnerNodeID) == strings.TrimSpace(newOwnerNodeID) {
		return nil
	}
	return MoveGuestIdentityClaimTxn(tx, &GuestIdentityMoveOwner{
		GuestKind: policy.GuestType, GuestID: policy.GuestID,
		OldOwnerNodeID: oldOwnerNodeID, NewOwnerNodeID: newOwnerNodeID,
	})
}
