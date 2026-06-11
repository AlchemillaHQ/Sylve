// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"fmt"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

func TestReplicationPolicyStateSurvivesLeaderFailover(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	leader := fx.LeaderNode()
	if leader == nil {
		t.Fatal("expected a leader after setup")
	}
	t.Logf("initial leader: %s", leader.id)

	policyID := uint(200)
	policy := &clusterModels.ReplicationPolicy{
		ID:           policyID,
		Name:         "test-policy-failover",
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      1,
		SourceNodeID: fx.LocalNodeID,
		OwnerEpoch:   1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverAutoSafe,
		Enabled:      true,
		CronExpr:     "0 * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{PolicyID: policyID, NodeID: fx.Nodes[1].id, Weight: 100},
			{PolicyID: policyID, NodeID: fx.Nodes[2].id, Weight: 50},
		},
	}
	for _, n := range fx.Nodes {
		if err := clusterModels.UpsertReplicationPolicyTxn(n.db, policy, policy.Targets); err != nil {
			t.Fatalf("seed policy on node %s: %v", n.id, err)
		}
	}
	t.Log("policy seeded on all nodes")

	svc := fx.NewZeltaService()

	svc.updateReplicationPolicyResult(policy, nil)

	time.Sleep(300 * time.Millisecond)
	for _, n := range fx.Nodes {
		var check clusterModels.ReplicationPolicy
		if err := n.db.First(&check, policyID).Error; err != nil {
			t.Fatalf("node %s: policy not found: %v", n.id, err)
		}
		if check.LastStatus != "success" {
			t.Errorf("node %s: expected LastStatus=success, got %q", n.id, check.LastStatus)
		}
		if check.LastRunAt == nil {
			t.Errorf("node %s: expected LastRunAt to be set", n.id)
		}
	}
	t.Log("success state replicated to all nodes")

	svc.updateReplicationPolicyResult(policy, fmt.Errorf("test_replication_error"))

	time.Sleep(300 * time.Millisecond)
	for _, n := range fx.Nodes {
		var check clusterModels.ReplicationPolicy
		n.db.First(&check, policyID)
		if check.LastStatus != "failed" {
			t.Errorf("node %s: expected LastStatus=failed, got %q", n.id, check.LastStatus)
		}
		if check.LastError != "test_replication_error" {
			t.Errorf("node %s: expected LastError=test_replication_error, got %q", n.id, check.LastError)
		}
	}
	t.Log("failure state replicated to all nodes")

	t.Logf("killing leader %s", leader.id)
	leader.raft.Shutdown()
	leader.transport.DisconnectAll()

	var newLeader *zeltaRaftNode
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range fx.Nodes {
			if n.id == leader.id {
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
		t.Fatal("no new leader elected after killing old leader")
	}
	t.Logf("new leader: %s", newLeader.id)

	var check clusterModels.ReplicationPolicy
	if err := newLeader.db.First(&check, policyID).Error; err != nil {
		t.Fatalf("new leader %s: policy not found: %v", newLeader.id, err)
	}
	if check.LastStatus != "failed" {
		t.Errorf("new leader: expected LastStatus=failed, got %q", check.LastStatus)
	}
	if check.LastError != "test_replication_error" {
		t.Errorf("new leader: expected LastError=test_replication_error, got %q", check.LastError)
	}
	if check.LastRunAt == nil {
		t.Errorf("new leader: expected LastRunAt to be set")
	}
	t.Logf("state survived leader failover on node %s", newLeader.id)
}
