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
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/hashicorp/raft"
)

func newLeaveTestService(t *testing.T, record clusterModels.Cluster) *Service {
	t.Helper()
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterNode{}, &vmModels.VM{}, &jailModels.Jail{}, &taskModels.GuestLifecycleTask{})
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return withFSM(&Service{DB: db, NodeID: "node-1", mutationGate: newOpenTestMutationGate(t)})
}

func requireLeavePhase(t *testing.T, service *Service, phase string) {
	t.Helper()
	status, err := service.LeaveStatus()
	if err != nil || status.Phase != phase {
		t.Fatalf("leave status = %+v, %v; want phase %q", status, err, phase)
	}
}

func requireAdmitted(t *testing.T, service *Service, admitted bool) {
	t.Helper()
	_, release, err := service.EnterMutation(context.Background())
	if err == nil {
		release()
	}
	if admitted != (err == nil) {
		t.Fatalf("mutation admission error = %v, want admitted=%v", err, admitted)
	}
}

func TestInitializeLeaveRuntimeLoadsGateState(t *testing.T) {
	for _, test := range []struct {
		phase    string
		admitted bool
	}{{"", true}, {LeavePhaseRemoving, false}} {
		service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true, LeavePhase: test.phase})
		service.mutationGate = NewMutationGate()
		if err := service.InitializeLeaveRuntime(); err != nil {
			t.Fatal(err)
		}
		requireAdmitted(t, service, test.admitted)
	}
}

func TestInitializeLeaveRuntimeFailureKeepsGateClosed(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	service := withFSM(&Service{DB: db, mutationGate: NewMutationGate()})
	if err := service.InitializeLeaveRuntime(); err == nil {
		t.Fatal("expected leave-state load failure")
	}
	requireAdmitted(t, service, false)
}

func TestLeaveBlockedByLocalWorkReopensTheNode(t *testing.T) {
	service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true})
	if err := service.DB.Create(&taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM, GuestID: 10, Action: "start", Status: taskModels.LifecycleTaskStatusQueued,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service.guestIdentityLocalReservations = map[uint]localGuestIdentityReservation{11: {token: "in-flight", kind: clusterModels.ReplicationGuestTypeVM}}

	err := service.Leave(context.Background(), true)
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || len(blocked.Conflict.Dependencies) != 2 ||
		blocked.Conflict.Dependencies[0].Kind != PeerRemovalDependencyGuestOperation ||
		blocked.Conflict.Dependencies[1].Kind != PeerRemovalDependencyLifecycleTask {
		t.Fatalf("leave error = %v, conflict = %+v", err, blocked)
	}
	requireLeavePhase(t, service, "")
	requireAdmitted(t, service, true)
}

func TestLeaveListsGuestsUntilConfirmed(t *testing.T) {
	service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true})
	if err := service.DB.Create(&vmModels.VM{RID: 100, Name: "kept-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	err := service.Leave(context.Background(), false)
	var blocked *PeerRemovalBlockedError
	if !errors.As(err, &blocked) || len(blocked.Conflict.Dependencies) != 1 ||
		blocked.Conflict.Dependencies[0].Kind != PeerRemovalDependencyGuest || blocked.Conflict.Dependencies[0].Name != "kept-vm" {
		t.Fatalf("leave error = %v, conflict = %+v", err, blocked)
	}
	requireLeavePhase(t, service, "")

	err = service.Leave(context.Background(), true)
	var leaveErr *ClusterLeaveError
	if !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_membership_unconfirmed" {
		t.Fatalf("confirmed leave error = %v", err)
	}
	requireLeavePhase(t, service, LeavePhaseRemoving)
	requireAdmitted(t, service, false)
}

func TestLeaveReportsActiveMutationsAndReopens(t *testing.T) {
	service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true})
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = service.Leave(ctx, true)
	var leaveErr *ClusterLeaveError
	if !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_active_mutations" {
		t.Fatalf("leave error = %v", err)
	}
	requireLeavePhase(t, service, "")
	release()
	requireAdmitted(t, service, true)
}

