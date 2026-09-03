// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

func guestIdentityRaftIntegrationModels() []any {
	return []any{
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.GuestIdentityRegistry{},
		&clusterModels.GuestIdentityEnrollment{},
		&clusterModels.GuestIdentityClaim{},
		&clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{},
		&jailModels.Jail{},
	}
}

func initializeActiveGuestIdentityRegistryForTest(
	t *testing.T,
	leader *clusterRaftTestNode,
	nodes []*clusterRaftTestNode,
) {
	t.Helper()
	voterIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		voterIDs = append(voterIDs, node.id)
		if err := leader.service.registerGuestIdentityInventoryReport(
			node.id,
			BuildGuestIdentityInventoryReport(nil),
		); err != nil {
			t.Fatalf("enroll %s: %v", node.id, err)
		}
	}
	if err := leader.service.applyGuestIdentityRaftAction(
		"activate_registry",
		clusterModels.GuestIdentityActivateRegistry{VoterNodeIDs: voterIDs},
	); err != nil {
		t.Fatalf("activate guest identity registry: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "active guest identity registry convergence", func() bool {
		for _, node := range nodes {
			var registry clusterModels.GuestIdentityRegistry
			if err := node.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error; err != nil ||
				registry.Phase != clusterModels.GuestIdentityRegistryPhaseActive {
				return false
			}
			var enrollmentCount int64
			if err := node.service.DB.Model(&clusterModels.GuestIdentityEnrollment{}).
				Count(&enrollmentCount).Error; err != nil || enrollmentCount != int64(len(nodes)) {
				return false
			}
		}
		return true
	})
}

func enableClusteredGuestIdentityServicesForTest(t *testing.T, nodes []*clusterRaftTestNode) {
	t.Helper()
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{
			Enabled: true, Key: "guest-identity-integration",
		}).Error; err != nil {
			t.Fatalf("enable cluster on %s: %v", node.id, err)
		}
	}
	for _, node := range nodes {
		caller := node
		caller.service.guestIdentityControlForNode = func(
			ctx context.Context,
			_ string,
			request GuestIdentityControlRequest,
		) (GuestIdentityControlResponse, error) {
			leader := findClusterRaftLeader(nodes)
			if leader == nil {
				return GuestIdentityControlResponse{}, fmt.Errorf("cluster_consensus_unavailable: leader_not_available")
			}
			return leader.service.HandleGuestIdentityControl(ctx, caller.id, request)
		}
	}
}

func disconnectGuestIdentityRaftNode(nodes []*clusterRaftTestNode, target *clusterRaftTestNode) {
	if target == nil {
		return
	}
	target.transport.DisconnectAll()
	for _, node := range nodes {
		if node != target {
			node.transport.Disconnect(target.addr)
		}
	}
}

func guestIdentityControlReservation(
	ownerNodeID, token, guestKind string,
	guestID uint,
) clusterServiceInterfaces.GuestIdentityReservation {
	return clusterServiceInterfaces.GuestIdentityReservation{
		OwnerNodeID: ownerNodeID,
		Token:       token,
		Clustered:   true,
		Entries: []clusterServiceInterfaces.GuestIdentityReference{{
			GuestKind: guestKind,
			GuestID:   guestID,
		}},
	}
}

