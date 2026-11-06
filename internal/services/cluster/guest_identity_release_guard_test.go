// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"errors"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestGuestIdentityReleaseWaitsForTheLocalGuestToGo(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	service := withFSM(&Service{DB: database, NodeID: "node-a"})
	reservation, err := service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{903})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := database.Create(&vmModels.VM{RID: 903, Name: "retained-vm"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	err = service.ReleaseGuestIdentities(t.Context(), reservation)
	if !errors.Is(err, clusterModels.ErrGuestIdentityClaimConflict) {
		t.Fatalf("release while the VM exists: %v", err)
	}
	if claim, err := service.guestIdentityClaim(t.Context(), 903); err != nil || claim == nil {
		t.Fatalf("refused release dropped the claim: claim=%+v err=%v", claim, err)
	}
	service.guestIdentityRuntimeMu.Lock()
	inFlight := len(service.guestIdentityLocalReservations)
	service.guestIdentityRuntimeMu.Unlock()
	if inFlight != 0 {
		t.Fatal("refused release left the ID in flight")
	}

	if err := database.Where("rid = ?", 903).Delete(&vmModels.VM{}).Error; err != nil {
		t.Fatalf("remove VM: %v", err)
	}
	if err := service.ReleaseGuestIdentities(t.Context(), reservation); err != nil {
		t.Fatalf("release after the VM is gone: %v", err)
	}
	if claim, err := service.guestIdentityClaim(t.Context(), 903); err != nil || claim != nil {
		t.Fatalf("claim after release: claim=%+v err=%v", claim, err)
	}
}
