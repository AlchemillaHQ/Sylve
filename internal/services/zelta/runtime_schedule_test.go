// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

type disposableReplicationGuestDriver struct {
	sourceCalls int
	datasets    []string
}

func (d *disposableReplicationGuestDriver) sourceDatasets(context.Context, uint) ([]string, error) {
	d.sourceCalls++
	return append([]string(nil), d.datasets...), nil
}

func (*disposableReplicationGuestDriver) activate(context.Context, uint, string, bool) error {
	return nil
}

func (*disposableReplicationGuestDriver) demote(context.Context, uint) error {
	return nil
}

func (*disposableReplicationGuestDriver) selfFence(context.Context, uint, uint, string, string, string) {
}

func TestFollowerOwnerForwardsExactReplicationClaimAndRepublishesAfterApplyLag(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	owner := fx.Nodes[0]
	initialLeader := fx.Nodes[1]
	finalLeader := fx.Nodes[2]
	if err := owner.raft.LeadershipTransferToServer(
		raft.ServerID(initialLeader.id), initialLeader.addr,
	).Error(); err != nil {
		t.Fatalf("transfer leadership: %v", err)
	}
	fx.WaitForCondition(8*time.Second, "leadership transfer", func() bool {
		return initialLeader.raft.State() == raft.Leader && owner.raft.State() == raft.Follower
	})

	now := time.Now().UTC()
	nextRunAt := now.Add(5 * time.Minute)
	policy := clusterModels.ReplicationPolicy{
		ID: 8801, Name: "follower-owner-manual-run",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 107,
		SourceNodeID: owner.id, ActiveNodeID: owner.id, OwnerEpoch: 3,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:      true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		CronExpr: "*/5 * * * *", NextRunAt: &nextRunAt, ScheduleRevision: 7,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: initialLeader.id, Weight: 200},
			{NodeID: finalLeader.id, Weight: 100},
		},
	}
	for _, node := range fx.Nodes {
		nodePolicy := policy
		nodePolicy.Targets = append([]clusterModels.ReplicationPolicyTarget(nil), policy.Targets...)
		if err := clusterModels.UpsertReplicationPolicyTxn(node.db, &nodePolicy, nodePolicy.Targets); err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
		if err := node.db.Create(&clusterModels.ReplicationLease{
			PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
			OwnerNodeID: owner.id, OwnerEpoch: policy.OwnerEpoch,
			ExpiresAt: now.Add(time.Hour), Version: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease on %s: %v", node.id, err)
		}
	}

	service := newTestZeltaService(owner.db)
	service.Cluster = owner.cService
	if err := service.validateLocalReplicationPolicyLease(&policy); err == nil ||
		!strings.Contains(err.Error(), "replication_lease_expired") {
		t.Fatalf("startup lease was not a fenced baseline: %v", err)
	}
	for _, node := range fx.Nodes {
		if err := node.db.Model(&clusterModels.ReplicationLease{}).
			Where("policy_id = ?", policy.ID).Update("version", 2).Error; err != nil {
			t.Fatalf("advance lease on %s: %v", node.id, err)
		}
	}
	var queued []replicationJobPayload
	service.replicationOperationEnqueue = func(_ context.Context, name string, payload any) error {
		if name != replicationJobQueueName {
			t.Fatalf("queue name=%q, want %q", name, replicationJobQueueName)
		}
		claim, ok := payload.(replicationJobPayload)
		if !ok {
			t.Fatalf("queue payload type=%T", payload)
		}
		queued = append(queued, claim)
		return nil
	}

	forwardAttempts := 0
	claimToken := ""
	service.replicationRunClaimForward = func(
		_ context.Context,
		leaderNodeID string,
		decision clusterModels.ReplicationPolicyScheduleDecision,
	) error {
		if decision.HolderNodeID != owner.id || decision.PolicyID != policy.ID || decision.Scheduled {
			t.Fatalf("forwarded claim lost owner/manual fence: %+v", decision)
		}
		forwardAttempts++
		if forwardAttempts == 1 {
			if leaderNodeID != initialLeader.id {
				t.Fatalf("first claim target=%q, want %q", leaderNodeID, initialLeader.id)
			}
			claimToken = decision.ClaimToken
			if err := initialLeader.raft.LeadershipTransferToServer(
				raft.ServerID(finalLeader.id), finalLeader.addr,
			).Error(); err != nil {
				t.Fatalf("change leader during claim: %v", err)
			}
			fx.WaitForCondition(8*time.Second, "claim-time leadership change", func() bool {
				_, observedLeaderID := owner.raft.LeaderWithID()
				return finalLeader.raft.State() == raft.Leader &&
					strings.TrimSpace(string(observedLeaderID)) == finalLeader.id
			})
			return fmt.Errorf("replication_control_replication-run-claim_failed_status_503: not_leader")
		}
		if leaderNodeID != finalLeader.id {
			t.Fatalf("attempt %d claim target=%q, want %q", forwardAttempts, leaderNodeID, finalLeader.id)
		}
		if forwardAttempts == 2 {
			if decision.ClaimToken != claimToken {
				t.Fatalf("claim retry changed token: first=%q retry=%q", claimToken, decision.ClaimToken)
			}
			for _, node := range fx.Nodes[1:] {
				owner.transport.Disconnect(node.addr)
				node.transport.Disconnect(owner.addr)
			}
		}
		return finalLeader.cService.ApplyReplicationPolicyScheduleDecision(decision, false)
	}

	if err := service.EnqueueReplicationPolicyRun(context.Background(), policy.ID); err != nil {
		t.Fatalf("enqueue through follower owner: %v", err)
	}
	if len(queued) != 1 || queued[0].PolicyID != policy.ID ||
		queued[0].HolderNodeID != owner.id || queued[0].OperationToken == "" {
		t.Fatalf("unexpected initial queue publication: %+v", queued)
	}

	var ownerOperationCount int64
	if err := owner.db.Model(&clusterModels.ReplicationRunOperation{}).
		Where("policy_id = ?", policy.ID).Count(&ownerOperationCount).Error; err != nil {
		t.Fatalf("count lagging owner operations: %v", err)
	}
	if ownerOperationCount != 0 {
		t.Fatalf("owner FSM was not delayed: operations=%d", ownerOperationCount)
	}
	var leaderOperation clusterModels.ReplicationRunOperation
	if err := finalLeader.db.First(&leaderOperation, "policy_id = ?", policy.ID).Error; err != nil {
		t.Fatalf("leader did not commit claim: %v", err)
	}
	if leaderOperation.Token != queued[0].OperationToken || leaderOperation.HolderNodeID != owner.id {
		t.Fatalf("committed claim differs from queued claim: operation=%+v queued=%+v", leaderOperation, queued[0])
	}

	for _, source := range fx.Nodes {
		for _, target := range fx.Nodes {
			if source == target {
				continue
			}
			source.transport.Connect(target.addr, target.transport)
		}
	}
	fx.WaitForCondition(8*time.Second, "owner FSM catch-up", func() bool {
		var operation clusterModels.ReplicationRunOperation
		return owner.db.First(&operation, "policy_id = ?", policy.ID).Error == nil &&
			operation.Token == leaderOperation.Token
	})
	fx.WaitForCondition(8*time.Second, "owner leader discovery", func() bool {
		_, leaderID := owner.raft.LeaderWithID()
		return strings.TrimSpace(string(leaderID)) != ""
	})

	if err := service.RepublishQueuedReplicationRuns(context.Background()); err != nil {
		t.Fatalf("republish caught-up claim: %v", err)
	}
	if len(queued) != 2 || queued[1] != queued[0] {
		t.Fatalf("republisher changed exact claim: %+v", queued)
	}

	err := service.EnqueueReplicationPolicyRun(context.Background(), policy.ID)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "already_running") {
		t.Fatalf("duplicate manual run was not rejected: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("duplicate run published another queue item: %+v", queued)
	}
	if forwardAttempts != 3 {
		t.Fatalf("forward attempts=%d, want two exact retries plus one duplicate claim", forwardAttempts)
	}
	var operationCount int64
	if err := finalLeader.db.Model(&clusterModels.ReplicationRunOperation{}).
		Where("policy_id = ?", policy.ID).Count(&operationCount).Error; err != nil {
		t.Fatalf("count leader operations: %v", err)
	}
	if operationCount != 1 {
		t.Fatalf("operation count=%d, want 1", operationCount)
	}

	rejected := []struct {
		name            string
		id              uint
		enabled         bool
		activeNodeID    string
		ownerEpoch      uint64
		leaseOwnerEpoch uint64
		leaseExpiresAt  time.Time
		wantError       string
	}{
		{
			name: "non-runnable", id: 8810, enabled: false, activeNodeID: owner.id,
			ownerEpoch: 1, leaseOwnerEpoch: 1, leaseExpiresAt: now.Add(time.Hour),
			wantError: "replication_policy_not_runnable",
		},
		{
			name: "expired local lease", id: 8811, enabled: true, activeNodeID: owner.id,
			ownerEpoch: 2, leaseOwnerEpoch: 2, leaseExpiresAt: now.Add(-time.Minute),
			wantError: "replication_lease_expired",
		},
		{
			name: "stale lease epoch", id: 8812, enabled: true, activeNodeID: owner.id,
			ownerEpoch: 3, leaseOwnerEpoch: 2, leaseExpiresAt: now.Add(time.Hour),
			wantError: "replication_lease_epoch_mismatch",
		},
		{
			name: "different active owner", id: 8813, enabled: true, activeNodeID: initialLeader.id,
			ownerEpoch: 1, leaseOwnerEpoch: 1, leaseExpiresAt: now.Add(time.Hour),
			wantError: "replication_policy_not_owned_by_local_node",
		},
	}
	baselineForwardAttempts := forwardAttempts
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			rejectedPolicy := clusterModels.ReplicationPolicy{
				ID: test.id, Name: test.name,
				GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: test.id,
				SourceNodeID: test.activeNodeID, ActiveNodeID: test.activeNodeID,
				OwnerEpoch: test.ownerEpoch, SourceMode: clusterModels.ReplicationSourceModeFollowActive,
				FailoverMode: clusterModels.ReplicationFailoverManual,
				Enabled:      test.enabled, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
				CronExpr: "*/5 * * * *",
				Targets: []clusterModels.ReplicationPolicyTarget{
					{NodeID: initialLeader.id, Weight: 200},
					{NodeID: finalLeader.id, Weight: 100},
				},
			}
			if err := clusterModels.UpsertReplicationPolicyTxn(
				owner.db, &rejectedPolicy, rejectedPolicy.Targets,
			); err != nil {
				t.Fatalf("seed rejected policy: %v", err)
			}
			if err := owner.db.Create(&clusterModels.ReplicationLease{
				PolicyID: test.id, GuestType: rejectedPolicy.GuestType, GuestID: rejectedPolicy.GuestID,
				OwnerNodeID: test.activeNodeID, OwnerEpoch: test.leaseOwnerEpoch,
				ExpiresAt: test.leaseExpiresAt,
			}).Error; err != nil {
				t.Fatalf("seed rejected lease: %v", err)
			}

			err := service.EnqueueReplicationPolicyRun(context.Background(), test.id)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantError) {
				t.Fatalf("error=%v, want marker %q", err, test.wantError)
			}
			for _, node := range fx.Nodes {
				var count int64
				if err := node.db.Model(&clusterModels.ReplicationRunOperation{}).
					Where("policy_id = ?", test.id).Count(&count).Error; err != nil {
					t.Fatalf("count rejected operations on %s: %v", node.id, err)
				}
				if count != 0 {
					t.Fatalf("rejected policy created %d operations on %s", count, node.id)
				}
			}
		})
	}
	if forwardAttempts != baselineForwardAttempts {
		t.Fatalf("locally rejected claims were forwarded: before=%d after=%d", baselineForwardAttempts, forwardAttempts)
	}
}