func TestIntegrationRaftGuestIdentityEnrollmentAcrossAvailabilityWindows(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	followers := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node != leader {
			followers = append(followers, node)
		}
	}
	firstAvailable, laterAvailable := followers[0], followers[1]

	if err := leader.service.DB.Create(&vmModels.VM{RID: 200, Name: "leader-vm"}).Error; err != nil {
		t.Fatalf("seed leader VM: %v", err)
	}
	firstSimulator := newClusterPeerSimulator()
	defer firstSimulator.Close()
	registerGuestIdentityInventoryPeer(t, firstSimulator, firstAvailable.id, []GuestIdentityInventoryEntry{{
		NodeID: firstAvailable.id, GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 201, RecordID: 1, Name: "first-window-jail",
	}})
	laterSimulator := newClusterPeerSimulator()
	defer laterSimulator.Close()
	registerGuestIdentityInventoryPeer(t, laterSimulator, laterAvailable.id, []GuestIdentityInventoryEntry{{
		NodeID: laterAvailable.id, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 202, RecordID: 1, Name: "second-window-vm",
	}})
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	availability := map[string]bool{firstAvailable.id: true, laterAvailable.id: false}

	leader.service.guestIdentityInventoryAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		if !availability[nodeID] {
			return "", fmt.Errorf("node %s unavailable", nodeID)
		}
		switch nodeID {
		case firstAvailable.id:
			return firstSimulator.Addr(), nil
		case laterAvailable.id:
			return laterSimulator.Addr(), nil
		default:
			return "", fmt.Errorf("unexpected inventory node %s", nodeID)
		}
	}

	disconnectGuestIdentityRaftNode(nodes, laterAvailable)
	if err := leader.service.ReconcileGuestIdentityRegistry(context.Background()); err == nil ||
		!strings.Contains(err.Error(), laterAvailable.id) {
		t.Fatalf("first availability window error = %v", err)
	}
	var enrollmentCount int64
	if err := leader.service.DB.Model(&clusterModels.GuestIdentityEnrollment{}).Count(&enrollmentCount).Error; err != nil {
		t.Fatalf("count first-window enrollments: %v", err)
	}
	if enrollmentCount != 2 {
		t.Fatalf("first window enrollments=%d, want leader plus one follower", enrollmentCount)
	}
	var registry clusterModels.GuestIdentityRegistry
	if err := leader.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error; err != nil {
		t.Fatalf("load collecting registry: %v", err)
	}
	if registry.Phase != clusterModels.GuestIdentityRegistryPhaseCollecting {
		t.Fatalf("first window phase=%q, want collecting", registry.Phase)
	}

	disconnectGuestIdentityRaftNode(nodes, firstAvailable)
	availability[firstAvailable.id] = false
	leader.transport.Connect(laterAvailable.addr, laterAvailable.transport)
	laterAvailable.transport.Connect(leader.addr, leader.transport)
	waitForClusterCondition(t, 8*time.Second, "later voter catch-up", func() bool {
		return laterAvailable.raft.AppliedIndex() >= leader.raft.AppliedIndex()
	})
	availability[laterAvailable.id] = true
	if err := leader.service.ReconcileGuestIdentityRegistry(context.Background()); err != nil {
		t.Fatalf("second availability window: %v", err)
	}

	var claims []clusterModels.GuestIdentityClaim
	if err := leader.service.DB.Order("guest_id ASC").Find(&claims).Error; err != nil {
		t.Fatalf("load enrolled claims: %v", err)
	}
	if len(claims) != 3 || claims[0].GuestID != 200 || claims[1].GuestID != 201 || claims[2].GuestID != 202 {
		t.Fatalf("claims after staggered enrollment = %+v", claims)
	}
	if err := leader.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error; err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	if registry.Phase != clusterModels.GuestIdentityRegistryPhaseActive {
		t.Fatalf("second window phase=%q, want active", registry.Phase)
	}
	if firstSimulator.NumRequests() != 1 || laterSimulator.NumRequests() != 1 {
		t.Fatalf("inventory retries ignored durable enrollment: first=%d later=%d", firstSimulator.NumRequests(), laterSimulator.NumRequests())
	}
}

