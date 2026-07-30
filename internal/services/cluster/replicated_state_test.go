// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func replicatedStateTestModels() []any {
	return []any{
		&clusterModels.ClusterNote{},
		&clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupTargetProvisionOperation{},
		&clusterModels.BackupTargetNodeReadiness{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
		&clusterModels.ReplicationRunOperation{},
		&clusterModels.ScheduledRunReceipt{},
		&clusterModels.ScheduledRunResultOutbox{},
		&clusterModels.BackupTargetRestoreOperation{},
		&clusterModels.BackupJobRunnerRebind{},
		&clusterModels.BackupJobRunnerRebindItem{},
		&clusterModels.BackupEvent{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationTransitionEvent{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
	}
}

func connectReplicatedStateTestDigestHooks(
	nodes []*clusterRaftTestNode,
	leader *clusterRaftTestNode,
) {
	leader.service.stateDigestForNode = func(
		ctx context.Context,
		nodeID string,
		_ raft.ServerAddress,
		minimumIndex uint64,
	) (ReplicatedStateDigest, error) {
		for _, node := range nodes {
			if node.id == nodeID {
				return node.service.LocalReplicatedStateDigest(ctx, nodeID, minimumIndex)
			}
		}
		return ReplicatedStateDigest{}, fmt.Errorf("unknown replicated-state node %s", nodeID)
	}
}

func rebuildInMemoryReplicatedStateNode(
	node *clusterRaftTestNode,
	nodes []*clusterRaftTestNode,
) error {
	if node == nil || node.service == nil {
		return fmt.Errorf("test node unavailable")
	}
	if node.raft != nil && node.raft.State() != raft.Shutdown {
		if err := node.raft.Shutdown().Error(); err != nil {
			return fmt.Errorf("shutdown test raft: %w", err)
		}
	}
	if node.transport != nil {
		_ = node.transport.Close()
	}
	if err := node.service.DB.Transaction(clusterModels.ClearReplicatedStateTx); err != nil {
		return fmt.Errorf("clear test replicated state: %w", err)
	}

	fsm := clusterModels.NewFSMDispatcher(node.service.DB)
	clusterModels.RegisterDefaultHandlers(fsm)
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(node.id)
	config.Logger = hclog.NewNullLogger()
	config.HeartbeatTimeout = 200 * time.Millisecond
	config.ElectionTimeout = 200 * time.Millisecond
	config.LeaderLeaseTimeout = 100 * time.Millisecond
	config.CommitTimeout = 25 * time.Millisecond
	address, transport := raft.NewInmemTransport(raft.ServerAddress(node.id))
	instance, err := raft.NewRaft(
		config,
		fsm,
		raft.NewInmemStore(),
		raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(),
		transport,
	)
	if err != nil {
		_ = transport.Close()
		return fmt.Errorf("recreate test raft: %w", err)
	}
	node.addr = address
	node.transport = transport
	node.raft = instance
	node.service.Raft = instance
	node.service.Transport = nil
	node.service.raftFSM = fsm
	node.service.stateFSM = fsm

	for _, peer := range nodes {
		if peer == nil || peer == node || peer.transport == nil {
			continue
		}
		peer.transport.Connect(node.addr, node.transport)
		node.transport.Connect(peer.addr, peer.transport)
	}
	return nil
}

func seedReplicatedStateForRepair(
	t *testing.T,
	leader *clusterRaftTestNode,
) {
	t.Helper()
	notePayload, _ := json.Marshal(clusterModels.ClusterNote{
		ID: 1, Title: "authoritative", Content: "leader-state",
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "note", Action: "create", Data: notePayload,
	}); err != nil {
		t.Fatalf("replicate note: %v", err)
	}
	keyPayload, _ := json.Marshal(clusterModels.EncryptionKey{
		UUID: "repair-key", KeyData: "replicated-key-data", KeyFormat: "passphrase",
	})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "encryption_key", Action: "upsert", Data: keyPayload,
	}); err != nil {
		t.Fatalf("replicate encryption key: %v", err)
	}
}

