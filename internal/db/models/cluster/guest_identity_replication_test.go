// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func seedGuestIdentityOwnershipTransition(t *testing.T, db *gorm.DB) ReplicationOwnershipTransitionPayload {
	t.Helper()
	seedControlPlanePolicy(t, db)
	if err := db.Create(&GuestIdentityRegistry{
		ID: GuestIdentityRegistryID, Version: GuestIdentityRegistryVersion,
		Phase: GuestIdentityRegistryPhaseActive,
	}).Error; err != nil {
		t.Fatalf("seed guest identity registry: %v", err)
	}
	if err := db.Create(&GuestIdentityClaim{
		GuestID: 100, GuestKind: ReplicationGuestTypeVM, OwnerNodeID: "node-1",
		Token: "claim-generation-1",
	}).Error; err != nil {
		t.Fatalf("seed guest identity claim: %v", err)
	}
	payload := ownershipCommitPayload()
	payload.GuestIdentityMove = &GuestIdentityMoveOwner{
		GuestKind: ReplicationGuestTypeVM, GuestID: 100,
		OldOwnerNodeID: "node-1", NewOwnerNodeID: "node-2",
		OldToken: "claim-generation-1", NewToken: "claim-generation-2",
	}
	return payload
}

func assertGuestIdentityOwnershipBeforeTransition(t *testing.T, db *gorm.DB) {
	t.Helper()
	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 {
		t.Fatalf("policy partially moved: owner=%q epoch=%d", policy.ActiveNodeID, policy.OwnerEpoch)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatalf("reload lease: %v", err)
	}
	if lease.OwnerNodeID != "node-1" || lease.OwnerEpoch != 1 || lease.Version != 10 {
		t.Fatalf("lease partially moved: %+v", lease)
	}
	var claim GuestIdentityClaim
	if err := db.First(&claim, 100).Error; err != nil {
		t.Fatalf("reload claim: %v", err)
	}
	if claim.OwnerNodeID != "node-1" || claim.Token != "claim-generation-1" {
		t.Fatalf("claim partially moved: %+v", claim)
	}
}

func TestGuestIdentityReplicationOwnershipTransitionAtomicCommit(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&GuestIdentityRegistry{}, &GuestIdentityClaim{},
	)
	payload := seedGuestIdentityOwnershipTransition(t, db)

	if err := ApplyReplicationOwnershipTransitionTxn(db, &payload); err != nil {
		t.Fatalf("commit ownership transition: %v", err)
	}

	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatalf("reload lease: %v", err)
	}
	var claim GuestIdentityClaim
	if err := db.First(&claim, 100).Error; err != nil {
		t.Fatalf("reload claim: %v", err)
	}
	if policy.ActiveNodeID != "node-2" || policy.OwnerEpoch != 2 ||
		lease.OwnerNodeID != "node-2" || lease.OwnerEpoch != 2 ||
		claim.OwnerNodeID != "node-2" || claim.Token != "claim-generation-2" {
		t.Fatalf("ownership did not commit atomically: policy=%+v lease=%+v claim=%+v", policy, lease, claim)
	}
}

func TestGuestIdentityReplicationOwnershipTransitionRollsBackClaim(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&GuestIdentityRegistry{}, &GuestIdentityClaim{},
	)
	payload := seedGuestIdentityOwnershipTransition(t, db)
	if err := db.Callback().Create().Before("gorm:create").Register("fail_target_after_claim_move", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ReplicationPolicyTarget" {
			tx.AddError(errors.New("injected_target_write_failure_after_claim_move"))
		}
	}); err != nil {
		t.Fatalf("register target failure: %v", err)
	}

	err := ApplyReplicationOwnershipTransitionTxn(db, &payload)
	if err == nil || !strings.Contains(err.Error(), "injected_target_write_failure_after_claim_move") {
		t.Fatalf("ownership transition error = %v", err)
	}
	assertGuestIdentityOwnershipBeforeTransition(t, db)
}

func TestGuestIdentityReplicationOwnershipClaimCASRollsBackEverything(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&GuestIdentityRegistry{}, &GuestIdentityClaim{},
	)
	payload := seedGuestIdentityOwnershipTransition(t, db)
	payload.GuestIdentityMove.OldToken = "stale-claim-generation"

	err := ApplyReplicationOwnershipTransitionTxn(db, &payload)
	if err == nil || !strings.Contains(err.Error(), "guest_identity_claim_conflict") {
		t.Fatalf("ownership transition claim CAS error = %v", err)
	}
	assertGuestIdentityOwnershipBeforeTransition(t, db)
}

func TestGuestIdentityReplicationOwnershipRequiresInitializedRegistry(t *testing.T) {
	db := newClusterModelTestDB(t,
		&ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{},
		&GuestIdentityRegistry{}, &GuestIdentityClaim{},
	)
	seedControlPlanePolicy(t, db)
	payload := ownershipCommitPayload()
	payload.GuestIdentityMove = &GuestIdentityMoveOwner{
		GuestKind: ReplicationGuestTypeVM, GuestID: 100,
		OldOwnerNodeID: "node-1", NewOwnerNodeID: "node-2",
		OldToken: "claim-generation-1", NewToken: "claim-generation-2",
	}

	err := ApplyReplicationOwnershipTransitionTxn(db, &payload)
	if !errors.Is(err, ErrGuestIdentityRegistryInitializing) {
		t.Fatalf("ownership transition error = %v, want %v", err, ErrGuestIdentityRegistryInitializing)
	}

	var policy ReplicationPolicy
	if err := db.First(&policy, 1).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if policy.ActiveNodeID != "node-1" || policy.OwnerEpoch != 1 {
		t.Fatalf("policy moved before registry initialization: owner=%q epoch=%d", policy.ActiveNodeID, policy.OwnerEpoch)
	}
	var lease ReplicationLease
	if err := db.Where("policy_id = ?", 1).First(&lease).Error; err != nil {
		t.Fatalf("reload lease: %v", err)
	}
	if lease.OwnerNodeID != "node-1" || lease.OwnerEpoch != 1 || lease.Version != 10 {
		t.Fatalf("lease moved before registry initialization: %+v", lease)
	}
}
