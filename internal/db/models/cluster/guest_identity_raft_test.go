// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

func newGuestIdentityFSM(t *testing.T) *FSMDispatcher {
	t.Helper()
	db := newClusterModelTestDB(t,
		&GuestIdentityRegistry{},
		&GuestIdentityEnrollment{},
		&GuestIdentityClaim{},
	)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	return fsm
}

func applyGuestIdentityFSMCommand(t *testing.T, fsm *FSMDispatcher, action string, payload any) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal guest identity command: %v", err)
	}
	return applyFSMCommand(t, fsm, Command{
		Type:   "guest_identity_registry",
		Action: action,
		Data:   raw,
	})
}

func registerGuestIdentityTestInventory(
	t *testing.T,
	fsm *FSMDispatcher,
	nodeID string,
	entries []GuestIdentityEntry,
) error {
	t.Helper()
	return applyGuestIdentityFSMCommand(t, fsm, "register_node_inventory", GuestIdentityRegisterNodeInventory{
		NodeID:          nodeID,
		InventoryDigest: GuestIdentityInventoryDigest(nodeID, entries),
		Entries:         entries,
	})
}

func activateGuestIdentityTestRegistry(t *testing.T, fsm *FSMDispatcher, voters ...string) {
	t.Helper()
	for _, voter := range voters {
		if err := registerGuestIdentityTestInventory(t, fsm, voter, nil); err != nil {
			t.Fatalf("register empty inventory for %s: %v", voter, err)
		}
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "activate_registry", GuestIdentityActivateRegistry{
		VoterNodeIDs: voters,
	}); err != nil {
		t.Fatalf("activate registry: %v", err)
	}
}

func TestGuestIdentityRegistryEnrollmentIsIncrementalAndAtomic(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	aEntries := []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 100}}
	if err := registerGuestIdentityTestInventory(t, fsm, "node-a", aEntries); err != nil {
		t.Fatalf("register node-a: %v", err)
	}

	conflicting := []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeJail, GuestID: 100}}
	err := registerGuestIdentityTestInventory(t, fsm, "node-b", conflicting)
	if !errors.Is(err, ErrGuestIdentityInventoryConflict) {
		t.Fatalf("conflicting registration error=%v, want inventory conflict", err)
	}
	var enrollmentCount int64
	if err := fsm.DB.Model(&GuestIdentityEnrollment{}).Count(&enrollmentCount).Error; err != nil {
		t.Fatal(err)
	}
	if enrollmentCount != 1 {
		t.Fatalf("enrollment count=%d, want 1", enrollmentCount)
	}

	err = applyGuestIdentityFSMCommand(t, fsm, "activate_registry", GuestIdentityActivateRegistry{
		VoterNodeIDs: []string{"node-a", "node-b"},
	})
	if !errors.Is(err, ErrGuestIdentityRegistryInitializing) {
		t.Fatalf("activation without node-b error=%v, want initializing", err)
	}

	bEntries := []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeJail, GuestID: 200}}
	if err := registerGuestIdentityTestInventory(t, fsm, "node-b", bEntries); err != nil {
		t.Fatalf("register node-b: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "activate_registry", GuestIdentityActivateRegistry{
		VoterNodeIDs: []string{"node-b", "node-a"},
	}); err != nil {
		t.Fatalf("activate complete registry: %v", err)
	}

	var registry GuestIdentityRegistry
	if err := fsm.DB.First(&registry, GuestIdentityRegistryID).Error; err != nil {
		t.Fatal(err)
	}
	if registry.Phase != GuestIdentityRegistryPhaseActive {
		t.Fatalf("phase=%q, want active", registry.Phase)
	}
	var claims []GuestIdentityClaim
	if err := fsm.DB.Order("guest_id ASC").Find(&claims).Error; err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].GuestID != 100 || claims[1].GuestID != 200 {
		t.Fatalf("unexpected claims: %#v", claims)
	}

	postActivationClaim := GuestIdentityClaimSet{
		OwnerNodeID: "node-a",
		Token:       "post-activation",
		Entries:     []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 300}},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", postActivationClaim); err != nil {
		t.Fatalf("reserve after activation: %v", err)
	}

	if err := registerGuestIdentityTestInventory(t, fsm, "node-a", aEntries); err != nil {
		t.Fatalf("retry node-a registration: %v", err)
	}
}