func TestIntegrationRaftGuestIdentityBootstrapWaitsForSealedMigration(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	now := time.Now().UTC()
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeJail,
		GuestID:      23,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:source-node:23",
		OwnerNodeID:  "source-node",
		TargetNodeID: "target-node",
		TaskID:       23,
		AcquiredAt:   now,
		SealedAt:     &now,
	}
	if err := leader.service.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed cutover migration: %v", err)
	}

	err := leader.service.ReconcileGuestIdentityRegistry(context.Background())
	if !errors.Is(err, clusterModels.ErrGuestIdentityRegistryInitializing) {
		t.Fatalf("reconcile with cutover migration error = %v", err)
	}
	var enrollmentCount int64
	if err := leader.service.DB.Model(&clusterModels.GuestIdentityEnrollment{}).Count(&enrollmentCount).Error; err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("enrollments during cutover = %d, want 0", enrollmentCount)
	}

	if err := leader.service.DB.Delete(&operation).Error; err != nil {
		t.Fatalf("complete cutover migration: %v", err)
	}
	if err := leader.service.ReconcileGuestIdentityRegistry(context.Background()); err != nil {
		t.Fatalf("reconcile after cutover: %v", err)
	}
	var registry clusterModels.GuestIdentityRegistry
	if err := leader.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error; err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if registry.Phase != clusterModels.GuestIdentityRegistryPhaseActive {
		t.Fatalf("registry phase = %q, want active", registry.Phase)
	}
}

func TestIntegrationRaftGuestIdentityMoveDefersToSealedMigrationBeforeBootstrap(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	target := nodes[0]
	if target == leader {
		target = nodes[1]
	}
	now := time.Now().UTC()
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeJail,
		GuestID:      23,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:source-node:23",
		OwnerNodeID:  leader.id,
		TargetNodeID: target.id,
		TaskID:       23,
		AcquiredAt:   now,
		SealedAt:     &now,
	}
	if err := leader.service.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed cutover migration: %v", err)
	}

	if err := leader.service.MoveGuestIdentityOwner(
		context.Background(),
		clusterModels.ReplicationGuestTypeJail,
		23,
		target.id,
		operation.Token,
	); err != nil {
		t.Fatalf("defer ownership to sealed migration: %v", err)
	}
	var claimCount int64
	if err := leader.service.DB.Model(&clusterModels.GuestIdentityClaim{}).Count(&claimCount).Error; err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claimCount != 0 {
		t.Fatalf("claims before bootstrap = %d, want 0", claimCount)
	}
}

