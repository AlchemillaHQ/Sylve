// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func newGuestIdentityClaimsTestCluster(t *testing.T) (nodes []*clusterRaftTestNode, leader, a, b *clusterRaftTestNode) {
	t.Helper()
	nodes = setupClusterRaftTestNodes(t, 3, &clusterModels.Cluster{}, &vmModels.VM{}, &jailModels.Jail{})
	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	for _, node := range nodes {
		switch {
		case node == leader:
		case a == nil:
			a = node
		default:
			b = node
		}
	}
	routeTestPeerRequests(nodes)
	return nodes, leader, a, b
}

func waitForGuestIdentityClaim(t *testing.T, nodes []*clusterRaftTestNode, guestID uint, owner string) {
	t.Helper()
	waitForClusterCondition(t, 8*time.Second, fmt.Sprintf("claim %d owned by %q", guestID, owner), func() bool {
		for _, node := range nodes {
			claim, err := node.service.guestIdentityClaim(context.Background(), guestID)
			if err != nil || (claim == nil) != (owner == "") || claim != nil && claim.OwnerNodeID != owner {
				return false
			}
		}
		return true
	})
}

func seedGuestOperation(t *testing.T, node *clusterRaftTestNode, guestID uint, owner, target string) {
	t.Helper()
	if err := node.service.DB.Create(&clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: guestID,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     fmt.Sprintf("migration-%d", guestID), OwnerNodeID: owner, TargetNodeID: target,
		TaskID: guestID, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed guest operation %d: %v", guestID, err)
	}
}

func TestIntegrationGuestIdentityClaimsAreClusterWide(t *testing.T) {
	nodes, _, a, b := newGuestIdentityClaimsTestCluster(t)

	reservation, err := a.service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{100})
	if err != nil {
		t.Fatalf("reserve on a follower: %v", err)
	}
	if claim, err := a.service.guestIdentityClaim(t.Context(), 100); err != nil || claim == nil || claim.OwnerNodeID != a.id {
		t.Fatalf("follower's claim right after reserving = %+v, %v", claim, err)
	}
	if _, err := b.service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeJail, []uint{100}); !errors.Is(err, clusterModels.ErrGuestIdentityAlreadyInUse) {
		t.Fatalf("another follower's claim on the ID = %v", err)
	}
	waitForGuestIdentityClaim(t, nodes, 100, a.id)
	if err := b.service.RequireGuestIDAvailable(t.Context(), 100); err == nil {
		t.Fatal("the claimed ID looked free on another node")
	}

	if err := a.service.ReleaseGuestIdentities(t.Context(), reservation); err != nil {
		t.Fatalf("release: %v", err)
	}
	waitForGuestIdentityClaim(t, nodes, 100, "")
	if _, err := b.service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeJail, []uint{100}); err != nil {
		t.Fatalf("reserve the released ID: %v", err)
	}
}

func TestIntegrationGuestIdentityRacingClaimsHaveOneWinner(t *testing.T) {
	nodes, _, a, b := newGuestIdentityClaimsTestCluster(t)
	racers := []*clusterRaftTestNode{a, b}
	errs := make([]error, len(racers))
	var wait sync.WaitGroup
	for i, racer := range racers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, errs[i] = racer.service.ReserveGuestIdentities(context.Background(), clusterModels.ReplicationGuestTypeVM, []uint{200})
		}()
	}
	wait.Wait()

	var winners []string
	for i, err := range errs {
		if err == nil {
			winners = append(winners, racers[i].id)
		} else if !errors.Is(err, clusterModels.ErrGuestIdentityAlreadyInUse) {
			t.Fatalf("losing claim = %v", err)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %v, errors = %v", winners, errs)
	}
	waitForGuestIdentityClaim(t, nodes, 200, winners[0])
}

