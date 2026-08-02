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
	"encoding/json"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestRaftReplicationPolicyCRUDTwoNodes(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	payload := clusterModels.ReplicationPolicyPayload{
		Policy: clusterModels.ReplicationPolicy{
			ID: 1, Name: "raft-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 100, SourceNodeID: "node-1",
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
		},
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: "node-2", Weight: 100},
		},
	}
	createRaw, _ := json.Marshal(payload)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("leader create policy via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "policy create replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&count)
			if count != 1 {
				return false
			}
			var policy clusterModels.ReplicationPolicy
			n.service.DB.Preload("Targets").First(&policy, 1)
			if policy.Name != "raft-policy" || len(policy.Targets) != 1 {
				return false
			}
		}
		return true
	})

	deletedPolicyID := uint(1)
	otherPolicyID := uint(2)
	for _, node := range nodes {
		localEvents := []clusterModels.ReplicationEvent{
			{ID: 101, PolicyID: &deletedPolicyID, EventType: "replication", Status: "failed", StartedAt: time.Now().UTC()},
			{ID: 102, PolicyID: &otherPolicyID, EventType: "replication", Status: "success", StartedAt: time.Now().UTC()},
			{ID: 103, PolicyID: nil, EventType: "replication", Status: "success", StartedAt: time.Now().UTC()},
		}
		if err := node.service.DB.Create(&localEvents).Error; err != nil {
			t.Fatalf("seed local events on %s: %v", node.id, err)
		}
		transitionEvents := []clusterModels.ReplicationTransitionEvent{
			{ID: 201, PolicyID: &deletedPolicyID, TransitionRunID: "delete", EventType: "failover", Status: "failed", StartedAt: time.Now().UTC()},
			{ID: 202, PolicyID: &otherPolicyID, TransitionRunID: "keep", EventType: "failover", Status: "success", StartedAt: time.Now().UTC()},
			{ID: 203, PolicyID: nil, TransitionRunID: "unscoped", EventType: "failover", Status: "success", StartedAt: time.Now().UTC()},
		}
		if err := node.service.DB.Create(&transitionEvents).Error; err != nil {
			t.Fatalf("seed transition events on %s: %v", node.id, err)
		}
	}

	// update
	payload.Policy.Name = "raft-policy-updated"
	payload.Policy.FailoverMode = clusterModels.ReplicationFailoverAutoSafe
	payload.ExpectedOwnerEpoch = 1
	payload.Targets = []clusterModels.ReplicationPolicyTarget{
		{NodeID: "node-2", Weight: 200},
	}
	updateRaw, _ := json.Marshal(payload)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: updateRaw,
	}); err != nil {
		t.Fatalf("leader update policy via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "policy update replicated", func() bool {
		for _, n := range nodes {
			var policy clusterModels.ReplicationPolicy
			if err := n.service.DB.Preload("Targets").First(&policy, 1).Error; err != nil {
				return false
			}
			if policy.Name != "raft-policy-updated" || policy.FailoverMode != clusterModels.ReplicationFailoverAutoSafe {
				return false
			}
			if len(policy.Targets) != 1 || policy.Targets[0].Weight != 200 {
				return false
			}
		}
		return true
	})

	// delete
	if err := leader.service.UpdateReplicationPolicyProtectionState(
		1,
		1,
		clusterModels.ReplicationProtectionStateDeleting,
		false,
	); err != nil {
		t.Fatalf("mark policy deleting via raft: %v", err)
	}
	deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "delete", Data: deleteRaw,
	}); err != nil {
		t.Fatalf("leader delete policy via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "policy delete replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&count)
			if count != 0 {
				return false
			}
			for _, model := range []any{&clusterModels.ReplicationEvent{}, &clusterModels.ReplicationTransitionEvent{}} {
				var exactCount int64
				if n.service.DB.Model(model).Where("policy_id = ?", deletedPolicyID).Count(&exactCount).Error != nil || exactCount != 0 {
					return false
				}
				var otherCount int64
				if n.service.DB.Model(model).Where("policy_id = ?", otherPolicyID).Count(&otherCount).Error != nil || otherCount != 1 {
					return false
				}
				var nullCount int64
				if n.service.DB.Model(model).Where("policy_id IS NULL").Count(&nullCount).Error != nil || nullCount != 1 {
					return false
				}
			}
		}
		return true
	})
}

