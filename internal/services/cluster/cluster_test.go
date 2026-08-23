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
	"github.com/hashicorp/raft"
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

func TestClusterRaftNodeDetails(t *testing.T) {
	configuration := raft.Configuration{Servers: []raft.Server{
		{ID: "node-1", Address: "127.0.0.1:7001", Suffrage: raft.Voter},
		{ID: "node-2", Address: "127.0.0.1:7002", Suffrage: raft.Nonvoter},
	}}
	nodes := clusterRaftNodeDetails(
		configuration,
		"127.0.0.1:7001",
		"node-1",
		map[string][]uint{"node-1": {10, 20}},
	)
	if len(nodes) != 2 {
		t.Fatalf("node count = %d, want 2", len(nodes))
	}
	if !nodes[0].IsLeader || nodes[0].Suffrage != "voter" || len(nodes[0].GuestIDs) != 2 {
		t.Fatalf("leader details = %+v", nodes[0])
	}
	if nodes[1].IsLeader || nodes[1].Suffrage != "nonvoter" {
		t.Fatalf("follower details = %+v", nodes[1])
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
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
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
	seedDB("backup_target_restore_operations", map[string]any{
		"token": "target-restore:node-1:clear", "target_id": 1, "holder_node_id": "node-1",
		"destination_dataset": "zroot/restored", "request_payload": `{"snapshot":"@clear"}`,
		"state": "queued", "revision": 1, "acquired_at": time.Now(), "updated_at": time.Now(),
	})
	seedDB("replication_events", map[string]any{"id": 1, "event_type": "run", "status": "success"})
	seedDB("replication_transition_events", map[string]any{
		"id": 2, "transition_run_id": "transition-clear", "event_type": "failover", "status": "success",
	})
	seedDB("replication_guest_operations", map[string]any{"guest_type": "vm", "guest_id": 1, "operation": "migration", "state": "active", "token": "migration:n1:1", "owner_node_id": "n1", "target_node_id": "n2", "task_id": 1, "acquired_at": time.Now().Add(-time.Minute)})
	seedDB("replication_guest_operation_receipts", map[string]any{"token": "migration:n1:1", "guest_type": "vm", "guest_id": 1, "operation": "migration", "owner_node_id": "n1", "target_node_id": "n2", "task_id": 1, "acquired_at": time.Now().Add(-time.Minute), "completed_at": time.Now()})
	seedDB("replication_leases", map[string]any{"id": 1, "policy_id": 1, "guest_type": "vm", "guest_id": 1, "owner_node_id": "n1", "owner_epoch": 1, "expires_at": time.Now().Add(time.Hour)})
	seedDB("replication_policies", map[string]any{"id": 1, "name": "pol1", "guest_type": "vm", "guest_id": 1, "cron_expr": "* * * * *", "owner_epoch": 1})
	seedDB("replication_policy_targets", map[string]any{"id": 1, "policy_id": 1, "node_id": "n2", "weight": 100})
	seedDB("cluster_ssh_identities", map[string]any{"id": 1, "node_uuid": "uuid-1", "ssh_host": "h1", "public_key": "k1"})
	if err := s.ClearClusteredData(); err != nil {
		t.Fatalf("ClearClusteredData failed: %v", err)
	}

	tables := []string{
		"cluster_notes", "cluster_options", "backup_target_restore_operations", "backup_jobs",
		"backup_targets", "replication_transition_events", "replication_guest_operations", "replication_guest_operation_receipts", "replication_leases",
		"replication_policy_targets", "replication_policies", "cluster_ssh_identities",
	}
	for _, table := range tables {
		var count int64
		db.Table(table).Count(&count)
		if count != 0 {
			t.Fatalf("expected table %s to be empty, got %d rows", table, count)
		}
	}
	var localEventCount int64
	db.Model(&clusterModels.ReplicationEvent{}).Count(&localEventCount)
	if localEventCount != 1 {
		t.Fatalf("expected node-local replication history to survive cluster reset, got %d rows", localEventCount)
	}
	var localBackupEventCount int64
	db.Model(&clusterModels.BackupEvent{}).Count(&localBackupEventCount)
	if localBackupEventCount != 1 {
		t.Fatalf("expected node-local backup history to survive cluster reset, got %d rows", localBackupEventCount)
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

func TestResyncClusterStateRequiresRaft(t *testing.T) {
	s := &Service{Raft: nil}
	if err := s.ResyncClusterState(); err == nil {
		t.Fatal("expected error when Raft is nil")
	}
}

func TestIntegrationRaftResyncClusterStateRejectsFollower(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 2,
		&clusterModels.ClusterNote{}, &clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{}, &clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{}, &clusterModels.ReplicationEvent{},
	)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	var follower *clusterRaftTestNode
	for _, node := range nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}

	if err := follower.service.ResyncClusterState(); err == nil {
		t.Fatal("expected error when calling ResyncClusterState on follower")
	}
}

func TestIntegrationRaftSnapshotPreClusterState(t *testing.T) {
	allModels := []any{
		&clusterModels.ClusterNote{}, &clusterModels.ClusterOption{},
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{},
		&clusterModels.BackupTargetNodeReadiness{},
		&clusterModels.BackupJob{}, &clusterModels.BackupJobOperation{},
		&clusterModels.BackupJobRunnerRebind{}, &clusterModels.BackupJobRunnerRebindItem{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{}, &clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
		&clusterModels.ReplicationEvent{},
	}

	nodes := setupClusterRaftTestNodes(t, 1, allModels...)

	leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)
	if err := leader.service.DB.Create(&clusterModels.ReplicationEvent{
		ID: 1, EventType: "replication", Status: "success",
		Message: "local-history-" + leader.id, StartedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed local replication history on %s: %v", leader.id, err)
	}

	// seed pre-existing state on the leader's DB (simulating pre-cluster data)
	seedDB := leader.service.DB

	note := clusterModels.ClusterNote{ID: 1, Title: "note1", Content: "content1"}
	seedDB.Create(&note)
	option := clusterModels.ClusterOption{ID: 1, KeyboardLayout: "de"}
	seedDB.Create(&option)

	target := clusterModels.BackupTarget{
		ID: 10, Name: "bk-target", SSHHost: "host@remote",
		SSHPort: 22, BackupRoot: "tank/backups", CreateBackupRoot: true,
		Enabled: true, SSHKey: "some-key",
	}
	seedDB.Create(&target)
	readinessAt := time.Now().UTC()
	readinessUntil := readinessAt.Add(time.Hour)
	seedDB.Create(&clusterModels.BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "node-1",
		TargetFingerprint:   clusterModels.BackupTargetConnectivityFingerprint(&target),
		ValidationSucceeded: true, LastVerifiedAt: readinessAt, ReadyUntil: &readinessUntil,
		Revision: 7, RaftAppliedIndex: 88, UpdatedAt: readinessAt,
	})

	seedDB.Create(&clusterModels.BackupJob{
		ID: 20, Name: "bk-job", TargetID: 10, Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 2 * * *", Enabled: true,
		ScheduleRevision: 3,
	})
	operationAt := time.Now().UTC()
	seedDB.Create(&clusterModels.BackupJobOperation{
		JobID: 20, Token: "backup:node-1:backfill", Operation: clusterModels.BackupJobOperationBackup,
		State: clusterModels.BackupJobOperationRunning, HolderNodeID: "node-1",
		Scheduled: true, ScheduleRevision: 3,
		Revision: 2, AcquiredAt: operationAt, UpdatedAt: operationAt,
	})
	seedDB.Create(&clusterModels.BackupTargetRestoreOperation{
		Token: "target-restore:node-1:backfill", TargetID: target.ID, HolderNodeID: "node-1",
		DestinationDataset: "zroot/restored", RequestPayload: `{"targetId":10,"snapshot":"@backfill"}`,
		State:    clusterModels.BackupTargetRestoreOperationCompleted,
		Revision: 4, AcquiredAt: operationAt, UpdatedAt: operationAt,
	})

	seedDB.Create(&clusterModels.ReplicationPolicy{
		ID: 30, Name: "rep-pol", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "node-1",
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1, Enabled: true, ScheduleRevision: 5,
	})
	seedDB.Create(&clusterModels.ReplicationPolicyTarget{
		PolicyID: 30, NodeID: "node-2", Weight: 100,
	})
	seedDB.Create(&clusterModels.ReplicationRunOperation{
		PolicyID: 30, Token: "replication:node-1:backfill",
		State: clusterModels.ReplicationRunOperationQueued, HolderNodeID: "node-1",
		Scheduled: true, ScheduleRevision: 5, OwnerEpoch: 1,
		Revision: 1, AcquiredAt: operationAt, UpdatedAt: operationAt,
	})
	seedDB.Create(&clusterModels.ScheduledRunReceipt{
		Token: "backup:node-1:completed-backfill", Kind: clusterModels.ScheduledRunKindBackup,
		ObjectID: 19, HolderNodeID: "node-1", ScheduleRevision: 2,
		Status: "success", Applied: true, CompletedAt: operationAt,
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

	seedDB.Create(&clusterModels.ReplicationTransitionEvent{
		ID: 40, TransitionRunID: "transition-backfill", EventType: "failover", Status: "success",
		PolicyID: uintPtr(30), SourceNodeID: "node-1", TargetNodeID: "node-2",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 100,
	})

	// backfill
	if err := leader.service.snapshotPreClusterState(); err != nil {
		t.Fatalf("snapshotPreClusterState failed: %v", err)
	}

	joiner := newClusterRaftTestNode(t, "node-2", allModels...)
	nodes = append(nodes, joiner)
	leader.transport.Connect(joiner.addr, joiner.transport)
	joiner.transport.Connect(leader.addr, leader.transport)
	if err := joiner.service.DB.Create(&clusterModels.ReplicationEvent{
		ID: 1, EventType: "replication", Status: "success",
		Message: "local-history-" + joiner.id, StartedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed local replication history on %s: %v", joiner.id, err)
	}
	if err := leader.raft.AddNonvoter(
		raft.ServerID(joiner.id),
		joiner.addr,
		0,
		5*time.Second,
	).Error(); err != nil {
		t.Fatalf("add snapshot joiner: %v", err)
	}
	// Verify a fresh member installs the canonical snapshot while keeping its
	// node-local history.
	waitForClusterCondition(t, 8*time.Second, "pre-cluster snapshot install", func() bool {
		for _, node := range nodes {
			var noteCount int64
			node.service.DB.Model(&clusterModels.ClusterNote{}).Count(&noteCount)
			if noteCount != 1 {
				return false
			}
			var replicatedNote clusterModels.ClusterNote
			if err := node.service.DB.First(&replicatedNote, note.ID).Error; err != nil ||
				!replicatedNote.CreatedAt.Equal(note.CreatedAt) ||
				!replicatedNote.UpdatedAt.Equal(note.UpdatedAt) {
				return false
			}

			var optCount int64
			node.service.DB.Model(&clusterModels.ClusterOption{}).Count(&optCount)
			if optCount != 1 {
				return false
			}
			var replicatedOption clusterModels.ClusterOption
			if err := node.service.DB.First(&replicatedOption, option.ID).Error; err != nil ||
				!replicatedOption.CreatedAt.Equal(option.CreatedAt) ||
				!replicatedOption.UpdatedAt.Equal(option.UpdatedAt) {
				return false
			}

			var tgtCount int64
			node.service.DB.Model(&clusterModels.BackupTarget{}).Count(&tgtCount)
			if tgtCount != 1 {
				return false
			}
			var replicatedTarget clusterModels.BackupTarget
			if err := node.service.DB.First(&replicatedTarget, target.ID).Error; err != nil ||
				!replicatedTarget.CreatedAt.Equal(target.CreatedAt) ||
				!replicatedTarget.UpdatedAt.Equal(target.UpdatedAt) {
				return false
			}

			var readiness clusterModels.BackupTargetNodeReadiness
			if err := node.service.DB.Where("target_id = ? AND node_id = ?", target.ID, "node-1").
				First(&readiness).Error; err != nil || readiness.Revision != 7 ||
				readiness.RaftAppliedIndex != 88 || readiness.TargetFingerprint != clusterModels.BackupTargetConnectivityFingerprint(&target) {
				return false
			}

			var jobCount int64
			node.service.DB.Model(&clusterModels.BackupJob{}).Count(&jobCount)
			if jobCount != 1 {
				return false
			}
			var operation clusterModels.BackupJobOperation
			if err := node.service.DB.Where("job_id = ?", 20).First(&operation).Error; err != nil ||
				operation.Token != "backup:node-1:backfill" ||
				operation.State != clusterModels.BackupJobOperationRunning ||
				!operation.Scheduled || operation.ScheduleRevision != 3 {
				return false
			}
			var targetRestoreOperation clusterModels.BackupTargetRestoreOperation
			if err := node.service.DB.Where("token = ?", "target-restore:node-1:backfill").
				First(&targetRestoreOperation).Error; err != nil ||
				targetRestoreOperation.State != clusterModels.BackupTargetRestoreOperationCompleted ||
				targetRestoreOperation.Revision != 4 ||
				targetRestoreOperation.DestinationDataset != "zroot/restored" {
				return false
			}

			var polCount int64
			node.service.DB.Model(&clusterModels.ReplicationPolicy{}).Count(&polCount)
			if polCount != 1 {
				return false
			}
			var replicationRun clusterModels.ReplicationRunOperation
			if err := node.service.DB.Where("policy_id = ?", 30).First(&replicationRun).Error; err != nil ||
				replicationRun.Token != "replication:node-1:backfill" ||
				replicationRun.ScheduleRevision != 5 || replicationRun.OwnerEpoch != 1 {
				return false
			}
			var scheduledReceipt clusterModels.ScheduledRunReceipt
			if err := node.service.DB.Where("token = ?", "backup:node-1:completed-backfill").
				First(&scheduledReceipt).Error; err != nil || !scheduledReceipt.Applied {
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
			node.service.DB.Model(&clusterModels.ReplicationTransitionEvent{}).Count(&evtCount)
			if evtCount != 1 {
				return false
			}
			var localEvent clusterModels.ReplicationEvent
			if err := node.service.DB.First(&localEvent, 1).Error; err != nil ||
				localEvent.Message != "local-history-"+node.id {
				return false
			}
		}
		return true
	})
}

func TestIntegrationRaftWaitUntilLeader(t *testing.T) {
	nodes := setupClusterRaftTestNodes(t, 3)

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

	var follower *clusterRaftTestNode
	for _, n := range nodes {
		if n.id != leader.id {
			follower = n
			break
		}
	}

	becameLeader, addr, err = follower.service.waitUntilLeader(3 * raftLeaderPollInterval)
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
