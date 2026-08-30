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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/google/uuid"
)

func TestInitializeLeaveRuntimeLoadsGateState(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		wantFence bool
	}{
		{name: "normal", phase: "", wantFence: false},
		{name: "fenced", phase: LeavePhaseFenced, wantFence: true},
		{name: "removing", phase: LeavePhaseRemoving, wantFence: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
			if err := db.Create(&clusterModels.Cluster{Enabled: true, LeaveID: uuid.NewString(), LeavePhase: test.phase}).Error; err != nil {
				t.Fatal(err)
			}
			service := &Service{DB: db, mutationGate: NewMutationGate()}
			if err := service.InitializeLeaveRuntime(); err != nil {
				t.Fatal(err)
			}
			_, release, err := service.EnterMutation(context.Background())
			if test.wantFence {
				if !errors.Is(err, ErrNodeLeaveFenced) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}

func TestInitializeLeaveRuntimeFailureKeepsGateClosed(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	service := &Service{DB: db, mutationGate: NewMutationGate()}
	if err := service.InitializeLeaveRuntime(); err == nil {
		t.Fatal("expected leave-state load failure")
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("gate error=%v", err)
	}
}

func TestPrepareLocalLeavePreflightFailureReopensGate(t *testing.T) {
	db := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   10,
		Action:    "start",
		Status:    taskModels.LifecycleTaskStatusQueued,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)}
	if _, err := service.prepareLocalLeave(context.Background(), uuid.NewString(), "", false, false); err == nil {
		t.Fatal("expected dependency conflict")
	}
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatalf("gate remained fenced: %v", err)
	}
	release()
}

func TestConcurrentPrepareLocalLeaveKeepsDurableFenceClosed(t *testing.T) {
	db := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)}
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	for _, leaveID := range []string{uuid.NewString(), uuid.NewString()} {
		leaveID := leaveID
		go func() {
			_, err := service.prepareLocalLeave(context.Background(), leaveID, "", false, true)
			results <- err
		}()
	}

	deadline := time.Now().Add(time.Second)
	for !service.IsMutationFenced() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !service.IsMutationFenced() {
		t.Fatal("leave preparation did not start draining")
	}
	release()

	successes := 0
	conflicts := 0
	for range 2 {
		select {
		case result := <-results:
			if result == nil {
				successes++
			} else if strings.Contains(result.Error(), "cluster_leave_already_in_progress") {
				conflicts++
			} else {
				t.Fatalf("unexpected leave result: %v", result)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for leave preparation")
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	status, err := service.LeaveStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != LeavePhaseFenced || status.LeaveID == "" {
		t.Fatalf("leave status=%+v", status)
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("durable leave fence reopened: %v", err)
	}
}

func TestPrepareLocalLeaveReportsActiveMutationsAndReopens(t *testing.T) {
	if leaveDrainTimeout >= leaveRequestTimeout {
		t.Fatalf("drain timeout %s must be shorter than request timeout %s", leaveDrainTimeout, leaveRequestTimeout)
	}
	db := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	if err := db.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)}
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = service.prepareLocalLeave(drainCtx, uuid.NewString(), "", false, true)
	var leaveErr *ClusterLeaveError
	if !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_active_mutations" {
		t.Fatalf("leave error=%v", err)
	}
	status, statusErr := service.LeaveStatus()
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Phase != "" || status.LeaveID != "" {
		t.Fatalf("leave intent persisted after drain failure: %+v", status)
	}
	release()
	_, secondRelease, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatalf("gate did not reopen: %v", err)
	}
	secondRelease()
}

func TestPrepareLocalLeaveRejectsDeclusteredNode(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)}
	if _, err := service.prepareLocalLeave(context.Background(), uuid.NewString(), "", false, false); err == nil || !strings.Contains(err.Error(), "cluster_not_enabled") {
		t.Fatalf("prepare error=%v", err)
	}
	status, err := service.LeaveStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "" || status.LeaveID != "" {
		t.Fatalf("leave intent persisted on declustered node: %+v", status)
	}
}

func configureLeaveWorkflowTestTransport(
	t *testing.T,
	nodes []*clusterRaftTestNode,
	target *clusterRaftTestNode,
	loseRemovalResponse bool,
) {
	t.Helper()
	target.service.leaveMembershipForNode = func(
		_ context.Context,
		_ clusterModels.Cluster,
		nodeID string,
	) (MembershipStatus, error) {
		leader := findClusterRaftLeader(nodes)
		if leader == nil {
			return MembershipStatus{NodeID: nodeID}, errors.New("leader unavailable")
		}
		return leader.service.AuthoritativeMembershipStatus(nodeID)
	}
	target.service.leaveRemovalForNode = func(
		ctx context.Context,
		_ string,
		request RemoveMembershipRequest,
	) error {
		leader := findClusterRaftLeader(nodes)
		if leader == nil {
			return errors.New("leader unavailable")
		}
		if err := leader.service.RemoveMembership(ctx, request, request.NodeID); err != nil {
			return err
		}
		if loseRemovalResponse {
			return errors.New("response lost after membership commit")
		}
		return nil
	}
}

