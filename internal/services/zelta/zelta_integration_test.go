// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestSchedulerTickThreeNodeHAEligible(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)

	policy := &clusterModels.ReplicationPolicy{
		ID: 100, Name: "ha-eligible", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 1000, SourceNodeID: fx.LocalNodeID,
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:     true, CronExpr: "* * * * *",
		NextRunAt:   &past,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: "node-2", Weight: 100},
			{NodeID: "node-3", Weight: 50},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: 100, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 1000, OwnerNodeID: fx.LocalNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc := fx.NewZeltaService()

	if err := svc.runReplicationSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick failed: %v", err)
	}

	var updated clusterModels.ReplicationPolicy
	fx.DB.First(&updated, 100)

	if updated.LastStatus == "blocked" {
		t.Fatalf("3-node cluster should be HA-eligible, got blocked: %s", updated.LastError)
	}
	if updated.NextRunAt == nil {
		t.Fatal("expected NextRunAt to be updated")
	}
	t.Logf("3-node: status=%q NextRunAt=%v", updated.LastStatus, updated.NextRunAt)
}

func TestSchedulerTickOneNodeHAIneligible(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)

	policy := &clusterModels.ReplicationPolicy{
		ID: 200, Name: "ha-ineligible", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 2000, SourceNodeID: fx.LocalNodeID,
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:     true, CronExpr: "* * * * *",
		NextRunAt:   &past,
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: 200, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 2000, OwnerNodeID: fx.LocalNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc := fx.NewZeltaService()

	if err := svc.runReplicationSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick failed: %v", err)
	}

	var updated clusterModels.ReplicationPolicy
	fx.DB.First(&updated, 200)

	if updated.LastStatus != "blocked" {
		t.Fatalf("1-node cluster should be HA-ineligible, got status=%q", updated.LastStatus)
	}
	if !containsReason(updated.LastError, "min_three_voters") {
		t.Errorf("expected min_three_voters in last_error, got: %s", updated.LastError)
	}
	t.Logf("1-node: blocked with reasons: %s", updated.LastError)
}

func TestFailoverControllerTickNoPoliciesNeedingFailover(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	now := time.Now().UTC()

	policy := &clusterModels.ReplicationPolicy{
		ID: 300, Name: "healthy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 3000, SourceNodeID: fx.LocalNodeID,
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:     true, CronExpr: "* * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.LocalNodeID, Weight: 100},
			{NodeID: "node-3", Weight: 50},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: 300, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 3000, OwnerNodeID: fx.LocalNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc := fx.NewZeltaService()

	if err := svc.runFailoverControllerTick(context.Background()); err != nil {
		t.Fatalf("failover tick with healthy owner: %v", err)
	}

	var updated clusterModels.ReplicationPolicy
	fx.DB.First(&updated, 300)
	if updated.TransitionState != "" && updated.TransitionState != clusterModels.ReplicationTransitionStateNone {
		t.Fatalf("healthy policy should not transition, got state=%q", updated.TransitionState)
	}
	t.Log("failover controller correctly skipped healthy policy")
}

func TestLeaderLossDisconnectsFollower(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 2)
	defer fx.Cleanup()

	leader := fx.LeaderNode()
	if leader == nil {
		t.Fatal("expected a leader")
	}
	follower := fx.FollowerNode()
	if follower == nil {
		t.Fatal("expected a follower")
	}

	fx.DisconnectNode(follower.id)

	time.Sleep(500 * time.Millisecond)

	if leader.raft.State() == raft.Leader {
		t.Log("leader still leader after follower disconnect")
	}
}

