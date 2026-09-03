// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/hashicorp/raft"
)

const guestIdentityJoinTestKey = "guest-identity-join-test-key"

func guestIdentityJoinTestModels() []any {
	return []any{
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.ClusterOption{},
		&clusterModels.ClusterNote{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
		&clusterModels.GuestIdentityRegistry{},
		&clusterModels.GuestIdentityEnrollment{},
		&clusterModels.GuestIdentityClaim{},
		&vmModels.VM{},
		&jailModels.Jail{},
	}
}

func seedGuestIdentityJoinTestCluster(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	node.service.NodeID = node.id
	if err := node.service.DB.Create(&clusterModels.Cluster{
		Enabled:  true,
		Key:      guestIdentityJoinTestKey,
		RaftIP:   "127.0.0.1",
		RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if err := node.service.initializeGuestIdentityRegistryForFoundingNode(node.id, BuildGuestIdentityInventoryReport(nil)); err != nil {
		t.Fatalf("seed active guest identity registry: %v", err)
	}
}

func seedGuestIdentityJoinTestClaim(
	t *testing.T,
	node *clusterRaftTestNode,
	ownerNodeID, guestKind string,
	guestID uint,
) {
	t.Helper()
	claimSet := clusterModels.GuestIdentityClaimSet{
		OwnerNodeID: ownerNodeID,
		Token:       fmt.Sprintf("join-test-%s-%d", ownerNodeID, guestID),
		Entries: []clusterModels.GuestIdentityEntry{{
			GuestKind: guestKind,
			GuestID:   guestID,
		}},
	}
	if err := node.service.applyGuestIdentityRaftAction("reserve_ids", claimSet); err != nil {
		t.Fatalf("reserve test guest identity: %v", err)
	}
}

func raftConfigurationForGuestIdentityJoinTest(t *testing.T, node *clusterRaftTestNode) raft.Configuration {
	t.Helper()
	future := node.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatalf("get Raft configuration: %v", err)
	}
	return future.Configuration()
}

func TestCanonicalSubmittedGuestIdentityInventoryRejectsDigestAndNodeMismatch(t *testing.T) {
	t.Run("digest mismatch", func(t *testing.T) {
		submitted := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "joiner", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 100, RecordID: 1, Name: "vm-100",
		}})
		submitted.Digest = strings.Repeat("0", 64)

		_, err := canonicalSubmittedGuestIdentityInventory("joiner", submitted)
		if err == nil || !strings.Contains(err.Error(), "joining_inventory_digest_mismatch") {
			t.Fatalf("error = %v, want digest mismatch", err)
		}
	})

	t.Run("node mismatch", func(t *testing.T) {
		submitted := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
			NodeID: "different-node", GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 101, RecordID: 2, Name: "jail-101",
		}})

		_, err := canonicalSubmittedGuestIdentityInventory("joiner", submitted)
		if err == nil || !strings.Contains(err.Error(), "joining_inventory_node_mismatch") {
			t.Fatalf("error = %v, want node mismatch", err)
		}
	})
}

func TestIntegrationRaftAcceptJoinRejectsConflictAfterPreflight(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	joiner := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: "joining-node", GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 301, RecordID: 1, Name: "joiner-jail",
	}})
	if _, err := leader.service.PreflightJoinInventory(
		context.Background(), "joining-node", "127.0.0.2", guestIdentityJoinTestKey, joiner,
	); err != nil {
		t.Fatalf("initial preflight: %v", err)
	}
	seedGuestIdentityJoinTestClaim(t, leader, leader.id, clusterModels.ReplicationGuestTypeVM, 301)

	before := raftConfigurationForGuestIdentityJoinTest(t, leader)
	err := leader.service.AcceptJoinInventory(
		context.Background(), "joining-node", "127.0.0.2", guestIdentityJoinTestKey, joiner,
	)
	var conflict *GuestIdentityInventoryConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want final-recheck inventory conflict", err)
	}
	after := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed final check mutated membership: before=%+v after=%+v", before, after)
	}
}

