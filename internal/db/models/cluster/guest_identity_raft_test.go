// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func newGuestIdentityFSM(t *testing.T) *FSMDispatcher {
	t.Helper()
	fsm := NewFSMDispatcher(newClusterModelTestDB(t))
	RegisterDefaultHandlers(fsm)
	return fsm
}

func applyGuestIdentityFSMCommand(t *testing.T, fsm *FSMDispatcher, action string, payload any) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal guest identity command: %v", err)
	}
	return applyFSMCommand(t, fsm, Command{Type: "guest_identity", Action: action, Data: raw})
}

func guestIdentityClaimRows(t *testing.T, fsm *FSMDispatcher) []string {
	t.Helper()
	var claims []GuestIdentityClaim
	if err := fsm.DB.Order("guest_id ASC").Find(&claims).Error; err != nil {
		t.Fatal(err)
	}
	rows := make([]string, len(claims))
	for i, claim := range claims {
		rows[i] = fmt.Sprintf("%s/%s/%d", claim.OwnerNodeID, claim.GuestKind, claim.GuestID)
	}
	return rows
}

func claimSet(owner string, entries ...GuestIdentityEntry) GuestIdentityClaimSet {
	return GuestIdentityClaimSet{OwnerNodeID: owner, Entries: entries}
}

func TestGuestIdentityClaimsAreUniqueAcrossNodesAndKinds(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	vm100 := GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 100}
	jail101 := GuestIdentityEntry{GuestKind: ReplicationGuestTypeJail, GuestID: 101}
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", vm100, jail101)); err != nil {
		t.Fatal(err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", jail101)); err != nil {
		t.Fatalf("reclaim by the owner: %v", err)
	}
	for name, set := range map[string]GuestIdentityClaimSet{
		"another node":   claimSet("node-b", vm100),
		"another kind":   claimSet("node-a", GuestIdentityEntry{GuestKind: GuestIdentityKindContainer, GuestID: 100}),
		"partly in use":  claimSet("node-b", GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 102}, jail101),
		"another node 2": claimSet("node-b", GuestIdentityEntry{GuestKind: ReplicationGuestTypeJail, GuestID: 101}),
	} {
		if err := applyGuestIdentityFSMCommand(t, fsm, "claim", set); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
			t.Fatalf("%s: %v, want ErrGuestIdentityAlreadyInUse", name, err)
		}
	}
	if got, want := guestIdentityClaimRows(t, fsm), []string{"node-a/vm/100", "node-a/jail/101"}; !slices.Equal(got, want) {
		t.Fatalf("claims = %v, want %v (a refused claim adds nothing)", got, want)
	}
	for _, invalid := range []uint{0, GuestIdentityMaxID + 1} {
		if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: invalid})); err == nil {
			t.Fatalf("claimed guest ID %d", invalid)
		}
	}
}

func TestGuestIdentityReleaseOnlyDropsTheOwnersClaims(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	vm100 := GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 100}
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", vm100)); err != nil {
		t.Fatal(err)
	}
	for _, set := range []GuestIdentityClaimSet{
		claimSet("node-b", vm100),
		claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeJail, GuestID: 100}),
	} {
		if err := applyGuestIdentityFSMCommand(t, fsm, "release", set); err != nil {
			t.Fatal(err)
		}
	}
	if got := guestIdentityClaimRows(t, fsm); !slices.Equal(got, []string{"node-a/vm/100"}) {
		t.Fatalf("released someone else's claim: %v", got)
	}
	for range 2 {
		if err := applyGuestIdentityFSMCommand(t, fsm, "release", claimSet("node-a", vm100)); err != nil {
			t.Fatal(err)
		}
	}
	if got := guestIdentityClaimRows(t, fsm); len(got) != 0 {
		t.Fatalf("claims after release = %v", got)
	}
}