func TestCapturedReplicationQueueTokenExecutesOnceAndCreatesOneEvent(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	owner := fx.Nodes[0]
	if owner.raft.State() != raft.Leader {
		t.Fatalf("execution owner state=%s, want leader for deterministic result finalization", owner.raft.State())
	}
	if err := owner.db.AutoMigrate(&clusterModels.ClusterSSHIdentity{}); err != nil {
		t.Fatalf("migrate SSH identity table: %v", err)
	}
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	now := time.Now().UTC()
	nextRunAt := now.Add(5 * time.Minute)
	policy := clusterModels.ReplicationPolicy{
		ID: 8890, Name: "captured-token-single-execution",
		GuestType: clusterModels.ReplicationGuestTypeJail, GuestID: 8890,
		SourceNodeID: owner.id, ActiveNodeID: owner.id, OwnerEpoch: 4,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		Enabled:      true, ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		CronExpr: "*/5 * * * *", NextRunAt: &nextRunAt, ScheduleRevision: 9,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.Nodes[1].id, Weight: 200},
			{NodeID: fx.Nodes[2].id, Weight: 100},
		},
	}
	for _, node := range fx.Nodes {
		nodePolicy := policy
		nodePolicy.Targets = append([]clusterModels.ReplicationPolicyTarget(nil), policy.Targets...)
		if err := clusterModels.UpsertReplicationPolicyTxn(node.db, &nodePolicy, nodePolicy.Targets); err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
		if err := node.db.Create(&clusterModels.ReplicationLease{
			PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
			OwnerNodeID: owner.id, OwnerEpoch: policy.OwnerEpoch,
			ExpiresAt: now.Add(time.Hour), Version: 1,
		}).Error; err != nil {
			t.Fatalf("seed lease on %s: %v", node.id, err)
		}
	}

	service := newTestZeltaService(owner.db)
	service.Cluster = owner.cService
	if err := service.validateLocalReplicationPolicyLease(&policy); err == nil ||
		!strings.Contains(err.Error(), "replication_lease_expired") {
		t.Fatalf("startup lease was not a fenced baseline: %v", err)
	}
	for _, node := range fx.Nodes {
		if err := node.db.Model(&clusterModels.ReplicationLease{}).
			Where("policy_id = ?", policy.ID).Update("version", 2).Error; err != nil {
			t.Fatalf("advance lease on %s: %v", node.id, err)
		}
	}
	driver := &disposableReplicationGuestDriver{
		datasets: []string{"disposable/sylve/jails/8890"},
	}
	service.replicationGuestDriverFactory = func(guestType string) (replicationGuestDriver, error) {
		if guestType != clusterModels.ReplicationGuestTypeJail {
			return nil, fmt.Errorf("unexpected guest type %q", guestType)
		}
		return driver, nil
	}

	queued := make([]replicationJobPayload, 0, 1)
	service.replicationOperationEnqueue = func(_ context.Context, name string, payload any) error {
		if name != replicationJobQueueName {
			t.Fatalf("queue name=%q, want %q", name, replicationJobQueueName)
		}
		claim, ok := payload.(replicationJobPayload)
		if !ok {
			t.Fatalf("queue payload type=%T", payload)
		}
		queued = append(queued, claim)
		return nil
	}

	if err := service.EnqueueReplicationPolicyRun(context.Background(), policy.ID); err != nil {
		t.Fatalf("capture replication run: %v", err)
	}
	if len(queued) != 1 || queued[0].PolicyID != policy.ID ||
		queued[0].OperationToken == "" || queued[0].HolderNodeID != owner.id {
		t.Fatalf("captured queue payload=%+v", queued)
	}
	captured := queued[0]
	if err := service.handleReplicationJob(context.Background(), captured); err != nil {
		t.Fatalf("execute captured queue payload: %v", err)
	}
	if driver.sourceCalls != 1 {
		t.Fatalf("driver executions=%d, want 1", driver.sourceCalls)
	}

	var events []clusterModels.ReplicationEvent
	if err := owner.db.Where("policy_id = ?", policy.ID).Order("id ASC").Find(&events).Error; err != nil {
		t.Fatalf("load replication events: %v", err)
	}
	if len(events) != 1 || events[0].CompletedAt == nil || events[0].Status == replicationEventStatusRunning {
		t.Fatalf("replication events=%+v, want one terminal event", events)
	}
	var receiptCount int64
	if err := owner.db.Model(&clusterModels.ScheduledRunReceipt{}).
		Where("token = ? AND kind = ? AND object_id = ?",
			captured.OperationToken, clusterModels.ScheduledRunKindReplication, policy.ID,
		).Count(&receiptCount).Error; err != nil {
		t.Fatalf("count exact execution receipt: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("exact execution receipts=%d, want 1", receiptCount)
	}
	var operationCount int64
	if err := owner.db.Model(&clusterModels.ReplicationRunOperation{}).
		Where("policy_id = ?", policy.ID).Count(&operationCount).Error; err != nil {
		t.Fatalf("count completed operation: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("completed operation rows=%d, want 0", operationCount)
	}

	if err := service.handleReplicationJob(context.Background(), captured); err != nil {
		t.Fatalf("replay captured queue payload: %v", err)
	}
	if driver.sourceCalls != 1 {
		t.Fatalf("replayed token executed driver %d times", driver.sourceCalls)
	}
	if err := owner.db.Where("policy_id = ?", policy.ID).Find(&events).Error; err != nil {
		t.Fatalf("reload replication events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("replayed token created %d events, want 1", len(events))
	}
}

func TestStandaloneReplicationClaimAppliesLocally(t *testing.T) {
	service := newReplicationSchedulerTestDB(t)
	now := time.Now().UTC()
	policy := clusterModels.ReplicationPolicy{
		ID: 8802, Name: "standalone-manual-run",
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 108,
		OwnerEpoch: 1, Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
	}
	if err := service.DB.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	handle, err := service.acquireReplicationRunOperation(
		context.Background(), &policy, false, now, nil, nil, true,
	)
	if err != nil {
		t.Fatalf("acquire standalone claim: %v", err)
	}
	if handle.Token == "" || handle.HolderNodeID != "local" || handle.PolicyID != policy.ID {
		t.Fatalf("unexpected standalone handle: %+v", handle)
	}
	var operation clusterModels.ReplicationRunOperation
	if err := service.DB.First(&operation, "policy_id = ?", policy.ID).Error; err != nil {
		t.Fatalf("load standalone operation: %v", err)
	}
	if operation.Token != handle.Token || operation.HolderNodeID != handle.HolderNodeID ||
		operation.State != clusterModels.ReplicationRunOperationQueued {
		t.Fatalf("unexpected standalone operation: %+v", operation)
	}
}

func TestBackupClaimSurvivesPublishFailureAndRestartRepublishesSameToken(t *testing.T) {
	service := newSchedulerTestDB(t)
	target := clusterModels.BackupTarget{
		ID: 1, Name: "durable-publish", SSHHost: "localhost", BackupRoot: "tank/backups", Enabled: true,
	}
	if err := service.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	due := time.Now().UTC().Add(-time.Minute)
	job := clusterModels.BackupJob{
		ID: 51, Name: "durable-publish", TargetID: target.ID,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "* * * * *", Enabled: true, NextRunAt: &due,
	}
	if err := service.DB.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	service.backupOperationEnqueue = func(context.Context, string, any) error {
		return fmt.Errorf("queue unavailable")
	}

	if err := service.runBackupSchedulerTick(context.Background()); err != nil {
		// The immediate publication may be delayed by persisted jitter; either
		// way the durable claim itself must have committed.
		t.Logf("initial publish deferred: %v", err)
	}
	var operation clusterModels.BackupJobOperation
	if err := service.DB.First(&operation, "job_id = ?", job.ID).Error; err != nil {
		t.Fatalf("durable operation missing: %v", err)
	}
	if operation.Token == "" || operation.State != clusterModels.BackupJobOperationQueued {
		t.Fatalf("unexpected operation: %+v", operation)
	}
	var claimed clusterModels.BackupJob
	if err := service.DB.First(&claimed, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if claimed.NextRunAt == nil || !claimed.NextRunAt.After(due) || claimed.ScheduleRevision != 1 {
		t.Fatalf("schedule was not advanced atomically with claim: %+v", claimed)
	}

	past := time.Now().UTC().Add(-time.Second)
	if err := service.DB.Model(&clusterModels.BackupJobOperation{}).
		Where("job_id = ?", job.ID).Update("publish_after", past).Error; err != nil {
		t.Fatalf("expire publish jitter: %v", err)
	}
	if err := service.RepublishQueuedBackupJobOperations(context.Background()); err == nil {
		t.Fatal("expected injected queue failure")
	}

	restarted := newTestZeltaService(service.DB)
	var published backupJobPayload
	restarted.backupOperationEnqueue = func(_ context.Context, name string, payload any) error {
		if name != backupJobQueueName {
			t.Fatalf("unexpected queue name %q", name)
		}
		var ok bool
		published, ok = payload.(backupJobPayload)
		if !ok {
			t.Fatalf("unexpected payload type %T", payload)
		}
		return nil
	}
	if err := restarted.ReconcileBackupJobOperationsAfterRestart(context.Background()); err != nil {
		t.Fatalf("restart reconcile: %v", err)
	}
	if published.OperationToken != operation.Token || published.JobID != job.ID {
		t.Fatalf("restart published a different occurrence: %+v want token=%q", published, operation.Token)
	}
	if err := service.DB.First(&claimed, job.ID).Error; err != nil {
		t.Fatalf("reload claimed job: %v", err)
	}
	if claimed.ScheduleRevision != 1 {
		t.Fatalf("restart advanced schedule twice: revision=%d", claimed.ScheduleRevision)
	}
}

func TestClusteredCompletionOutboxWaitsForRaftAndThenDrains(t *testing.T) {
	clusterService, localNodeID, cleanup := setupRaftClusterService(t)
	defer cleanup()

	now := time.Now().UTC()
	next := now.Add(time.Hour)
	job := clusterModels.BackupJob{
		ID: 52, Name: "outbox", TargetID: 1, RunnerNodeID: localNodeID,
		Mode: clusterModels.BackupJobModeDataset, CronExpr: "0 * * * *",
		Enabled: true, NextRunAt: &next, ScheduleRevision: 3,
	}
	if err := clusterService.DB.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	operation := clusterModels.BackupJobOperation{
		JobID: job.ID, Token: "backup:" + localNodeID + ":outbox",
		Operation: clusterModels.BackupJobOperationBackup,
		State:     clusterModels.BackupJobOperationRunning, HolderNodeID: localNodeID,
		ScheduleRevision: 3, Revision: 2, AcquiredAt: now, UpdatedAt: now,
	}
	if err := clusterService.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	service := newTestZeltaService(clusterService.DB)
	service.Cluster = clusterService

	runtime := clusterService.Raft
	clusterService.Raft = nil
	service.updateBackupJobResult(&job, nil, true)

	var unchanged clusterModels.BackupJob
	if err := clusterService.DB.First(&unchanged, job.ID).Error; err != nil {
		t.Fatalf("reload while raft unavailable: %v", err)
	}
	if unchanged.LastRunAt != nil || unchanged.LastStatus != "" || unchanged.Encrypted {
		t.Fatalf("clustered completion mutated locally without raft: %+v", unchanged)
	}
	var outbox clusterModels.ScheduledRunResultOutbox
	if err := clusterService.DB.First(&outbox, "token = ?", operation.Token).Error; err != nil {
		t.Fatalf("terminal result was not stored locally: %v", err)
	}
	var pendingOperation clusterModels.BackupJobOperation
	if err := clusterService.DB.First(&pendingOperation, "token = ?", operation.Token).Error; err != nil {
		t.Fatalf("durable operation was removed before terminal delivery: %v", err)
	}

	clusterService.Raft = runtime
	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("drain outbox: %v", err)
	}
	var applied clusterModels.BackupJob
	if err := clusterService.DB.First(&applied, job.ID).Error; err != nil {
		t.Fatalf("reload applied job: %v", err)
	}
	if applied.LastRunAt == nil || applied.LastStatus != "success" || !applied.Encrypted {
		t.Fatalf("drained result not applied: %+v", applied)
	}
	var outboxCount, operationCount int64
	clusterService.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).Count(&outboxCount)
	clusterService.DB.Model(&clusterModels.BackupJobOperation{}).Count(&operationCount)
	if outboxCount != 0 || operationCount != 0 {
		t.Fatalf("terminal finalize incomplete: outbox=%d operations=%d", outboxCount, operationCount)
	}
}

func TestScheduledRunResultOutboxRejectsConflictingPayloadForToken(t *testing.T) {
	service := newSchedulerTestDB(t)
	completedAt := time.Date(2026, time.August, 1, 4, 30, 0, 0, time.UTC)
	first := clusterModels.BackupJobRunResult{
		JobID: 71, Token: "backup:local:immutable", HolderNodeID: "local",
		CompletedAt: completedAt, LastStatus: "success",
	}
	if err := service.storeScheduledRunResult(
		clusterModels.ScheduledRunKindBackup, first.JobID, first.Token, first,
	); err != nil {
		t.Fatalf("store first result: %v", err)
	}
	if err := service.storeScheduledRunResult(
		clusterModels.ScheduledRunKindBackup, first.JobID, first.Token, first,
	); err != nil {
		t.Fatalf("store identical result idempotently: %v", err)
	}

	conflicting := first
	conflicting.LastStatus = "failed"
	conflicting.LastError = "different terminal outcome"
	err := service.storeScheduledRunResult(
		clusterModels.ScheduledRunKindBackup, conflicting.JobID, conflicting.Token, conflicting,
	)
	if err == nil || !strings.Contains(err.Error(), "scheduled_run_outbox_token_conflict") {
		t.Fatalf("conflicting terminal result accepted: %v", err)
	}

	var count int64
	if err := service.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).Count(&count).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("outbox rows=%d, want 1", count)
	}
}

