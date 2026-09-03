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

func TestIntegrationRaftReplicationDeleteFenceWaitsForFollower(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)
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

func TestIntegrationRaftReplicationDeleteFenceTimeoutIsRetryable(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &clusterModels.ReplicationPolicy{})
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

func TestIntegrationRaftReplicationPolicyThreeNodeFailover(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
	)

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

func TestIntegrationRaftReplicationOwnershipAndTargetReadiness(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.GuestIdentityRegistry{},
		&clusterModels.GuestIdentityClaim{},
	)
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
		if err := node.service.DB.Create(&clusterModels.GuestIdentityRegistry{
			ID: clusterModels.GuestIdentityRegistryID, Version: clusterModels.GuestIdentityRegistryVersion,
			Phase: clusterModels.GuestIdentityRegistryPhaseActive,
		}).Error; err != nil {
			t.Fatalf("seed guest identity registry on %s: %v", node.id, err)
		}
		if err := node.service.DB.Create(&clusterModels.GuestIdentityClaim{
			GuestID: 100, GuestKind: clusterModels.ReplicationGuestTypeVM, OwnerNodeID: "node-1",
			Token: "claim-generation-1",
		}).Error; err != nil {
			t.Fatalf("seed guest identity claim on %s: %v", node.id, err)
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
	expectedClaimToken, err := guestIdentityOwnershipMoveToken("replication:run-raft:node-1:node-2:2")
	if err != nil {
		t.Fatalf("build expected claim token: %v", err)
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
			var claim clusterModels.GuestIdentityClaim
			if err := node.service.DB.First(&claim, 100).Error; err != nil ||
				claim.OwnerNodeID != "node-2" || claim.Token != expectedClaimToken {
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