func TestReplicationPolicyGuestIdentityMoveDefersOnlyExactSealedMigration(t *testing.T) {
	db := newClusterServiceTestDB(
		t,
		&clusterModels.GuestIdentityRegistry{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationGuestOperation{},
	)
	policy := clusterModels.ReplicationPolicy{
		Name:         "jail-23",
		GuestType:    clusterModels.ReplicationGuestTypeJail,
		GuestID:      23,
		SourceNodeID: "source-node",
		ActiveNodeID: "source-node",
		CronExpr:     "0 * * * *",
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
	now := time.Now().UTC()
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeJail,
		GuestID:      23,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:source-node:23",
		OwnerNodeID:  "source-node",
		TargetNodeID: "target-node",
		TaskID:       23,
		AcquiredAt:   now,
		SealedAt:     &now,
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed cutover migration: %v", err)
	}
	service := &Service{DB: db}

	move, err := service.replicationPolicyGuestIdentityMove(
		policy.ID,
		"source-node",
		"target-node",
		"migration-disabled-owner",
	)
	if err != nil || move != nil {
		t.Fatalf("exact sealed migration move = %+v, %v", move, err)
	}
	if err := db.Model(&operation).Update("target_node_id", "other-node").Error; err != nil {
		t.Fatalf("change cutover target: %v", err)
	}
	_, err = service.replicationPolicyGuestIdentityMove(
		policy.ID,
		"source-node",
		"target-node",
		"migration-disabled-owner",
	)
	if !errors.Is(err, clusterModels.ErrGuestIdentityRegistryInitializing) {
		t.Fatalf("nonmatching migration error = %v", err)
	}
}

func TestIntegrationRaftGuestIdentityConcurrentCrossKindReservation(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	enableClusteredGuestIdentityServicesForTest(t, nodes)

	type reservationResult struct {
		caller      *clusterRaftTestNode
		reservation clusterServiceInterfaces.GuestIdentityReservation
		err         error
	}
	callers := []*clusterRaftTestNode{leader, nodes[1]}
	if callers[1] == leader {
		callers[1] = nodes[2]
	}
	kinds := []string{clusterModels.ReplicationGuestTypeVM, clusterModels.ReplicationGuestTypeJail}
	results := make(chan reservationResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range callers {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reservation, err := callers[index].service.ReserveGuestIdentities(
				context.Background(), kinds[index], []uint{501},
			)
			results <- reservationResult{caller: callers[index], reservation: reservation, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner *reservationResult
	failures := 0
	for result := range results {
		result := result
		if result.err == nil {
			winner = &result
			continue
		}
		if !strings.Contains(result.err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
			t.Fatalf("reservation loser error = %v", result.err)
		}
		failures++
	}
	if winner == nil || failures != 1 {
		t.Fatalf("reservation race winner=%+v failures=%d", winner, failures)
	}
	waitForClusterCondition(t, 8*time.Second, "cross-kind claim convergence", func() bool {
		for _, node := range nodes {
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 501).Error; err != nil ||
				claim.OwnerNodeID != winner.reservation.OwnerNodeID ||
				claim.GuestKind != winner.reservation.Entries[0].GuestKind ||
				claim.Token != winner.reservation.Token {
				return false
			}
		}
		return true
	})
}

func TestIntegrationRaftGuestIdentityLocalLifecycleGuard(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	enableClusteredGuestIdentityServicesForTest(t, nodes)
	ctx := context.Background()

	reservation, err := leader.service.ReserveGuestIdentities(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		[]uint{530},
	)
	if err != nil {
		t.Fatalf("reserve create identity: %v", err)
	}
	if _, err := leader.service.GuestIdentityClaim(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		530,
		"",
	); err == nil || !errors.Is(err, clusterModels.ErrGuestIdentityClaimConflict) ||
		!strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("delete claim during create error = %v", err)
	}
	if err := leader.service.FinalizeGuestIdentities(ctx, reservation); err != nil {
		t.Fatalf("finalize create identity: %v", err)
	}

	claim, err := leader.service.GuestIdentityClaim(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		530,
		"",
	)
	if err != nil {
		t.Fatalf("claim identity for deletion: %v", err)
	}
	if claim.Token != reservation.Token ||
		strings.TrimSpace(claim.LocalOperationToken) == "" ||
		claim.LocalOperationToken == claim.Token {
		t.Fatalf("deletion claim tokens = %+v", claim)
	}
	if err := leader.service.ValidateGuestIdentityClaim(ctx, claim); err != nil {
		t.Fatalf("validate deletion claim: %v", err)
	}
	if _, err := leader.service.GuestIdentityClaim(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		530,
		"",
	); err == nil || !strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("second deletion claim error = %v", err)
	}

	wrongClaim := claim
	wrongClaim.LocalOperationToken = uuid.NewString()
	leader.service.CancelGuestIdentityClaim(wrongClaim)
	if _, err := leader.service.GuestIdentityClaim(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		530,
		"",
	); err == nil || !strings.Contains(err.Error(), "local_mutation_in_progress") {
		t.Fatalf("claim after mismatched cancel error = %v", err)
	}

	if _, err := leader.service.HandleGuestIdentityControl(ctx, leader.id, GuestIdentityControlRequest{
		Operation:   guestIdentityControlRelease,
		Reservation: reservation,
	}); err != nil {
		t.Fatalf("replace claim release: %v", err)
	}
	replacement := guestIdentityControlReservation(
		leader.id,
		uuid.NewString(),
		clusterModels.ReplicationGuestTypeVM,
		530,
	)
	if _, err := leader.service.HandleGuestIdentityControl(ctx, leader.id, GuestIdentityControlRequest{
		Operation:   guestIdentityControlReserve,
		Reservation: replacement,
	}); err != nil {
		t.Fatalf("replace claim reserve: %v", err)
	}
	if err := leader.service.ValidateGuestIdentityClaim(ctx, claim); err == nil ||
		!errors.Is(err, clusterModels.ErrGuestIdentityClaimConflict) ||
		!strings.Contains(err.Error(), "claim_changed") {
		t.Fatalf("stale claim validation error = %v", err)
	}

	leader.service.CancelGuestIdentityClaim(claim)
	replacementClaim, err := leader.service.GuestIdentityClaim(
		ctx,
		clusterModels.ReplicationGuestTypeVM,
		530,
		"",
	)
	if err != nil {
		t.Fatalf("claim replacement identity: %v", err)
	}
	if replacementClaim.Token != replacement.Token {
		t.Fatalf("replacement token = %q, want %q", replacementClaim.Token, replacement.Token)
	}
	leader.service.CancelGuestIdentityClaim(replacementClaim)
}

func TestIntegrationRaftGuestIdentityManualReclaim(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	enableClusteredGuestIdentityServicesForTest(t, nodes)

	reservation, err := leader.service.ReserveGuestIdentities(
		context.Background(), clusterModels.ReplicationGuestTypeVM, []uint{505},
	)
	if err != nil {
		t.Fatalf("reserve reclaim candidate: %v", err)
	}
	if err := leader.service.ReclaimGuestIdentity(
		context.Background(), 505, false, "",
	); err == nil || !errors.Is(err, clusterModels.ErrGuestIdentityReclaimUnsafe) ||
		!strings.Contains(err.Error(), "operation_in_progress") {
		t.Fatalf("reclaim during local create error=%v", err)
	}

	if err := leader.service.FinalizeGuestIdentities(context.Background(), reservation); err != nil {
		t.Fatalf("finalize local create guard: %v", err)
	}
	if err := leader.service.DB.Create(&vmModels.VM{RID: 505, Name: "still-registered"}).Error; err != nil {
		t.Fatalf("seed canonical VM: %v", err)
	}
	if err := leader.service.ReclaimGuestIdentity(
		context.Background(), 505, false, "",
	); err == nil || !errors.Is(err, clusterModels.ErrGuestIdentityStillRegistered) {
		t.Fatalf("reclaim registered VM error=%v", err)
	}
	if err := leader.service.DB.Delete(&vmModels.VM{}, "rid = ?", 505).Error; err != nil {
		t.Fatalf("remove canonical VM: %v", err)
	}

	if err := leader.service.ReclaimGuestIdentity(
		context.Background(), 505, false, "",
	); err != nil {
		t.Fatalf("reclaim orphan claim: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "manual reclaim convergence", func() bool {
		var count int64
		return leader.service.DB.Model(&clusterModels.GuestIdentityClaim{}).
			Where("guest_id = ?", 505).Count(&count).Error == nil && count == 0
	})

	reused, err := leader.service.ReserveGuestIdentities(
		context.Background(), clusterModels.ReplicationGuestTypeJail, []uint{505},
	)
	if err != nil {
		t.Fatalf("reuse reclaimed ID across kind: %v", err)
	}
	if err := leader.service.FinalizeGuestIdentities(context.Background(), reused); err != nil {
		t.Fatalf("finalize reuse guard: %v", err)
	}
	if err := leader.service.ReclaimGuestIdentity(
		context.Background(), 505, true, "wrong",
	); err == nil || !errors.Is(err, clusterModels.ErrGuestIdentityReclaimUnsafe) ||
		!strings.Contains(err.Error(), "confirmation_must_equal_guest_id") {
		t.Fatalf("force reclaim confirmation error=%v", err)
	}
	if err := leader.service.ReleaseGuestIdentities(context.Background(), reused); err != nil {
		t.Fatalf("release reuse probe: %v", err)
	}
}

func TestIntegrationRaftGuestIdentityForceReclaimChecksReachableVoters(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	enableClusteredGuestIdentityServicesForTest(t, nodes)

	remote := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node != leader {
			remote = append(remote, node)
		}
	}
	owner, unavailable := remote[0], remote[1]
	reservation, err := owner.service.ReserveGuestIdentities(
		context.Background(), clusterModels.ReplicationGuestTypeVM, []uint{506},
	)
	if err != nil {
		t.Fatalf("reserve remote reclaim candidate: %v", err)
	}
	if err := owner.service.FinalizeGuestIdentities(context.Background(), reservation); err != nil {
		t.Fatalf("finalize remote reclaim candidate: %v", err)
	}
	if err := owner.service.DB.Create(&vmModels.VM{RID: 506, Name: "reachable-owner-vm"}).Error; err != nil {
		t.Fatalf("seed owner VM: %v", err)
	}

	ownerAPI := newClusterPeerSimulator()
	defer ownerAPI.Close()
	registerGuestIdentityInventoryPeer(t, ownerAPI, owner.id, []GuestIdentityInventoryEntry{{
		NodeID: owner.id, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 506, RecordID: 1, Name: "reachable-owner-vm",
	}})
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	leader.service.guestIdentityInventoryAPIForNode = func(nodeID string, _ raft.ServerAddress) (string, error) {
		switch nodeID {
		case owner.id:
			return ownerAPI.Addr(), nil
		case unavailable.id:
			return "", fmt.Errorf("voter unavailable")
		default:
			return "", fmt.Errorf("unexpected voter %s", nodeID)
		}
	}

	err = leader.service.ReclaimGuestIdentity(
		context.Background(), 506, true, "506",
	)
	if !errors.Is(err, clusterModels.ErrGuestIdentityStillRegistered) {
		t.Fatalf("force reclaim with reachable canonical guest error=%v", err)
	}
	var retained clusterModels.GuestIdentityClaim
	if err := leader.service.DB.First(&retained, 506).Error; err != nil {
		t.Fatalf("claim was not retained: %v", err)
	}

	if err := owner.service.DB.Delete(&vmModels.VM{}, "rid = ?", 506).Error; err != nil {
		t.Fatalf("remove owner VM: %v", err)
	}
	if err := owner.service.ReleaseGuestIdentities(context.Background(), reservation); err != nil {
		t.Fatalf("release remote reclaim candidate: %v", err)
	}
}