func TestScheduledRunResultOutboxQuarantinesReceiptConflict(t *testing.T) {
	service := newSchedulerTestDB(t)
	completedAt := time.Date(2026, time.August, 1, 4, 31, 0, 0, time.UTC)
	token := "backup:local:receipt-conflict"
	if err := service.DB.Create(&clusterModels.ScheduledRunReceipt{
		Token: token, Kind: clusterModels.ScheduledRunKindBackup, ObjectID: 72,
		HolderNodeID: "local", ScheduleRevision: 3, Status: "success",
		CompletedAt: completedAt,
	}).Error; err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
	result := clusterModels.BackupJobRunResult{
		JobID: 72, Token: token, HolderNodeID: "local", ScheduleRevision: 3,
		CompletedAt: completedAt.Add(time.Second), LastStatus: "failed", LastError: "late duplicate",
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := service.DB.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: token, Kind: clusterModels.ScheduledRunKindBackup,
		ObjectID: result.JobID, Payload: string(payload),
	}).Error; err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("quarantine drain: %v", err)
	}
	var row clusterModels.ScheduledRunResultOutbox
	if err := service.DB.First(&row, "token = ?", token).Error; err != nil {
		t.Fatalf("load quarantined row: %v", err)
	}
	if !row.Quarantined || row.AttemptCount != 1 || row.NextAttemptAt != nil ||
		!strings.Contains(row.LastError, "scheduled_run_receipt_token_conflict") {
		t.Fatalf("unexpected quarantined row: %+v", row)
	}

	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if err := service.DB.First(&row, "token = ?", token).Error; err != nil {
		t.Fatalf("reload quarantined row: %v", err)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("quarantined row retried: attempts=%d", row.AttemptCount)
	}

	old := time.Now().UTC().Add(-scheduledRunOutboxQuarantineRetention - time.Hour)
	if err := service.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).
		Where("token = ?", token).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatalf("age quarantined row: %v", err)
	}
	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("retention drain: %v", err)
	}
	var count int64
	if err := service.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).
		Where("token = ?", token).Count(&count).Error; err != nil {
		t.Fatalf("count retained row: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired quarantined row retained: count=%d", count)
	}

	activeToken := "backup:local:active-quarantine"
	if err := service.DB.Create(&clusterModels.BackupTarget{
		ID: 720, Name: "active-quarantine", SSHHost: "localhost",
		BackupRoot: "tank/backups", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed active target: %v", err)
	}
	if err := service.DB.Create(&clusterModels.BackupJob{
		ID: 720, Name: "active-quarantine", TargetID: 720,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 * * * *", ScheduleRevision: 1,
	}).Error; err != nil {
		t.Fatalf("seed active job: %v", err)
	}
	if err := service.DB.Create(&clusterModels.BackupJobOperation{
		JobID: 720, Token: activeToken, Operation: clusterModels.BackupJobOperationBackup,
		State: clusterModels.BackupJobOperationRunning, HolderNodeID: "local",
		ScheduleRevision: 1, Revision: 2, AcquiredAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatalf("seed active operation: %v", err)
	}
	if err := service.DB.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: activeToken, Kind: clusterModels.ScheduledRunKindBackup, ObjectID: 720,
		Payload: `{}`, Quarantined: true, CreatedAt: old, UpdatedAt: old,
	}).Error; err != nil {
		t.Fatalf("seed active quarantine: %v", err)
	}
	if err := service.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).
		Where("token = ?", activeToken).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatalf("age active quarantine: %v", err)
	}
	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("active quarantine retention drain: %v", err)
	}
	if err := service.DB.Model(&clusterModels.ScheduledRunResultOutbox{}).
		Where("token = ?", activeToken).Count(&count).Error; err != nil {
		t.Fatalf("count active quarantine: %v", err)
	}
	if count != 1 {
		t.Fatalf("active operation lost its terminal guard: count=%d", count)
	}
}

