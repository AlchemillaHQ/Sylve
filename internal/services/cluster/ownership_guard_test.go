// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestCanNodeMutateProtectedGuestNoPolicy(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	// no matching policy → allowed
	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when no policy exists")
	}
}

func TestCanNodeMutateProtectedGuestEmptyGuestType(t *testing.T) {
	db := newClusterServiceTestDB(t)

	allowed, err := CanNodeMutateProtectedGuest(db, "", 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when guest type is empty")
	}
}

func TestCanNodeMutateProtectedGuestZeroGuestID(t *testing.T) {
	db := newClusterServiceTestDB(t)

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 0, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when guest id is 0")
	}
}

func TestCanNodeMutateProtectedGuestEmptyLocalNodeID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{})
	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "protected", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1", OwnerEpoch: 1,
	})

	_, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "  ")
	if err == nil {
		t.Fatal("expected error for empty local node ID")
	}
}

func TestCanNodeMutateDisabledGuestWithoutLocalNodeID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{})
	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "disabled", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: false, ActiveNodeID: "node-1", OwnerEpoch: 1,
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "")
	if err != nil {
		t.Fatalf("disabled policy is unprotected: %v", err)
	}
	if !allowed {
		t.Fatal("expected disabled policy to be unprotected")
	}
}

func TestCanNodeMutateProtectedGuestPolicyDisabled(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "disabled-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: false,
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when policy is disabled")
	}
}

func TestCanNodeMutateProtectedGuestActiveNodeMismatch(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "remote-owned", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-remote",
		SourceNodeID: "node-remote", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-local")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied when active node is a different node")
	}
}

func TestCanNodeMutateProtectedGuestNoLease(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "no-lease", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied when no lease exists")
	}
}

func TestCanNodeMutateProtectedGuestExpiredLease(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "expired", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(-time.Hour), // expired
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied when lease is expired")
	}
}

func TestCanNodeMutateProtectedGuestLeaseOwnerMismatch(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "owner-mismatch", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-other", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied when lease owner is a different node")
	}
}

func TestCanNodeMutateProtectedGuestEpochMismatch(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "epoch-mismatch", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 5,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 3, // different epoch
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied when lease epoch does not match policy epoch")
	}
}

func TestCanNodeMutateProtectedGuestValidLease(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "valid", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when local node holds valid lease at correct epoch")
	}
}

func TestCanNodeMutateProtectedGuestSourceNodeFallback(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "source-fallback", GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 200, Enabled: true, ActiveNodeID: "", // empty active, falls back to source
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModePinned,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverAutoSafe,
		CronExpr:     "0 */6 * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 200,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeJail, 200, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed when source node is the owner")
	}
}

func TestCanNodeStartProtectedGuestDelegatesToMutateGuard(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})

	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "start-guard", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1",
		SourceNodeID: "node-1", SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeStartProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed for start with valid lease")
	}

	// remote node cannot start
	allowed, err = CanNodeStartProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-other")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected denied for start by remote node")
	}
}

func TestTransitionLockRequiresMatchingRunID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})
	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "transitioning", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, Enabled: true, ActiveNodeID: "node-1", OwnerEpoch: 4,
		ProtectionState: clusterModels.ReplicationProtectionStateSuspended,
		TransitionState: clusterModels.ReplicationTransitionStatePromoting,
		TransitionRunID: "run-4",
	})
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 4, Version: 4,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 100, "node-1")
	if err != nil {
		t.Fatalf("ordinary guard: %v", err)
	}
	if allowed {
		t.Fatal("ordinary mutation bypassed persisted transition lock")
	}

	allowed, err = CanNodeMutateProtectedGuestForTransition(
		db, clusterModels.ReplicationGuestTypeVM, 100, "node-1", "wrong-run",
	)
	if err != nil {
		t.Fatalf("wrong-run guard: %v", err)
	}
	if allowed {
		t.Fatal("wrong run ID bypassed persisted transition lock")
	}

	allowed, err = CanNodeStartProtectedGuestForTransition(
		db, clusterModels.ReplicationGuestTypeVM, 100, "node-1", "run-4",
	)
	if err != nil {
		t.Fatalf("matching-run guard: %v", err)
	}
	if !allowed {
		t.Fatal("matching persisted run ID should authorize the transition operation")
	}
}

func TestMigrationGuestOperationBlocksSourceButAllowsSealedTarget(t *testing.T) {
	db := newClusterServiceTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 404,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     "migration:node-a:404", OwnerNodeID: "node-a", TargetNodeID: "node-b",
		TaskID: 404, AcquiredAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create migration guard: %v", err)
	}

	allowed, err := CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 404, "node-a")
	if err != nil || allowed {
		t.Fatalf("source mutation was not blocked: allowed=%v err=%v", allowed, err)
	}
	allowed, err = CanNodeMutateProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 404, "node-b")
	if err != nil || allowed {
		t.Fatalf("ordinary target mutation was not blocked: allowed=%v err=%v", allowed, err)
	}
	allowed, err = CanNodeStartProtectedGuest(db, clusterModels.ReplicationGuestTypeVM, 404, "node-b")
	if err != nil || !allowed {
		t.Fatalf("sealed target activation was blocked: allowed=%v err=%v", allowed, err)
	}
	allowed, err = CanNodeStopGuestForMigration(db, clusterModels.ReplicationGuestTypeVM, 404, "node-a")
	if err != nil || !allowed {
		t.Fatalf("exact source stop was blocked: allowed=%v err=%v", allowed, err)
	}
	allowed, err = CanMutateProtectedGuestStorageTopology(db, clusterModels.ReplicationGuestTypeVM, 404)
	if err != nil || allowed {
		t.Fatalf("storage topology changed during migration: allowed=%v err=%v", allowed, err)
	}
}
