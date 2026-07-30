// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"strings"
	"testing"
)

func backupTargetUpdateV2Command(existing, proposed BackupTarget, kind string) BackupTargetUpdateV2 {
	return BackupTargetUpdateV2{
		TargetID:            existing.ID,
		Kind:                kind,
		ExpectedFingerprint: BackupTargetConfigurationFingerprint(&existing),
		ProposedFingerprint: BackupTargetConfigurationFingerprint(&proposed),
		Name:                proposed.Name,
		Description:         proposed.Description,
		Enabled:             proposed.Enabled,
		SSHKey:              proposed.SSHKey,
	}
}

func TestBackupTargetConfigurationFingerprintIgnoresNodeLocalPath(t *testing.T) {
	left := BackupTarget{ID: 90, Name: "target", SSHHost: "root@backup", SSHKey: "key", SSHKeyPath: "/node-a/key", BackupRoot: "tank/backups", Enabled: true}
	right := left
	right.SSHKeyPath = "/node-b/key"
	if BackupTargetConfigurationFingerprint(&left) != BackupTargetConfigurationFingerprint(&right) {
		t.Fatal("node-local path changed replicated configuration fingerprint")
	}
	right.SSHKey = "replacement"
	if BackupTargetConfigurationFingerprint(&left) == BackupTargetConfigurationFingerprint(&right) {
		t.Fatal("replacement key did not change configuration fingerprint")
	}
}

func TestBackupTargetUpdateV2Contracts(t *testing.T) {
	db := newClusterModelTestDB(t,
		&BackupTarget{}, &BackupTargetNodeReadiness{}, &BackupJob{},
		&BackupJobOperation{}, &BackupTargetRestoreOperation{},
	)
	target := BackupTarget{
		ID: 91, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "key-one", BackupRoot: "tank/backups", CreateBackupRoot: true,
		Description: "old", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	metadata := target
	metadata.Name = "renamed"
	metadata.Description = "new description"
	metadataCommand := backupTargetUpdateV2Command(target, metadata, BackupTargetUpdateKindMetadata)
	if err := ApplyBackupTargetUpdateV2Txn(db, &metadataCommand); err != nil {
		t.Fatalf("metadata update: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &metadataCommand); err != nil {
		t.Fatalf("metadata replay: %v", err)
	}

	disable := metadata
	disable.Enabled = false
	disableCommand := backupTargetUpdateV2Command(metadata, disable, BackupTargetUpdateKindDisable)
	if err := ApplyBackupTargetUpdateV2Txn(db, &disableCommand); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if err := db.Create(&BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "node-a",
		TargetFingerprint: BackupTargetConnectivityFingerprint(&disable), Revision: 1,
	}).Error; err != nil {
		t.Fatalf("seed readiness: %v", err)
	}
	rotation := disable
	rotation.SSHKey = "key-two"
	rotationCommand := backupTargetUpdateV2Command(disable, rotation, BackupTargetUpdateKindRotateKey)
	if err := ApplyBackupTargetUpdateV2Txn(db, &rotationCommand); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	var readinessCount int64
	if err := db.Model(&BackupTargetNodeReadiness{}).Where("target_id = ?", target.ID).Count(&readinessCount).Error; err != nil || readinessCount != 0 {
		t.Fatalf("rotation readiness count=%d err=%v", readinessCount, err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &rotationCommand); err != nil {
		t.Fatalf("rotate replay: %v", err)
	}

	enable := rotation
	enable.Enabled = true
	enableCommand := backupTargetUpdateV2Command(rotation, enable, BackupTargetUpdateKindEnable)
	if err := ApplyBackupTargetUpdateV2Txn(db, &enableCommand); err != nil {
		t.Fatalf("enable: %v", err)
	}

	var stored BackupTarget
	if err := db.First(&stored, target.ID).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if stored.Name != "renamed" || stored.Description != "new description" ||
		stored.SSHHost != target.SSHHost || stored.SSHPort != target.SSHPort ||
		stored.BackupRoot != target.BackupRoot || stored.CreateBackupRoot != target.CreateBackupRoot ||
		stored.SSHKey != "key-two" || !stored.Enabled || stored.SSHKeyPath != "" {
		t.Fatalf("stored target: %+v", stored)
	}
}

func TestBackupTargetUpdateV2RotationRequiresDisabledQuiescentTarget(t *testing.T) {
	db := newClusterModelTestDB(t,
		&BackupTarget{}, &BackupJob{}, &BackupJobOperation{}, &BackupTargetRestoreOperation{},
	)
	target := BackupTarget{ID: 92, Name: "target", SSHHost: "root@backup", SSHKey: "old", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	proposed := target
	proposed.SSHKey = "new"
	proposed.Enabled = false
	command := backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindRotateKey)
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil ||
		!strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("enabled rotation error=%v", err)
	}

	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	target.Enabled = false
	proposed.Enabled = false
	command = backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindRotateKey)
	job := BackupJob{ID: 501, Name: "job", TargetID: target.ID, Mode: BackupJobModeDataset, CronExpr: "0 0 * * *"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := db.Create(&BackupJobOperation{JobID: job.ID, Token: "token", Operation: BackupJobOperationBackup, State: BackupJobOperationRunning, HolderNodeID: "node", Revision: 1}).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil ||
		!strings.Contains(err.Error(), "active_operations") {
		t.Fatalf("active rotation error=%v", err)
	}
	if err := db.Delete(&BackupJobOperation{}, job.ID).Error; err != nil {
		t.Fatalf("delete operation: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err != nil {
		t.Fatalf("quiescent rotation: %v", err)
	}
}

func TestBackupTargetDeleteRequiresDisabledTarget(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	target := BackupTarget{ID: 94, Name: "target", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := DeleteBackupTargetTxn(db, target.ID); err == nil || !strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("enabled delete error=%v", err)
	}
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := DeleteBackupTargetTxn(db, target.ID); err != nil {
		t.Fatalf("delete disabled target: %v", err)
	}
}

func TestBackupTargetUpdateV2RejectsStaleCommand(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{})
	target := BackupTarget{ID: 93, Name: "target", SSHHost: "root@backup", SSHKey: "key", BackupRoot: "tank/backups", Enabled: true}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	proposed := target
	proposed.Description = "proposed"
	command := backupTargetUpdateV2Command(target, proposed, BackupTargetUpdateKindMetadata)
	if err := db.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("description", "concurrent").Error; err != nil {
		t.Fatalf("concurrent update: %v", err)
	}
	if err := ApplyBackupTargetUpdateV2Txn(db, &command); err == nil || !strings.Contains(err.Error(), "update_stale") {
		t.Fatalf("stale error=%v", err)
	}
}