func assertLeaveWorkflowCompleted(t *testing.T, target *clusterRaftTestNode, result ClusterLeaveResult) {
	t.Helper()
	if !result.MembershipRemoved || !result.CleanupAcknowledged {
		t.Fatalf("leave result=%+v", result)
	}
	status, err := target.service.LeaveStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Phase != "" || status.LeaveID != "" {
		t.Fatalf("target leave status=%+v", status)
	}
	if _, _, err := target.service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("completed target admitted work before restart: %v", err)
	}
}

func TestIntegrationCooperativeFollowerLeaveCompletes(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	for _, test := range []struct {
		name      string
		nodeCount int
	}{
		{name: "two voters", nodeCount: 2},
		{name: "three voters", nodeCount: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes := setupClusterRaftTestNodes(
				t,
				test.nodeCount,
				&clusterModels.Cluster{},
				&clusterModels.ClusterNode{},
				&vmModels.VM{},
				&jailModels.Jail{},
				&taskModels.GuestLifecycleTask{},
			)
			for _, node := range nodes {
				if err := node.service.DB.Create(&clusterModels.Cluster{
					Enabled: true, Key: "cluster-key", RaftIP: node.id, RaftPort: ClusterRaftPort,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
			var target *clusterRaftTestNode
			for _, node := range nodes {
				if node.id != leader.id {
					target = node
					break
				}
			}
			if target == nil {
				t.Fatal("follower not found")
			}
			configureLeaveWorkflowTestTransport(t, nodes, target, false)
			result, err := target.service.StartCooperativeLeave(context.Background(), StartLeaveRequest{
				LeaveID:        uuid.NewString(),
				ExpectedNodeID: target.id,
				LeaderIP:       raftAddressHost(string(leader.addr)),
			}, leader.id)
			if err != nil {
				t.Fatalf("cooperative leave: %v", err)
			}
			assertLeaveWorkflowCompleted(t, target, result)
			waitForClusterRaftVoterCount(t, nodes, test.nodeCount-1, 8*time.Second)
		})
	}
}

func TestIntegrationActiveLeavingLeaderTransfersAndConfirmsLostResponse(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{
			Enabled: true, Key: "cluster-key", RaftIP: node.id, RaftPort: ClusterRaftPort,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	configureLeaveWorkflowTestTransport(t, nodes, target, true)
	peerAddresses, err := target.service.captureLeavePeerAddresses()
	if err != nil {
		t.Fatal(err)
	}
	if err := target.service.persistLeaveIntent(
		uuid.NewString(),
		raftAddressHost(string(target.addr)),
		LeavePhaseRemoving,
		peerAddresses,
	); err != nil {
		t.Fatal(err)
	}
	target.service.mutationGate.Close()
	result, err := target.service.advanceLocalLeave(context.Background(), false)
	if err != nil {
		t.Fatalf("active leader leave: %v", err)
	}
	assertLeaveWorkflowCompleted(t, target, result)
	waitForClusterRaftVoterCount(t, nodes, 2, 8*time.Second)
}

func TestIntegrationLeaderLeaveTriesAnotherTransferCandidate(t *testing.T) {
	nodes := setupClusterRaftTestNodes(
		t,
		3,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{
			Enabled: true, Key: "cluster-key", RaftIP: node.id, RaftPort: ClusterRaftPort,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	candidates := make([]*clusterRaftTestNode, 0, 2)
	for _, node := range nodes {
		if node.id != leader.id {
			candidates = append(candidates, node)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	unavailable := candidates[0]
	expectedLeader := candidates[1]
	unavailable.transport.DisconnectAll()
	for _, node := range nodes {
		node.transport.Disconnect(unavailable.addr)
	}

	future := leader.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
	leaderIP, err := leader.service.transferLeadershipForLeave(context.Background(), future.Configuration(), leader.id)
	if err != nil {
		t.Fatalf("transfer leadership: %v", err)
	}
	if leaderIP != raftAddressHost(string(expectedLeader.addr)) {
		t.Fatalf("leader IP = %q, want %q", leaderIP, raftAddressHost(string(expectedLeader.addr)))
	}
	if elected := waitForClusterRaftLeader(t, []*clusterRaftTestNode{leader, expectedLeader}, 8*time.Second); elected.id != expectedLeader.id {
		t.Fatalf("leader = %s, want %s", elected.id, expectedLeader.id)
	}
}

func TestIntegrationActiveLeavingLeaderWithoutSuccessorStaysFenced(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &clusterModels.Cluster{})
	target := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := target.service.DB.Create(&clusterModels.Cluster{
		Enabled: true, Key: "cluster-key", RaftIP: target.id, RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.service.persistLeaveIntent(
		uuid.NewString(),
		raftAddressHost(string(target.addr)),
		LeavePhaseRemoving,
		[]byte(`[]`),
	); err != nil {
		t.Fatal(err)
	}
	target.service.mutationGate.Close()
	_, err := target.service.advanceLocalLeave(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "cluster_leave_leadership_transfer_unavailable") {
		t.Fatalf("leave error=%v", err)
	}
	status, statusErr := target.service.LeaveStatus()
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Phase != LeavePhaseRemoving || !status.Enabled {
		t.Fatalf("leave status=%+v", status)
	}
	if _, _, gateErr := target.service.EnterMutation(context.Background()); !errors.Is(gateErr, ErrNodeLeaveFenced) {
		t.Fatalf("leave gate reopened without a successor: %v", gateErr)
	}
}

func TestLeaveClusterSupersedesDisabledJoinIntent(t *testing.T) {
	db := newClusterServiceTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
	)
	if err := db.Create(&clusterModels.Cluster{
		Enabled:      false,
		Key:          "cluster-key",
		JoinLeaderIP: "127.0.0.1",
		JoinNodeID:   "node-1",
		JoinNodeIP:   "192.0.2.20",
		JoinPhase:    JoinPhaseIntentSaved,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{
		DB:           db,
		NodeID:       "node-1",
		AuthService:  authService.NewAuthService(db),
		mutationGate: newOpenTestMutationGate(t),
	}

	_, err := service.LeaveCluster(context.Background())
	var leaveErr *ClusterLeaveError
	if !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_membership_unconfirmed" {
		t.Fatalf("leave error=%v", err)
	}
	status, statusErr := service.LeaveStatus()
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Phase != LeavePhaseRemoving || status.LeaderIP != "127.0.0.1" || status.LeaveID == "" {
		t.Fatalf("leave status=%+v", status)
	}
	if _, _, gateErr := service.EnterMutation(context.Background()); !errors.Is(gateErr, ErrNodeLeaveFenced) {
		t.Fatalf("join reset did not remain fenced: %v", gateErr)
	}
}

func TestFinalizeLocalDeclusterIsIdempotent(t *testing.T) {
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterNode{})
	bootstrap := true
	record := clusterModels.Cluster{
		Enabled: true, Key: "secret", RaftBootstrap: &bootstrap, RaftIP: "192.0.2.1", RaftPort: ClusterRaftPort,
		JoinLeaderIP: "192.0.2.2", JoinNodeID: "node-1", JoinNodeIP: "192.0.2.1", JoinNodeVersion: "version",
		JoinPhase: "voter", JoinLastError: "error", JoinAttempts: 2,
		LeaveID: uuid.NewString(), LeavePhase: LeavePhaseCleaning, LeaveLeaderIP: "192.0.2.2",
		LeavePeerAddrs: []byte(`["192.0.2.2:8180"]`), LeaveLastError: "error", LeaveAttempts: 3,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clusterModels.ClusterNode{NodeUUID: "node-2"}).Error; err != nil {
		t.Fatal(err)
	}
	raftDir := filepath.Join(dataPath, "raft")
	sshDir := filepath.Join(dataPath, clusterSSHDirName)
	if err := os.MkdirAll(raftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(raftDir, "raft-log.db"),
		filepath.Join(sshDir, clusterSSHPrivateFileName),
		filepath.Join(sshDir, clusterSSHPublicFileName),
	} {
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{DB: db, NodeID: "node-1", mutationGate: NewMutationGate()}
	if err := service.FinalizeLocalDecluster(); err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeLocalDecluster(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	var current clusterModels.Cluster
	if err := db.First(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.Enabled || current.Key != "" || current.RaftBootstrap != nil || current.RaftIP != "" ||
		current.JoinPhase != "" || current.JoinAttempts != 0 || current.LeavePhase != "" ||
		current.LeaveID != "" || current.LeaveAttempts != 0 {
		t.Fatalf("cluster state not cleared: %+v", current)
	}
	for _, path := range []string{
		filepath.Join(raftDir, "raft-log.db"),
		filepath.Join(sshDir, clusterSSHPrivateFileName),
		filepath.Join(sshDir, clusterSSHPublicFileName),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path still exists: %s err=%v", path, err)
		}
	}
	if _, _, err := service.EnterMutation(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("cleanup reopened gate: %v", err)
	}
}
