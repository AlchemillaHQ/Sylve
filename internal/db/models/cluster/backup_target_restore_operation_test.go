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

	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newBackupTargetRestoreOperationTestDB(t *testing.T) (*gorm.DB, BackupTarget, time.Time) {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &BackupTarget{}, &BackupTargetRestoreOperation{})
	target := BackupTarget{
		ID: 71, Name: "restore-target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	return db, target, time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
}

func backupTargetRestoreAcquire(
	targetID uint,
	token string,
	holder string,
	destination string,
	at time.Time,
) BackupTargetRestoreOperationAcquire {
	return BackupTargetRestoreOperationAcquire{
		Token: token, TargetID: targetID, HolderNodeID: holder,
		DestinationDataset: destination,
		RequestPayload:     `{"remoteDataset":"tank/backups/data","snapshot":"@bk_j1_c1_test"}`,
		AcquiredAt:         at,
	}
}

func backupTargetRestoreTransition(
	acquire BackupTargetRestoreOperationAcquire,
	at time.Time,
) BackupTargetRestoreOperationTransition {
	return BackupTargetRestoreOperationTransition{
		Token: acquire.Token, TargetID: acquire.TargetID, HolderNodeID: acquire.HolderNodeID,
		DestinationDataset: acquire.DestinationDataset, RequestPayload: acquire.RequestPayload,
		OccurredAt: at,
	}
}

func TestBackupTargetRestoreOperationSerializesOverlappingDestinationsPerHolder(t *testing.T) {
	db, target, now := newBackupTargetRestoreOperationTestDB(t)

	root := backupTargetRestoreAcquire(target.ID, "restore:node-a:root", "node-a", "/zroot/restore/root/", now)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &root); err != nil {
		t.Fatalf("acquire root: %v", err)
	}
	if root.DestinationDataset != "zroot/restore/root" {
		t.Fatalf("normalized destination = %q", root.DestinationDataset)
	}

	for _, tc := range []struct {
		name         string
		token        string
		holder       string
		destination  string
		wantConflict bool
	}{
		{name: "exact", token: "restore:node-a:exact", holder: "node-a", destination: "zroot/restore/root", wantConflict: true},
		{name: "child", token: "restore:node-a:child", holder: "node-a", destination: "zroot/restore/root/child", wantConflict: true},
		{name: "ancestor", token: "restore:node-a:ancestor", holder: "node-a", destination: "zroot/restore", wantConflict: true},
		{name: "unrelated", token: "restore:node-a:other", holder: "node-a", destination: "tank/other"},
		{name: "same path other node", token: "restore:node-b:same", holder: "node-b", destination: "zroot/restore/root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := backupTargetRestoreAcquire(target.ID, tc.token, tc.holder, tc.destination, now.Add(time.Minute))
			err := AcquireBackupTargetRestoreOperationTxn(db, &payload)
			if tc.wantConflict {
				if err == nil || !strings.Contains(err.Error(), "restore_destination_reserved") {
					t.Fatalf("acquire error = %v, want destination reservation conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("acquire unrelated operation: %v", err)
			}
		})
	}
}

func TestBackupTargetRestoreOperationExactReplayAndTamperFence(t *testing.T) {
	db, target, now := newBackupTargetRestoreOperationTestDB(t)
	acquire := backupTargetRestoreAcquire(target.ID, "restore:node-a:exact", "node-a", "zroot/restore", now)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("exact acquire replay: %v", err)
	}

	tampered := acquire
	tampered.RequestPayload = `{"snapshot":"@different"}`
	if err := AcquireBackupTargetRestoreOperationTxn(db, &tampered); err == nil ||
		!strings.Contains(err.Error(), "token_mismatch") {
		t.Fatalf("tampered exact token error = %v", err)
	}
	var count int64
	if err := db.Model(&BackupTargetRestoreOperation{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("operation count = %d err=%v", count, err)
	}
}

