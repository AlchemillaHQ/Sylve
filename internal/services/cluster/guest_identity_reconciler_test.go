// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestGuestIdentityReconcileSkipsAFencedNode(t *testing.T) {
	database := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	if err := database.Create(&vmModels.VM{RID: 900, Name: "unclaimed"}).Error; err != nil {
		t.Fatal(err)
	}
	service := withFSM(&Service{DB: database, NodeID: "node-a", mutationGate: NewMutationGate()})

	service.reconcileGuestIdentities(t.Context())
	if claim, err := service.guestIdentityClaim(t.Context(), 900); err != nil || claim != nil {
		t.Fatalf("fenced node's claim = %+v, %v", claim, err)
	}
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	service.reconcileGuestIdentities(t.Context())
	if claim, err := service.guestIdentityClaim(t.Context(), 900); err != nil || claim == nil {
		t.Fatalf("open node's claim = %+v, %v", claim, err)
	}
}
