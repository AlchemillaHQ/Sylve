// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
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
	database := newClusterServiceTestDB(t, replicatedStateTestModels()...)
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
		service: withRaft(&Service{
			DB:           database,
			NodeID:       nodeID,
			fsm:          fsm,
			mutationGate: newOpenTestMutationGate(t),
		}, raftNode),
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
	seedGuestIdentityJoinTestClaim(t, leader, leader.id, clusterModels.ReplicationGuestTypeVM, 540)

	snapshotFuture := leader.raft.Snapshot()
	if err := snapshotFuture.Error(); err != nil {
		t.Fatalf("take claims snapshot: %v", err)
	}
	metadata, reader, err := snapshotFuture.Open()
	if err != nil {
		t.Fatalf("open claims snapshot: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close claims snapshot: %v", err)
	}
	if metadata.Index == 0 {
		t.Fatal("claims snapshot has zero index")
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
	waitForClusterCondition(t, 8*time.Second, "snapshot claim installed on staged joiner", func() bool {
		var claim clusterModels.GuestIdentityClaim
		return joiner.service.DB.First(&claim, 540).Error == nil && claim.OwnerNodeID == leader.id
	})

	seedGuestIdentityJoinTestClaim(t, leader, joiner.id, clusterModels.ReplicationGuestTypeJail, 541)
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

	if err := leader.raft.AddVoter(
		raft.ServerID(joiner.id), joiner.addr, 0, 5*time.Second,
	).Error(); err != nil {
		t.Fatalf("promote converged joiner: %v", err)
	}
	waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{leader, joiner}, 2, 8*time.Second)
	for _, node := range []*clusterRaftTestNode{leader, joiner} {
		claims, err := node.service.ListGuestIdentityClaims(t.Context())
		if err != nil || len(claims) != 2 || claims[0].GuestID != 540 || claims[1].GuestID != 541 {
			t.Fatalf("%s claims after promotion = %+v, error=%v", node.id, claims, err)
		}
	}
}
