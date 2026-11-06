// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"errors"
	"testing"
)

func TestContainerIdentitySharesTheGuestIDNamespace(t *testing.T) {
	fsm := newGuestIdentityFSM(t)
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", claimSet("node-a", GuestIdentityEntry{GuestKind: ReplicationGuestTypeVM, GuestID: 1})); err != nil {
		t.Fatal(err)
	}
	container := claimSet("node-a", GuestIdentityEntry{GuestKind: GuestIdentityKindContainer, GuestID: 1})
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", container); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
		t.Fatalf("VM/container collision: %v", err)
	}
	container.Entries[0].GuestID = GuestIdentityMaxID
	if err := applyGuestIdentityFSMCommand(t, fsm, "claim", container); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{ReplicationGuestTypeVM, ReplicationGuestTypeJail} {
		conflict := claimSet("node-b", GuestIdentityEntry{GuestKind: kind, GuestID: GuestIdentityMaxID})
		if err := applyGuestIdentityFSMCommand(t, fsm, "claim", conflict); !errors.Is(err, ErrGuestIdentityAlreadyInUse) {
			t.Fatalf("container/%s collision: %v", kind, err)
		}
	}
	for _, invalid := range []uint{0, GuestIdentityMaxID + 1} {
		container.Entries[0].GuestID = invalid
		if err := applyGuestIdentityFSMCommand(t, fsm, "claim", container); err == nil {
			t.Fatal("container changed the shared numeric range")
		}
	}
}

func TestContainerIdentityDoesNotEnableProtectionOrOwnershipMigration(t *testing.T) {
	if !ValidGuestIdentityKind(" CONTAINER ") {
		t.Fatal("containers must be a guest identity kind")
	}
	fsm := newGuestIdentityFSM(t)
	if err := MoveGuestIdentityClaimTxn(fsm.DB, &GuestIdentityMoveOwner{
		GuestKind: GuestIdentityKindContainer, GuestID: 701, OldOwnerNodeID: "node-a", NewOwnerNodeID: "node-b",
	}); err == nil {
		t.Fatal("container entered ownership migration")
	}
	if err := RequireGuestDeletionDetachedTxn(fsm.DB, GuestIdentityKindContainer, 701); err == nil {
		t.Fatal("VM/jail protection helper admitted containers")
	}
}
