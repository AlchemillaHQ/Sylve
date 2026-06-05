// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func TestListReplicationLeases(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationLease{})
	s := &Service{DB: db}

	now := time.Now()
	leases := []clusterModels.ReplicationLease{
		{PolicyID: 2, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 200, OwnerNodeID: "node-b", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour)},
		{PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 100, OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: now.Add(time.Hour)},
	}
	for i := range leases {
		if err := db.Create(&leases[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := s.ListReplicationLeases()
	if err != nil {
		t.Fatalf("ListReplicationLeases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(got))
	}
	if got[0].PolicyID != 1 {
		t.Fatalf("expected ASC order by policy_id, got policy_id=%d first", got[0].PolicyID)
	}
}

func TestListReplicationLeasesEmpty(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationLease{})
	s := &Service{DB: db}
	got, err := s.ListReplicationLeases()
	if err != nil {
		t.Fatalf("ListReplicationLeases empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestGetReplicationLeaseByPolicyID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationLease{})
	s := &Service{DB: db}

	now := time.Now()
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 5, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 500, OwnerNodeID: "node-x", OwnerEpoch: 2,
		ExpiresAt: now.Add(time.Hour),
	})

	lease, err := s.GetReplicationLeaseByPolicyID(5)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if lease.OwnerNodeID != "node-x" {
		t.Fatalf("owner: %q", lease.OwnerNodeID)
	}

	_, err = s.GetReplicationLeaseByPolicyID(999)
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("expected record not found, got: %v", err)
	}

	_, err = s.GetReplicationLeaseByPolicyID(0)
	if err == nil {
		t.Fatal("expected error for id=0")
	}
}

func TestUpsertReplicationLeaseBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationLease{})
	s := &Service{DB: db}

	now := time.Now()
	t.Run("insert", func(t *testing.T) {
		err := s.UpsertReplicationLease(clusterModels.ReplicationLease{
			PolicyID: 10, GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 1000, OwnerNodeID: "node-1", OwnerEpoch: 1,
			ExpiresAt: now.Add(time.Hour),
		}, true)
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		var count int64
		db.Model(&clusterModels.ReplicationLease{}).Where("policy_id = ?", 10).Count(&count)
		if count != 1 {
			t.Fatalf("expected 1 row, got %d", count)
		}
	})

	t.Run("upsert update", func(t *testing.T) {
		err := s.UpsertReplicationLease(clusterModels.ReplicationLease{
			PolicyID: 10, GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 2000, OwnerNodeID: "node-2", OwnerEpoch: 3,
			ExpiresAt: now.Add(2 * time.Hour),
		}, true)
		if err != nil {
			t.Fatalf("upsert update: %v", err)
		}
		var count int64
		db.Model(&clusterModels.ReplicationLease{}).Where("policy_id = ?", 10).Count(&count)
		if count != 1 {
			t.Fatalf("expected 1 row after upsert, got %d", count)
		}
		var lease clusterModels.ReplicationLease
		db.Where("policy_id = ?", 10).First(&lease)
		if lease.OwnerNodeID != "node-2" || lease.OwnerEpoch != 3 {
			t.Fatalf("not updated: owner=%q epoch=%d", lease.OwnerNodeID, lease.OwnerEpoch)
		}
	})
}

func TestDeleteReplicationLeaseBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationLease{})
	s := &Service{DB: db}

	now := time.Now()
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 15, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 1500, OwnerNodeID: "node-d", OwnerEpoch: 1,
		ExpiresAt: now.Add(time.Hour),
	})

	if err := s.DeleteReplicationLease(15, true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int64
	db.Model(&clusterModels.ReplicationLease{}).Where("policy_id = ?", 15).Count(&count)
	if count != 0 {
		t.Fatalf("expected deleted, got %d", count)
	}

	if err := s.DeleteReplicationLease(0, true); err == nil {
		t.Fatal("expected error for id=0")
	}
}

func TestListReplicationEvents(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationEvent{})
	s := &Service{DB: db}

	now := time.Now()
	events := []clusterModels.ReplicationEvent{
		{ID: 1, PolicyID: uintPtr(10), EventType: "run", Status: "success", StartedAt: now},
		{ID: 2, PolicyID: uintPtr(10), EventType: "run", Status: "failed", StartedAt: now.Add(time.Minute)},
		{ID: 3, PolicyID: uintPtr(20), EventType: "failover", Status: "running", StartedAt: now.Add(-time.Hour)},
	}
	for i := range events {
		db.Create(&events[i])
	}

	t.Run("all events no filter", func(t *testing.T) {
		got, err := s.ListReplicationEvents(200, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 events, got %d", len(got))
		}
	})

	t.Run("filter by policyID", func(t *testing.T) {
		got, err := s.ListReplicationEvents(200, 10)
		if err != nil {
			t.Fatalf("list by policy: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 events for policy 10, got %d", len(got))
		}
	})

	t.Run("limit", func(t *testing.T) {
		got, err := s.ListReplicationEvents(1, 0)
		if err != nil {
			t.Fatalf("list limit: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 event with limit, got %d", len(got))
		}
	})
}

