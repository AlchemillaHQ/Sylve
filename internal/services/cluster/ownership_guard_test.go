// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestStorageTopologyMutationBlocksOnlyRunningReplication(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{})
	policy := clusterModels.ReplicationPolicy{
		ID: 901, Name: "disabled-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 902, Enabled: false,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("create disabled replication policy: %v", err)
	}
	operation := clusterModels.ReplicationRunOperation{
		PolicyID: 901, Token: "replication:node-a:running", State: clusterModels.ReplicationRunOperationRunning,
		HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("create running replication operation: %v", err)
	}

	allowed, err := CanMutateProtectedGuestStorageTopology(
		db, clusterModels.ReplicationGuestTypeVM, policy.GuestID,
	)
	if allowed || !errors.Is(err, ErrReplicationRunInProgress) {
		t.Fatalf("running replication allowed topology mutation: allowed=%v err=%v", allowed, err)
	}

	if err := db.Model(&operation).Update("state", clusterModels.ReplicationRunOperationQueued).Error; err != nil {
		t.Fatalf("mark replication operation queued: %v", err)
	}
	allowed, err = CanMutateProtectedGuestStorageTopology(
		db, clusterModels.ReplicationGuestTypeVM, policy.GuestID,
	)
	if err != nil || !allowed {
		t.Fatalf("queued replication blocked topology mutation: allowed=%v err=%v", allowed, err)
	}
}