func TestIntegrationRaftGuestIdentityAllocationWithUnavailableVoter(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	enableClusteredGuestIdentityServicesForTest(t, nodes)

	var unavailable *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			unavailable = node
			break
		}
	}
	disconnectGuestIdentityRaftNode(nodes, unavailable)
	_, err := leader.service.ReserveGuestIdentities(
		context.Background(), clusterModels.ReplicationGuestTypeVM, []uint{510},
	)
	if err != nil {
		t.Fatalf("reserve with one voter unavailable: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "quorate claim", func() bool {
		for _, node := range nodes {
			if node == unavailable {
				continue
			}
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 510).Error; err != nil {
				return false
			}
		}
		return true
	})

	connectClusterRaftTestNodes(nodes)
	waitForClusterCondition(t, 8*time.Second, "offline voter claim catch-up", func() bool {
		var claim clusterModels.GuestIdentityClaim
		return unavailable.service.DB.First(&claim, 510).Error == nil
	})
	leader = waitForClusterRaftLeader(t, nodes, 8*time.Second)
	disconnectGuestIdentityRaftNode(nodes, leader)
	result := make(chan error, 1)
	go func() {
		_, reserveErr := leader.service.ReserveGuestIdentities(
			context.Background(), clusterModels.ReplicationGuestTypeJail, []uint{511},
		)
		result <- reserveErr
	}()
	survivors := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node != leader {
			survivors = append(survivors, node)
		}
	}
	newLeader := waitForClusterRaftLeader(t, survivors, 8*time.Second)
	select {
	case reserveErr := <-result:
		if reserveErr == nil {
			t.Fatal("reservation without quorum unexpectedly succeeded")
		}
		if !strings.Contains(reserveErr.Error(), "cluster_consensus_unavailable") {
			t.Fatalf("reservation without quorum error = %v", reserveErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("reservation without quorum did not terminate")
	}
	var uncommittedCount int64
	if err := newLeader.service.DB.Model(&clusterModels.GuestIdentityClaim{}).
		Where("guest_id = ?", 511).Count(&uncommittedCount).Error; err != nil {
		t.Fatalf("count no-quorum claim: %v", err)
	}
	if uncommittedCount != 0 {
		t.Fatalf("no-quorum reservation reached the surviving quorum: count=%d", uncommittedCount)
	}
}

func TestIntegrationRaftGuestIdentityClaimSurvivesLeaderFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	reservation := guestIdentityControlReservation(
		leader.id,
		"leader-failover-reservation",
		clusterModels.ReplicationGuestTypeJail,
		520,
	)
	if _, err := leader.service.HandleGuestIdentityControl(context.Background(), leader.id, GuestIdentityControlRequest{
		Operation: guestIdentityControlReserve, Reservation: reservation,
	}); err != nil {
		t.Fatalf("reserve before leader failover: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "claim replication", func() bool {
		for _, node := range nodes {
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 520).Error; err != nil ||
				claim.Token != reservation.Token {
				return false
			}
		}
		return true
	})

	disconnectGuestIdentityRaftNode(nodes, leader)
	if err := leader.raft.Shutdown().Error(); err != nil {
		t.Fatalf("shutdown original leader: %v", err)
	}
	survivors := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node != leader {
			survivors = append(survivors, node)
		}
	}
	newLeader := waitForClusterRaftLeader(t, survivors, 8*time.Second)
	waitForClusterCondition(t, 8*time.Second, "post-failover claim", func() bool {
		for _, node := range survivors {
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 520).Error; err != nil || claim.Token != reservation.Token {
				return false
			}
		}
		return true
	})
	if _, err := newLeader.service.HandleGuestIdentityControl(context.Background(), leader.id, GuestIdentityControlRequest{
		Operation: guestIdentityControlRelease, Reservation: reservation,
	}); err != nil {
		t.Fatalf("release exact reservation after failover: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "post-failover exact release", func() bool {
		for _, node := range survivors {
			var count int64
			if err := node.service.DB.Model(&clusterModels.GuestIdentityClaim{}).
				Where("guest_id = ?", 520).Count(&count).Error; err != nil || count != 0 {
				return false
			}
		}
		return true
	})
}