func TestReplicationDeleteAppliedIndexFenceWaitsForLaggingFollower(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	payload := clusterModels.ReplicationPolicyPayload{Policy: clusterModels.ReplicationPolicy{
		ID: 91, Name: "fenced-delete", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 91,
		SourceNodeID: leader.id, ActiveNodeID: leader.id, OwnerEpoch: 1,
		CronExpr: "*/5 * * * *", Enabled: true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
	}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: raw,
	}); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	waitForClusterCondition(t, 5*time.Second, "initial policy convergence", func() bool {
		for _, node := range nodes {
			var count int64
			if node.service.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", 91).Count(&count).Error != nil || count != 1 {
				return false
			}
		}
		return true
	})

	var lagging *clusterRaftTestNode
	for _, node := range nodes {
		if node != leader {
			lagging = node
			break
		}
	}
	if lagging == nil {
		t.Fatal("lagging follower not found")
	}
	lagging.transport.DisconnectAll()
	for _, node := range nodes {
		if node != lagging {
			node.transport.Disconnect(lagging.addr)
		}
	}

	if err := leader.service.UpdateReplicationPolicyProtectionState(
		91, 1, clusterModels.ReplicationProtectionStateDeleting, false,
	); err != nil {
		t.Fatalf("mark policy deleting: %v", err)
	}
	minimum := leader.raft.AppliedIndex()
	if lagging.raft.AppliedIndex() >= minimum {
		t.Fatalf("follower did not lag: follower=%d minimum=%d", lagging.raft.AppliedIndex(), minimum)
	}

	type waitResult struct {
		applied uint64
		err     error
	}
	resultCh := make(chan waitResult, 1)
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		applied, waitErr := lagging.service.WaitForReplicatedStateAppliedIndex(waitCtx, minimum)
		resultCh <- waitResult{applied: applied, err: waitErr}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("fence returned before follower caught up: %+v", result)
	case <-time.After(75 * time.Millisecond):
	}
	connectClusterRaftTestNodes(nodes)

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("wait for follower catchup: %v", result.err)
		}
		if result.applied < minimum {
			t.Fatalf("applied index=%d, want >=%d", result.applied, minimum)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for follower fence")
	}
	var policy clusterModels.ReplicationPolicy
	if err := lagging.service.DB.First(&policy, 91).Error; err != nil {
		t.Fatalf("load caught-up policy: %v", err)
	}
	if policy.ProtectionState != clusterModels.ReplicationProtectionStateDeleting {
		t.Fatalf("caught-up lifecycle=%q, want deleting", policy.ProtectionState)
	}
}

func TestReplicationDeleteAppliedIndexFenceTimeoutIsRetryableAndNonMutating(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &clusterModels.ReplicationPolicy{})
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	policy := clusterModels.ReplicationPolicy{
		ID: 92, Name: "fence-timeout", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 92,
		SourceNodeID: leader.id, ActiveNodeID: leader.id, OwnerEpoch: 1,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
	}
	if err := leader.service.DB.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := leader.service.WaitForReplicatedStateAppliedIndex(ctx, leader.raft.AppliedIndex()+1000)
	if err == nil || !strings.Contains(err.Error(), "replicated_state_catchup_failed") ||
		!strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("unexpected fence timeout: %v", err)
	}
	var retained clusterModels.ReplicationPolicy
	if err := leader.service.DB.First(&retained, policy.ID).Error; err != nil {
		t.Fatalf("fence timeout removed policy: %v", err)
	}
	if retained.ProtectionState != clusterModels.ReplicationProtectionStateDeleting {
		t.Fatalf("fence timeout changed lifecycle to %q", retained.ProtectionState)
	}
}

func TestRaftReplicationPolicyThreeNodeFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	initialLeader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	payload := clusterModels.ReplicationPolicyPayload{
		Policy: clusterModels.ReplicationPolicy{
			ID: 1, Name: "before-failover", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 100, SourceNodeID: "node-1",
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
		},
	}
	createRaw, _ := json.Marshal(payload)

	if err := initialLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("initial leader create: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "initial policy replication", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	// kill initial leader
	survivors := make([]*clusterRaftTestNode, 0, len(nodes)-1)
	for _, n := range nodes {
		if n.id != initialLeader.id {
			survivors = append(survivors, n)
		}
	}
	for _, n := range survivors {
		n.transport.Disconnect(initialLeader.addr)
	}
	initialLeader.transport.DisconnectAll()
	initialLeader.raft.Shutdown()

	newLeader := waitForClusterRaftLeader(t, survivors, 12*time.Second)

	payload2 := clusterModels.ReplicationPolicyPayload{
		Policy: clusterModels.ReplicationPolicy{
			ID: 2, Name: "after-failover", GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 200, SourceNodeID: "node-2",
			SourceMode:   clusterModels.ReplicationSourceModePinned,
			FailbackMode: clusterModels.ReplicationFailbackAuto,
			FailoverMode: clusterModels.ReplicationFailoverAutoSafe,
			CronExpr:     "0 */6 * * *", OwnerEpoch: 2,
		},
	}
	createRaw2, _ := json.Marshal(payload2)

	if err := newLeader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: createRaw2,
	}); err != nil {
		t.Fatalf("new leader create: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "post-failover replication", func() bool {
		for _, n := range survivors {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&count)
			if count != 2 {
				return false
			}
			var names []string
			n.service.DB.Model(&clusterModels.ReplicationPolicy{}).Pluck("name", &names)
			hasBefore := false
			hasAfter := false
			for _, name := range names {
				if name == "before-failover" {
					hasBefore = true
				}
				if name == "after-failover" {
					hasAfter = true
				}
			}
			if !hasBefore || !hasAfter {
				return false
			}
		}
		return true
	})
}

func TestRaftReplicationLeaseUpsertReplication(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	for _, node := range nodes {
		if err := node.service.DB.Create(&[]clusterModels.ReplicationPolicy{
			{ID: 1, Name: "policy-1", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100, ActiveNodeID: "node-1", OwnerEpoch: 1, Enabled: true},
			{ID: 2, Name: "policy-2", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 200, ActiveNodeID: "node-a", OwnerEpoch: 1, Enabled: true},
			{ID: 3, Name: "policy-3", GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 300, ActiveNodeID: "node-b", OwnerEpoch: 1, Enabled: true},
		}).Error; err != nil {
			t.Fatalf("seed policies on %s: %v", node.id, err)
		}
	}

	// upsert single
	lease := clusterModels.ReplicationLease{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	leaseRaw, _ := json.Marshal(lease)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_lease", Action: "upsert", Data: leaseRaw,
	}); err != nil {
		t.Fatalf("upsert via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "lease upsert replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationLease{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	// upsert batch
	batch := []clusterModels.ReplicationLease{
		{PolicyID: 2, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 200, OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: time.Now().Add(time.Hour)},
		{PolicyID: 3, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 300, OwnerNodeID: "node-b", OwnerEpoch: 1, ExpiresAt: time.Now().Add(time.Hour)},
	}
	batchRaw, _ := json.Marshal(batch)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_lease", Action: "upsert_batch", Data: batchRaw,
	}); err != nil {
		t.Fatalf("upsert batch via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "lease batch upsert replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationLease{}).Count(&count)
			if count != 3 {
				return false
			}
		}
		return true
	})
}

