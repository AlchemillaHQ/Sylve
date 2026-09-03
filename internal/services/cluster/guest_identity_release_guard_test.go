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

func TestGuestIdentityClusteredReleaseRequiresCanonicalRegistrationGone(t *testing.T) {
	database := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	if err := database.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("seed clustered state: %v", err)
	}
	if err := database.Create(&vmModels.VM{RID: 903, Name: "retained-vm"}).Error; err != nil {
		t.Fatalf("seed retained VM: %v", err)
	}
	service := &Service{DB: database, NodeID: "node-a"}
	reservation := clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: "node-a",
		Token:       "clustered-release-guard",
		Clustered:   true,
		Entries: []clusterServiceInterfaces.GuestIdentityReference{{
			GuestKind: clusterModels.ReplicationGuestTypeVM,
			GuestID:   903,
		}},
	}
	if err := service.trackClusterGuestIdentityOperation(reservation); err != nil {
		t.Fatalf("track clustered operation: %v", err)
	}

	err := service.ReleaseGuestIdentities(t.Context(), reservation)
	if err == nil || !strings.Contains(err.Error(), "canonical_registration_present") {
		t.Fatalf("release with canonical VM error = %v", err)
	}
	service.guestIdentityRuntimeMu.Lock()
	inFlight := len(service.guestIdentityLocalReservations)
	service.guestIdentityRuntimeMu.Unlock()
	if inFlight != 0 {
		t.Fatal("completed caller remained tracked after guarded release")
	}

	if err := database.Where("rid = ?", 903).Delete(&vmModels.VM{}).Error; err != nil {
		t.Fatalf("remove canonical VM: %v", err)
	}
	if err := service.requireNoLocalCanonicalGuestIdentitiesForRelease(t.Context(), reservation); err != nil {
		t.Fatalf("release boundary after canonical delete: %v", err)
	}
}