func TestGetReplicationEventByID(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationEvent{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ReplicationEvent{
		ID: 1, EventType: "run", Status: "success",
	})

	event, err := s.GetReplicationEventByID(1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if event.EventType != "run" {
		t.Fatalf("type: %q", event.EventType)
	}

	_, err = s.GetReplicationEventByID(0)
	if err == nil {
		t.Fatal("expected error for id=0")
	}
}

func TestCreateOrUpdateReplicationEventBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationEvent{})
	s := &Service{DB: db}

	t.Run("create bypass with assigned ID", func(t *testing.T) {
		id, err := s.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
			ID: 100, EventType: "run", Status: "running",
		}, true)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if id != 100 {
			t.Fatalf("expected id=100, got %d", id)
		}
	})

	t.Run("update bypass", func(t *testing.T) {
		id, err := s.CreateOrUpdateReplicationEvent(clusterModels.ReplicationEvent{
			ID: 100, EventType: "run", Status: "success", Message: "done",
		}, true)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if id != 100 {
			t.Fatalf("expected id=100, got %d", id)
		}
		var event clusterModels.ReplicationEvent
		db.First(&event, 100)
		if event.Status != "success" || event.Message != "done" {
			t.Fatalf("not updated: status=%q msg=%q", event.Status, event.Message)
		}
	})
}

func TestUpsertLocalReplicationReceipt(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationReceipt{})
	s := &Service{DB: db}

	now := time.Now().UTC()
	err := s.UpsertLocalReplicationReceipt(clusterModels.ReplicationReceipt{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "n1", TargetNodeID: "n2", Status: "success",
		LastAttemptAt: now,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var receipt clusterModels.ReplicationReceipt
	db.Where("policy_id = ? AND target_node_id = ?", 1, "n2").First(&receipt)
	if receipt.Status != "success" {
		t.Fatalf("status: %q", receipt.Status)
	}
	if receipt.LastAttemptAt.IsZero() {
		t.Fatal("last_attempt_at not set")
	}
}

func TestDeleteLocalReplicationReceiptsByPolicy(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ReplicationReceipt{})
	s := &Service{DB: db}

	now := time.Now()
	for i := 0; i < 3; i++ {
		db.Create(&clusterModels.ReplicationReceipt{
			PolicyID: 10, GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: uint(100 + i), SourceNodeID: "n1", TargetNodeID: "n2",
			Status: "success", LastAttemptAt: now,
		})
	}

	if err := s.DeleteLocalReplicationReceiptsByPolicy(10); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int64
	db.Model(&clusterModels.ReplicationReceipt{}).Where("policy_id = ?", 10).Count(&count)
	if count != 0 {
		t.Fatalf("expected all deleted, got %d", count)
	}
}

func TestListClusterSSHIdentities(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterSSHIdentity{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "uuid-a", SSHHost: "10.0.0.1", PublicKey: "key-a",
	})
	db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "uuid-b", SSHHost: "10.0.0.2", PublicKey: "key-b",
	})

	got, err := s.ListClusterSSHIdentities()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestUpsertClusterSSHIdentityBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterSSHIdentity{})
	s := &Service{DB: db}

	if err := s.UpsertClusterSSHIdentity(clusterModels.ClusterSSHIdentity{
		NodeUUID: "uuid-test", SSHHost: "10.0.0.1", SSHPort: 22,
		SSHUser: "root", PublicKey: "ssh-ed25519 AAA...",
	}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var count int64
	db.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 identity, got %d", count)
	}
}

func TestDeleteClusterSSHIdentityBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterSSHIdentity{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "to-delete", SSHHost: "host", PublicKey: "key",
	})

	if err := s.DeleteClusterSSHIdentity("to-delete", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int64
	db.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected deleted, got %d", count)
	}

	if err := s.DeleteClusterSSHIdentity("", true); err != nil {
		t.Fatalf("empty nodeUUID should be no-op: %v", err)
	}
	if err := s.DeleteClusterSSHIdentity("  ", true); err != nil {
		t.Fatalf("whitespace nodeUUID should be no-op: %v", err)
	}
}

func TestLocalNodeIsLeader(t *testing.T) {
	s := &Service{Raft: nil}
	if s.LocalNodeIsLeader() {
		t.Fatal("expected false when Raft is nil")
	}

	nodes := setupClusterRaftTestNodes(t, 2)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if !leader.service.LocalNodeIsLeader() {
		t.Fatal("expected leader to report true")
	}

	var follower *clusterRaftTestNode
	for _, n := range nodes {
		if n.id != leader.id {
			follower = n
			break
		}
	}
	if follower.service.LocalNodeIsLeader() {
		t.Fatal("expected follower to report false")
	}
}

