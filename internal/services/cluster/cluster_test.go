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
)

func TestGetClusterDetailsRaftNotInit(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	s := &Service{DB: db}

	if err := db.Create(&clusterModels.Cluster{
		Enabled: false, Key: "", RaftIP: "", RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster row: %v", err)
	}

	details, err := s.GetClusterDetails()
	if err != nil {
		t.Fatal("expected no error when Raft is nil")
	}
	if details.Cluster == nil {
		t.Fatal("expected cluster row")
	}
	if details.Cluster.Enabled {
		t.Fatal("expected cluster not enabled")
	}
}

func TestGetClusterDetailsWithInMemoryRaft(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
	)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	if err := leader.service.DB.Create(&clusterModels.Cluster{
		Enabled: true, Key: "test-key", RaftIP: "127.0.0.1",
		RaftPort: ClusterRaftPort, RaftBootstrap: boolPtr(true),
	}).Error; err != nil {
		t.Fatalf("seed cluster row: %v", err)
	}

	details, err := leader.service.GetClusterDetails()
	if err != nil {
		t.Fatalf("GetClusterDetails failed: %v", err)
	}
	if details.Cluster == nil {
		t.Fatal("expected cluster row")
	}
	if !details.Cluster.Enabled {
		t.Fatal("expected cluster to be enabled")
	}

	if details.LeaderID == "" {
		t.Fatal("expected non-empty leader ID")
	}

	if len(details.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in config, got %d", len(details.Nodes))
	}

	foundLeader := false
	for _, node := range details.Nodes {
		if node.IsLeader {
			foundLeader = true
			if node.ID != details.LeaderID {
				t.Fatalf("leader node ID mismatch: node=%q detail=%q", node.ID, details.LeaderID)
			}
		}
	}
	if !foundLeader {
		t.Fatal("no leader node found in config")
	}
}

