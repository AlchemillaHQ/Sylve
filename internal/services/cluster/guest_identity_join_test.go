// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
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
	err := acceptJoinInventoryForTest(
		t,
		leader.service,
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

	if err := acceptJoinInventoryForTest(
		t,
		leader.service,
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
	if err := acceptJoinInventoryForTest(
		t,
		leader.service,
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
		joinResult <- acceptJoinInventoryForTest(
			t,
			leader.service,
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

func TestIntegrationRaftJoinCompletesWithLegacyPeerIndexes(t *testing.T) {
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
	waitForStagedJoinerConsumption(t, leader, joiner)

	// A peer built before the marker reports only the Raft dispatch index, which
	// is what an older leader compares against as well.
	leader.service.joinProgressForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		if _, err := joiner.service.WaitForReplicatedStateAppliedIndex(ctx, minimumIndex); err != nil {
			return ClusterJoinProgress{}, err
		}
		return ClusterJoinProgress{
			NodeID:       nodeID,
			AppliedIndex: joiner.raft.AppliedIndex(),
			LastIndex:    joiner.raft.LastIndex(),
		}, nil
	}
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		digest, err := joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
		digest.FSMIndex = 0
		digest.FSMIndexKnown = false
		return digest, err
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		_ ReplicatedStateRepairRequest,
	) error {
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	if err := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("join with a legacy peer failed: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationRaftJoinNudgesBlockedStagedImageOnce(t *testing.T) {
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
	waitForStagedJoinerConsumption(t, leader, joiner)

	legacySnapshot, err := clusterModels.CaptureClusterSnapshot(joiner.service.DB)
	if err != nil {
		t.Fatalf("capture joiner snapshot: %v", err)
	}
	legacySnapshot.AppliedIndex = 0
	payload, err := json.Marshal(legacySnapshot)
	if err != nil {
		t.Fatalf("marshal joiner snapshot: %v", err)
	}
	if err := joiner.service.stateFSM.Restore(io.NopCloser(bytes.NewReader(payload))); err != nil {
		t.Fatalf("restore legacy joiner image: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFSM := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFSM()
	joiner.service.stateFSM.Register("cluster_state", func(
		*gorm.DB,
		string,
		json.RawMessage,
	) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	})

	leader.service.joinProgressForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		if _, err := joiner.service.WaitForReplicatedStateAppliedIndex(ctx, minimumIndex); err != nil {
			return ClusterJoinProgress{}, err
		}
		return ClusterJoinProgress{
			NodeID:        nodeID,
			AppliedIndex:  joiner.raft.AppliedIndex(),
			FSMIndex:      joiner.service.replicatedStateFSMIndex(),
			FSMIndexKnown: true,
			LastIndex:     joiner.raft.LastIndex(),
		}, nil
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
		_ ReplicatedStateRepairRequest,
	) error {
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	firstBefore := leader.raft.LastIndex()
	firstErr := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "catchup_pending") {
		t.Fatalf("first attempt error = %v, want catch-up pending", firstErr)
	}
	firstDelta := leader.raft.LastIndex() - firstBefore

	secondBefore := leader.raft.LastIndex()
	secondErr := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "catchup_pending") {
		t.Fatalf("second attempt error = %v, want catch-up pending", secondErr)
	}
	secondDelta := leader.raft.LastIndex() - secondBefore

	if firstDelta != 2 {
		t.Fatalf("first attempt appended %d entries, want 2", firstDelta)
	}
	if secondDelta != 1 {
		t.Fatalf("retry appended %d entries, want 1 (claims-read barrier only)", secondDelta)
	}
}

func TestIntegrationRaftJoinStatusReportsLeaderTarget(t *testing.T) {
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
	if err := joiner.service.DB.Create(&clusterModels.Cluster{
		Enabled: true, Key: guestIdentityJoinTestKey, RaftIP: joinerIP,
		RaftPort: ClusterRaftPort, JoinNodeID: string(joinerID), JoinNodeIP: joinerIP,
		JoinLeaderIP: "127.0.0.1", JoinPhase: JoinPhaseStaged,
	}).Error; err != nil {
		t.Fatalf("seed joiner intent: %v", err)
	}
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}
	waitForStagedJoinerConsumption(t, leader, joiner)

	staged, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if err != nil {
		t.Fatalf("restage join: %v", err)
	}
	leaderMarker := leader.service.replicatedStateFSMIndex()
	if leaderMarker == 0 || staged.TargetIndex != leaderMarker {
		t.Fatalf("staged target = %d, want the leader marker %d", staged.TargetIndex, leaderMarker)
	}

	joiner.service.setJoinLeaderTarget(leaderMarker)
	status, err := joiner.service.JoinStatus()
	if err != nil {
		t.Fatalf("joiner status: %v", err)
	}
	if status.AppliedIndex != joiner.service.replicatedStateFSMIndex() {
		t.Fatalf("status applied index = %d, want the FSM marker", status.AppliedIndex)
	}
	if status.TargetIndex != leaderMarker || !status.AwaitingPromotion {
		t.Fatalf("synchronized status = %+v, want the leader target and awaiting promotion", status)
	}

	joiner.service.setJoinLeaderTarget(leaderMarker + 5)
	status, err = joiner.service.JoinStatus()
	if err != nil {
		t.Fatalf("joiner status: %v", err)
	}
	if status.TargetIndex != leaderMarker+5 || status.AwaitingPromotion {
		t.Fatalf("behind status = %+v, want a target ahead and no awaiting flag", status)
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
			NodeID:        nodeID,
			AppliedIndex:  minimumIndex,
			FSMIndex:      minimumIndex,
			FSMIndexKnown: true,
			Digest:        strings.Repeat("0", 64),
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
	waitForStagedJoinerConsumption(t, leader, joiner)

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
	versionProbes := 0
	leader.service.joinVersionForNode = func(
		_ context.Context,
		server raft.Server,
		clusterKey string,
	) (string, error) {
		versionProbes++
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
	if versionProbes != 1 {
		t.Fatalf("joiner version probes = %d, want one per reconcile attempt", versionProbes)
	}
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

func TestIntegrationRaftJoinReportsLeaderPromotionDeferral(t *testing.T) {
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
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal join inventory: %v", err)
	}
	if err := joiner.service.DB.Create(&clusterModels.Cluster{
		Enabled: true, Key: guestIdentityJoinTestKey, RaftIP: joinerIP,
		RaftPort: ClusterRaftPort, JoinNodeID: string(joinerID), JoinNodeIP: joinerIP,
		JoinLeaderIP: "127.0.0.1", JoinNodeVersion: cmd.Version,
		JoinInventory: reportJSON, JoinPhase: JoinPhaseStaged,
	}).Error; err != nil {
		t.Fatalf("seed joiner intent: %v", err)
	}
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
		return "0.3.0-different-build", nil
	}
	probeCalled := false
	leader.service.joinProgressForNode = func(
		_ context.Context,
		_ string,
		_ raft.ServerAddress,
		_ uint64,
	) (ClusterJoinProgress, error) {
		probeCalled = true
		return ClusterJoinProgress{}, errors.New("catch-up probe must not run during a staged poll")
	}
	leader.service.reconcileLeaderPendingJoins(context.Background())

	reason := leader.service.JoinAdmissionLeaderState(string(joinerID)).Deferral
	if !strings.Contains(reason, "cluster_version_mismatch") {
		t.Fatalf("leader deferral = %q, want a version mismatch reason", reason)
	}
	if probeCalled {
		t.Fatal("deferred promotion reached the catch-up probe")
	}

	failureBody, err := json.Marshal(internal.APIResponse[GuestIdentityInventoryReport]{
		Status: "error", Message: "cluster_join_failed", Error: reason,
	})
	if err != nil {
		t.Fatalf("marshal deferral response: %v", err)
	}
	joiner.service.joinIntentRequest = func(
		_ context.Context,
		_ string,
		_ []byte,
		_ map[string]string,
	) (int, []byte, error) {
		return http.StatusBadRequest, failureBody, nil
	}

	result := joiner.service.pollJoinIntent(context.Background())
	if result.Err == nil || !strings.Contains(result.Err.Error(), "cluster_version_mismatch") {
		t.Fatalf("staged poll error = %v, want the leader's reason", result.Err)
	}

	waitForClusterCondition(t, 8*time.Second, "joiner to report its staged membership", func() bool {
		status, err := joiner.service.JoinStatus()
		return err == nil &&
			status.Suffrage == raftSuffrageName(raft.Nonvoter) &&
			status.AppliedIndex > 0
	})
	status, err := joiner.service.JoinStatus()
	if err != nil {
		t.Fatalf("joiner status: %v", err)
	}
	if status.Phase != JoinPhaseCatchingUp || !status.Retrying {
		t.Fatalf("joiner status = %+v, want a retrying catching-up phase", status)
	}
	if !strings.Contains(status.LastError, "cluster_version_mismatch") {
		t.Fatalf("joiner lastError = %q, want the leader's reason", status.LastError)
	}
}

func TestIntegrationRaftJoinVersionGateAppendsNothing(t *testing.T) {
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

	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: string(joinerID), GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 701, RecordID: 1, Name: "joining-jail",
	}})
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
	}
	leader.service.joinVersionForNode = func(
		context.Context,
		raft.Server,
		string,
	) (string, error) {
		return "0.3.0-different-build", nil
	}

	before := leader.raft.LastIndex()
	err := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if err == nil || !strings.Contains(err.Error(), "cluster_version_mismatch") {
		t.Fatalf("finalize error = %v, want a version mismatch", err)
	}
	if after := leader.raft.LastIndex(); after != before {
		t.Fatalf("rejected attempt appended log entries: before=%d after=%d", before, after)
	}
}