func TestRaftReplicationPolicyEnabledFalseClearsLeases(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// create enabled policy with a lease
	payload := clusterModels.ReplicationPolicyPayload{
		Policy: clusterModels.ReplicationPolicy{
			ID: 1, Name: "lease-test", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 100, SourceNodeID: "node-1", Enabled: true,
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
		},
	}
	createRaw, _ := json.Marshal(payload)
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "create", Data: createRaw,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// seed a lease directly (simulating runtime lease creation)
	for _, n := range nodes {
		n.service.DB.Create(&clusterModels.ReplicationLease{
			PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1,
			ExpiresAt: time.Now().Add(time.Hour),
		})
	}

	// update to disabled — FSM handler clears leases
	payload.Policy.Enabled = false
	payload.ExpectedOwnerEpoch = 1
	updateRaw, _ := json.Marshal(payload)
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: updateRaw,
	}); err != nil {
		t.Fatalf("update to disabled: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "leases cleared when policy disabled", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ReplicationLease{}).Count(&count)
			if count != 0 {
				return false
			}
		}
		return true
	})

	// re-enable
	payload.Policy.Enabled = true
	reEnableRaw, _ := json.Marshal(payload)
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "replication_policy", Action: "update", Data: reEnableRaw,
	}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}

	// verify policy is enabled on all nodes
	waitForClusterCondition(t, 8*time.Second, "policy re-enabled", func() bool {
		for _, n := range nodes {
			var policy clusterModels.ReplicationPolicy
			if err := n.service.DB.First(&policy, 1).Error; err != nil {
				return false
			}
			if !policy.Enabled {
				return false
			}
		}
		return true
	})
}

func TestRaftReplicationOwnershipCommitAndTargetReadiness(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)
	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	now := time.Now().UTC()
	for _, node := range nodes {
		if err := node.service.DB.Create(&clusterModels.ReplicationPolicy{
			ID: 1, Name: "ownership", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			SourceNodeID: "node-1", ActiveNodeID: "node-1", OwnerEpoch: 1, Enabled: true,
			ProtectionState: clusterModels.ReplicationProtectionStateSuspended,
			TransitionState: clusterModels.ReplicationTransitionStateDemoting,
			TransitionRunID: "run-raft", TransitionOwnerEpoch: 1,
			TransitionSourceNodeID: "node-1", TransitionTargetNodeID: "node-2",
		}).Error; err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationPolicyTarget{
			PolicyID: 1, NodeID: "node-2", Weight: 100,
		}).Error; err != nil {
			t.Fatalf("seed target on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.ReplicationLease{
			PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-1", OwnerEpoch: 1, Version: 1,
			ExpiresAt: now.Add(time.Hour),
		}).Error; err != nil {
			t.Fatalf("seed lease on %s: %v", node.id, err)
		}
	}

	source := "node-2"
	payload := clusterModels.ReplicationOwnershipTransitionPayload{
		PolicyID: 1, ExpectedActiveNodeID: "node-1", ExpectedOwnerEpoch: 1,
		ExpectedTransitionRunID: "run-raft", ActiveNodeID: "node-2",
		SourceNodeID: &source, OwnerEpoch: 2, ReplaceTargets: true,
		Targets: []clusterModels.ReplicationPolicyTarget{{NodeID: "node-1", Weight: 100}},
		Lease: clusterModels.ReplicationLease{
			PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
			OwnerNodeID: "node-2", OwnerEpoch: 2, Version: 2,
			ExpiresAt: now.Add(time.Hour),
		},
		Transition: clusterModels.ReplicationPolicyTransition{
			State: clusterModels.ReplicationTransitionStatePromoting,
			RunID: "run-raft", SourceNodeID: "node-1", TargetNodeID: "node-2", OwnerEpoch: 2,
		},
		ProtectionState: clusterModels.ReplicationProtectionStateSuspended,
	}
	if err := leader.service.CommitReplicationOwnershipTransition(payload, false); err != nil {
		t.Fatalf("commit ownership through Raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "atomic ownership commit replication", func() bool {
		for _, node := range nodes {
			var policy clusterModels.ReplicationPolicy
			if err := node.service.DB.Preload("Targets").First(&policy, 1).Error; err != nil {
				return false
			}
			if policy.ActiveNodeID != "node-2" || policy.OwnerEpoch != 2 ||
				policy.TransitionState != clusterModels.ReplicationTransitionStatePromoting ||
				len(policy.Targets) != 1 || policy.Targets[0].NodeID != "node-1" {
				return false
			}
			var lease clusterModels.ReplicationLease
			if err := node.service.DB.Where("policy_id = ?", 1).First(&lease).Error; err != nil ||
				lease.OwnerNodeID != "node-2" || lease.OwnerEpoch != 2 {
				return false
			}
		}
		return true
	})
	if err := leader.service.UpdateReplicationPolicyTransition(1, clusterModels.ReplicationPolicyTransition{
		State: clusterModels.ReplicationTransitionStateCompleted,
		RunID: "run-raft", SourceNodeID: "node-1", TargetNodeID: "node-2", OwnerEpoch: 2,
	}); err != nil {
		t.Fatalf("complete transition: %v", err)
	}

	verified := now.Add(time.Minute)
	readyUntil := verified.Add(time.Hour)
	if err := leader.service.UpdateReplicationTargetReadiness(clusterModels.ReplicationTargetReadinessUpdate{
		PolicyID: 1, NodeID: "node-1", ExpectedOwnerEpoch: 2, Ready: true,
		GenerationID: "generation-2", ManifestHash: "manifest-2",
		RequiredDatasetCount: 1, CompletedDatasetCount: 1,
		LastVerifiedAt: &verified, ReadyUntil: &readyUntil,
	}, false); err != nil {
		t.Fatalf("publish readiness through Raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "target readiness replication", func() bool {
		for _, node := range nodes {
			var policy clusterModels.ReplicationPolicy
			if err := node.service.DB.Preload("Targets").First(&policy, 1).Error; err != nil {
				return false
			}
			if policy.ProtectionState != clusterModels.ReplicationProtectionStateArmed ||
				len(policy.Targets) != 1 || !policy.Targets[0].Ready ||
				policy.Targets[0].GenerationID != "generation-2" {
				return false
			}
		}
		return true
	})
}

