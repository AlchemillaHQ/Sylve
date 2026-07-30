// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"fmt"
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