func TestGuestIdentityRegistryReservationSerializesSharedNamespace(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")

	vm := GuestIdentityClaimSet{
		OwnerNodeID: "node-a",
		Token:       "reserve-vm-100",
		Entries:     []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 100}},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", vm); err != nil {
		t.Fatalf("reserve VM: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", vm); err != nil {
		t.Fatalf("idempotent reserve: %v", err)
	}

	jail := GuestIdentityClaimSet{
		OwnerNodeID: "node-a",
		Token:       "reserve-jail-100",
		Entries:     []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeJail, GuestID: 100}},
	}
	err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", jail)
	if !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
		t.Fatalf("cross-kind reservation error=%v, want already in use", err)
	}

	stale := vm
	stale.Token = "stale-token"
	err = applyGuestIdentityFSMCommand(t, fsm, "release_ids", stale)
	if !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("stale release error=%v, want claim conflict", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "release_ids", vm); err != nil {
		t.Fatalf("release VM: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "release_ids", vm); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", jail); err != nil {
		t.Fatalf("reserve jail after release: %v", err)
	}
}

func TestGuestIdentityRegistryReclaimUsesGenerationCASAndGuestOperationGuard(t *testing.T) {
	db := newClusterModelTestDB(t,
		&GuestIdentityRegistry{},
		&GuestIdentityEnrollment{},
		&GuestIdentityClaim{},
		&ReplicationGuestOperation{},
	)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")

	claim := GuestIdentityClaimSet{
		OwnerNodeID: "node-a",
		Token:       "reclaim-generation-1",
		Entries: []GuestIdentityEntry{{
			GuestKind: ReplicationGuestTypeVM,
			GuestID:   150,
		}},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", claim); err != nil {
		t.Fatalf("reserve reclaim candidate: %v", err)
	}

	stale := claim
	stale.Token = "reclaim-stale-generation"
	if err := applyGuestIdentityFSMCommand(t, fsm, "reclaim_id", stale); !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("stale reclaim error=%v, want claim conflict", err)
	}

	now := time.Now().UTC()
	if err := db.Create(&ReplicationGuestOperation{
		GuestType:   ReplicationGuestTypeVM,
		GuestID:     150,
		Operation:   ReplicationGuestOperationRestore,
		State:       ReplicationGuestOperationPreCutover,
		Token:       "restore:node-a:150",
		OwnerNodeID: "node-a",
		TaskID:      1,
		AcquiredAt:  now,
	}).Error; err != nil {
		t.Fatalf("seed active guest operation: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reclaim_id", claim); err == nil ||
		!strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("reclaim during guest operation error=%v", err)
	}
	var retained GuestIdentityClaim
	if err := db.First(&retained, 150).Error; err != nil || retained.Token != claim.Token {
		t.Fatalf("guarded reclaim changed claim: claim=%+v err=%v", retained, err)
	}

	if err := db.Delete(&ReplicationGuestOperation{}, "guest_type = ? AND guest_id = ?", ReplicationGuestTypeVM, 150).Error; err != nil {
		t.Fatalf("clear guest operation: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reclaim_id", claim); err != nil {
		t.Fatalf("reclaim exact generation: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reclaim_id", claim); err != nil {
		t.Fatalf("idempotent reclaim replay: %v", err)
	}
	var count int64
	if err := db.Model(&GuestIdentityClaim{}).Where("guest_id = ?", 150).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("reclaimed claim count=%d err=%v", count, err)
	}
}

func TestGuestIdentityRegistryBatchReservationIsAllOrNone(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")

	seed := GuestIdentityClaimSet{
		OwnerNodeID: "node-a", Token: "seed-10",
		Entries: []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 10}},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", seed); err != nil {
		t.Fatal(err)
	}
	batch := GuestIdentityClaimSet{
		OwnerNodeID: "node-a", Token: "batch-10-11",
		Entries: []GuestIdentityEntry{
			{GuestKind: ReplicationGuestTypeJail, GuestID: 11},
			{GuestKind: ReplicationGuestTypeJail, GuestID: 10},
		},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", batch); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
		t.Fatalf("batch conflict error=%v", err)
	}
	var count int64
	if err := fsm.DB.Model(&GuestIdentityClaim{}).Where("guest_id = ?", 11).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("guest 11 was partially reserved")
	}
}

