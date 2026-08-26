// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"strings"
	"testing"
	"time"
)

func TestBackupJobOperationLifecycleAndDeleteInterlock(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{}, &BackupJobOperation{}, &BackupEvent{})
	if err := db.Create(&BackupJob{
		ID: 10, Name: "operation-job", Mode: BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *", RunnerNodeID: "node-a",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	acquiredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	acquire := BackupJobOperationAcquire{
		JobID: 10, Token: "backup:node-a:10", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-a", RequestPayload: `{}`, AcquiredAt: acquiredAt,
	}
	if err := AcquireBackupJobOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := AcquireBackupJobOperationTxn(db, &acquire); err != nil {
		t.Fatalf("idempotent acquire: %v", err)
	}
	if err := DeleteBackupJobTxn(db, 10); err == nil || !strings.Contains(err.Error(), "backup_job_running") {
		t.Fatalf("active operation did not block delete: %v", err)
	}

	startedAt := acquiredAt.Add(time.Second)
	transition := BackupJobOperationTransition{
		JobID: 10, Token: acquire.Token, Operation: acquire.Operation,
		HolderNodeID: acquire.HolderNodeID, RequestPayload: acquire.RequestPayload,
		OccurredAt: startedAt,
	}
	if err := StartBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := StartBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("idempotent start: %v", err)
	}
	if err := ReleaseBackupJobOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "not_releasable") {
		t.Fatalf("running operation released without finish: %v", err)
	}

	transition.OccurredAt = startedAt.Add(time.Second)
	if err := FinishBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := StartBackupJobOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "finishing") {
		t.Fatalf("finishing operation restarted: %v", err)
	}
	if err := ReleaseBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := ReleaseBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	if err := DeleteBackupJobTxn(db, 10); err != nil {
		t.Fatalf("delete after release: %v", err)
	}
}

func TestBackupJobOperationRejectsCompetingTokenAndWrongRunner(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{}, &BackupJobOperation{})
	if err := db.Create(&BackupJob{
		ID: 11, Name: "operation-job", Mode: BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *", RunnerNodeID: "node-a",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	wrongRunner := BackupJobOperationAcquire{
		JobID: 11, Token: "backup:node-b:11", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-b", AcquiredAt: time.Now().UTC(),
	}
	if err := AcquireBackupJobOperationTxn(db, &wrongRunner); err == nil ||
		!strings.Contains(err.Error(), "runner_mismatch") {
		t.Fatalf("wrong runner acquired operation: %v", err)
	}

	first := BackupJobOperationAcquire{
		JobID: 11, Token: "backup:node-a:first", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(),
	}
	if err := AcquireBackupJobOperationTxn(db, &first); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	competing := first
	competing.Token = "restore:node-a:second"
	competing.Operation = BackupJobOperationRestore
	if err := AcquireBackupJobOperationTxn(db, &competing); err == nil ||
		!strings.Contains(err.Error(), "backup_job_running") {
		t.Fatalf("competing operation acquired: %v", err)
	}

	wrongToken := BackupJobOperationTransition{
		JobID: 11, Token: competing.Token, Operation: competing.Operation,
		HolderNodeID: competing.HolderNodeID, OccurredAt: time.Now().UTC(),
	}
	if err := AbortBackupJobOperationTxn(db, &wrongToken); err == nil ||
		!strings.Contains(err.Error(), "token_mismatch") {
		t.Fatalf("wrong token released operation: %v", err)
	}
	var count int64
	if err := db.Model(&BackupJobOperation{}).Where("job_id = ?", 11).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("operation changed after wrong-token release: count=%d err=%v", count, err)
	}
}

func TestBackupJobDeleteIgnoresAndRetainsLocalEvents(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{}, &BackupJobOperation{}, &BackupEvent{})
	if err := db.Create(&BackupJob{
		ID: 12, Name: "event-job", Mode: BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := db.Create(&BackupEvent{JobID: ptr[uint](12), Status: "running"}).Error; err != nil {
		t.Fatalf("seed local event: %v", err)
	}

	if err := DeleteBackupJobTxn(db, 12); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var eventCount int64
	if err := db.Model(&BackupEvent{}).Where("job_id = ?", 12).Count(&eventCount).Error; err != nil || eventCount != 1 {
		t.Fatalf("local event history was deleted: count=%d err=%v", eventCount, err)
	}
	if err := DeleteBackupJobTxn(db, 12); err == nil || !strings.Contains(err.Error(), "backup_job_not_found") {
		t.Fatalf("missing delete did not report not found: %v", err)
	}
}
