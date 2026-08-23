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
)

func TestDurableBackupJobOperationClosesPreEventDeleteWindow(t *testing.T) {
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
	service := newTestZeltaService(database)
	if err := database.Create(&clusterModels.BackupJob{
		ID: 51, Name: "reserved-job", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	handle, err := service.acquireDurableBackupJobOperation(
		context.Background(), 51, clusterModels.BackupJobOperationBackup, "",
	)
	if err != nil {
		t.Fatalf("acquire operation: %v", err)
	}
	if err := clusterModels.DeleteBackupJobTxn(database, 51); err == nil ||
		!strings.Contains(err.Error(), "backup_job_running") {
		t.Fatalf("queued operation did not block delete: %v", err)
	}

	prepared, execute, err := service.prepareQueuedBackupJobOperation(
		context.Background(), 51, clusterModels.BackupJobOperationBackup,
		handle.Token, handle.HolderNodeID, "",
	)
	if err != nil || !execute {
		t.Fatalf("prepare operation: execute=%v err=%v", execute, err)
	}
	var operation clusterModels.BackupJobOperation
	if err := database.First(&operation, "job_id = ?", 51).Error; err != nil {
		t.Fatalf("load running operation: %v", err)
	}
	if operation.State != clusterModels.BackupJobOperationRunning {
		t.Fatalf("state = %q, want running", operation.State)
	}

	if err := service.finishDurableBackupJobOperation(prepared); err != nil {
		t.Fatalf("finish operation: %v", err)
	}
	if err := clusterModels.DeleteBackupJobTxn(database, 51); err != nil {
		t.Fatalf("delete after operation release: %v", err)
	}
}

func TestBackupJobOperationRestartRequeuesExactToken(t *testing.T) {
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
	if err := database.Create(&clusterModels.BackupJob{
		ID: 53, Name: "restart-job", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	now := time.Now().UTC()
	if err := database.Create(&clusterModels.BackupJobOperation{
		JobID: 53, Token: "backup:local:restart", Operation: clusterModels.BackupJobOperationBackup,
		State: clusterModels.BackupJobOperationRunning, HolderNodeID: "local",
		Revision: 2, AcquiredAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	for restart := 1; restart <= 2; restart++ {
		service := newTestZeltaService(database)
		calls := 0
		service.backupOperationEnqueue = func(_ context.Context, name string, payload any) error {
			calls++
			if name != backupJobQueueName {
				t.Fatalf("restart %d queue = %q", restart, name)
			}
			queued, ok := payload.(backupJobPayload)
			if !ok || queued.JobID != 53 || queued.OperationToken != "backup:local:restart" || queued.HolderNodeID != "local" {
				t.Fatalf("restart %d payload = %#v", restart, payload)
			}
			return nil
		}
		if err := service.ReconcileBackupJobOperationsAfterRestart(context.Background()); err != nil {
			t.Fatalf("restart %d reconcile: %v", restart, err)
		}
		if calls != 1 {
			t.Fatalf("restart %d enqueue calls = %d, want 1", restart, calls)
		}
	}
}

func TestRunningBackupOperationWithTerminalOutboxIsNotReexecuted(t *testing.T) {
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
	if err := database.Create(&clusterModels.BackupJob{
		ID: 54, Name: "terminal-outbox", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	now := time.Now().UTC()
	operation := clusterModels.BackupJobOperation{
		JobID: 54, Token: "backup:local:terminal-outbox", Operation: clusterModels.BackupJobOperationBackup,
		State: clusterModels.BackupJobOperationRunning, HolderNodeID: "local",
		Revision: 2, AcquiredAt: now, UpdatedAt: now,
	}
	if err := database.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if err := database.Create(&clusterModels.ScheduledRunResultOutbox{
		Token: operation.Token, Kind: clusterModels.ScheduledRunKindBackup,
		ObjectID: operation.JobID, Payload: `{}`,
	}).Error; err != nil {
		t.Fatalf("seed terminal outbox: %v", err)
	}

	service := newTestZeltaService(database)
	_, execute, err := service.prepareQueuedBackupJobOperation(
		context.Background(), operation.JobID, operation.Operation,
		operation.Token, operation.HolderNodeID, operation.RequestPayload,
	)
	if err != nil {
		t.Fatalf("prepare terminalized operation: %v", err)
	}
	if execute {
		t.Fatal("running backup token with a terminal outbox was allowed to execute again")
	}
}

func TestQueuedBackupJobOperationIsAbortedAfterTargetDisable(t *testing.T) {
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
	)
	service := newTestZeltaService(database)
	target := clusterModels.BackupTarget{
		ID: 61, Name: "target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := database.Create(&clusterModels.BackupJob{
		ID: 62, Name: "job", TargetID: target.ID, Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	handle, err := service.acquireDurableBackupJobOperation(
		context.Background(), 62, clusterModels.BackupJobOperationBackup, "",
	)
	if err != nil {
		t.Fatalf("acquire queued operation: %v", err)
	}
	if err := database.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	_, execute, err := service.prepareQueuedBackupJobOperation(
		context.Background(), handle.JobID, handle.Operation, handle.Token, handle.HolderNodeID, handle.RequestPayload,
	)
	if err != nil || execute {
		t.Fatalf("prepare disabled operation execute=%v err=%v", execute, err)
	}
	var count int64
	if err := database.Model(&clusterModels.BackupJobOperation{}).Where("job_id = ?", handle.JobID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("queued operation count=%d err=%v", count, err)
	}
}

func TestQueuedRestoreTokenIsBoundToReplicatedRequest(t *testing.T) {
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobOperation{},
	)
	service := newTestZeltaService(database)
	if err := database.Create(&clusterModels.BackupJob{
		ID: 52, Name: "restore-job", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	handle, err := service.acquireDurableBackupJobOperation(
		context.Background(), 52, clusterModels.BackupJobOperationRestore,
		`{"snapshot":"@expected","remoteDataset":"backup/data"}`,
	)
	if err != nil {
		t.Fatalf("acquire restore operation: %v", err)
	}
	_, execute, err := service.prepareQueuedBackupJobOperation(
		context.Background(), 52, clusterModels.BackupJobOperationRestore,
		handle.Token, handle.HolderNodeID,
		`{"snapshot":"@different","remoteDataset":"backup/data"}`,
	)
	if err != nil {
		t.Fatalf("stale/tampered payload should be consumed safely: %v", err)
	}
	if execute {
		t.Fatal("restore executed with request data different from its replicated reservation")
	}

	var operation clusterModels.BackupJobOperation
	if err := database.First(&operation, "job_id = ?", 52).Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if operation.State != clusterModels.BackupJobOperationQueued {
		t.Fatalf("mismatched message changed operation state to %q", operation.State)
	}
	if err := service.abortDurableBackupJobOperation(context.Background(), handle); err != nil {
		t.Fatalf("abort operation: %v", err)
	}
}
