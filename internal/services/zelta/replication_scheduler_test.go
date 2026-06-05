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
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

func newReplicationSchedulerTestDB(t *testing.T) *Service {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{})
	return &Service{
		DB:                 db,
		runningReplication: make(map[uint]struct{}),
		runningTransitions: make(map[uint]struct{}),
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		poolDownMisses:     make(map[string]int),
		runningWorkloadOp:  make(map[string]string),
	}
}

func TestBadgerKeyGeneration(t *testing.T) {
	if k := badgerCrashKey(5); k != "repl:crash:5" {
		t.Fatalf("badgerCrashKey(5) = %q", k)
	}
	if k := badgerCrashKey(0); k != "repl:crash:0" {
		t.Fatalf("badgerCrashKey(0) = %q", k)
	}
	if k := badgerDownKey(10); k != "repl:down:10" {
		t.Fatalf("badgerDownKey(10) = %q", k)
	}
}

func TestIntersectSnapshotNames(t *testing.T) {
	common := intersectSnapshotNames(
		[]string{"snap1", "snap2", "snap3"},
		[]string{"snap2", "snap3", "snap4"},
	)
	if len(common) != 2 {
		t.Fatalf("expected 2 common, got %d: %v", len(common), common)
	}
	if common[0] != "snap2" || common[1] != "snap3" {
		t.Fatalf("unexpected order: %v", common)
	}
	if got := intersectSnapshotNames([]string{"a", "b"}, []string{"c", "d"}); len(got) != 0 {
		t.Fatal("no common elements")
	}
	if got := intersectSnapshotNames(nil, []string{"a"}); len(got) != 0 {
		t.Fatal("nil A should return empty")
	}
	if got := intersectSnapshotNames([]string{"a"}, nil); len(got) != 0 {
		t.Fatal("nil B should return empty")
	}
	if got := intersectSnapshotNames([]string{}, []string{}); len(got) != 0 {
		t.Fatal("both empty")
	}
}

func TestReplicationSchedulerTickNoDB(t *testing.T) {
	svc := &Service{runningReplication: make(map[uint]struct{})}
	if err := svc.runReplicationSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick with nil DB should return nil: %v", err)
	}
}

func TestReplicationSchedulerTickNoCluster(t *testing.T) {
	svc := newReplicationSchedulerTestDB(t)
	if err := svc.runReplicationSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick with nil Cluster should return nil: %v", err)
	}
}

func TestAcquireReplicationZeroPolicyID(t *testing.T) {
	s := &Service{runningReplication: make(map[uint]struct{})}
	if !s.acquireReplication(0) {
		t.Fatal("acquire with zero policy ID currently succeeds")
	}
	s.releaseReplication(0)
}

func TestSelfFenceNoCluster(t *testing.T) {
	svc := &Service{runningReplication: make(map[uint]struct{})}
	if err := svc.selfFenceExpiredLeases(context.Background()); err != nil {
		t.Fatalf("self-fence with nil Cluster should return nil: %v", err)
	}
}

func TestFailoverControllerNoCluster(t *testing.T) {
	svc := &Service{runningReplication: make(map[uint]struct{})}
	if err := svc.runFailoverControllerTick(context.Background()); err != nil {
		t.Fatalf("failover tick with nil Cluster should return nil: %v", err)
	}
}

func TestReplicationSchedulerSkipWhenReplicationRunning(t *testing.T) {
	svc := &Service{runningReplication: make(map[uint]struct{})}
	if !svc.acquireReplication(42) {
		t.Fatal("initial acquire should succeed")
	}
	if svc.acquireReplication(42) {
		t.Fatal("second acquire should fail while running")
	}
	svc.releaseReplication(42)
	if !svc.acquireReplication(42) {
		t.Fatal("acquire after release should succeed")
	}
}

// --- Raft-integrated scheduler tests below ---