func TestConcurrentLeavesKeepTheNodeFenced(t *testing.T) {
	service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true})
	_, release, err := service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- service.Leave(context.Background(), true) }()
	}
	waitForClusterCondition(t, time.Second, "leave to start draining", service.IsMutationFenced)
	release()
	for range 2 {
		var leaveErr *ClusterLeaveError
		if err := <-results; !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_membership_unconfirmed" {
			t.Fatalf("leave error = %v", err)
		}
	}
	requireLeavePhase(t, service, LeavePhaseRemoving)
	requireAdmitted(t, service, false)
}

func TestLeaveOfAStandaloneNodeDoesNothing(t *testing.T) {
	service := newLeaveTestService(t, clusterModels.Cluster{})
	if err := service.Leave(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	requireLeavePhase(t, service, "")
	requireAdmitted(t, service, true)
}

func TestLeaveDropsAJoinThatNeverStarted(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	service := newLeaveTestService(t, clusterModels.Cluster{
		Key: guestIdentityJoinTestKey, JoinLeaderIP: "127.0.0.1", RaftIP: "192.0.2.20",
	})
	if err := service.DB.Create(&vmModels.VM{RID: 100, Name: "kept-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Leave(context.Background(), false); err != nil {
		t.Fatalf("leave: %v", err)
	}
	record, err := service.loadClusterRecord()
	if err != nil || record.HasIncompleteJoin() || record.Key != "" || record.LeavePhase != "" {
		t.Fatalf("cluster record after reset = %+v, %v", record, err)
	}
	var guests int64
	if err := service.DB.Model(&vmModels.VM{}).Count(&guests).Error; err != nil || guests != 1 {
		t.Fatalf("guests after reset = %d, %v", guests, err)
	}
}

func TestFinalizeLocalDeclusterIsIdempotent(t *testing.T) {
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)
	bootstrap := true
	service := newLeaveTestService(t, clusterModels.Cluster{
		Enabled: true, Key: "secret", RaftBootstrap: &bootstrap, RaftIP: "192.0.2.1", RaftPort: ClusterRaftPort,
		JoinLeaderIP: "192.0.2.2", LeavePhase: LeavePhaseCleaning, LeaveLastError: "error",
	})
	if err := service.DB.Create(&clusterModels.ClusterNode{NodeUUID: "node-2"}).Error; err != nil {
		t.Fatal(err)
	}
	raftDir := filepath.Join(dataPath, "raft")
	sshDir := filepath.Join(dataPath, clusterSSHDirName)
	for _, dir := range []string{raftDir, sshDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{
		filepath.Join(raftDir, "raft-log.db"),
		filepath.Join(sshDir, clusterSSHPrivateFileName),
		filepath.Join(sshDir, clusterSSHPublicFileName),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := service.FinalizeLocalDecluster(); err != nil {
			t.Fatal(err)
		}
	}
	current, err := service.loadClusterRecord()
	if err != nil {
		t.Fatal(err)
	}
	if current.Enabled || current.Key != "" || current.RaftBootstrap != nil || current.RaftIP != "" ||
		current.JoinLeaderIP != "" || current.LeavePhase != "" || current.LeaveLastError != "" {
		t.Fatalf("cluster state not cleared: %+v", current)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path still exists: %s err=%v", path, err)
		}
	}
	requireAdmitted(t, service, false)
}

func TestCancelLeaveWithoutALeader(t *testing.T) {
	for _, test := range []struct {
		phase     string
		cancelled bool
	}{{LeavePhaseRemoving, true}, {LeavePhaseCleaning, false}} {
		service := newLeaveTestService(t, clusterModels.Cluster{Enabled: true, LeavePhase: test.phase})
		service.mutationGate.Close()
		err := service.CancelLeave(context.Background())
		if test.cancelled != (err == nil) || !test.cancelled && !errors.Is(err, ErrLeaveAlreadyRemoved) {
			t.Fatalf("%s: cancel error = %v", test.phase, err)
		}
		if test.cancelled {
			requireLeavePhase(t, service, "")
		} else {
			requireLeavePhase(t, service, test.phase)
		}
		requireAdmitted(t, service, test.cancelled)
	}
}

func newLeaveTestCluster(t *testing.T, size int) ([]*clusterRaftTestNode, *clusterRaftTestNode, *testPeerNetwork) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	nodes := setupClusterRaftTestNodes(t, size, &clusterModels.Cluster{}, &clusterModels.ClusterNode{}, &vmModels.VM{}, &jailModels.Jail{}, &taskModels.GuestLifecycleTask{})
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.Cluster{
			Enabled: true, Key: guestIdentityJoinTestKey, RaftIP: node.id, RaftPort: ClusterRaftPort,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	network := routeTestPeerRequests(nodes)
	return nodes, waitForClusterRaftLeader(t, nodes, 8*time.Second), network
}

func leaveTestFollowers(nodes []*clusterRaftTestNode, leader *clusterRaftTestNode) []*clusterRaftTestNode {
	var followers []*clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			followers = append(followers, node)
		}
	}
	sort.Slice(followers, func(i, j int) bool { return followers[i].id < followers[j].id })
	return followers
}