func TestDeleteClusterSSHIdentityWithInMemoryRaft(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2, &clusterModels.ClusterSSHIdentity{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	if err := leader.service.UpsertClusterSSHIdentity(clusterModels.ClusterSSHIdentity{
		NodeUUID: "raft-del", SSHHost: "10.0.0.1", PublicKey: "ssh-key",
	}, false); err != nil {
		t.Fatalf("create via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "identity replicated before delete", func() bool {
		for _, n := range nodes {
			var count int64
			n.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
			if count != 1 {
				return false
			}
		}
		return true
	})

	if err := leader.service.DeleteClusterSSHIdentity("raft-del", false); err != nil {
		t.Fatalf("delete via raft: %v", err)
	}

	waitForClusterCondition(t, 8*time.Second, "identity deleted on all nodes", func() bool {
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

func TestResolveSSHHostForNode(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-x", API: "10.0.0.1:8184", Status: "online",
	})

	host, err := s.ResolveSSHHostForNode("node-x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if host != "10.0.0.1" {
		t.Fatalf("expected host 10.0.0.1, got %q", host)
	}

	_, err = s.ResolveSSHHostForNode("")
	if err == nil {
		t.Fatal("expected error for empty nodeID")
	}

	_, err = s.ResolveSSHHostForNode("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent node")
	}
}

func TestResolveSSHHostForNodeViaRaftConfig(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 1, &clusterModels.ClusterNode{})
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	host, err := leader.service.ResolveSSHHostForNode("node-1")
	if err != nil {
		t.Fatalf("resolve via raft: %v", err)
	}
	if host == "" {
		t.Fatal("expected non-empty host from raft config")
	}
}

func TestPruneLocalReplicationReceipts(t *testing.T) {
	db := newClusterServiceTestDB(t,
		&clusterModels.ReplicationReceipt{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	s := &Service{DB: db}

	now := time.Now()

	// seed a policy with a target
	db.Create(&clusterModels.ReplicationPolicy{
		ID: 1, Name: "prune-policy", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "n1",
		SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr: "* * * * *", OwnerEpoch: 1,
	})
	db.Create(&clusterModels.ReplicationPolicyTarget{
		PolicyID: 1, NodeID: "node-target", Weight: 100,
	})

	// create receipt matching target
	db.Create(&clusterModels.ReplicationReceipt{
		PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "n1", TargetNodeID: "node-target",
		Status: "success", LastAttemptAt: now,
	})
	// create stale receipt with non-existent target node
	db.Create(&clusterModels.ReplicationReceipt{
		ID: 999, PolicyID: 1, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "n1", TargetNodeID: "stale-node",
		Status: "success", LastAttemptAt: now,
	})

	if err := s.PruneLocalReplicationReceipts(""); err != nil {
		t.Fatalf("prune: %v", err)
	}

	// valid receipt should remain, stale should be removed
	var receipts []clusterModels.ReplicationReceipt
	db.Find(&receipts)
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt after prune, got %d", len(receipts))
	}
	if receipts[0].TargetNodeID != "node-target" {
		t.Fatalf("expected valid receipt, got target=%q", receipts[0].TargetNodeID)
	}
}

func TestListReplicationReceipts(t *testing.T) {
	db := newClusterServiceTestDB(t,
		&clusterModels.ReplicationReceipt{},
		&clusterModels.ReplicationPolicy{},
	)
	s := &Service{DB: db}

	now := time.Now()
	db.Create(&clusterModels.ReplicationReceipt{
		PolicyID: 5, GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
		SourceNodeID: "n1", TargetNodeID: "n2", Status: "success", LastAttemptAt: now,
	})

	got, err := s.ListReplicationReceipts(0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(got))
	}

	got, err = s.ListReplicationReceipts(5)
	if err != nil {
		t.Fatalf("list by policy: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 receipt for policy 5, got %d", len(got))
	}
}

func TestNodes(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	s := &Service{DB: db}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: "n1", Hostname: "host-1", API: "10.0.0.1:8184", Status: "online",
	})
	db.Create(&clusterModels.ClusterNode{
		NodeUUID: "n2", Hostname: "host-2", API: "10.0.0.2:8184", Status: "online",
	})

	nodes, err := s.Nodes()
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestNodesEmpty(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.ClusterNode{})
	s := &Service{DB: db}
	nodes, err := s.Nodes()
	if err != nil {
		t.Fatalf("Nodes empty: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected empty, got %d", len(nodes))
	}
}