func TestCanNodeMutateProtectedGuest(t *testing.T) {
	// protected is an enabled VM 100 policy actively owned by node-1 at epoch 1; lease is node-1's valid lease for it.
	protected := func(change func(*clusterModels.ReplicationPolicy)) *clusterModels.ReplicationPolicy {
		policy := &clusterModels.ReplicationPolicy{
			ID: 1, Name: "protected", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			Enabled: true, ActiveNodeID: "node-1", SourceNodeID: "node-1",
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
		}
		if change != nil {
			change(policy)
		}
		return policy
	}
	lease := func(change func(*clusterModels.ReplicationLease)) *clusterModels.ReplicationLease {
		out := &clusterModels.ReplicationLease{
			PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1, ExpiresAt: time.Now().Add(time.Hour),
		}
		if change != nil {
			change(out)
		}
		return out
	}
	vm := clusterModels.ReplicationGuestTypeVM
	tests := []struct {
		name        string
		policy      *clusterModels.ReplicationPolicy
		lease       *clusterModels.ReplicationLease
		guestType   string
		guestID     uint
		localNode   string
		wantAllowed bool
		wantErr     bool
	}{
		{name: "no policy", guestType: vm, guestID: 100, localNode: "node-1", wantAllowed: true},
		{name: "empty guest type", guestID: 100, localNode: "node-1", wantAllowed: true},
		{name: "zero guest id", guestType: vm, localNode: "node-1", wantAllowed: true},
		{name: "empty local node id", policy: protected(nil), guestType: vm, guestID: 100, localNode: "  ", wantErr: true},
		{
			name:   "disabled policy without local node id",
			policy: protected(func(p *clusterModels.ReplicationPolicy) { p.Enabled = false }), guestType: vm, guestID: 100, wantAllowed: true,
		},
		{
			name:   "disabled policy",
			policy: protected(func(p *clusterModels.ReplicationPolicy) { p.Enabled, p.ActiveNodeID = false, "" }), guestType: vm, guestID: 100,
			localNode: "node-1", wantAllowed: true,
		},
		{
			name: "active node mismatch",
			policy: protected(func(p *clusterModels.ReplicationPolicy) {
				p.ActiveNodeID, p.SourceNodeID = "node-remote", "node-remote"
			}),
			guestType: vm, guestID: 100, localNode: "node-local",
		},
		{name: "no lease", policy: protected(nil), guestType: vm, guestID: 100, localNode: "node-1"},
		{
			name: "expired lease", policy: protected(nil), guestType: vm, guestID: 100, localNode: "node-1",
			lease: lease(func(l *clusterModels.ReplicationLease) { l.ExpiresAt = time.Now().Add(-time.Hour) }),
		},
		{
			name: "lease owner mismatch", policy: protected(nil), guestType: vm, guestID: 100, localNode: "node-1",
			lease: lease(func(l *clusterModels.ReplicationLease) { l.OwnerNodeID = "node-other" }),
		},
		{
			name: "epoch mismatch", guestType: vm, guestID: 100, localNode: "node-1",
			policy: protected(func(p *clusterModels.ReplicationPolicy) { p.OwnerEpoch = 5 }),
			lease:  lease(func(l *clusterModels.ReplicationLease) { l.OwnerEpoch = 3 }),
		},
		{name: "valid lease", policy: protected(nil), lease: lease(nil), guestType: vm, guestID: 100, localNode: "node-1", wantAllowed: true},
		{
			name: "falls back to source node when no active node", guestType: clusterModels.ReplicationGuestTypeJail, guestID: 200,
			localNode: "node-1", wantAllowed: true,
			policy: protected(func(p *clusterModels.ReplicationPolicy) {
				p.GuestType, p.GuestID, p.ActiveNodeID = clusterModels.ReplicationGuestTypeJail, 200, ""
				p.SourceMode, p.FailoverMode, p.CronExpr = clusterModels.ReplicationSourceModePinned, clusterModels.ReplicationFailoverAutoSafe, "0 */6 * * *"
			}),
			lease: lease(func(l *clusterModels.ReplicationLease) {
				l.GuestType, l.GuestID = clusterModels.ReplicationGuestTypeJail, 200
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newClusterServiceTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})
			if tt.policy != nil {
				if err := db.Create(tt.policy).Error; err != nil {
					t.Fatalf("seed policy: %v", err)
				}
			}
			if tt.lease != nil {
				if err := db.Create(tt.lease).Error; err != nil {
					t.Fatalf("seed lease: %v", err)
				}
			}
			allowed, err := CanNodeMutateProtectedGuest(db, tt.guestType, tt.guestID, tt.localNode)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allowed != tt.wantAllowed {
				t.Fatalf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
		})
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

func TestCreationMutationGuardAllowsOnlyOwnedInitialProvisioning(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.JailCreation{}, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{})
	ctID := uint(870)
	op := jailModels.JailCreation{ID: "creation-owner", CTID: ctID, ActiveCTID: &ctID, Phase: "preparing", Request: "{}", State: "{}"}
	if err := db.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	if allowed, err := CanNodeMutateProtectedGuest(db, "jail", ctID, "node-a"); allowed || err == nil {
		t.Fatalf("ordinary mutation allowed active creation: %v, %v", allowed, err)
	}
	if allowed, err := CanNodeProvisionJail(db, ctID, "node-a", "foreign"); allowed || err == nil {
		t.Fatalf("foreign creation bypass: %v, %v", allowed, err)
	}
	if allowed, err := CanNodeProvisionJail(db, ctID, "node-a", op.ID); !allowed || err != nil {
		t.Fatalf("owned initial provisioning: %v, %v", allowed, err)
	}
	guard := clusterModels.ReplicationGuestOperation{GuestType: "jail", GuestID: ctID, Operation: clusterModels.ReplicationGuestOperationRestore}
	if err := db.Create(&guard).Error; err != nil {
		t.Fatal(err)
	}
	if allowed, err := CanNodeProvisionJail(db, ctID, "node-a", op.ID); allowed || err != nil {
		t.Fatalf("creation bypassed restore protection: %v, %v", allowed, err)
	}
}

func TestCreationMutationGuardProtectsSourceUntilCleanupCompletes(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.JailCreation{}, &clusterModels.ReplicationPolicy{})
	ctID := uint(871)
	op := jailModels.JailCreation{ID: "capture", CTID: ctID, ActiveCTID: &ctID, Phase: "copying", Request: `{"zfsSource":{"dataset":"pool/sylve/jails/872"}}`, State: "{}"}
	if err := db.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	for _, guestID := range []uint{ctID, 872} {
		for _, action := range []string{"mutate", "start", "stop", "transition", "topology"} {
			t.Run(fmt.Sprintf("%d/%s", guestID, action), func(t *testing.T) {
				var allowed bool
				var err error
				switch action {
				case "mutate":
					allowed, err = CanNodeMutateProtectedGuest(db, "jail", guestID, "node-a")
				case "start":
					allowed, err = CanNodeStartProtectedGuest(db, "jail", guestID, "node-a")
				case "stop":
					allowed, err = CanNodeStopGuestForMigration(db, "jail", guestID, "node-a")
				case "transition":
					allowed, err = CanNodeMutateProtectedGuestForTransition(db, "jail", guestID, "node-a", "transition-run")
				case "topology":
					allowed, err = CanMutateProtectedGuestStorageTopology(db, "jail", guestID)
				}
				if allowed || err == nil || !strings.Contains(err.Error(), "jail_creation_in_progress") {
					t.Fatalf("creation mutation allowed: %v, %v", allowed, err)
				}
			})
		}
	}
	if err := db.Model(&op).Update("active_ct_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := requireJailCreationMutationAllowed(db, "jail", 872, ""); err != nil {
		t.Fatalf("completed cleanup still blocked source mutation: %v", err)
	}
}