func TestResyncClusterStateSkipsEqualVoters(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, replicatedStateTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	connectReplicatedStateTestDigestHooks(nodes, leader)
	repairCalls := 0
	leader.service.stateRepairForNode = func(
		context.Context,
		string,
		raft.ServerAddress,
		ReplicatedStateRepairRequest,
	) error {
		repairCalls++
		return nil
	}

	before := leader.raft.GetConfiguration()
	if err := before.Error(); err != nil {
		t.Fatalf("get configuration before resync: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := leader.service.ResyncClusterStateWithResult(ctx)
	if err != nil {
		t.Fatalf("resync equal voters: %v", err)
	}
	if repairCalls != 0 {
		t.Fatalf("equal voters triggered %d repair calls", repairCalls)
	}
	if len(result.Members) != 2 {
		t.Fatalf("member results=%+v", result.Members)
	}
	for _, member := range result.Members {
		if member.Status != "matched" || member.Digest != result.ReferenceDigest {
			t.Fatalf("unexpected equal member result: %+v", member)
		}
	}
	after := leader.raft.GetConfiguration()
	if err := after.Error(); err != nil {
		t.Fatalf("get configuration after resync: %v", err)
	}
	if fmt.Sprint(before.Configuration().Servers) != fmt.Sprint(after.Configuration().Servers) {
		t.Fatalf(
			"equal resync changed membership: before=%+v after=%+v",
			before.Configuration().Servers,
			after.Configuration().Servers,
		)
	}
}

func TestResyncClusterStateRebuildsDivergentFollowerAndPreservesLocalState(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3, replicatedStateTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	seedReplicatedStateForRepair(t, leader)
	waitForClusterCondition(t, 8*time.Second, "authoritative seed convergence", func() bool {
		for _, node := range nodes {
			var note clusterModels.ClusterNote
			if err := node.service.DB.First(&note, 1).Error; err != nil ||
				note.Content != "leader-state" {
				return false
			}
		}
		return true
	})

	var divergent *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			divergent = node
			break
		}
	}
	if divergent == nil {
		t.Fatal("divergent follower unavailable")
	}
	now := time.Date(2026, time.July, 30, 6, 7, 8, 0, time.UTC)
	if err := divergent.service.DB.Model(&clusterModels.ClusterNote{}).
		Where("id = ?", 1).
		Update("content", "mutated-follower-state").Error; err != nil {
		t.Fatalf("mutate follower note: %v", err)
	}
	if err := divergent.service.DB.Create(&clusterModels.ClusterNote{
		ID: 2, Title: "follower-only", Content: "stale",
	}).Error; err != nil {
		t.Fatalf("seed follower-only note: %v", err)
	}
	if err := divergent.service.DB.Where("uuid = ?", "repair-key").
		Delete(&clusterModels.EncryptionKey{}).Error; err != nil {
		t.Fatalf("delete follower key: %v", err)
	}
	if err := divergent.service.DB.Create(&clusterModels.BackupEvent{
		ID: 1, Status: "success", StartedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed local backup event: %v", err)
	}
	if err := divergent.service.DB.Create(&clusterModels.ReplicationEvent{
		ID: 1, Status: "success", StartedAt: now, Message: "local-history",
	}).Error; err != nil {
		t.Fatalf("seed local replication event: %v", err)
	}
	if err := divergent.service.DB.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: "local-outbox", Kind: clusterModels.ScheduledRunKindBackup,
		ObjectID: 1, Payload: `{}`,
	}).Error; err != nil {
		t.Fatalf("seed local outbox: %v", err)
	}

	connectReplicatedStateTestDigestHooks(nodes, leader)
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		if nodeID != divergent.id {
			return fmt.Errorf("unexpected repair target %s", nodeID)
		}
		switch request.Action {
		case ReplicatedStateRepairFence:
			return divergent.service.SetReplicatedStateRepairFence(nodeID, true)
		case ReplicatedStateRepairReset:
			return rebuildInMemoryReplicatedStateNode(divergent, nodes)
		case ReplicatedStateRepairUnfence:
			return divergent.service.SetReplicatedStateRepairFence(nodeID, false)
		default:
			return fmt.Errorf("unexpected repair action %s", request.Action)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := leader.service.ResyncClusterStateWithResult(ctx)
	if err != nil {
		leaderSnapshot, _ := clusterModels.CaptureClusterSnapshot(leader.service.DB)
		followerSnapshot, _ := clusterModels.CaptureClusterSnapshot(divergent.service.DB)
		leaderJSON, _ := json.Marshal(leaderSnapshot)
		followerJSON, _ := json.Marshal(followerSnapshot)
		t.Fatalf(
			"resync divergent follower: %v; result=%+v\nleader=%s\nfollower=%s",
			err,
			result,
			leaderJSON,
			followerJSON,
		)
	}
	var rebuilt *ClusterStateMemberResult
	for index := range result.Members {
		if result.Members[index].NodeID == divergent.id {
			rebuilt = &result.Members[index]
			break
		}
	}
	if rebuilt == nil || rebuilt.Status != "rebuilt" ||
		rebuilt.Digest != result.ReferenceDigest {
		t.Fatalf("divergent member result=%+v, complete result=%+v", rebuilt, result)
	}
	waitForClusterRaftVoterCount(t, nodes, 3, 8*time.Second)

	var repairedNote clusterModels.ClusterNote
	if err := divergent.service.DB.First(&repairedNote, 1).Error; err != nil ||
		repairedNote.Content != "leader-state" {
		t.Fatalf("repaired note=%+v err=%v", repairedNote, err)
	}
	var count int64
	if err := divergent.service.DB.Model(&clusterModels.ClusterNote{}).
		Where("id = ?", 2).
		Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("follower-only note count=%d err=%v", count, err)
	}
	if err := divergent.service.DB.Model(&clusterModels.EncryptionKey{}).
		Where("uuid = ?", "repair-key").
		Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("restored encryption key count=%d err=%v", count, err)
	}
	for _, model := range []any{
		&clusterModels.BackupEvent{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ScheduledRunResultOutbox{},
	} {
		if err := divergent.service.DB.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("local %T count=%d err=%v", model, count, err)
		}
	}
	if divergent.service.stateRepair.Load() {
		t.Fatal("successfully rebuilt follower remained repair-fenced")
	}
}