func TestBackupTargetRestoreOperationQueuedRunningCASAndTerminalRelease(t *testing.T) {
	db, target, now := newBackupTargetRestoreOperationTestDB(t)
	acquire := backupTargetRestoreAcquire(target.ID, "restore:node-a:cas", "node-a", "zroot/restore", now)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	transition := backupTargetRestoreTransition(acquire, now.Add(time.Second))
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "already_started") {
		t.Fatalf("duplicate start error = %v", err)
	}

	var operation BackupTargetRestoreOperation
	if err := db.First(&operation, "token = ?", acquire.Token).Error; err != nil {
		t.Fatalf("load running operation: %v", err)
	}
	if operation.State != BackupTargetRestoreOperationRunning || operation.Revision != 2 {
		t.Fatalf("running operation = %+v", operation)
	}

	transition.OccurredAt = now.Add(2 * time.Second)
	if err := FinishBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := FinishBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("idempotent finish: %v", err)
	}
	transition.OccurredAt = now.Add(3 * time.Second)
	if err := ReleaseBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := ReleaseBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("idempotent completed release: %v", err)
	}
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "already_completed") {
		t.Fatalf("completed start error = %v", err)
	}
	if err := db.First(&operation, "token = ?", acquire.Token).Error; err != nil {
		t.Fatalf("load completed operation: %v", err)
	}
	if operation.State != BackupTargetRestoreOperationCompleted || operation.Revision != 4 {
		t.Fatalf("completed operation = %+v", operation)
	}
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("completed exact-token retry: %v", err)
	}
	newOperation := acquire
	newOperation.Token = "restore:node-a:new-intent"
	newOperation.AcquiredAt = now.Add(4 * time.Second)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &newOperation); err != nil {
		t.Fatalf("new operation ID after completed receipt: %v", err)
	}
}

func TestBackupTargetRestoreOperationRestartRequeueAndQueuedAbort(t *testing.T) {
	db, target, now := newBackupTargetRestoreOperationTestDB(t)
	acquire := backupTargetRestoreAcquire(target.ID, "restore:node-a:restart", "node-a", "zroot/restore", now)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	transition := backupTargetRestoreTransition(acquire, now.Add(time.Second))
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("start: %v", err)
	}
	transition.OccurredAt = now.Add(2 * time.Second)
	if err := RequeueBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if err := RequeueBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("idempotent queued requeue: %v", err)
	}
	transition.OccurredAt = now.Add(3 * time.Second)
	if err := AbortBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("abort queued: %v", err)
	}
	if err := AbortBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("stale abort: %v", err)
	}
}

func TestBackupTargetDeleteIsFencedByRestoreOperation(t *testing.T) {
	db, target, now := newBackupTargetRestoreOperationTestDB(t)
	if err := db.AutoMigrate(&BackupJob{}); err != nil {
		t.Fatalf("migrate backup jobs: %v", err)
	}
	acquire := backupTargetRestoreAcquire(target.ID, "restore:node-a:delete", "node-a", "zroot/restore", now)
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	payload := backupTargetRestoreTransition(acquire, now.Add(time.Second))
	if err := DeleteBackupTargetTxn(db, target.ID); err == nil || !strings.Contains(err.Error(), "restore_operations") {
		t.Fatalf("delete target with reservation error = %v", err)
	}
	if err := StartBackupTargetRestoreOperationTxn(db, &payload); err != nil {
		t.Fatalf("start: %v", err)
	}
	payload.OccurredAt = now.Add(2 * time.Second)
	if err := FinishBackupTargetRestoreOperationTxn(db, &payload); err != nil {
		t.Fatalf("finish: %v", err)
	}
	payload.OccurredAt = now.Add(3 * time.Second)
	if err := ReleaseBackupTargetRestoreOperationTxn(db, &payload); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := DeleteBackupTargetTxn(db, target.ID); err != nil {
		t.Fatalf("delete target after completion: %v", err)
	}
	var count int64
	if err := db.Model(&BackupTargetRestoreOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("completion receipts after target deletion = %d err=%v", count, err)
	}
}
