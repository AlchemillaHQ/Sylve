// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
	"time"
)

func TestDisabledBackupTargetRejectsNewAndQueuedJobWork(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{}, &BackupJobOperation{})
	target := BackupTarget{ID: 801, Name: "target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true}
	job := BackupJob{ID: 802, Name: "job", TargetID: target.ID, RunnerNodeID: "node-a", Mode: BackupJobModeDataset, CronExpr: "0 0 * * *"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	acquire := BackupJobOperationAcquire{
		JobID: job.ID, Token: "job-token", Operation: BackupJobOperationBackup,
		HolderNodeID: "node-a", AcquiredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
	if err := AcquireBackupJobOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire before disable: %v", err)
	}
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := AcquireBackupJobOperationTxn(db, &acquire); err != nil {
		t.Fatalf("exact queued replay after disable: %v", err)
	}
	transition := BackupJobOperationTransition{
		JobID: job.ID, Token: acquire.Token, Operation: acquire.Operation,
		HolderNodeID: acquire.HolderNodeID, OccurredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
	if err := StartBackupJobOperationTxn(db, &transition); err == nil || !strings.Contains(err.Error(), "backup_target_disabled") {
		t.Fatalf("queued start error=%v", err)
	}
	if err := AbortBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("abort queued work: %v", err)
	}
	acquire.Token = "new-token"
	if err := AcquireBackupJobOperationTxn(db, &acquire); err == nil || !strings.Contains(err.Error(), "backup_target_disabled") {
		t.Fatalf("new acquire error=%v", err)
	}
	// Historical log payloads omit the version gate and must still replay.
	acquire.Token = "legacy-token"
	acquire.RequireEnabledTarget = false
	if err := AcquireBackupJobOperationTxn(db, &acquire); err != nil {
		t.Fatalf("legacy acquire replay: %v", err)
	}
	transition.Token = acquire.Token
	transition.RequireEnabledTarget = false
	if err := StartBackupJobOperationTxn(db, &transition); err != nil {
		t.Fatalf("legacy start replay: %v", err)
	}
}

func TestDisabledBackupTargetRejectsNewAndQueuedOOBRestore(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetRestoreOperation{})
	target := BackupTarget{ID: 811, Name: "target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	acquire := BackupTargetRestoreOperationAcquire{
		Token: "restore-token", TargetID: target.ID, HolderNodeID: "node-a",
		DestinationDataset: "tank/restore", RequestPayload: "{}", AcquiredAt: time.Now().UTC(),
		RequireEnabledTarget: true,
	}
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("acquire before disable: %v", err)
	}
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("exact restore replay after disable: %v", err)
	}
	transition := BackupTargetRestoreOperationTransition{
		Token: acquire.Token, TargetID: acquire.TargetID, HolderNodeID: acquire.HolderNodeID,
		DestinationDataset: acquire.DestinationDataset, RequestPayload: acquire.RequestPayload,
		OccurredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err == nil || !strings.Contains(err.Error(), "backup_target_disabled") {
		t.Fatalf("queued restore start error=%v", err)
	}
	if err := AbortBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("abort queued restore: %v", err)
	}
	acquire.Token = "new-restore-token"
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err == nil || !strings.Contains(err.Error(), "backup_target_disabled") {
		t.Fatalf("new restore acquire error=%v", err)
	}
	acquire.Token = "legacy-restore-token"
	acquire.RequireEnabledTarget = false
	if err := AcquireBackupTargetRestoreOperationTxn(db, &acquire); err != nil {
		t.Fatalf("legacy restore acquire replay: %v", err)
	}
	transition.Token = acquire.Token
	transition.RequireEnabledTarget = false
	if err := StartBackupTargetRestoreOperationTxn(db, &transition); err != nil {
		t.Fatalf("legacy restore start replay: %v", err)
	}
}