func TestGuestIdentityMoveIsACompareAndSwapOnOwner(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 100})); err != nil {
		t.Fatal(err)
	}
	move := GuestIdentityMoveOwner{GuestKind: ReplicationGuestTypeVM, GuestID: 100, OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b"}
	for range 2 {
		if err := applyGuestIdentityFSMCommand(t, fsm, "move", move); err != nil {
			t.Fatalf("move (or its retry): %v", err)
		}
	}
	stale := GuestIdentityMoveOwner{GuestKind: ReplicationGuestTypeVM, GuestID: 100, OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-c"}
	if err := applyGuestIdentityFSMCommand(t, fsm, "move", stale); !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("move from a former owner = %v", err)
	}
	wrongKind := move
	wrongKind.GuestKind = ReplicationGuestTypeJail
	if err := applyGuestIdentityFSMCommand(t, fsm, "move", wrongKind); !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("move as another kind = %v", err)
	}
	unclaimed := GuestIdentityMoveOwner{GuestKind: ReplicationGuestTypeJail, GuestID: 200, OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b"}
	if err := applyGuestIdentityFSMCommand(t, fsm, "move", unclaimed); err != nil {
		t.Fatal(err)
	}
	if got, want := guestIdentityClaimRows(t, fsm), []string{"node-b/vm/100", "node-b/jail/200"}; !slices.Equal(got, want) {
		t.Fatalf("claims = %v, want %v", got, want)
	}
	container := GuestIdentityMoveOwner{GuestKind: GuestIdentityKindContainer, GuestID: 300, OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b"}
	if err := applyGuestIdentityFSMCommand(t, fsm, "move", container); err == nil {
		t.Fatal("containers don't migrate between nodes")
	}
}

func TestClusterMemberRemovalDropsTheNodesClaimsAndIdentity(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	for owner, id := range map[string]uint{"node-a": 100, "node-b": 101} {
		if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet(owner, GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: id})); err != nil {
			t.Fatal(err)
		}
		if err := fsm.DB.Create(&ClusterSSHIdentity{NodeUUID: owner, SSHUser: "root", SSHHost: owner, SSHPort: 8183, PublicKey: "key-" + owner}).Error; err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(ClusterMemberRemoval{NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := applyFSMCommand(t, fsm, Command{Type: "cluster_member", Action: "remove", Data: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if got := guestIdentityClaimRows(t, fsm); !slices.Equal(got, []string{"node-b/vm/101"}) {
		t.Fatalf("claims = %v", got)
	}
	var identities []string
	if err := fsm.DB.Model(&ClusterSSHIdentity{}).Order("node_uuid").Pluck("node_uuid", &identities).Error; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(identities, []string{"node-b"}) {
		t.Fatalf("SSH identities = %v", identities)
	}
}

func TestGuestIdentityClaimsSurviveSnapshotRoundTrip(t *testing.T) {
	source := newGuestIdentityFSM(t)
	if err := applyGuestIdentityFSMCommand(t, source, "claim", claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeJail, GuestID: 450})); err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureClusterSnapshot(source.DB)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	destination := newGuestIdentityFSM(t)
	if err := destination.Restore(snapshotReader(t, snapshot)); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	if got := guestIdentityClaimRows(t, destination); !slices.Equal(got, []string{"node-a/jail/450"}) {
		t.Fatalf("restored claims = %v", got)
	}
}

type retiredGuestIdentityClaim struct {
	GuestID     uint   `gorm:"primaryKey;autoIncrement:false"`
	GuestKind   string `gorm:"not null;size:32"`
	OwnerNodeID string `gorm:"not null;size:128;index"`
	Token       string `gorm:"not null;size:128;index"`
}

func (retiredGuestIdentityClaim) TableName() string { return "guest_identity_claims" }

func TestFSMDropsRetiredGuestIdentityState(t *testing.T) {
	db := newClusterModelTestDB(t, &retiredGuestIdentityClaim{})
	for _, statement := range []string{
		"CREATE TABLE guest_identity_registries (id integer PRIMARY KEY, version integer, phase text)",
		"CREATE TABLE guest_identity_enrollments (node_id text PRIMARY KEY, inventory_digest text)",
		"CREATE TABLE guest_identity_departures (node_id text PRIMARY KEY, leave_id text, inventory_digest text)",
		"INSERT INTO guest_identity_claims (guest_id, guest_kind, owner_node_id, token) VALUES (100, 'vm', 'node-a', 'old-token')",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	for _, table := range []string{"guest_identity_registries", "guest_identity_enrollments", "guest_identity_departures"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("%s still exists", table)
		}
	}
	if db.Migrator().HasColumn(&GuestIdentityClaim{}, "token") {
		t.Fatal("token column still exists")
	}
	if got := guestIdentityClaimRows(t, fsm); !slices.Equal(got, []string{"node-a/vm/100"}) {
		t.Fatalf("claims after the cleanup = %v", got)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeJail, GuestID: 101})); err != nil {
		t.Fatalf("claim on the cleaned table: %v", err)
	}
}

func TestGuestIdentityFSMDoesNotDeadlockSingleConnection(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	payload, err := json.Marshal(claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 999}))
	if err != nil {
		t.Fatal(err)
	}
	command, err := json.Marshal(Command{Version: CurrentCommandVersion, DecidedAt: time.Now(), Type: "guest_identity", Action: "claim", Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response := fsm.Apply(&raft.Log{Type: raft.LogCommand, Index: fsm.AppliedIndex() + 1, Data: command})
		if response == nil {
			done <- nil
			return
		}
		done <- response.(error)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("single-connection apply: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("guest identity FSM apply deadlocked")
	}
}

func TestGuestIdentityRejectsUnknownAction(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "unsupported_raft_command_action_unknown") {
			t.Fatalf("unknown action: %v", recovered)
		}
	}()
	_ = applyGuestIdentityFSMCommand(t, fsm, "unknown", struct{}{})
}