func requireLeft(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	status, err := node.service.LeaveStatus()
	if err != nil || status.Enabled || status.Phase != "" {
		t.Fatalf("%s leave status = %+v, %v", node.id, status, err)
	}
	requireAdmitted(t, node.service, false)
}

func TestIntegrationFollowerLeaveCompletes(t *testing.T) {
	for _, size := range []int{2, 3} {
		nodes, leader, _ := newLeaveTestCluster(t, size)
		target := leaveTestFollowers(nodes, leader)[0]
		if err := target.service.Leave(context.Background(), false); err != nil {
			t.Fatalf("%d voters: leave: %v", size, err)
		}
		requireLeft(t, target)
		waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{leader}, size-1, 8*time.Second)
	}
}

func TestIntegrationFollowerLeaveKeepsGuestsAndReleasesClaims(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	seedGuestIdentityJoinTestClaim(t, leader, target.id, clusterModels.ReplicationGuestTypeVM, 100)
	seedGuestIdentityJoinTestClaim(t, leader, target.id, clusterModels.ReplicationGuestTypeJail, 120)
	if err := target.service.DB.Create(&vmModels.VM{RID: 100, Name: "kept-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.service.DB.Create(&jailModels.Jail{CTID: 120, Name: "kept-jail"}).Error; err != nil {
		t.Fatal(err)
	}

	var blocked *PeerRemovalBlockedError
	if err := target.service.Leave(context.Background(), false); !errors.As(err, &blocked) || len(blocked.Conflict.Dependencies) != 2 {
		t.Fatalf("unconfirmed leave = %v", err)
	}
	requireAdmitted(t, target.service, true)
	if err := target.service.Leave(context.Background(), true); err != nil {
		t.Fatalf("leave keeping guests: %v", err)
	}
	requireLeft(t, target)
	var guests int64
	if err := target.service.DB.Model(&vmModels.VM{}).Count(&guests).Error; err != nil || guests != 1 {
		t.Fatalf("VMs kept = %d, %v", guests, err)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 100, "")
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 120, "")
}

func TestIntegrationLeaderLeaveFencesBeforeHandingOff(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	_, release, err := leader.service.EnterMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var leaveErr *ClusterLeaveError
	if err := leader.service.Leave(ctx, true); !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_active_mutations" {
		t.Fatalf("leave with running work = %v", err)
	}
	release()
	if leader.raft.State() != raft.Leader {
		t.Fatal("a leave that couldn't fence handed off leadership")
	}

	if err := leader.service.Leave(context.Background(), true); err != nil {
		t.Fatalf("leader leave: %v", err)
	}
	requireLeft(t, leader)
	successor := waitForClusterRaftLeader(t, leaveTestFollowers(nodes, leader), 8*time.Second)
	waitForClusterRaftVoterCount(t, []*clusterRaftTestNode{successor}, 2, 8*time.Second)
}

func TestIntegrationLeaderLeaveTriesAnotherTransferCandidate(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	followers := leaveTestFollowers(nodes, leader)
	unavailable, expected := followers[0], followers[1]
	unavailable.transport.DisconnectAll()
	for _, node := range nodes {
		node.transport.Disconnect(unavailable.addr)
	}
	future := leader.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		t.Fatal(err)
	}
	if err := leader.service.transferLeadershipForLeave(context.Background(), future.Configuration()); err != nil {
		t.Fatalf("transfer leadership: %v", err)
	}
	if elected := waitForClusterRaftLeader(t, []*clusterRaftTestNode{leader, expected}, 8*time.Second); elected != expected {
		t.Fatalf("leader = %s, want %s", elected.id, expected.id)
	}
}

