// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"testing"
	"time"
)

func TestGuestIdentityDisabledReplicationOwnerReassignmentAtomicCommit(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&ReplicationGuestOperation{}, &GuestIdentityRegistry{}, &GuestIdentityClaim{},
	)
	now := time.Now().UTC()
	policy := replicationGuestOperationPolicy(1, 101, false)
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed disabled policy: %v", err)
	}
	if err := db.Create(&ReplicationPolicyTarget{
		PolicyID: policy.ID, NodeID: "node-b", Weight: 100,
	}).Error; err != nil {
		t.Fatalf("seed policy target: %v", err)
	}
	if err := db.Create(&ReplicationLease{
		PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
		OwnerNodeID: "node-a", OwnerEpoch: 1, Version: 1, ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed stale disabled-policy lease: %v", err)
	}
	operation := ReplicationGuestOperation{
		GuestType: policy.GuestType, GuestID: policy.GuestID,
		Operation: ReplicationGuestOperationMigration, State: ReplicationGuestOperationCutover,
		Token: "migration:node-a:101", OwnerNodeID: "node-a", TargetNodeID: "node-b",
		AcquiredAt: now, SealedAt: &now,
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed migration operation: %v", err)
	}
	if err := db.Create(&GuestIdentityRegistry{
		ID: GuestIdentityRegistryID, Version: GuestIdentityRegistryVersion,
		Phase: GuestIdentityRegistryPhaseActive,
	}).Error; err != nil {
		t.Fatalf("seed guest identity registry: %v", err)
	}
	if err := db.Create(&GuestIdentityClaim{
		GuestID: policy.GuestID, GuestKind: policy.GuestType, OwnerNodeID: "node-a",
		Token: "disabled-claim-generation-1",
	}).Error; err != nil {
		t.Fatalf("seed guest identity claim: %v", err)
	}

	payload := ReplicationDisabledOwnerReassignment{
		PolicyID: policy.ID, ExpectedActiveNodeID: "node-a", ExpectedOwnerEpoch: 1,
		ActiveNodeID: "node-b", SourceNodeID: "node-b", OwnerEpoch: 2,
		Targets: []ReplicationPolicyTarget{{NodeID: "node-a", Weight: 100}},
		RunID:   "manual-migration-101", OperationToken: operation.Token, OccurredAt: now.Add(time.Second),
		GuestIdentityMove: &GuestIdentityMoveOwner{
			GuestKind: policy.GuestType, GuestID: policy.GuestID,
			OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b",
			OldToken: "disabled-claim-generation-1", NewToken: "disabled-claim-generation-2",
		},
	}
	if err := ReassignDisabledReplicationPolicyOwnerTxn(db, &payload); err != nil {
		t.Fatalf("reassign disabled policy owner: %v", err)
	}

	var reloadedPolicy ReplicationPolicy
	if err := db.Preload("Targets").First(&reloadedPolicy, policy.ID).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	var claim GuestIdentityClaim
	if err := db.First(&claim, policy.GuestID).Error; err != nil {
		t.Fatalf("reload claim: %v", err)
	}
	var leaseCount int64
	if err := db.Model(&ReplicationLease{}).Where("policy_id = ?", policy.ID).Count(&leaseCount).Error; err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if reloadedPolicy.ActiveNodeID != "node-b" || reloadedPolicy.OwnerEpoch != 2 ||
		len(reloadedPolicy.Targets) != 1 || reloadedPolicy.Targets[0].NodeID != "node-a" ||
		claim.OwnerNodeID != "node-b" || claim.Token != "disabled-claim-generation-2" || leaseCount != 0 {
		t.Fatalf("disabled ownership did not commit atomically: policy=%+v claim=%+v leases=%d", reloadedPolicy, claim, leaseCount)
	}
}
