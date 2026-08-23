// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
)

func backupTargetProvisionTestPrepare(token string) BackupTargetProvisionPrepare {
	target := BackupTarget{
		ID: 701, Name: "provisioned", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "private-key", BackupRoot: "tank/backups", CreateBackupRoot: true, Enabled: true,
	}
	return BackupTargetProvisionPrepare{
		Token: token, Target: BackupTargetToReplicationPayload(target),
		ProposedFingerprint: BackupTargetConfigurationFingerprint(&target),
	}
}

func TestBackupTargetProvisionPrepareCompleteAndReplay(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetProvisionOperation{})
	prepare := backupTargetProvisionTestPrepare("provision-token")
	if err := PrepareBackupTargetProvisionOperationTxn(db, &prepare); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := PrepareBackupTargetProvisionOperationTxn(db, &prepare); err != nil {
		t.Fatalf("prepare replay: %v", err)
	}
	var targetCount int64
	if err := db.Model(&BackupTarget{}).Count(&targetCount).Error; err != nil || targetCount != 0 {
		t.Fatalf("target visible before completion count=%d err=%v", targetCount, err)
	}
	transition := BackupTargetProvisionTransition{
		Token: prepare.Token, TargetID: prepare.Target.ID, ProposedFingerprint: prepare.ProposedFingerprint,
	}
	if err := CompleteBackupTargetProvisionOperationTxn(db, &transition); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := CompleteBackupTargetProvisionOperationTxn(db, &transition); err != nil {
		t.Fatalf("complete replay: %v", err)
	}
	var target BackupTarget
	if err := db.First(&target, prepare.Target.ID).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if BackupTargetConfigurationFingerprint(&target) != prepare.ProposedFingerprint {
		t.Fatalf("committed target mismatch: %+v", target)
	}
	var operation BackupTargetProvisionOperation
	if err := db.First(&operation, "token = ?", prepare.Token).Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if operation.State != BackupTargetProvisionStateCompleted || operation.Revision != 2 || operation.TargetPayload != "" {
		t.Fatalf("operation: %+v", operation)
	}
}

func TestBackupTargetProvisionReservationsAndStaleTransitions(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetProvisionOperation{})
	prepare := backupTargetProvisionTestPrepare("token-one")
	if err := PrepareBackupTargetProvisionOperationTxn(db, &prepare); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	tampered := prepare
	tampered.Target.BackupRoot = "tank/other"
	if err := PrepareBackupTargetProvisionOperationTxn(db, &tampered); err == nil {
		t.Fatal("tampered exact token was accepted")
	}
	concurrent := prepare
	concurrent.Token = "token-two"
	if err := PrepareBackupTargetProvisionOperationTxn(db, &concurrent); err == nil ||
		!strings.Contains(err.Error(), "provision_pending") {
		t.Fatalf("concurrent reservation error=%v", err)
	}
	create := BackupTargetCreateV2{Target: prepare.Target, ProposedFingerprint: prepare.ProposedFingerprint}
	if err := ApplyBackupTargetCreateV2Txn(db, &create); err == nil || !strings.Contains(err.Error(), "provision_pending") {
		t.Fatalf("direct create bypassed reservation: %v", err)
	}
	transition := BackupTargetProvisionTransition{
		Token: prepare.Token, TargetID: prepare.Target.ID, ProposedFingerprint: strings.Repeat("0", 64),
	}
	if err := CompleteBackupTargetProvisionOperationTxn(db, &transition); err == nil ||
		!strings.Contains(err.Error(), "token_mismatch") {
		t.Fatalf("stale complete error=%v", err)
	}
}

func TestBackupTargetProvisionDefiniteFailureLeavesTargetAbsent(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupTargetProvisionOperation{})
	prepare := backupTargetProvisionTestPrepare("failed-token")
	if err := PrepareBackupTargetProvisionOperationTxn(db, &prepare); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	transition := BackupTargetProvisionTransition{
		Token: prepare.Token, TargetID: prepare.Target.ID,
		ProposedFingerprint: prepare.ProposedFingerprint, Error: "permission denied",
	}
	if err := FailBackupTargetProvisionOperationTxn(db, &transition); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := FailBackupTargetProvisionOperationTxn(db, &transition); err != nil {
		t.Fatalf("fail replay: %v", err)
	}
	var failed BackupTargetProvisionOperation
	if err := db.First(&failed, "token = ?", prepare.Token).Error; err != nil || failed.TargetPayload != "" {
		t.Fatalf("failed receipt retained key payload=%q err=%v", failed.TargetPayload, err)
	}
	if err := CompleteBackupTargetProvisionOperationTxn(db, &BackupTargetProvisionTransition{
		Token: prepare.Token, TargetID: prepare.Target.ID, ProposedFingerprint: prepare.ProposedFingerprint,
	}); err == nil || !strings.Contains(err.Error(), "not_completable") {
		t.Fatalf("failed operation completed: %v", err)
	}
	var count int64
	if err := db.Model(&BackupTarget{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed provision target count=%d err=%v", count, err)
	}
}