func TestIntegrationGuestIdentityOwnerReconcile(t *testing.T) {
	nodes, leader, a, b := newGuestIdentityClaimsTestCluster(t)
	for _, guest := range []vmModels.VM{{RID: 300, Name: "unclaimed"}, {RID: 303, Name: "held-elsewhere"}} {
		if err := a.service.DB.Create(&guest).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedGuestIdentityJoinTestClaim(t, leader, a.id, clusterModels.ReplicationGuestTypeVM, 301)
	seedGuestIdentityJoinTestClaim(t, leader, b.id, clusterModels.ReplicationGuestTypeVM, 303)
	seedGuestIdentityJoinTestClaim(t, leader, a.id, clusterModels.ReplicationGuestTypeVM, 304)
	if _, err := a.service.ReserveGuestIdentities(t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{302}); err != nil {
		t.Fatalf("reserve an ID in flight: %v", err)
	}
	seedGuestOperation(t, a, 304, a.id, b.id)
	waitForGuestIdentityClaim(t, nodes, 301, a.id)
	waitForGuestIdentityClaim(t, nodes, 303, b.id)
	waitForGuestIdentityClaim(t, nodes, 304, a.id)

	heldElsewhere, err := a.service.reconcileOwnGuestIdentityClaims(t.Context())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !slices.Equal(heldElsewhere, []uint{303}) {
		t.Fatalf("held elsewhere = %v, want [303]", heldElsewhere)
	}
	waitForGuestIdentityClaim(t, nodes, 300, a.id)
	waitForGuestIdentityClaim(t, nodes, 301, "")
	waitForGuestIdentityClaim(t, nodes, 302, a.id)
	waitForGuestIdentityClaim(t, nodes, 303, b.id)
	waitForGuestIdentityClaim(t, nodes, 304, a.id)
}

func TestIntegrationDepartedMembersAreForgotten(t *testing.T) {
	nodes, leader, a, _ := newGuestIdentityClaimsTestCluster(t)
	seedGuestIdentityJoinTestClaim(t, leader, "departed-node", clusterModels.ReplicationGuestTypeJail, 500)
	seedGuestIdentityJoinTestClaim(t, leader, a.id, clusterModels.ReplicationGuestTypeVM, 501)
	for _, nodeID := range []string{"departed-host", a.id} {
		if err := leader.service.apply("cluster_ssh_identity", "upsert", clusterModels.ClusterSSHIdentity{
			NodeUUID: nodeID, SSHUser: "root", SSHHost: nodeID, PublicKey: "key-" + nodeID,
		}); err != nil {
			t.Fatalf("seed SSH identity for %s: %v", nodeID, err)
		}
	}

	if err := leader.service.forgetDepartedMembers(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	waitForGuestIdentityClaim(t, nodes, 500, "")
	waitForGuestIdentityClaim(t, nodes, 501, a.id)
	waitForClusterCondition(t, 8*time.Second, "only the member's SSH identity left", func() bool {
		for _, node := range nodes {
			var identities []string
			if err := node.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Pluck("node_uuid", &identities).Error; err != nil ||
				!slices.Equal(identities, []string{a.id}) {
				return false
			}
		}
		return true
	})
}

func TestIntegrationReclaimGuestIdentity(t *testing.T) {
	nodes, leader, a, _ := newGuestIdentityClaimsTestCluster(t)
	seedGuestIdentityJoinTestClaim(t, leader, a.id, clusterModels.ReplicationGuestTypeVM, 400)
	seedGuestIdentityJoinTestClaim(t, leader, a.id, clusterModels.ReplicationGuestTypeVM, 401)
	seedGuestOperation(t, leader, 401, a.id, leader.id)

	if err := leader.service.ReclaimGuestIdentity(t.Context(), 401); err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("reclaim during a guest operation = %v", err)
	}
	if err := leader.service.ReclaimGuestIdentity(t.Context(), 400); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	waitForGuestIdentityClaim(t, nodes, 400, "")
	waitForGuestIdentityClaim(t, nodes, 401, a.id)
	if err := leader.service.ReclaimGuestIdentity(t.Context(), 400); !errors.Is(err, clusterModels.ErrGuestIdentityClaimConflict) {
		t.Fatalf("reclaim of an unclaimed ID = %v", err)
	}
}