func TestIntegrationRaftJoinVerificationRetryStopsAppending(t *testing.T) {
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

	report := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: string(joinerID), GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 704, RecordID: 1, Name: "joining-jail",
	}})
	if _, err := leader.service.StageJoinInventory(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("stage join: %v", err)
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
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		_ ReplicatedStateRepairRequest,
	) error {
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	firstBefore := leader.raft.LastIndex()
	firstErr := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "replicated_state_digest_mismatch") {
		t.Fatalf("first attempt error = %v, want a digest mismatch", firstErr)
	}
	firstDelta := leader.raft.LastIndex() - firstBefore

	secondBefore := leader.raft.LastIndex()
	secondErr := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "replicated_state_digest_mismatch") {
		t.Fatalf("second attempt error = %v, want a digest mismatch", secondErr)
	}
	secondDelta := leader.raft.LastIndex() - secondBefore

	if firstDelta != 2 {
		t.Fatalf("first attempt appended %d entries, want 2", firstDelta)
	}
	if secondDelta != 1 {
		t.Fatalf("retry appended %d entries, want 1 (claims-read barrier only)", secondDelta)
	}
	claims, err := leader.service.authoritativeGuestIdentityClaims()
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if len(claims) != 1 || claims[0].GuestID != 704 {
		t.Fatalf("claims = %+v, want the single joining reservation", claims)
	}
}