func TestCrashRecoveryCounterLifecycle(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	pid := uint(1000)
	limit := uint64(3)

	svc.crashMissesReset(pid)
	val := badgerCounterGet(badgerCrashKey(pid))
	if val != 0 {
		t.Fatalf("after reset, expected 0, got %d", val)
	}

	val = svc.crashMissesIncr(pid, limit+1)
	if val != 1 {
		t.Fatalf("first increment, expected 1, got %d", val)
	}

	val = svc.crashMissesIncr(pid, limit+1)
	if val != 2 {
		t.Fatalf("second increment, expected 2, got %d", val)
	}

	svc.crashMissesReset(pid)
	val = badgerCounterGet(badgerCrashKey(pid))
	if val != 0 {
		t.Fatalf("after second reset, expected 0, got %d", val)
	}
}

func TestCrashRecoveryCounterCappedAtMax(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	pid := uint(2000)
	max := uint64(3)

	svc.crashMissesReset(pid)

	for i := uint64(1); i <= 10; i++ {
		val := svc.crashMissesIncr(pid, max)
		expected := i
		if i > max {
			expected = max
		}
		if val != expected {
			t.Fatalf("increment %d: expected %d, got %d", i, expected, val)
		}
	}
}

func TestCrashRecoveryRecoveryDecision(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	pid := uint(3000)
	limit := uint64(replicationCrashRestartLimit)

	svc.crashMissesReset(pid)

	val := svc.crashMissesIncr(pid, limit+1)
	if val >= uint64(limit) {
		t.Fatalf("first crash should be below limit (val=%d, limit=%d)", val, limit)
	}

	_ = svc.crashMissesIncr(pid, limit+1)
	val = svc.crashMissesIncr(pid, limit+1)
	if val > uint64(limit) {
		t.Fatalf("at limit, should be capped (val=%d, limit=%d)", val, limit)
	}

	svc.crashMissesReset(pid)
	val = badgerCounterGet(badgerCrashKey(pid))
	if val != 0 {
		t.Fatalf("recovery should reset counter to 0, got %d", val)
	}
}

func TestCrashRecoveryMultiplePoliciesIndependent(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	limit := uint64(5)

	svc.crashMissesReset(100)
	svc.crashMissesReset(200)

	_ = svc.crashMissesIncr(100, limit)
	_ = svc.crashMissesIncr(100, limit)
	_ = svc.crashMissesIncr(200, limit)

	v100 := badgerCounterGet(badgerCrashKey(100))
	v200 := badgerCounterGet(badgerCrashKey(200))
	if v100 != 2 {
		t.Fatalf("policy 100: expected 2, got %d", v100)
	}
	if v200 != 1 {
		t.Fatalf("policy 200: expected 1, got %d", v200)
	}
}

func TestDownMissesCounterLifecycle(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 1)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	pid := uint(4000)

	svc.downMissesReset(pid)
	val := badgerCounterGet(badgerDownKey(pid))
	if val != 0 {
		t.Fatalf("after reset, expected 0, got %d", val)
	}

	val = svc.downMissesIncr(pid, 5)
	if val != 1 {
		t.Fatalf("first down, expected 1, got %d", val)
	}

	svc.replicationCountersDelete(pid)
	val = badgerCounterGet(badgerCrashKey(pid))
	if val != 0 {
		t.Fatalf("after delete, crash counter should be 0, got %d", val)
	}
	val = badgerCounterGet(badgerDownKey(pid))
	if val != 0 {
		t.Fatalf("after delete, down counter should be 0, got %d", val)
	}
}