func TestRaftClusterSSHIdentityReplication(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterSSHIdentity{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// create
	identity := clusterModels.ClusterSSHIdentity{
		NodeUUID: "raft-ssh-1", SSHUser: "root",
		SSHHost: "10.0.0.10", SSHPort: 8183,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1...",
	}
	createRaw, _ := json.Marshal(identity)

	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "cluster_ssh_identity", Action: "upsert", Data: createRaw,
	}); err != nil {
		t.Fatalf("upsert identity via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "SSH identity replicated", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
			if count != 1 {
				return false
			}
			var id clusterModels.ClusterSSHIdentity
			n.service.DB.First(&id)
			if id.NodeUUID != "raft-ssh-1" || id.PublicKey != "ssh-ed25519 AAAAC3NzaC1..." {
				return false
			}
		}
		return true
	})

	// update
	identity.SSHHost = "10.0.0.20"
	updateRaw, _ := json.Marshal(identity)
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "cluster_ssh_identity", Action: "upsert", Data: updateRaw,
	}); err != nil {
		t.Fatalf("update identity via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "SSH identity updated on all nodes", func() bool {
		for _, n := range nodes {
			var id clusterModels.ClusterSSHIdentity
			if err := n.service.DB.Where("node_uuid = ?", "raft-ssh-1").First(&id).Error; err != nil {
				return false
			}
			if id.SSHHost != "10.0.0.20" {
				return false
			}
		}
		return true
	})

	// delete
	deleteRaw, _ := json.Marshal(map[string]any{"nodeUUID": "raft-ssh-1"})
	if err := leader.service.applyRaftCommand(clusterModels.Command{
		Type: "cluster_ssh_identity", Action: "delete", Data: deleteRaw,
	}); err != nil {
		t.Fatalf("delete identity via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "SSH identity deleted on all nodes", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
			if count != 0 {
				return false
			}
		}
		return true
	})
}
