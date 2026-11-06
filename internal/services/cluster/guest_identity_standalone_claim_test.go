// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func TestGuestIdentityStandaloneAcquireRequiresOperationToken(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	service := withFSM(&Service{DB: database, NodeID: "standalone-node"})
	reservation := clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: service.NodeID,
		Entries:     []clusterServiceInterfaces.GuestIdentityReference{{GuestKind: clusterModels.ReplicationGuestTypeVM, GuestID: 902}},
	}
	if err := service.AcquireGuestIdentityReservation(t.Context(), reservation); err == nil {
		t.Fatal("creation acquired an unowned, unreleasable local reservation")
	}
	if len(service.guestIdentityLocalReservations) != 0 {
		t.Fatal("rejected acquisition left a local reservation")
	}
}

func TestGuestIdentityStandaloneClaimGuardsDeletion(t *testing.T) {
	database := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := database.Create(&vmModels.VM{RID: 902, Name: "guarded-vm"}).Error; err != nil {
		t.Fatalf("seed standalone VM: %v", err)
	}
	service := withFSM(&Service{DB: database, NodeID: "standalone-node"})

	claim, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	)
	if err != nil {
		t.Fatalf("claim standalone VM for deletion: %v", err)
	}
	if strings.TrimSpace(claim.LocalOperationToken) == "" {
		t.Fatalf("standalone deletion guard = %+v", claim)
	}
	if err := service.ValidateGuestIdentityClaim(t.Context(), claim); err != nil {
		t.Fatalf("validate standalone deletion guard: %v", err)
	}
	service.guestIdentityRuntimeMu.Lock()
	guardToken := service.guestIdentityLocalReservations[902].token
	service.guestIdentityRuntimeMu.Unlock()
	if guardToken != claim.LocalOperationToken {
		t.Fatalf("local deletion guard token = %q, want %q", guardToken, claim.LocalOperationToken)
	}
	if _, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	); err == nil || !strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("second standalone deletion claim error = %v", err)
	}
	mismatchedClaim := claim
	mismatchedClaim.LocalOperationToken = "different-local-operation"
	service.CancelGuestIdentityClaim(mismatchedClaim)
	if _, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	); err == nil || !strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("claim after mismatched cancel error = %v", err)
	}

	if err := database.Where("rid = ?", 902).Delete(&vmModels.VM{}).Error; err != nil {
		t.Fatalf("delete standalone VM: %v", err)
	}
	if err := service.ReleaseGuestIdentities(t.Context(), claim); err != nil {
		t.Fatalf("release standalone deletion guard: %v", err)
	}
	service.guestIdentityRuntimeMu.Lock()
	remainingGuards := len(service.guestIdentityLocalReservations)
	service.guestIdentityRuntimeMu.Unlock()
	if remainingGuards != 0 {
		t.Fatalf("standalone deletion guards after release = %d", remainingGuards)
	}
}
