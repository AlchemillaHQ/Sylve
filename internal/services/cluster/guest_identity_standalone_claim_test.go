// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestGuestIdentityStandaloneClaimGuardsDeletionAndClusterFormation(t *testing.T) {
	database := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := database.Create(&vmModels.VM{RID: 902, Name: "guarded-vm"}).Error; err != nil {
		t.Fatalf("seed standalone VM: %v", err)
	}
	service := &Service{DB: database, NodeID: "standalone-node"}

	claim, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	)
	if err != nil {
		t.Fatalf("claim standalone VM for deletion: %v", err)
	}
	if claim.Clustered || strings.TrimSpace(claim.Token) == "" {
		t.Fatalf("standalone deletion guard = %+v", claim)
	}

	service.guestIdentityRuntimeMu.Lock()
	guardToken := service.guestIdentityLocalReservations[902]
	service.guestIdentityRuntimeMu.Unlock()
	if guardToken != claim.Token {
		t.Fatalf("local deletion guard token = %q, want %q", guardToken, claim.Token)
	}
	if _, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	); err == nil || !strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("second standalone deletion claim error = %v", err)
	}

	if err := service.ReleaseGuestIdentities(t.Context(), claim); err != nil {
		t.Fatalf("release standalone deletion guard: %v", err)
	}
	service.guestIdentityRuntimeMu.Lock()
	remainingGuards := len(service.guestIdentityLocalReservations)
	service.guestIdentityClusterFormation = true
	service.guestIdentityRuntimeMu.Unlock()
	if remainingGuards != 0 {
		t.Fatalf("standalone deletion guards after release = %d", remainingGuards)
	}
	if _, err := service.GuestIdentityClaim(
		t.Context(),
		clusterModels.ReplicationGuestTypeVM,
		902,
		"",
	); err == nil || !strings.Contains(err.Error(), "cluster_formation_in_progress") {
		t.Fatalf("deletion during cluster formation error = %v", err)
	}
}
