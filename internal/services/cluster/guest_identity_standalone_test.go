// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"strings"
	"sync"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func TestGuestIdentityStandaloneConcurrentCrossKindReservation(t *testing.T) {
	database := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	service := &Service{DB: database, NodeID: "standalone-node"}

	type result struct {
		kind        string
		reservation clusterServiceInterfaces.GuestIdentityReservation
		err         error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for _, kind := range []string{
		clusterModels.ReplicationGuestTypeVM,
		clusterModels.ReplicationGuestTypeJail,
	} {
		kind := kind
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			reservation, err := service.ReserveGuestIdentities(t.Context(), kind, []uint{901})
			results <- result{kind: kind, reservation: reservation, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var winner result
	successes := 0
	conflicts := 0
	for candidate := range results {
		if candidate.err == nil {
			winner = candidate
			successes++
			continue
		}
		if !strings.Contains(candidate.err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
			t.Fatalf("reservation loser error = %v", candidate.err)
		}
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("standalone reservation race successes=%d conflicts=%d", successes, conflicts)
	}
	if winner.reservation.Clustered {
		t.Fatalf("standalone reservation marked clustered: %+v", winner.reservation)
	}
	if err := service.ReleaseGuestIdentities(t.Context(), winner.reservation); err != nil {
		t.Fatalf("release race winner: %v", err)
	}

	reuseKind := clusterModels.ReplicationGuestTypeVM
	if winner.kind == reuseKind {
		reuseKind = clusterModels.ReplicationGuestTypeJail
	}
	reused, err := service.ReserveGuestIdentities(t.Context(), reuseKind, []uint{901})
	if err != nil {
		t.Fatalf("reserve released ID for other kind: %v", err)
	}
	if err := service.ReleaseGuestIdentities(t.Context(), reused); err != nil {
		t.Fatalf("release reuse reservation: %v", err)
	}
}