func TestIntegrationLeaveWithUnreachableLeaderStaysCancellable(t *testing.T) {
	nodes, leader, network := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	network.setCut(leader.id, true)
	var leaveErr *ClusterLeaveError
	if err := target.service.Leave(context.Background(), true); !errors.As(err, &leaveErr) || leaveErr.Code != "cluster_leave_membership_unconfirmed" {
		t.Fatalf("leave without the leader = %v", err)
	}
	requireLeavePhase(t, target.service, LeavePhaseRemoving)
	requireAdmitted(t, target.service, false)
	if err := target.service.CancelLeave(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	requireLeavePhase(t, target.service, "")
	requireAdmitted(t, target.service, true)

	network.setCut(leader.id, false)
	if err := target.service.Leave(context.Background(), true); err != nil {
		t.Fatalf("leave with the leader back: %v", err)
	}
	requireLeft(t, target)
}

func TestIntegrationCancelIsRefusedOnceRemoved(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	if err := target.service.setLeavePhase(LeavePhaseRemoving, nil); err != nil {
		t.Fatal(err)
	}
	target.service.mutationGate.Close()
	if err := leader.service.RemoveMember(context.Background(), target.id, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := target.service.CancelLeave(context.Background()); !errors.Is(err, ErrLeaveAlreadyRemoved) {
		t.Fatalf("cancel after removal = %v", err)
	}
	if err := target.service.advanceLeave(context.Background()); err != nil {
		t.Fatalf("finish the leave: %v", err)
	}
	requireLeft(t, target)
}

func reconcileUntilLeft(t *testing.T, node *clusterRaftTestNode) {
	t.Helper()
	waitForClusterCondition(t, 8*time.Second, node.id+" leaves by itself", func() bool {
		var lastProbe time.Time
		node.service.reconcileLeave(context.Background(), &lastProbe)
		status, err := node.service.LeaveStatus()
		return err == nil && !status.Enabled
	})
	requireLeft(t, node)
}

func TestIntegrationRemovedFollowerLeavesByItself(t *testing.T) {
	nodes, leader, _ := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	if err := leader.service.RemoveMember(context.Background(), target.id, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	reconcileUntilLeft(t, target)
}

func TestIntegrationNodeRemovedWhilePartitionedLeavesWhenItReturns(t *testing.T) {
	nodes, leader, network := newLeaveTestCluster(t, 3)
	target := leaveTestFollowers(nodes, leader)[0]
	seedGuestIdentityJoinTestClaim(t, leader, target.id, clusterModels.ReplicationGuestTypeVM, 300)
	if err := target.service.DB.Create(&vmModels.VM{RID: 300, Name: "kept-vm"}).Error; err != nil {
		t.Fatal(err)
	}
	waitForGuestIdentityClaim(t, nodes, 300, target.id)
	target.transport.DisconnectAll()
	for _, node := range nodes {
		node.transport.Disconnect(target.addr)
	}
	network.setCut(target.id, true)

	if err := leader.service.RemoveMember(context.Background(), target.id, true); err != nil {
		t.Fatalf("force remove: %v", err)
	}
	waitForGuestIdentityClaim(t, []*clusterRaftTestNode{leader}, 300, "")

	// Raft stays cut, so only the cluster API can tell it.
	network.setCut(target.id, false)
	reconcileUntilLeft(t, target)
	// The removal used up the key.
	seedJoinKey(t, leader)
	inventory := BuildGuestIdentityInventoryReport([]GuestIdentityInventoryEntry{{
		NodeID: target.id, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 300, RecordID: 1, Name: "kept-vm",
	}})
	if _, err := leader.service.AdmitJoiner(context.Background(), JoinAdmissionRequest{
		NodeID: target.id, NodeIP: "127.0.0.9", NodeVersion: cmd.Version, Inventory: inventory, SSHPublicKey: "ssh-ed25519 AAAA",
	}, guestIdentityJoinTestSecret, testJoinPin); err != nil {
		t.Fatalf("rejoin admission: %v", err)
	}
}