func TestIntegrationRaftJoinRecoversFromTransientDigestMismatch(t *testing.T) {
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
	waitForClusterCondition(t, 8*time.Second, "joiner to consume the staged image", func() bool {
		leaderMarker := leader.service.replicatedStateFSMIndex()
		return leaderMarker > 0 && joiner.service.replicatedStateFSMIndex() == leaderMarker
	})

	attempts := 0
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		attempts++
		if attempts <= 2 {
			return ReplicatedStateDigest{
				NodeID:       nodeID,
				AppliedIndex: minimumIndex + 1,
				Digest:       strings.Repeat("0", 64),
			}, nil
		}
		return joiner.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
	}
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		_ ReplicatedStateRepairRequest,
	) error {
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	if err := leader.service.finalizeStagedJoin(
		context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
	); err != nil {
		t.Fatalf("finalize staged join: %v", err)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
	if attempts < 3 {
		t.Fatalf("digest fetches = %d, want the comparison to retry after a mismatch", attempts)
	}
}

func TestIntegrationRaftJoinRecoversLegacyStagedImage(t *testing.T) {
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
	waitForStagedJoinerConsumption(t, leader, joiner)

	legacySnapshot, err := clusterModels.CaptureClusterSnapshot(joiner.service.DB)
	if err != nil {
		t.Fatalf("capture joiner snapshot: %v", err)
	}
	legacySnapshot.AppliedIndex = 0
	payload, err := json.Marshal(legacySnapshot)
	if err != nil {
		t.Fatalf("marshal joiner snapshot: %v", err)
	}
	if err := joiner.service.stateFSM.Restore(io.NopCloser(bytes.NewReader(payload))); err != nil {
		t.Fatalf("restore legacy joiner image: %v", err)
	}
	if joiner.service.stateFSM.AppliedIndexKnown() {
		t.Fatal("legacy restore left the joiner marker known")
	}

	leader.service.joinProgressForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ClusterJoinProgress, error) {
		if _, err := joiner.service.WaitForReplicatedStateAppliedIndex(ctx, minimumIndex); err != nil {
			return ClusterJoinProgress{}, err
		}
		return ClusterJoinProgress{
			NodeID:        nodeID,
			AppliedIndex:  joiner.raft.AppliedIndex(),
			FSMIndex:      joiner.service.replicatedStateFSMIndex(),
			FSMIndexKnown: true,
			LastIndex:     joiner.raft.LastIndex(),
		}, nil
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
		_ ReplicatedStateRepairRequest,
	) error {
		return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = leader.service.finalizeStagedJoin(
			context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
		)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		t.Fatalf("legacy staged join never promoted: %v", lastErr)
	}
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationClearClusteredDataResetsReplicatedStateMarker(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(
		t,
		leader,
		leader.id,
		clusterModels.ReplicationGuestTypeVM,
		811,
	)
	if !leader.service.stateFSM.AppliedIndexKnown() || leader.service.stateFSM.AppliedIndex() == 0 {
		t.Fatalf(
			"marker = %d known=%t, want a consumed command",
			leader.service.stateFSM.AppliedIndex(),
			leader.service.stateFSM.AppliedIndexKnown(),
		)
	}

	if err := leader.service.ClearClusteredData(); err != nil {
		t.Fatalf("clear clustered data: %v", err)
	}
	if !leader.service.stateFSM.AppliedIndexKnown() || leader.service.stateFSM.AppliedIndex() != 0 {
		t.Fatalf(
			"marker after clear = %d known=%t, want known zero",
			leader.service.stateFSM.AppliedIndex(),
			leader.service.stateFSM.AppliedIndexKnown(),
		)
	}
	digest, err := leader.service.LocalReplicatedStateDigest(context.Background(), leader.id, 0)
	if err != nil {
		t.Fatalf("digest after clear: %v", err)
	}
	if !digest.FSMIndexKnown || digest.FSMIndex != 0 {
		t.Fatalf(
			"digest fsm index = %d (known=%t), want a known-empty marker for the cleared image",
			digest.FSMIndex,
			digest.FSMIndexKnown,
		)
	}
}

func TestIntegrationClearClusteredDataWaitsForFSMApply(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(
		t,
		leader,
		leader.id,
		clusterModels.ReplicationGuestTypeVM,
		812,
	)

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFSM := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFSM()
	leader.service.stateFSM.Register("test_hold_apply", func(
		*gorm.DB,
		string,
		json.RawMessage,
	) error {
		close(started)
		<-release
		return nil
	})
	future := leader.raft.Apply(
		[]byte(`{"version":1,"decidedAt":"2026-09-24T00:00:00Z","type":"test_hold_apply","action":"hold","data":{}}`),
		10*time.Second,
	)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("hold command never reached the FSM")
	}

	cleared := make(chan error, 1)
	go func() { cleared <- leader.service.ClearClusteredData() }()
	select {
	case err := <-cleared:
		t.Fatalf("clear finished while the FSM lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	var claims int64
	if err := leader.service.DB.Model(&clusterModels.GuestIdentityClaim{}).
		Count(&claims).Error; err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claims == 0 {
		t.Fatal("clear ran before the FSM lock was released")
	}

	releaseFSM()
	if err := <-cleared; err != nil {
		t.Fatalf("clear clustered data: %v", err)
	}
	if err := future.Error(); err != nil {
		t.Fatalf("hold command apply: %v", err)
	}
	if !leader.service.stateFSM.AppliedIndexKnown() || leader.service.stateFSM.AppliedIndex() != 0 {
		t.Fatalf(
			"marker after clear = %d known=%t, want known zero",
			leader.service.stateFSM.AppliedIndex(),
			leader.service.stateFSM.AppliedIndexKnown(),
		)
	}
}

func TestIntegrationRaftJoinWarnsOnUnavailableMemberVersions(t *testing.T) {
	cases := []struct {
		name   string
		probe  func() (string, error)
		expect string
	}{
		{
			name: "version mismatch",
			probe: func() (string, error) {
				return "0.3.0-different-build", nil
			},
			expect: "cluster_version_mismatch",
		},
		{
			name: "unreachable member",
			probe: func() (string, error) {
				return "", errors.New("joining_node_health_failed: dial tcp: connection refused")
			},
			expect: "cluster_version_check_unavailable",
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			models := guestIdentityJoinTestModels()
			nodes := setupClusterRaftTestNodes(t, 2, models...)
			leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
			seedGuestIdentityJoinTestCluster(t, leader)
			leader.service.AuthService = &guestIdentityInventoryAuthStub{}

			var member *clusterRaftTestNode
			for _, node := range nodes {
				if node != leader {
					member = node
					break
				}
			}
			if member == nil {
				t.Fatal("second voter not found")
			}

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
			waitForStagedJoinerConsumption(t, leader, joiner)

			leader.service.joinVersionForNode = func(
				_ context.Context,
				server raft.Server,
				_ string,
			) (string, error) {
				if strings.TrimSpace(string(server.ID)) == member.id {
					return testCase.probe()
				}
				return cmd.Version, nil
			}
			leader.service.joinProgressForNode = func(
				ctx context.Context,
				nodeID string,
				_ raft.ServerAddress,
				minimumIndex uint64,
			) (ClusterJoinProgress, error) {
				if _, err := joiner.service.WaitForReplicatedStateAppliedIndex(ctx, minimumIndex); err != nil {
					return ClusterJoinProgress{}, err
				}
				return ClusterJoinProgress{
					NodeID:        nodeID,
					AppliedIndex:  joiner.raft.AppliedIndex(),
					FSMIndex:      joiner.service.replicatedStateFSMIndex(),
					FSMIndexKnown: true,
					LastIndex:     joiner.raft.LastIndex(),
				}, nil
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
				_ ReplicatedStateRepairRequest,
			) error {
				return joiner.service.SetReplicatedStateRepairFence(nodeID, false)
			}

			if err := leader.service.finalizeStagedJoin(
				context.Background(), joinerID, joinerIP, guestIdentityJoinTestKey, report,
			); err != nil {
				t.Fatalf("join blocked by an advisory member check: %v", err)
			}
			waitForClusterRaftVoterCount(t, nodes, 3, 8*time.Second)
			reason, warned := leader.service.joinVersionWarningReason(member.id)
			if !warned || !strings.Contains(reason, testCase.expect) {
				t.Fatalf("member warning = %q (set=%t), want %s", reason, warned, testCase.expect)
			}
		})
	}
}

func TestIntegrationClearClusteredDataPairsRecordUpdateWithClear(t *testing.T) {
	models := guestIdentityJoinTestModels()
	nodes := setupClusterRaftTestNodes(t, 1, models...)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedGuestIdentityJoinTestCluster(t, leader)
	seedGuestIdentityJoinTestClaim(
		t,
		leader,
		leader.id,
		clusterModels.ReplicationGuestTypeVM,
		813,
	)

	var record clusterModels.Cluster
	if err := leader.service.DB.First(&record).Error; err != nil {
		t.Fatalf("load cluster record: %v", err)
	}
	sentinel := errors.New("paired record update failed")
	if err := leader.service.clearClusteredData(func(*gorm.DB) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("paired clear error = %v, want the prepare failure", err)
	}
	var reloaded clusterModels.Cluster
	if err := leader.service.DB.First(&reloaded).Error; err != nil {
		t.Fatalf("reload cluster record: %v", err)
	}
	if reloaded.Enabled != record.Enabled || reloaded.Key != record.Key {
		t.Fatalf("failed clear changed the record: got=%+v want=%+v", reloaded, record)
	}
	var claims int64
	if err := leader.service.DB.Model(&clusterModels.GuestIdentityClaim{}).
		Count(&claims).Error; err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claims == 0 {
		t.Fatal("failed clear removed replicated rows")
	}
	if !leader.service.stateFSM.AppliedIndexKnown() || leader.service.stateFSM.AppliedIndex() == 0 {
		t.Fatalf(
			"failed clear reset the marker: %d known=%t",
			leader.service.stateFSM.AppliedIndex(),
			leader.service.stateFSM.AppliedIndexKnown(),
		)
	}

	enabled := record
	enabled.Enabled = true
	enabled.Key = "paired-clear-key"
	if err := leader.service.clearClusteredData(func(tx *gorm.DB) error {
		return tx.Save(&enabled).Error
	}); err != nil {
		t.Fatalf("paired clear: %v", err)
	}
	if err := leader.service.DB.First(&reloaded).Error; err != nil {
		t.Fatalf("reload cluster record: %v", err)
	}
	if !reloaded.Enabled || reloaded.Key != "paired-clear-key" {
		t.Fatalf("paired record update did not commit: %+v", reloaded)
	}
	if !leader.service.stateFSM.AppliedIndexKnown() || leader.service.stateFSM.AppliedIndex() != 0 {
		t.Fatalf(
			"marker after paired clear = %d known=%t, want known zero",
			leader.service.stateFSM.AppliedIndex(),
			leader.service.stateFSM.AppliedIndexKnown(),
		)
	}
}