func TestGuestIdentityRegistryMoveRotatesToken(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")
	claim := GuestIdentityClaimSet{
		OwnerNodeID: "node-a", Token: "claim-before-move",
		Entries: []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 300}},
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", claim); err != nil {
		t.Fatal(err)
	}
	move := GuestIdentityMoveOwner{
		GuestKind: ReplicationGuestTypeVM, GuestID: 300,
		OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b",
		OldToken: "claim-before-move", NewToken: "claim-after-move",
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "move_id_owner", move); err != nil {
		t.Fatalf("move claim: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "move_id_owner", move); err != nil {
		t.Fatalf("idempotent move: %v", err)
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "release_ids", claim); !errors.Is(err, ErrGuestIdentityClaimConflict) {
		t.Fatalf("stale source release error=%v", err)
	}

	var stored GuestIdentityClaim
	if err := fsm.DB.First(&stored, "guest_id = ?", 300).Error; err != nil {
		t.Fatal(err)
	}
	if stored.OwnerNodeID != "node-b" || stored.Token != "claim-after-move" {
		t.Fatalf("unexpected moved claim: %#v", stored)
	}
}

func TestGuestIdentityRegistrySnapshotRoundTripAndLegacyAbsence(t *testing.T) {
	source := newClusterModelTestDB(t, allSnapshotModels()...)
	sourceFSM := NewFSMDispatcher(source)
	RegisterDefaultHandlers(sourceFSM)
	activateGuestIdentityTestRegistry(t, sourceFSM, "node-a")
	reserved := GuestIdentityClaimSet{
		OwnerNodeID: "node-a", Token: "snapshot-reservation",
		Entries: []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeJail, GuestID: 450}},
	}
	if err := applyGuestIdentityFSMCommand(t, sourceFSM, "reserve_ids", reserved); err != nil {
		t.Fatal(err)
	}

	snapshot, err := CaptureClusterSnapshot(source)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	destination := newClusterModelTestDB(t, allSnapshotModels()...)
	destinationFSM := NewFSMDispatcher(destination)
	RegisterDefaultHandlers(destinationFSM)
	if err := destinationFSM.Restore(io.NopCloser(bytes.NewReader(raw))); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	var restored GuestIdentityClaim
	if err := destination.First(&restored, "guest_id = ?", 450).Error; err != nil {
		t.Fatal(err)
	}
	if restored.Token != reserved.Token {
		t.Fatalf("unexpected restored claim: %#v", restored)
	}

	legacy := &ClusterSnapshot{}
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := destinationFSM.Restore(io.NopCloser(bytes.NewReader(legacyRaw))); err != nil {
		t.Fatalf("restore legacy snapshot without registry: %v", err)
	}
	var registryCount int64
	if err := destination.Model(&GuestIdentityRegistry{}).Count(&registryCount).Error; err != nil {
		t.Fatal(err)
	}
	if registryCount != 0 {
		t.Fatalf("legacy restore registry count=%d, want 0 collecting-by-absence", registryCount)
	}
}

func TestGuestIdentityRegistryFSMDoesNotDeadlockSingleConnection(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")
	payload, err := json.Marshal(GuestIdentityClaimSet{
		OwnerNodeID: "node-a", Token: "single-connection",
		Entries: []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 999}},
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := json.Marshal(Command{Type: "guest_identity_registry", Action: "reserve_ids", Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response := fsm.Apply(&raft.Log{Type: raft.LogCommand, Data: command})
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

func TestGuestIdentityRegistryRejectsUnknownAction(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	err := applyGuestIdentityFSMCommand(t, fsm, "unknown", struct{}{})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("unsupported_guest_identity_registry_action_unknown")) {
		t.Fatalf("unknown action error=%v", err)
	}
}
