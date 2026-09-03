// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func newGuestIdentitySnapshotTestNode(
	t *testing.T,
	nodeID string,
) (*clusterRaftTestNode, *raft.InmemStore) {
	t.Helper()
	models := append(
		replicatedStateTestModels(),
		guestIdentityRaftIntegrationModels()...,
	)
	database := newClusterServiceTestDB(t, models...)
	fsm := clusterModels.NewFSMDispatcher(database)
	clusterModels.RegisterDefaultHandlers(fsm)

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)
	config.Logger = hclog.NewNullLogger()
	config.HeartbeatTimeout = 200 * time.Millisecond
	config.ElectionTimeout = 200 * time.Millisecond
	config.LeaderLeaseTimeout = 100 * time.Millisecond
	config.CommitTimeout = 25 * time.Millisecond
	config.TrailingLogs = 0

	address, transport := raft.NewInmemTransport(raft.ServerAddress(nodeID))
	logStore := raft.NewInmemStore()
	raftNode, err := raft.NewRaft(
		config,
		fsm,
		logStore,
		raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(),
		transport,
	)
	if err != nil {
		t.Fatalf("create snapshot test Raft node %s: %v", nodeID, err)
	}
	node := &clusterRaftTestNode{
		id:        nodeID,
		addr:      address,
		transport: transport,
		raft:      raftNode,
		service: &Service{
			DB:           database,
			Raft:         raftNode,
			NodeID:       nodeID,
			raftFSM:      fsm,
			stateFSM:     fsm,
			mutationGate: newOpenTestMutationGate(t),
		},
	}
	t.Cleanup(func() { cleanupClusterRaftTestNodes(t, []*clusterRaftTestNode{node}) })
	return node, logStore
}

func TestIntegrationRaftGuestIdentitySnapshotAndJoinConverge(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real in-memory Raft; covered by the native integration lane")
	}
	leader, leaderLogs := newGuestIdentitySnapshotTestNode(t, "snapshot-leader")
	if err := leader.raft.BootstrapCluster(raft.Configuration{Servers: []raft.Server{{
		ID: raft.ServerID(leader.id), Address: leader.addr, Suffrage: raft.Voter,
	}}}).Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
		t.Fatalf("bootstrap snapshot leader: %v", err)
	}
	waitForClusterRaftLeader(t, []*clusterRaftTestNode{leader}, 8*time.Second)
	if err := leader.service.initializeGuestIdentityRegistryForFoundingNode(
		leader.id,
		BuildGuestIdentityInventoryReport(nil),
	); err != nil {
		t.Fatalf("initialize registry before snapshot: %v", err)
	}
	seed := guestIdentityControlReservation(
		leader.id,
		"snapshot-existing-claim",
		clusterModels.ReplicationGuestTypeVM,
		540,
	)
	if _, err := leader.service.HandleGuestIdentityControl(context.Background(), leader.id, GuestIdentityControlRequest{
		Operation: guestIdentityControlReserve, Reservation: seed,
	}); err != nil {
		t.Fatalf("reserve seed snapshot claim: %v", err)
	}

	snapshotFuture := leader.raft.Snapshot()
	if err := snapshotFuture.Error(); err != nil {
		t.Fatalf("take registry snapshot: %v", err)
	}
	metadata, reader, err := snapshotFuture.Open()
	if err != nil {
		t.Fatalf("open registry snapshot: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close registry snapshot: %v", err)
	}
	if metadata.Index == 0 {
		t.Fatal("registry snapshot has zero index")
	}
	if firstIndex, err := leaderLogs.FirstIndex(); err != nil {
		t.Fatalf("read compacted leader log: %v", err)
	} else if firstIndex != 0 && firstIndex <= metadata.Index {
		t.Fatalf("snapshot did not compact covered logs: first=%d snapshot=%d", firstIndex, metadata.Index)
	}

	joiner, _ := newGuestIdentitySnapshotTestNode(t, "snapshot-joiner")
	connectClusterRaftTestNodes([]*clusterRaftTestNode{leader, joiner})
	if err := leader.raft.AddNonvoter(
		raft.ServerID(joiner.id), joiner.addr, 0, 5*time.Second,
	).Error(); err != nil {
		t.Fatalf("stage snapshot joiner: %v", err)
	}
	waitForClusterCondition(t, 8*time.Second, "snapshot registry installed on staged joiner", func() bool {
		var registry clusterModels.GuestIdentityRegistry
		var claim clusterModels.GuestIdentityClaim
		return joiner.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error == nil &&
			registry.Phase == clusterModels.GuestIdentityRegistryPhaseActive &&
			joiner.service.DB.First(&claim, 540).Error == nil &&
			claim.Token == seed.Token
	})

	joiningReport := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: joiner.id, GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID: 541, RecordID: 1, Name: "joining-jail",
	}})
	if err := leader.service.admitStagedJoinGuestIdentities(
		context.Background(), joiner.id, joiningReport,
	); err != nil {
		t.Fatalf("admit staged joiner claims: %v", err)
	}
	targetIndex := leader.raft.AppliedIndex()
	waitForClusterCondition(t, 8*time.Second, "joining claim caught up before promotion", func() bool {
		if joiner.raft.AppliedIndex() < targetIndex {
			return false
		}
		var claim clusterModels.GuestIdentityClaim
		return joiner.service.DB.First(&claim, 541).Error == nil &&
			claim.OwnerNodeID == joiner.id &&
			claim.GuestKind == clusterModels.ReplicationGuestTypeJail
	})
	configuration := leader.raft.GetConfiguration()
	if err := configuration.Error(); err != nil {
		t.Fatalf("read staged configuration: %v", err)
	}
	for _, server := range configuration.Configuration().Servers {
		if server.ID == raft.ServerID(joiner.id) && server.Suffrage != raft.Nonvoter {
			t.Fatalf("joiner promoted before claim convergence: suffrage=%s", server.Suffrage)
		}
	}

	if err := leader.raft.AddVoter(
		raft.ServerID(joiner.id), joiner.addr, 0, 5*time.Second,
	).Error(); err != nil {
		t.Fatalf("promote converged joiner: %v", err)
	}
	waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{leader, joiner}, 2, 8*time.Second)
	for _, node := range []*clusterRaftTestNode{leader, joiner} {
		var registry clusterModels.GuestIdentityRegistry
		var existing, joining clusterModels.GuestIdentityClaim
		if err := node.service.DB.First(&registry, clusterModels.GuestIdentityRegistryID).Error; err != nil ||
			registry.Phase != clusterModels.GuestIdentityRegistryPhaseActive {
			t.Fatalf("%s registry after promotion = %+v, error=%v", node.id, registry, err)
		}
		if err := node.service.DB.First(&existing, 540).Error; err != nil {
			t.Fatalf("%s existing snapshot claim: %v", node.id, err)
		}
		if err := node.service.DB.First(&joining, 541).Error; err != nil {
			t.Fatalf("%s joining claim: %v", node.id, err)
		}
	}
}
