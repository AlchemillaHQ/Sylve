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
)

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
