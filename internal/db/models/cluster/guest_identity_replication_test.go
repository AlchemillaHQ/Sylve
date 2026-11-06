// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func seedGuestIdentityOwnershipTransition(t *testing.T, db *gorm.DB, claimOwner string) ReplicationOwnershipTransitionPayload {
	t.Helper()
	seedControlPlanePolicy(t, db)
	if claimOwner != "" {
		if err := db.Create(&GuestIdentityClaim{GuestID: 100, GuestKind: ReplicationGuestTypeVM, OwnerNodeID: claimOwner}).Error; err != nil {
			t.Fatalf("seed guest identity claim: %v", err)
		}
	}
	return ownershipCommitPayload()
}

func newGuestIdentityOwnershipTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return newClusterModelTestDB(t, &ReplicationPolicy{}, &ReplicationPolicyTarget{}, &ReplicationLease{}, &GuestIdentityClaim{})
}

func requireGuestIdentityClaimOwner(t *testing.T, db *gorm.DB, owner string) {
	t.Helper()
	var claim GuestIdentityClaim
	if err := db.First(&claim, 100).Error; err != nil {
		t.Fatalf("reload claim: %v", err)
	}
	if claim.OwnerNodeID != owner {
		t.Fatalf("claim owner = %q, want %q", claim.OwnerNodeID, owner)
	}
}

func assertGuestIdentityOwnershipBeforeTransition(t *testing.T, db *gorm.DB, claimOwner string) {
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
	requireGuestIdentityClaimOwner(t, db, claimOwner)
}

func TestGuestIdentityReplicationOwnershipTransitionAtomicCommit(t *testing.T) {
	db := newGuestIdentityOwnershipTestDB(t)
	payload := seedGuestIdentityOwnershipTransition(t, db, "node-1")

	if err := applyReplicationOwnershipTransition(db, &payload); err != nil {
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
	if policy.ActiveNodeID != "node-2" || policy.OwnerEpoch != 2 || lease.OwnerNodeID != "node-2" || lease.OwnerEpoch != 2 {
		t.Fatalf("ownership did not commit: policy=%+v lease=%+v", policy, lease)
	}
	requireGuestIdentityClaimOwner(t, db, "node-2")
}

func TestGuestIdentityReplicationOwnershipTransitionRollsBackClaim(t *testing.T) {
	db := newGuestIdentityOwnershipTestDB(t)
	payload := seedGuestIdentityOwnershipTransition(t, db, "node-1")
	if err := db.Callback().Create().Before("gorm:create").Register("fail_target_after_claim_move", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ReplicationPolicyTarget" {
			tx.AddError(errors.New("injected_target_write_failure_after_claim_move"))
		}
	}); err != nil {
		t.Fatalf("register target failure: %v", err)
	}

	err := applyReplicationOwnershipTransition(db, &payload)
	if err == nil || !strings.Contains(err.Error(), "injected_target_write_failure_after_claim_move") {
		t.Fatalf("ownership transition error = %v", err)
	}
	assertGuestIdentityOwnershipBeforeTransition(t, db, "node-1")
}

func TestGuestIdentityReplicationOwnershipClaimCASRollsBackEverything(t *testing.T) {
	db := newGuestIdentityOwnershipTestDB(t)
	payload := seedGuestIdentityOwnershipTransition(t, db, "node-3")

	err := applyReplicationOwnershipTransition(db, &payload)
	if !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("ownership transition claim CAS error = %v", err)
	}
	assertGuestIdentityOwnershipBeforeTransition(t, db, "node-3")
}

func TestGuestIdentityReplicationOwnershipClaimsAnUnclaimedGuest(t *testing.T) {
	db := newGuestIdentityOwnershipTestDB(t)
	payload := seedGuestIdentityOwnershipTransition(t, db, "")

	if err := applyReplicationOwnershipTransition(db, &payload); err != nil {
		t.Fatalf("commit ownership transition: %v", err)
	}
	requireGuestIdentityClaimOwner(t, db, "node-2")
}