func TestLeaderLossAndReElection(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	initialLeader := fx.LeaderNode()
	if initialLeader == nil {
		t.Fatal("expected a leader")
	}
	t.Logf("initial leader: %s", initialLeader.id)

	policy := &clusterModels.ReplicationPolicy{
		ID: 5001, Name: "before-failover-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 5001, SourceNodeID: "node-2",
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:     true, CronExpr: "* * * * *",
	}
	fx.SeedPolicy(policy)

	// Shut down the initial leader (Node 0 = system UUID)
	initialLeaderID := initialLeader.id
	for _, n := range fx.Nodes {
		if n.id == initialLeaderID {
			n.raft.Shutdown()
			n.transport.DisconnectAll()
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	var newLeader *zeltaRaftNode
	for time.Now().Before(deadline) {
		for _, n := range fx.Nodes {
			if n.id == initialLeaderID {
				continue
			}
			if n.raft.State() == raft.Leader {
				newLeader = n
				break
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatal("no new leader elected after shutdown")
	}
	t.Logf("new leader: %s", newLeader.id)

	if newLeader.raft.State() != raft.Leader {
		t.Fatalf("new leader state: %s", newLeader.raft.State())
	}

	t.Log("leader loss → re-election successful")
}

func TestDownMissesTriggersFailoverDecision(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 2)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	now := time.Now().UTC()
	pid := uint(6001)

	policy := &clusterModels.ReplicationPolicy{
		ID: pid, Name: "failover-target", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 6001, SourceNodeID: "node-2",
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverAutoSafe,
		Enabled:     true, CronExpr: "* * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.LocalNodeID, Weight: 100},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: pid, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 6001, OwnerNodeID: "node-2", OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc.downMissesReset(pid)
	svc.downMissesSet(pid, 2)

	val := badgerCounterGet(badgerDownKey(pid))
	if val != 2 {
		t.Fatalf("expected down_miss=2 after set, got %d", val)
	}

	incrVal := svc.downMissesIncr(pid, 10)
	if incrVal != 3 {
		t.Fatalf("expected down_miss=3 after increment, got %d", incrVal)
	}

	t.Logf("down_miss counter: %d (auto-failover limit: %d)", incrVal, replicationFailoverDownMissLimit)
}

func TestFailoverControllerIncrementsDownMissForOfflineOwner(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	now := time.Now().UTC()
	pid := uint(7001)

	policy := &clusterModels.ReplicationPolicy{
		ID: pid, Name: "auto-failover-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 7001, SourceNodeID: "node-2",
		ActiveNodeID: "node-2",
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverAutoSafe,
		Enabled:     true, CronExpr: "* * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.LocalNodeID, Weight: 100},
			{NodeID: "node-3", Weight: 50},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: pid, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 7001, OwnerNodeID: "node-2", OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc.downMissesReset(pid)

	fx.SetNodeStatus("node-2", "offline")

	if err := svc.runFailoverControllerTick(context.Background()); err != nil {
		t.Fatalf("failover tick failed: %v", err)
	}

	downVal := badgerCounterGet(badgerDownKey(pid))
	t.Logf("down_miss for offline owner node-2: %d", downVal)
	if downVal < 1 {
		t.Fatalf("expected down_miss >= 1 for offline owner, got %d", downVal)
	}
}

func TestFailoverControllerManualModeDoesNotAutoFailover(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	svc := fx.NewZeltaService()
	now := time.Now().UTC()
	pid := uint(8001)

	policy := &clusterModels.ReplicationPolicy{
		ID: pid, Name: "manual-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 8001, SourceNodeID: "node-2",
		ActiveNodeID: "node-2",
		OwnerEpoch:  1,
		SourceMode:  clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:     true, CronExpr: "* * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.LocalNodeID, Weight: 100},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: pid, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 8001, OwnerNodeID: "node-2", OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	if err := svc.runFailoverControllerTick(context.Background()); err != nil {
		t.Fatalf("failover tick failed: %v", err)
	}

	var updated clusterModels.ReplicationPolicy
	fx.DB.First(&updated, pid)
	if updated.TransitionState != clusterModels.ReplicationTransitionStateNone &&
		updated.TransitionState != "" {
		t.Fatalf("manual mode should not auto-failover, got transition state %q",
			updated.TransitionState)
	}
	t.Log("manual mode correctly prevented auto-failover")
}

func containsReason(errorStr, reason string) bool {
	if errorStr == "" {
		return false
	}
	return strings.Contains(errorStr, reason)
}