func TestValidateJoinMembershipNeverReplacesConflictingServer(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "leader", Address: "127.0.0.1:8180", Suffrage: raft.Voter},
		{ID: "existing", Address: "127.0.0.2:8180", Suffrage: raft.Voter},
	}}
	original := append([]raft.Server(nil), configuration.Servers...)

	tests := []struct {
		name     string
		nodeID   string
		address  raft.ServerAddress
		wantText string
	}{
		{"same ID different address", "existing", "127.0.0.9:8180", "joining_node_id_already_in_use"},
		{"different ID same address", "new-node", "127.0.0.2:8180", "joining_node_address_already_in_use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alreadyVoter, err := validateJoinMembership(configuration, "leader", tt.nodeID, tt.address)
			if alreadyVoter || err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("alreadyVoter=%v error=%v, want %s", alreadyVoter, err, tt.wantText)
			}
			if !reflect.DeepEqual(configuration.Servers, original) {
				t.Fatalf("membership validation mutated configuration: %+v", configuration.Servers)
			}
		})
	}
}

func TestIntegrationRaftAcceptJoinExactExistingVoterRetry(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)

	if err := leader.service.DB.Create(&vmModels.VM{RID: 500, Name: "leader-vm"}).Error; err != nil {
		t.Fatalf("seed leader VM: %v", err)
	}
	seedGuestIdentityJoinTestClaim(t, leader, leader.id, clusterModels.ReplicationGuestTypeVM, 500)
	joinerReport := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: joinerID, GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 501, RecordID: 7, Name: "joiner-jail",
	}})
	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(t, sim, joinerID, joinerReport.Entries)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}
	leader.service.guestIdentityInventoryAPIForNode = func(
		nodeID string,
		_ raft.ServerAddress,
	) (string, error) {
		if nodeID != joinerID {
			return "", errors.New("unexpected inventory node")
		}
		return sim.Addr(), nil
	}
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		if nodeID != joinerID {
			return ReplicatedStateDigest{}, errors.New("unexpected replicated-state node")
		}
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		if nodeID != joinerID || request.Action != ReplicatedStateRepairUnfence {
			return errors.New("unexpected replicated-state repair request")
		}
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	if err := leader.service.AcceptJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, joinerReport,
	); err != nil {
		t.Fatalf("accept clean join: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
	waitForClusterCondition(t, 8*time.Second, "joining claims before voter completion", func() bool {
		for _, node := range nodes {
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 501).Error; err != nil ||
				claim.GuestKind != clusterModels.ReplicationGuestTypeJail ||
				claim.OwnerNodeID != joinerID {
				return false
			}
		}
		return true
	})

	var clusterNodes []clusterModels.ClusterNode
	if err := leader.service.DB.Find(&clusterNodes).Error; err != nil {
		t.Fatalf("load populated cluster nodes: %v", err)
	}
	if len(clusterNodes) != 2 {
		t.Fatalf("expected immediate node population after join, got %d nodes", len(clusterNodes))
	}

	beforeRetry := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if err := leader.service.AcceptJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, joinerReport,
	); err != nil {
		t.Fatalf("retry exact existing voter join: %v", err)
	}
	afterRetry := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf("exact voter retry changed membership: before=%+v after=%+v", beforeRetry, afterRetry)
	}
}