func TestScheduledRunResultOutboxBacksOffTransientClusterFailure(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.Cluster{})
	if err := database.Create(&clusterModels.Cluster{ID: 1, Enabled: true}).Error; err != nil {
		t.Fatalf("seed cluster state: %v", err)
	}
	service := newTestZeltaService(database)
	completedAt := time.Date(2026, time.August, 1, 4, 32, 0, 0, time.UTC)
	result := clusterModels.ReplicationPolicyRunResult{
		PolicyID: 73, Token: "replication:local:raft-down", HolderNodeID: "local",
		ScheduleRevision: 2, OwnerEpoch: 1, CompletedAt: completedAt, LastStatus: "failed",
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := database.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: result.Token, Kind: clusterModels.ScheduledRunKindReplication,
		ObjectID: result.PolicyID, Payload: string(payload),
	}).Error; err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	before := time.Now().UTC()
	err = service.DrainScheduledRunResultOutbox()
	if err == nil || !strings.Contains(err.Error(), "cluster_enabled_raft_unavailable") {
		t.Fatalf("expected transient raft error, got %v", err)
	}
	var row clusterModels.ScheduledRunResultOutbox
	if err := database.First(&row, "token = ?", result.Token).Error; err != nil {
		t.Fatalf("load deferred row: %v", err)
	}
	if row.Quarantined || row.AttemptCount != 1 || row.NextAttemptAt == nil ||
		!row.NextAttemptAt.After(before) {
		t.Fatalf("unexpected deferred row: %+v", row)
	}

	if err := service.DrainScheduledRunResultOutbox(); err != nil {
		t.Fatalf("immediate backoff drain: %v", err)
	}
	if err := database.First(&row, "token = ?", result.Token).Error; err != nil {
		t.Fatalf("reload deferred row: %v", err)
	}
	if row.AttemptCount != 1 {
		t.Fatalf("deferred row retried before deadline: attempts=%d", row.AttemptCount)
	}
}