func TestIntegrationRaftGuestIdentityForceRemovalRetainsClaims(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, guestIdentityRaftIntegrationModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	initializeActiveGuestIdentityRegistryForTest(t, leader, nodes)
	var target *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			target = node
			break
		}
	}
	reservation := guestIdentityControlReservation(
		target.id,
		"force-removed-owner-claim",
		clusterModels.ReplicationGuestTypeVM,
		530,
	)
	if _, err := leader.service.HandleGuestIdentityControl(context.Background(), target.id, GuestIdentityControlRequest{
		Operation: guestIdentityControlReserve, Reservation: reservation,
	}); err != nil {
		t.Fatalf("reserve target claim: %v", err)
	}

	err := leader.service.RemoveMembership(context.Background(), RemoveMembershipRequest{
		LeaveID: uuid.NewString(), NodeID: target.id,
		Inventory: BuildGuestIdentityInventoryReport(nil),
	}, target.id)
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || len(blocked.Conflict.Dependencies) != 1 ||
		blocked.Conflict.Dependencies[0].Kind != PeerRemovalDependencyGuest ||
		blocked.Conflict.Dependencies[0].ID != "530" {
		t.Fatalf("normal removal claim dependency = error=%v conflict=%+v", err, blocked)
	}

	if _, err := leader.service.ForceRemovePeer(context.Background(), ForceRemovePeerRequest{
		NodeID: target.id, TargetExternallyFenced: true,
	}); err != nil {
		t.Fatalf("force remove claimed owner: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
	var retained clusterModels.GuestIdentityClaim
	if err := leader.service.DB.First(&retained, 530).Error; err != nil {
		t.Fatalf("load retained force-removed claim: %v", err)
	}
	if retained.OwnerNodeID != target.id || retained.Token != reservation.Token {
		t.Fatalf("force removal changed claim: %+v", retained)
	}
	conflicting := guestIdentityControlReservation(
		leader.id,
		"replacement-after-force-removal",
		clusterModels.ReplicationGuestTypeJail,
		530,
	)
	if _, err := leader.service.HandleGuestIdentityControl(context.Background(), leader.id, GuestIdentityControlRequest{
		Operation: guestIdentityControlReserve, Reservation: conflicting,
	}); err == nil || !strings.Contains(err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
		t.Fatalf("force-removed ID became reusable: %v", err)
	}
}