func setupRaftClusterService(t *testing.T) (*cluster.Service, string, func()) {
	t.Helper()

	localNodeID, err := utils.GetSystemUUID()
	if err != nil {
		t.Fatalf("GetSystemUUID: %v", err)
	}
	localNodeID = strings.TrimSpace(localNodeID)

	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ClusterNode{},
		&clusterModels.Cluster{},
	)
	fsm := clusterModels.NewFSMDispatcher(db)
	clusterModels.RegisterDefaultHandlers(fsm)

	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(localNodeID)
	cfg.Logger = hclog.NewNullLogger()
	cfg.HeartbeatTimeout = 200 * time.Millisecond
	cfg.ElectionTimeout = 200 * time.Millisecond
	cfg.LeaderLeaseTimeout = 100 * time.Millisecond
	cfg.CommitTimeout = 25 * time.Millisecond

	_, transport := raft.NewInmemTransport(raft.ServerAddress(localNodeID))
	r, err := raft.NewRaft(cfg, fsm, raft.NewInmemStore(), raft.NewInmemStore(),
		raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatalf("raft.NewRaft: %v", err)
	}

	bootstrap := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(localNodeID), Address: raft.ServerAddress(localNodeID)},
		},
	}
	if err := r.BootstrapCluster(bootstrap).Error(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.State() == raft.Leader {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.State() != raft.Leader {
		r.Shutdown()
		t.Fatal("raft did not become leader")
	}

	db.Create(&clusterModels.ClusterNode{
		NodeUUID: localNodeID, Hostname: "node1", API: "node1:8181",
		Status: "online",
	})

	return &cluster.Service{DB: db, Raft: r}, localNodeID, func() {
		r.Shutdown()
		transport.Close()
	}
}

func TestReplicationSchedulerTickIntegration(t *testing.T) {
	cSvc, localNodeID, cleanup := setupRaftClusterService(t)
	defer cleanup()
	db := cSvc.DB
	now := time.Now().UTC()

	// --- Policy 1: HA-ineligible, past NextRunAt → should be marked "blocked" ---
	past := now.Add(-1 * time.Hour)
	p1 := &clusterModels.ReplicationPolicy{
		ID: 9001, Name: "ha-ineligible", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9101, SourceNodeID: localNodeID,
		OwnerEpoch: 1, SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled: true, CronExpr: "* * * * *", NextRunAt: &past,
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, p1, nil); err != nil {
		t.Fatalf("seed p1: %v", err)
	}
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 9001, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9101, OwnerNodeID: localNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	// --- Policy 2: first tick (NextRunAt=nil), valid cron → NextRunAt set ---
	p2 := &clusterModels.ReplicationPolicy{
		ID: 9002, Name: "first-tick", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9202, SourceNodeID: localNodeID,
		OwnerEpoch: 1, SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled: true, CronExpr: "0 0 * * *",
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, p2, nil); err != nil {
		t.Fatalf("seed p2: %v", err)
	}
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 9002, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9202, OwnerNodeID: localNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	// --- Policy 3: future NextRunAt → skipped ---
	future := now.Add(24 * time.Hour)
	p3 := &clusterModels.ReplicationPolicy{
		ID: 9003, Name: "future", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9303, SourceNodeID: localNodeID,
		OwnerEpoch: 1, SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled: true, CronExpr: "0 0 * * *", NextRunAt: &future,
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, p3, nil); err != nil {
		t.Fatalf("seed p3: %v", err)
	}

	// --- Policy 4: invalid cron → marked "failed" ---
	p4 := &clusterModels.ReplicationPolicy{
		ID: 9004, Name: "bad-cron", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9404, SourceNodeID: localNodeID,
		OwnerEpoch: 1, SourceMode: clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled: true, CronExpr: "not a valid cron",
	}
	if err := clusterModels.UpsertReplicationPolicyTxn(db, p4, nil); err != nil {
		t.Fatalf("seed p4: %v", err)
	}
	db.Create(&clusterModels.ReplicationLease{
		PolicyID: 9004, GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 9404, OwnerNodeID: localNodeID, OwnerEpoch: 1,
		ExpiresAt: now.Add(1 * time.Hour),
	})

	svc := &Service{
		DB:                 db,
		Cluster:            cSvc,
		runningReplication: make(map[uint]struct{}),
		runningTransitions: make(map[uint]struct{}),
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		poolDownMisses:     make(map[string]int),
		runningWorkloadOp:  make(map[string]string),
	}

	if err := svc.runReplicationSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick failed: %v", err)
	}

	// Policy 1: HA-ineligible → marked blocked
	var r1 clusterModels.ReplicationPolicy
	db.First(&r1, 9001)
	if r1.LastStatus != "blocked" {
		t.Errorf("p1: expected blocked, got %q", r1.LastStatus)
	}
	if r1.LastError == "" {
		t.Error("p1: expected last_error set")
	}
	t.Logf("p1 HA reasons: %s", r1.LastError)

	// Policy 2: first tick → NextRunAt set (and blocked due to single-voter HA)
	var r2 clusterModels.ReplicationPolicy
	db.First(&r2, 9002)
	if r2.NextRunAt == nil {
		t.Error("p2: expected NextRunAt to be set")
	}
	if r2.LastStatus == "" {
		t.Error("p2: expected last_status to be set")
	}
	t.Logf("p2: NextRunAt=%v status=%q", r2.NextRunAt, r2.LastStatus)

	// Policy 3: future NextRunAt → skipped entirely (no status change)
	var r3 clusterModels.ReplicationPolicy
	db.First(&r3, 9003)
	if r3.LastStatus != "" {
		t.Errorf("p3: expected no status change, got %q", r3.LastStatus)
	}

	// Policy 4: invalid cron → marked failed
	var r4 clusterModels.ReplicationPolicy
	db.First(&r4, 9004)
	if r4.LastStatus != "failed" {
		t.Errorf("p4: expected failed, got %q", r4.LastStatus)
	}
	if !strings.Contains(r4.LastError, "invalid_cron_expr") {
		t.Errorf("p4: expected invalid_cron_expr, got %q", r4.LastError)
	}
}