func TestMarkClustered(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	s := &Service{DB: db}

	if err := db.Create(&clusterModels.Cluster{Enabled: false}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	if err := s.MarkClustered(); err != nil {
		t.Fatalf("MarkClustered failed: %v", err)
	}

	var c clusterModels.Cluster
	db.First(&c)
	if !c.Enabled {
		t.Fatal("expected Cluster.Enabled=true")
	}
}

func TestMarkDeclustered(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	s := &Service{DB: db}

	if err := db.Create(&clusterModels.Cluster{
		Enabled: true, Key: "secret", RaftBootstrap: boolPtr(true),
		RaftIP: "10.0.0.1", RaftPort: ClusterRaftPort,
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	if err := s.MarkDeclustered(); err != nil {
		t.Fatalf("MarkDeclustered failed: %v", err)
	}

	var c clusterModels.Cluster
	db.First(&c)
	if c.Enabled {
		t.Fatal("expected Cluster.Enabled=false")
	}
	if c.Key != "" {
		t.Fatalf("expected empty key, got %q", c.Key)
	}
	if c.RaftBootstrap != nil {
		t.Fatal("expected nil RaftBootstrap")
	}
	if c.RaftIP != "" {
		t.Fatalf("expected empty RaftIP, got %q", c.RaftIP)
	}
}

func TestClearClusteredData(t *testing.T) {
	db := newClusterServiceTestDB(t,
		&clusterModels.ClusterNote{},
		&clusterModels.ClusterOption{},
		&clusterModels.BackupEvent{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationReceipt{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ClusterSSHIdentity{},
	)
	s := &Service{DB: db}

	seedDB := func(table string, record any) {
		if err := db.Table(table).Create(record).Error; err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}

	seedDB("cluster_notes", map[string]any{"id": 1, "title": "n1", "content": "c1"})
	seedDB("cluster_options", map[string]any{"id": 1, "keyboard_layout": "us"})
	seedDB("backup_events", map[string]any{"id": 1, "status": "running"})
	seedDB("backup_jobs", map[string]any{"id": 1, "name": "job1", "target_id": 1, "mode": "dataset", "cron_expr": "* * * * *"})
	seedDB("backup_targets", map[string]any{"id": 1, "name": "target1", "ssh_host": "host", "backup_root": "tank/bk"})
	seedDB("replication_events", map[string]any{"id": 1, "event_type": "run", "status": "success"})
	seedDB("replication_receipts", map[string]any{"id": 1, "policy_id": 1, "guest_type": "vm", "guest_id": 1, "source_node_id": "n1", "target_node_id": "n2", "status": "success", "last_attempt_at": time.Now()})
	seedDB("replication_leases", map[string]any{"id": 1, "policy_id": 1, "guest_type": "vm", "guest_id": 1, "owner_node_id": "n1", "owner_epoch": 1, "expires_at": time.Now().Add(time.Hour)})
	seedDB("replication_policies", map[string]any{"id": 1, "name": "pol1", "guest_type": "vm", "guest_id": 1, "cron_expr": "* * * * *", "owner_epoch": 1})
	seedDB("replication_policy_targets", map[string]any{"id": 1, "policy_id": 1, "node_id": "n2", "weight": 100})
	seedDB("cluster_ssh_identities", map[string]any{"id": 1, "node_uuid": "uuid-1", "ssh_host": "h1", "public_key": "k1"})

	if err := s.ClearClusteredData(); err != nil {
		t.Fatalf("ClearClusteredData failed: %v", err)
	}

	tables := []string{
		"cluster_notes", "cluster_options", "backup_events", "backup_jobs",
		"backup_targets", "replication_events", "replication_receipts", "replication_leases",
		"replication_policy_targets", "replication_policies", "cluster_ssh_identities",
	}
	for _, table := range tables {
		var count int64
		db.Table(table).Count(&count)
		if count != 0 {
			t.Fatalf("expected table %s to be empty, got %d rows", table, count)
		}
	}
}

func TestListBackupTargetsForSync(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{})
	s := &Service{DB: db}

	targets := []clusterModels.BackupTarget{
		{ID: 1, Name: "b-first", SSHHost: "h1", BackupRoot: "tank/bk1"},
		{ID: 2, Name: "a-second", SSHHost: "h2", BackupRoot: "tank/bk2"},
	}
	for _, target := range targets {
		if err := db.Create(&target).Error; err != nil {
			t.Fatalf("seed target: %v", err)
		}
	}

	got, err := s.ListBackupTargetsForSync()
	if err != nil {
		t.Fatalf("ListBackupTargetsForSync failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(got))
	}
}

func TestResyncClusterStateErrors(t *testing.T) {
	t.Run("raft not initialized", func(t *testing.T) {
		s := &Service{Raft: nil}
		err := s.ResyncClusterState()
		if err == nil {
			t.Fatal("expected error when Raft is nil")
		}
	})

	t.Run("not leader", func(t *testing.T) {
		nodes := setupClusterRaftTestNodes(t, 2,
			&clusterModels.ClusterNote{}, &clusterModels.ClusterOption{},
			&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
			&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
			&clusterModels.ReplicationLease{}, &clusterModels.ClusterSSHIdentity{},
			&clusterModels.EncryptionKey{}, &clusterModels.ReplicationEvent{},
		)
		defer cleanupClusterRaftTestNodes(t, nodes)

		leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
		var follower *clusterRaftTestNode
		for _, n := range nodes {
			if n.id != leader.id {
				follower = n
				break
			}
		}

		err := follower.service.ResyncClusterState()
		if err == nil {
			t.Fatal("expected error when calling ResyncClusterState on follower")
		}
	})
}

func TestBackfillPreClusterState(t *testing.T) {
	allModels := []any{
		&clusterModels.ClusterNote{}, &clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
		&clusterModels.ReplicationEvent{},
	}

	nodes := setupClusterRaftTestNodes(t, 2, allModels...)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	// seed pre-existing state on the leader's DB (simulating pre-cluster data)
	seedDB := leader.service.DB

	seedDB.Create(&clusterModels.ClusterNote{ID: 1, Title: "note1", Content: "content1"})
	seedDB.Create(&clusterModels.ClusterOption{ID: 1, KeyboardLayout: "de"})

	target := clusterModels.BackupTarget{
		ID: 10, Name: "bk-target", SSHHost: "host@remote",
		SSHPort: 22, BackupRoot: "tank/backups", CreateBackupRoot: true,
		Enabled: true, SSHKey: "some-key",
	}
	seedDB.Create(&target)

	seedDB.Create(&clusterModels.BackupJob{
		ID: 20, Name: "bk-job", TargetID: 10, Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 2 * * *", Enabled: true,
	})

	seedDB.Create(&clusterModels.ReplicationPolicy{
		ID: 30, Name: "rep-pol", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "node-1",
		SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr: "* * * * *", OwnerEpoch: 1, Enabled: true,
	})
	seedDB.Create(&clusterModels.ReplicationPolicyTarget{
		PolicyID: 30, NodeID: "node-2", Weight: 100,
	})

	seedDB.Create(&clusterModels.ReplicationLease{
		PolicyID: 30, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, OwnerNodeID: "node-1", OwnerEpoch: 1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	seedDB.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "uuid-backfill", SSHHost: "10.0.0.1",
		SSHUser: "root", PublicKey: "ssh-ed25519 AAA...",
	})

	seedDB.Create(&clusterModels.EncryptionKey{
		UUID: "enc-backfill", KeyData: "encrypted-data-min-32-bytes-long", KeyFormat: "passphrase",
	})

	seedDB.Create(&clusterModels.ReplicationEvent{
		ID: 40, EventType: "run", Status: "success",
		PolicyID: uintPtr(30), SourceNodeID: "node-1", TargetNodeID: "node-2",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
	})

	// backfill
	if err := leader.service.backfillPreClusterState(); err != nil {
		t.Fatalf("backfillPreClusterState failed: %v", err)
	}

	// verify replication to follower
	waitForClusterCondition(t, 8*time.Second, "backfill replication", func() bool {
		for _, node := range nodes {
			var noteCount int64
			node.service.DB.Model(&clusterModels.ClusterNote{}).Count(&noteCount)
			if noteCount != 1 {
				return false
			}

			var optCount int64
			node.service.DB.Model(&clusterModels.ClusterOption{}).Count(&optCount)
			if optCount != 1 {
				return false
			}

			var tgtCount int64
			node.service.DB.Model(&clusterModels.BackupTarget{}).Count(&tgtCount)
			if tgtCount != 1 {
				return false
			}

			var jobCount int64
			node.service.DB.Model(&clusterModels.BackupJob{}).Count(&jobCount)
			if jobCount != 1 {
				return false
			}

			var polCount int64
			node.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&polCount)
			if polCount != 1 {
				return false
			}

			var leaseCount int64
			node.service.DB.Model(&clusterModels.ReplicationLease{}).Count(&leaseCount)
			if leaseCount != 1 {
				return false
			}

			var identCount int64
			node.service.DB.Model(&clusterModels.ClusterSSHIdentity{}).Count(&identCount)
			if identCount != 1 {
				return false
			}

			var encCount int64
			node.service.DB.Model(&clusterModels.EncryptionKey{}).Count(&encCount)
			if encCount != 1 {
				return false
			}

			var evtCount int64
			node.service.DB.Model(&clusterModels.ReplicationEvent{}).Count(&evtCount)
			if evtCount != 1 {
				return false
			}
		}
		return true
	})
}

func TestWaitUntilLeaderWithInMemoryRaft(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3)
	defer cleanupClusterRaftTestNodes(t, nodes)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

	becameLeader, addr, err := leader.service.waitUntilLeader(2 * time.Second)
	if err != nil {
		t.Fatalf("waitUntilLeader failed: %v", err)
	}
	if !becameLeader {
		t.Fatal("expected becameLeader=true for actual leader")
	}
	if addr == "" {
		t.Fatal("expected non-empty leader address")
	}

	// find a follower to test against
	var follower *clusterRaftTestNode
	for _, n := range nodes {
		if n.id != leader.id {
			follower = n
			break
		}
	}

	// test on a follower - it should detect the leader but timeout trying to become leader itself
	becameLeader, addr, err = follower.service.waitUntilLeader(2 * time.Second)
	if err == nil {
		t.Fatal("expected timeout error when follower waits to become leader")
	}
	if becameLeader {
		t.Fatal("follower should not become leader")
	}
	if addr == "" {
		t.Fatal("follower should know leader address even on timeout")
	}
}

func uintPtr(v uint) *uint { return &v }
