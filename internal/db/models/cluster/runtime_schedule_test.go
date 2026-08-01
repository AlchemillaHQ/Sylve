// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func newRuntimeScheduleTestDB(t *testing.T) *gorm.DB {
	database := newClusterModelTestDB(t,
		&BackupTarget{},
		&BackupJob{},
		&BackupJobOperation{},
		&ReplicationPolicy{},
		&ReplicationRunOperation{},
		&ScheduledRunReceipt{},
	)
	if err := database.Create(&BackupTarget{
		ID: 1, Name: "runtime-schedule", SSHHost: "host", BackupRoot: "tank/backups", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed backup target: %v", err)
	}
	return database
}

func TestBackupScheduleClaimCASAllowsOneToken(t *testing.T) {
	database := newRuntimeScheduleTestDB(t)
	occurrence := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	next := occurrence.Add(time.Hour)
	if err := database.Create(&BackupJob{
		ID: 1, Name: "claim-race", TargetID: 1, RunnerNodeID: "node-a",
		Mode: BackupJobModeDataset, CronExpr: "0 * * * *", Enabled: true,
		NextRunAt: &occurrence, ScheduleRevision: 7,
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, token := range []string{"backup:node-a:first", "backup:node-a:second"} {
		token := token
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ApplyBackupJobScheduleDecisionTxn(database, &BackupJobScheduleDecision{
				JobID: 1, ExpectedScheduleRevision: 7, ExpectedNextRunAt: &occurrence,
				NextRunAt: &next, DecidedAt: occurrence, ClaimToken: token,
				HolderNodeID: "node-a", OccurrenceAt: &occurrence,
			})
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
	var operations []BackupJobOperation
	if err := database.Find(&operations).Error; err != nil {
		t.Fatalf("load operations: %v", err)
	}
	if len(operations) != 1 || operations[0].ScheduleRevision != 8 {
		t.Fatalf("unexpected winning operation: %+v", operations)
	}
	var job BackupJob
	if err := database.First(&job, 1).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.ScheduleRevision != 8 || job.NextRunAt == nil || !job.NextRunAt.Equal(next) {
		t.Fatalf("unexpected claimed schedule: %+v", job)
	}
}

func TestReplicationScheduleClaimCASAllowsOneToken(t *testing.T) {
	database := newRuntimeScheduleTestDB(t)
	occurrence := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	next := occurrence.Add(time.Hour)
	if err := database.Create(&ReplicationPolicy{
		ID: 2, Name: "replication-claim-race", GuestType: ReplicationGuestTypeVM,
		GuestID: 2, SourceNodeID: "node-a", ActiveNodeID: "node-a",
		OwnerEpoch: 4, SourceMode: ReplicationSourceModeFollowActive,
		FailbackMode: ReplicationFailbackManual, FailoverMode: ReplicationFailoverManual,
		CronExpr: "0 * * * *", Enabled: true, NextRunAt: &occurrence,
		ScheduleRevision: 11,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, token := range []string{"replication:node-a:first", "replication:node-a:second"} {
		token := token
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- ApplyReplicationPolicyScheduleDecisionTxn(database, &ReplicationPolicyScheduleDecision{
				PolicyID: 2, ExpectedScheduleRevision: 11, ExpectedOwnerEpoch: 4,
				ExpectedNextRunAt: &occurrence, NextRunAt: &next, DecidedAt: occurrence,
				ClaimToken: token, HolderNodeID: "node-a", Scheduled: true,
				OccurrenceAt: &occurrence,
			})
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
	var operations []ReplicationRunOperation
	if err := database.Find(&operations).Error; err != nil {
		t.Fatalf("load operations: %v", err)
	}
	if len(operations) != 1 || operations[0].ScheduleRevision != 12 || operations[0].OwnerEpoch != 4 {
		t.Fatalf("unexpected winning operation: %+v", operations)
	}
}

func TestReplicationRunStartRejectsDuplicateExecution(t *testing.T) {
	database := newRuntimeScheduleTestDB(t)
	occurrence := time.Date(2026, time.August, 1, 4, 34, 0, 0, time.UTC)
	if err := database.Create(&ReplicationPolicy{
		ID: 20, Name: "duplicate-start", GuestType: ReplicationGuestTypeVM,
		GuestID: 20, SourceNodeID: "node-a", ActiveNodeID: "node-a",
		OwnerEpoch: 2, SourceMode: ReplicationSourceModeFollowActive,
		FailbackMode: ReplicationFailbackManual, FailoverMode: ReplicationFailoverManual,
		CronExpr: "0 * * * *", Enabled: true, NextRunAt: &occurrence, ScheduleRevision: 5,
	}).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	next := occurrence.Add(time.Hour)
	decision := ReplicationPolicyScheduleDecision{
		PolicyID: 20, ExpectedScheduleRevision: 5, ExpectedOwnerEpoch: 2,
		ExpectedNextRunAt: &occurrence, NextRunAt: &next, DecidedAt: occurrence,
		ClaimToken: "replication:node-a:duplicate", HolderNodeID: "node-a",
		Scheduled: true, OccurrenceAt: &occurrence,
	}
	if err := ApplyReplicationPolicyScheduleDecisionTxn(database, &decision); err != nil {
		t.Fatalf("claim: %v", err)
	}
	transition := ReplicationRunOperationTransition{
		PolicyID: 20, Token: decision.ClaimToken, HolderNodeID: "node-a",
		OccurredAt: occurrence.Add(time.Minute),
	}
	if err := StartReplicationRunOperationTxn(database, &transition); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := StartReplicationRunOperationTxn(database, &transition); err == nil ||
		!strings.Contains(err.Error(), "replication_run_already_started") {
		t.Fatalf("duplicate start accepted: %v", err)
	}
}

func TestBackupStaleCompletionRecordsReceiptWithoutOverwritingEdit(t *testing.T) {
	database := newRuntimeScheduleTestDB(t)
	occurrence := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	next := occurrence.Add(time.Hour)
	if err := database.Create(&BackupJob{
		ID: 3, Name: "stale-completion", TargetID: 1, RunnerNodeID: "node-a",
		Mode: BackupJobModeDataset, CronExpr: "0 * * * *", Enabled: true,
		NextRunAt: &occurrence, ScheduleRevision: 2, LastStatus: "old",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	decision := BackupJobScheduleDecision{
		JobID: 3, ExpectedScheduleRevision: 2, ExpectedNextRunAt: &occurrence,
		NextRunAt: &next, DecidedAt: occurrence, ClaimToken: "backup:node-a:stale",
		HolderNodeID: "node-a", OccurrenceAt: &occurrence,
	}
	if err := ApplyBackupJobScheduleDecisionTxn(database, &decision); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := database.Model(&BackupJob{}).Where("id = ?", 3).Updates(map[string]any{
		"runner_node_id": "node-b", "cron_expr": "30 * * * *",
		"schedule_revision": gorm.Expr("schedule_revision + ?", 1),
	}).Error; err != nil {
		t.Fatalf("edit job: %v", err)
	}
	if err := StartBackupJobOperationTxn(database, &BackupJobOperationTransition{
		JobID: 3, Token: decision.ClaimToken, Operation: BackupJobOperationBackup,
		HolderNodeID: "node-a", OccurredAt: occurrence.Add(time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "schedule_stale") {
		t.Fatalf("stale queued backup started: %v", err)
	}
	completed := occurrence.Add(5 * time.Minute)
	recomputedNext := completed.Add(time.Hour)
	encrypted := true
	if err := CompleteBackupJobRunTxn(database, &BackupJobRunResult{
		JobID: 3, Token: decision.ClaimToken, HolderNodeID: "node-a",
		ScheduleRevision: 3, CompletedAt: completed, LastStatus: "success",
		NextRunAt: &recomputedNext, Encrypted: &encrypted,
	}); err != nil {
		t.Fatalf("complete stale run: %v", err)
	}

	var job BackupJob
	if err := database.First(&job, 3).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.RunnerNodeID != "node-b" || job.CronExpr != "30 * * * *" ||
		job.ScheduleRevision != 4 || job.LastStatus != "old" || job.Encrypted {
		t.Fatalf("stale result overwrote current job: %+v", job)
	}
	var receipt ScheduledRunReceipt
	if err := database.Where("token = ?", "backup:node-a:stale").First(&receipt).Error; err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if receipt.Applied || receipt.Status != "success" {
		t.Fatalf("unexpected stale receipt: %+v", receipt)
	}
	var operationCount int64
	if err := database.Model(&BackupJobOperation{}).Count(&operationCount).Error; err != nil || operationCount != 0 {
		t.Fatalf("operation count=%d err=%v", operationCount, err)
	}
}

func TestReplicationStaleOwnerCompletionDoesNotOverwritePolicy(t *testing.T) {
	database := newRuntimeScheduleTestDB(t)
	occurrence := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	next := occurrence.Add(time.Hour)
	policy := ReplicationPolicy{
		ID: 4, Name: "stale-owner", GuestType: ReplicationGuestTypeVM, GuestID: 4,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 6,
		SourceMode: ReplicationSourceModeFollowActive, FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual, CronExpr: "0 * * * *", Enabled: true,
		NextRunAt: &occurrence, ScheduleRevision: 9, LastStatus: "old",
	}
	if err := database.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	decision := ReplicationPolicyScheduleDecision{
		PolicyID: 4, ExpectedScheduleRevision: 9, ExpectedOwnerEpoch: 6,
		ExpectedNextRunAt: &occurrence, NextRunAt: &next, DecidedAt: occurrence,
		ClaimToken: "replication:node-a:stale", HolderNodeID: "node-a",
		Scheduled: true, OccurrenceAt: &occurrence,
	}
	if err := ApplyReplicationPolicyScheduleDecisionTxn(database, &decision); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := database.Model(&ReplicationPolicy{}).Where("id = ?", 4).Updates(map[string]any{
		"active_node_id": "node-b", "owner_epoch": uint64(7),
		"schedule_revision": gorm.Expr("schedule_revision + ?", 1),
	}).Error; err != nil {
		t.Fatalf("change owner: %v", err)
	}
	if err := StartReplicationRunOperationTxn(database, &ReplicationRunOperationTransition{
		PolicyID: 4, Token: decision.ClaimToken, HolderNodeID: "node-a",
		OccurredAt: occurrence.Add(time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "schedule_stale") {
		t.Fatalf("stale queued replication started: %v", err)
	}
	completed := occurrence.Add(5 * time.Minute)
	if err := CompleteReplicationPolicyRunTxn(database, &ReplicationPolicyRunResult{
		PolicyID: 4, Token: decision.ClaimToken, HolderNodeID: "node-a",
		ScheduleRevision: 10, OwnerEpoch: 6, CompletedAt: completed,
		LastStatus: "failed", LastError: "old owner failed", NextRunAt: &next,
	}); err != nil {
		t.Fatalf("complete stale replication run: %v", err)
	}
	if err := database.First(&policy, 4).Error; err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if policy.ActiveNodeID != "node-b" || policy.OwnerEpoch != 7 ||
		policy.ScheduleRevision != 11 || policy.LastStatus != "old" {
		t.Fatalf("stale result overwrote current policy: %+v", policy)
	}
	var receipt ScheduledRunReceipt
	if err := database.Where("token = ?", "replication:node-a:stale").First(&receipt).Error; err != nil {
		t.Fatalf("load receipt: %v", err)
	}
	if receipt.Applied || receipt.OwnerEpoch != 6 ||
		!strings.Contains(receipt.Error, "old owner failed") {
		t.Fatalf("unexpected replication receipt: %+v", receipt)
	}
}
