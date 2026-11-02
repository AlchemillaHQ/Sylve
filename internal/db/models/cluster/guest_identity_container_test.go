// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"testing"
)

func TestContainerIdentitySharesRegistryNamespace(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a")
	vm := GuestIdentityClaimSet{OwnerNodeID: "node-a", Token: "vm-token", Entries: []GuestIdentityEntry{{GuestKind: ReplicationGuestTypeVM, GuestID: 1}}}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", vm); err != nil {
		t.Fatal(err)
	}
	container := GuestIdentityClaimSet{OwnerNodeID: "node-a", Token: "container-token", Entries: []GuestIdentityEntry{{GuestKind: GuestIdentityKindContainer, GuestID: 1}}}
	if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", container); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
		t.Fatalf("VM/container collision: %v", err)
	}
	container.Entries[0].GuestID = 9999
	for range 2 {
		if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", container); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{ReplicationGuestTypeVM, ReplicationGuestTypeJail} {
		conflict := GuestIdentityClaimSet{OwnerNodeID: "node-a", Token: "conflict-" + kind, Entries: []GuestIdentityEntry{{GuestKind: kind, GuestID: 9999}}}
		if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", conflict); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
			t.Fatalf("container/%s collision: %v", kind, err)
		}
	}
	if err := applyGuestIdentityFSMCommand(t, fsm, "release_ids", container); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []uint{0, 10000} {
		container.Entries[0].GuestID = invalid
		if err := applyGuestIdentityFSMCommand(t, fsm, "reserve_ids", container); err == nil {
			t.Fatal("container changed the shared numeric range")
		}
	}
}

func TestContainerIdentityDoesNotEnableProtectionOrOwnershipMigration(t *testing.T) {
	if !ValidGuestIdentityKind(" CONTAINER ") {
		t.Fatal("containers must be a guest identity kind")
	}
	fsm := newGuestIdentityFSM(t)
	activateGuestIdentityTestRegistry(t, fsm, "node-a", "node-b")
	if err := MoveGuestIdentityClaimTxn(fsm.DB, &GuestIdentityMoveOwner{GuestKind: GuestIdentityKindContainer, GuestID: 701}); err == nil {
		t.Fatal("container entered ownership migration")
	}
	if err := RequireGuestDeletionDetachedTxn(fsm.DB, GuestIdentityKindContainer, 701); err == nil {
		t.Fatal("VM/jail protection helper admitted containers")
	}
}