func TestIntegrationRaftAcceptJoinVerifiesNonvoterBeforePromotion(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)

	digestStarted := make(chan struct{})
	releaseDigest := make(chan struct{})
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		if nodeID != joinerID {
			return ReplicatedStateDigest{}, fmt.Errorf("unexpected state node %s", nodeID)
		}
		select {
		case <-digestStarted:
		default:
			close(digestStarted)
		}
		select {
		case <-ctx.Done():
			return ReplicatedStateDigest{}, ctx.Err()
		case <-releaseDigest:
		}
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		if nodeID != joinerID || request.Action != ReplicatedStateRepairUnfence {
			return fmt.Errorf("unexpected repair request: node=%s action=%s", nodeID, request.Action)
		}
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	joinResult := make(chan error, 1)
	go func() {
		joinResult <- leader.service.AcceptJoinInventory(
			context.Background(),
			joinerID,
			joinerIP,
			guestIdentityJoinTestKey,
			BuildGuestIdentityInventoryReport(nil),
		)
	}()
	select {
	case <-digestStarted:
	case <-time.After(8 * time.Second):
		t.Fatal("join never reached replicated-state verification")
	}

	configuration := leader.raft.GetConfiguration()
	if err := configuration.Error(); err != nil {
		t.Fatalf("get staged join configuration: %v", err)
	}
	var staged *raft.Server
	for index := range configuration.Configuration().Servers {
		server := configuration.Configuration().Servers[index]
		if server.ID == raft.ServerID(joinerID) {
			copy := server
			staged = &copy
			break
		}
	}
	if staged == nil || staged.Suffrage != raft.Nonvoter {
		t.Fatalf("joining node was not staged as non-voter: %+v", staged)
	}
	close(releaseDigest)
	if err := <-joinResult; err != nil {
		t.Fatalf("complete staged join: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationRaftStageJoinIsIdempotentNonvoterAdmission(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	report := BuildGuestIdentityInventoryReport(nil)
	first, err := leader.service.StageJoinInventory(
		context.Background(),
		"joining-node",
		"127.0.0.2",
		guestIdentityJoinTestKey,
		report,
	)
	if err != nil {
		t.Fatalf("stage first join: %v", err)
	}
	if first.Phase != JoinPhaseStaged || first.Suffrage != "nonvoter" {
		t.Fatalf("first stage status = %+v", first)
	}
	configurationAfterFirst := raftConfigurationForGuestIdentityJoinTest(t, leader)

	second, err := leader.service.StageJoinInventory(
		context.Background(),
		"joining-node",
		"127.0.0.2",
		guestIdentityJoinTestKey,
		report,
	)
	if err != nil {
		t.Fatalf("stage duplicate join: %v", err)
	}
	if second.Phase != JoinPhaseStaged || second.Suffrage != "nonvoter" {
		t.Fatalf("duplicate stage status = %+v", second)
	}
	configurationAfterSecond := raftConfigurationForGuestIdentityJoinTest(t, leader)
	if !reflect.DeepEqual(configurationAfterSecond, configurationAfterFirst) {
		t.Fatalf(
			"duplicate stage changed membership: first=%+v second=%+v",
			configurationAfterFirst,
			configurationAfterSecond,
		)
	}
}

func TestIntegrationRaftJoinCatchupDoesNotHoldReplicatedStateFence(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)

	report := BuildGuestIdentityInventoryReport(nil)
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}

	progressStarted := make(chan struct{})
	releaseProgress := make(chan struct{})
	leader.service.joinProgressForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		if nodeID != joinerID {
			return ClusterJoinProgress{}, fmt.Errorf("unexpected progress node %s", nodeID)
		}
		select {
		case <-progressStarted:
		default:
			close(progressStarted)
		}
		select {
		case <-ctx.Done():
			return ClusterJoinProgress{}, ctx.Err()
		case <-releaseProgress:
		}
		return ClusterJoinProgress{NodeID: nodeID, AppliedIndex: minimumIndex}, nil
	}
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		if request.Action != ReplicatedStateRepairUnfence {
			return fmt.Errorf("unexpected repair action %s", request.Action)
		}
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	joinResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		joinResult <- leader.service.finalizeStagedJoin(
			ctx, joinerID, joinerIP, guestIdentityJoinTestKey, report,
		)
	}()
	select {
	case <-progressStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("join never entered catch-up progress probe")
	}

	fenceAcquired := make(chan struct{})
	go func() {
		leader.service.replicatedStateMu.Lock()
		leader.service.replicatedStateMu.Unlock()
		close(fenceAcquired)
	}()
	select {
	case <-fenceAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("replicated-state write fence was held during WAN catch-up")
	}
	mutationLockAcquired := make(chan struct{})
	go func() {
		leader.service.clusterJoinMu.Lock()
		leader.service.clusterJoinMu.Unlock()
		close(mutationLockAcquired)
	}()
	select {
	case <-mutationLockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cluster mutation lock was held during WAN catch-up")
	}
	close(releaseProgress)
	if err := <-joinResult; err != nil {
		t.Fatalf("finalize staged join: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationRaftJoinDigestMismatchNeverPromotes(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)
	report := BuildGuestIdentityInventoryReport(nil)
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}
	leader.service.joinProgressForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		return ClusterJoinProgress{NodeID: nodeID, AppliedIndex: minimumIndex}, nil
	}
	leader.service.stateDigestForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		return ReplicatedStateDigest{
			NodeID: nodeID, AppliedIndex: minimumIndex, Digest: strings.Repeat("0", 64),
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := leader.service.finalizeStagedJoin(
		ctx, joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if err == nil || !strings.Contains(err.Error(), "replicated_state_digest_mismatch") {
		t.Fatalf("finalize error = %v, want digest mismatch", err)
	}
	configuration := raftConfigurationForGuestIdentityJoinTest(t, leader)
	server, resolveErr := resolveJoinMembership(
		configuration,
		leader.id,
		joinerID,
		raft.ServerAddress(RaftServerAddress(joinerIP)),
	)
	if resolveErr != nil {
		t.Fatalf("resolve staged member: %v", resolveErr)
	}
	if server == nil || server.Suffrage != raft.Nonvoter {
		t.Fatalf("digest mismatch promoted or removed member: %+v", server)
	}
}

func TestIntegrationRaftLeaderReconcilesNonvoterFromRaftConfiguration(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)
	report := BuildGuestIdentityInventoryReport(nil)
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}

	sim := newClusterPeerSimulator()
	defer sim.Close()
	registerGuestIdentityInventoryPeer(t, sim, joinerID, report.Entries)
	leader.service.guestIdentityInventoryAPIForNode = func(
		nodeID string,
		_ raft.ServerAddress,
	) (string, error) {
		if nodeID != joinerID {
			return "", fmt.Errorf("unexpected inventory node %s", nodeID)
		}
		return sim.Addr(), nil
	}
	leader.service.joinVersionForNode = func(
		_ context.Context,
		server raft.Server,
		clusterKey string,
	) (string, error) {
		if server.ID != raft.ServerID(joinerID) || clusterKey != guestIdentityJoinTestKey {
			return "", fmt.Errorf("unexpected version probe: server=%s key=%s", server.ID, clusterKey)
		}
		return cmd.Version, nil
	}
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		if request.Action != ReplicatedStateRepairUnfence {
			return fmt.Errorf("unexpected repair action %s", request.Action)
		}
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	leader.service.reconcileLeaderPendingJoins(ctx)
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationRaftJoinRetriesAfterTransientProgressFailure(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	leader.service.AuthService = &guestIdentityInventoryAuthStub{}

	joinerIP := "127.0.0.2"
	joinerID := RaftServerAddress(joinerIP)
	joiner := newClusterRaftTestNode(t, joinerID, models...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)
	report := BuildGuestIdentityInventoryReport(nil)
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}

	progressAttempts := 0
	leader.service.joinProgressForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		progressAttempts++
		if progressAttempts == 1 {
			return ClusterJoinProgress{}, errors.New("wireguard path unavailable")
		}
		return ClusterJoinProgress{NodeID: nodeID, AppliedIndex: minimumIndex}, nil
	}
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}

	if err := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err == nil || !strings.Contains(err.Error(), "wireguard path unavailable") {
		t.Fatalf("first finalize error = %v, want transient path failure", err)
	}
	configuration := raftConfigurationForGuestIdentityJoinTest(t, leader)
	server, err := resolveJoinMembership(
		configuration,
		leader.id,
		joinerID,
		raft.ServerAddress(RaftServerAddress(joinerIP)),
	)
	if err != nil || server == nil || server.Suffrage != raft.Nonvoter {
		t.Fatalf("transient failure changed staged membership: server=%+v err=%v", server, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := leader.service.finalizeStagedJoin(
		ctx, joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("retry finalize: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationRaftLeaderReconcilerBlocksVersionMismatch(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, guestIdentityJoinTestModels()...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	report := BuildGuestIdentityInventoryReport(nil)
	joinerID := "joining-node"
	joinerIP := "127.0.0.2"
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}
	leader.service.joinVersionForNode = func(
		_ context.Context,
		_ raft.Server,
		_ string,
	) (string, error) {
		return "incompatible-version", nil
	}
	progressCalled := false
	leader.service.joinProgressForNode = func(
		_ context.Context,
		_ string,
		_ raft.ServerAddress,
		_ uint64,
	) (ClusterJoinProgress, error) {
		progressCalled = true
		return ClusterJoinProgress{}, nil
	}

	leader.service.reconcileLeaderPendingJoins(context.Background())
	configuration := raftConfigurationForGuestIdentityJoinTest(t, leader)
	server, err := resolveJoinMembership(
		configuration,
		leader.id,
		joinerID,
		raft.ServerAddress(RaftServerAddress(joinerIP)),
	)
	if err != nil || server == nil || server.Suffrage != raft.Nonvoter {
		t.Fatalf("version mismatch changed membership: server=%+v err=%v", server, err)
	}
	if progressCalled {
		t.Fatal("version mismatch reached catch-up verification")
	}
}
