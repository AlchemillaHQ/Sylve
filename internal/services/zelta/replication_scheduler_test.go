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
)

func newReplicationSchedulerTestDB(t *testing.T) *Service {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationRunOperation{}, &clusterModels.ScheduledRunReceipt{},
		&clusterModels.ScheduledRunResultOutbox{},
	)
	return &Service{
		DB:                 db,
		runningReplication: make(map[uint]struct{}),
		runningTransitions: make(map[uint]struct{}),
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		poolDownMisses:     make(map[string]int),
		failoverWarnings:   make(map[uint]map[string]struct{}),
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

func TestPrepareReplicationRunResumesOnlyWithoutTerminalResult(t *testing.T) {
	service := newReplicationSchedulerTestDB(t)
	now := time.Date(2026, time.August, 1, 4, 33, 0, 0, time.UTC)
	operation := clusterModels.ReplicationRunOperation{
		PolicyID: 8001, Token: "replication:local:running", State: clusterModels.ReplicationRunOperationRunning,
		HolderNodeID: "local", ScheduleRevision: 3, OwnerEpoch: 2,
		Revision: 2, AcquiredAt: now, UpdatedAt: now,
	}
	if err := service.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed running operation: %v", err)
	}

	prepared, execute, err := service.prepareReplicationRunOperation(
		context.Background(), operation.PolicyID, operation.Token, operation.HolderNodeID,
	)
	if err != nil {
		t.Fatalf("prepare interrupted run: %v", err)
	}
	if !execute || prepared == nil || prepared.Token != operation.Token {
		t.Fatalf("interrupted run not resumable: execute=%t operation=%+v", execute, prepared)
	}

	if err := service.DB.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: operation.Token, Kind: clusterModels.ScheduledRunKindReplication,
		ObjectID: operation.PolicyID, Payload: `{}`,
	}).Error; err != nil {
		t.Fatalf("seed terminal outbox: %v", err)
	}
	_, execute, err = service.prepareReplicationRunOperation(
		context.Background(), operation.PolicyID, operation.Token, operation.HolderNodeID,
	)
	if err != nil {
		t.Fatalf("prepare terminalized run: %v", err)
	}
	if execute {
		t.Fatal("running token with a terminal outbox was allowed to execute again")
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

func TestReplicationSchedulerTickAppliesControlledHA(t *testing.T) {
	service := newReplicationSchedulerTestDB(t)
	service.Cluster = &cluster.Service{DB: service.DB, NodeID: "node-local"}
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	policies := []clusterModels.ReplicationPolicy{
		{
			ID: 9001, Name: "ha-ineligible", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 9101, SourceNodeID: "node-local", OwnerEpoch: 1,
			Enabled: true, CronExpr: "* * * * *", NextRunAt: &past,
		},
		{
			ID: 9002, Name: "first-tick", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 9202, SourceNodeID: "node-local", OwnerEpoch: 1,
			Enabled: true, CronExpr: "0 0 * * *",
		},
		{
			ID: 9003, Name: "future", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 9303, SourceNodeID: "node-local", OwnerEpoch: 1,
			Enabled: true, CronExpr: "0 0 * * *", NextRunAt: &future,
		},
		{
			ID: 9004, Name: "bad-cron", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 9404, SourceNodeID: "node-local", OwnerEpoch: 1,
			Enabled: true, CronExpr: "not a valid cron",
		},
	}
	for i := range policies {
		policy := &policies[i]
		if err := clusterModels.UpsertReplicationPolicyTxn(service.DB, policy, nil); err != nil {
			t.Fatalf("seed policy %d: %v", policy.ID, err)
		}
		lease := clusterModels.ReplicationLease{
			PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
			OwnerNodeID: "node-local", OwnerEpoch: policy.OwnerEpoch,
			ExpiresAt: now.Add(time.Hour), Version: 1,
		}
		if err := service.DB.Create(&lease).Error; err != nil {
			t.Fatalf("seed lease %d: %v", policy.ID, err)
		}
		if err := service.validateLocalReplicationPolicyLease(policy); err == nil ||
			!strings.Contains(err.Error(), "replication_lease_expired") {
			t.Fatalf("prime lease %d: %v", policy.ID, err)
		}
		if err := service.DB.Model(&clusterModels.ReplicationLease{}).
			Where("policy_id = ?", policy.ID).Update("version", 2).Error; err != nil {
			t.Fatalf("advance lease %d: %v", policy.ID, err)
		}
	}

	evaluate := func(policy *clusterModels.ReplicationPolicy) cluster.ReplicationPolicyHAEvaluation {
		if policy.ID == 9001 || policy.ID == 9002 {
			return cluster.ReplicationPolicyHAEvaluation{
				Reasons: []string{cluster.ReplicationHAReasonMinThreeVoters},
			}
		}
		return cluster.ReplicationPolicyHAEvaluation{Eligible: true}
	}
	if err := service.runReplicationSchedulerTickWithHA(t.Context(), evaluate); err != nil {
		t.Fatalf("scheduler tick: %v", err)
	}

	var blocked clusterModels.ReplicationPolicy
	if err := service.DB.First(&blocked, 9001).Error; err != nil {
		t.Fatal(err)
	}
	if blocked.LastStatus != "blocked" ||
		!strings.Contains(blocked.LastError, cluster.ReplicationHAReasonMinThreeVoters) {
		t.Fatalf("blocked policy = %+v", blocked)
	}
	var first clusterModels.ReplicationPolicy
	if err := service.DB.First(&first, 9002).Error; err != nil {
		t.Fatal(err)
	}
	if first.NextRunAt == nil || first.LastStatus != "blocked" {
		t.Fatalf("first schedule = %+v", first)
	}
	var untouched clusterModels.ReplicationPolicy
	if err := service.DB.First(&untouched, 9003).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.LastStatus != "" || !untouched.NextRunAt.Equal(future) {
		t.Fatalf("future policy changed: %+v", untouched)
	}
	var invalid clusterModels.ReplicationPolicy
	if err := service.DB.First(&invalid, 9004).Error; err != nil {
		t.Fatal(err)
	}
	if invalid.LastStatus != "failed" || invalid.LastError != "invalid_cron_expr" {
		t.Fatalf("invalid cron policy = %+v", invalid)
	}
}