func TestResyncClusterStateLeavesFailedRepairUnpromoted(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, replicatedStateTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var divergent *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			divergent = node
			break
		}
	}
	if err := divergent.service.DB.Create(&clusterModels.ClusterNote{
		ID: 99, Title: "extra", Content: "stale",
	}).Error; err != nil {
		t.Fatalf("seed divergent row: %v", err)
	}
	connectReplicatedStateTestDigestHooks(nodes, leader)
	leader.service.stateRepairForNode = func(
		_ context.Context,
		nodeID string,
		_ raft.ServerAddress,
		request ReplicatedStateRepairRequest,
	) error {
		switch request.Action {
		case ReplicatedStateRepairFence:
			return divergent.service.SetReplicatedStateRepairFence(nodeID, true)
		case ReplicatedStateRepairReset:
			// Deliberately leave the old divergent database in place.
			return nil
		case ReplicatedStateRepairUnfence:
			return divergent.service.SetReplicatedStateRepairFence(nodeID, false)
		default:
			return errors.New("unexpected repair action")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	result, err := leader.service.ResyncClusterStateWithResult(ctx)
	if err == nil || !strings.Contains(err.Error(), "replicated_state_") {
		t.Fatalf("error=%v result=%+v, want visible repair failure", err, result)
	}

	configuration := leader.raft.GetConfiguration()
	if err := configuration.Error(); err != nil {
		t.Fatalf("get failed-repair configuration: %v", err)
	}
	for _, server := range configuration.Configuration().Servers {
		if server.ID == raft.ServerID(divergent.id) && server.Suffrage == raft.Voter {
			t.Fatalf("failed repair promoted divergent member: %+v", server)
		}
	}
	if !divergent.service.stateRepair.Load() {
		t.Fatal("failed repair did not retain target fence")
	}
}

func TestResyncClusterStateRejectsNonQuiescentFollowerBeforeRemoval(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, replicatedStateTestModels()...)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var follower *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}
	now := time.Date(2026, time.July, 30, 9, 10, 11, 0, time.UTC)
	if err := leader.service.DB.Create(&clusterModels.BackupJobOperation{
		JobID: 1, Token: "active-on-follower",
		Operation:    clusterModels.BackupJobOperationBackup,
		State:        clusterModels.BackupJobOperationRunning,
		HolderNodeID: follower.id, Revision: 1,
		AcquiredAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed active follower operation: %v", err)
	}
	connectReplicatedStateTestDigestHooks(nodes, leader)
	repairCalls := 0
	leader.service.stateRepairForNode = func(
		context.Context,
		string,
		raft.ServerAddress,
		ReplicatedStateRepairRequest,
	) error {
		repairCalls++
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := leader.service.ResyncClusterStateWithResult(ctx)
	var blocked *ReplicatedStateRepairBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error=%v result=%+v, want quiescence blocker", err, result)
	}
	if blocked.NodeID != follower.id || len(blocked.Dependencies) != 1 ||
		blocked.Dependencies[0].Kind != PeerRemovalDependencyBackupOperation {
		t.Fatalf("unexpected quiescence blocker: %+v", blocked)
	}
	if repairCalls != 0 {
		t.Fatalf("non-quiescent follower received %d repair actions", repairCalls)
	}
	configuration := leader.raft.GetConfiguration()
	if err := configuration.Error(); err != nil {
		t.Fatalf("get blocked configuration: %v", err)
	}
	for _, server := range configuration.Configuration().Servers {
		if server.ID == raft.ServerID(follower.id) && server.Suffrage != raft.Voter {
			t.Fatalf("non-quiescent follower membership changed: %+v", server)
		}
	}
}

func TestReplicatedStateRepairFenceRejectsLocalRuntimeAdmission(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	if err := leader.service.SetReplicatedStateRepairFence(leader.id, true); err != nil {
		t.Fatalf("set repair fence: %v", err)
	}
	if err := leader.service.RequireCurrentRaftVoter(leader.id); err == nil ||
		!strings.Contains(err.Error(), "local_node_repair_fenced") {
		t.Fatalf("runtime admission error=%v, want repair fence", err)
	}
	if err := leader.service.SetReplicatedStateRepairFence(leader.id, false); err != nil {
		t.Fatalf("clear repair fence: %v", err)
	}
	if err := leader.service.RequireCurrentRaftVoter(leader.id); err != nil {
		t.Fatalf("runtime admission remained fenced: %v", err)
	}
}