func TestLongRunningBackupCoalescesMissedOccurrence(t *testing.T) {
	service := newSchedulerTestDB(t)
	target := clusterModels.BackupTarget{
		ID: 2, Name: "coalesce", SSHHost: "localhost", BackupRoot: "tank/backups", Enabled: true,
	}
	if err := service.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	due := time.Now().UTC().Add(-time.Minute)
	started := due.Add(-30 * time.Minute)
	job := clusterModels.BackupJob{
		ID: 53, Name: "coalesce", TargetID: target.ID,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "* * * * *", Enabled: true, NextRunAt: &due, ScheduleRevision: 2,
	}
	if err := service.DB.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	operation := clusterModels.BackupJobOperation{
		JobID: job.ID, Token: "backup:local:long-running",
		Operation: clusterModels.BackupJobOperationBackup,
		State:     clusterModels.BackupJobOperationRunning, HolderNodeID: "local",
		Scheduled: true, OccurrenceAt: &started, ScheduleRevision: 2,
		Revision: 2, AcquiredAt: started, UpdatedAt: started,
	}
	if err := service.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed active operation: %v", err)
	}

	if err := service.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler coalesce tick: %v", err)
	}
	var updated clusterModels.BackupJob
	if err := service.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(time.Now().UTC()) ||
		updated.ScheduleRevision != 3 {
		t.Fatalf("missed occurrence was not coalesced: %+v", updated)
	}
	var carried clusterModels.BackupJobOperation
	if err := service.DB.First(&carried, "job_id = ?", job.ID).Error; err != nil {
		t.Fatalf("reload operation: %v", err)
	}
	if carried.Token != operation.Token || carried.ScheduleRevision != updated.ScheduleRevision {
		t.Fatalf("coalesce replaced or staled active token: %+v", carried)
	}
}
