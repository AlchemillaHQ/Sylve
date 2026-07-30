// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func seedBackupTargetReadinessTestTarget(t *testing.T) (*BackupTarget, *FSMDispatcher) {
	t.Helper()
	database := newClusterModelTestDB(t,
		&BackupTarget{}, &BackupTargetNodeReadiness{}, &BackupJob{},
	)
	target := &BackupTarget{
		ID: 71, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "private-key", BackupRoot: "tank/backups", Enabled: true,
	}
	if err := database.Create(target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	fsm := NewFSMDispatcher(database)
	RegisterDefaultHandlers(fsm)
	return target, fsm
}

func TestBackupTargetConnectivityFingerprintIgnoresManagedNodeLocalPath(t *testing.T) {
	left := &BackupTarget{
		SSHHost: "root@backup", SSHPort: 22, SSHKey: "managed-key",
		SSHKeyPath: "/node-a/target-1_id", BackupRoot: "tank/backups",
	}
	right := *left
	right.SSHKeyPath = "/node-b/target-1_id"
	if BackupTargetConnectivityFingerprint(left) != BackupTargetConnectivityFingerprint(&right) {
		t.Fatal("managed node-local paths must not alter target connectivity identity")
	}
	right.SSHKey = "replacement-key"
	if BackupTargetConnectivityFingerprint(left) == BackupTargetConnectivityFingerprint(&right) {
		t.Fatal("managed key replacement must alter target connectivity identity")
	}
}

func TestFSMBackupTargetReadinessUpdateIsFingerprintBound(t *testing.T) {
	target, fsm := seedBackupTargetReadinessTestTarget(t)
	verified := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	readyUntil := verified.Add(10 * time.Minute)
	update := BackupTargetNodeReadinessUpdate{
		TargetID: target.ID, NodeID: "node-a",
		TargetFingerprint:   BackupTargetConnectivityFingerprint(target),
		ValidationSucceeded: true, LastVerifiedAt: verified, ReadyUntil: &readyUntil,
	}
	payload, _ := json.Marshal(update)
	if err := applyFSMCommand(t, fsm, Command{
		Type: "backup_target_readiness", Action: "update", Data: payload,
	}); err != nil {
		t.Fatalf("apply readiness: %v", err)
	}
	var stored BackupTargetNodeReadiness
	if err := fsm.DB.First(&stored, "target_id = ? AND node_id = ?", target.ID, "node-a").Error; err != nil {
		t.Fatalf("load readiness: %v", err)
	}
	if !stored.ValidationSucceeded || stored.Revision != 1 || !stored.LastVerifiedAt.Equal(verified) {
		t.Fatalf("stored readiness: %+v", stored)
	}

	update.TargetFingerprint = strings.Repeat("0", 64)
	payload, _ = json.Marshal(update)
	err := applyFSMCommand(t, fsm, Command{
		Type: "backup_target_readiness", Action: "update", Data: payload,
	})
	if err == nil || !strings.Contains(err.Error(), "fingerprint_mismatch") {
		t.Fatalf("stale fingerprint error = %v", err)
	}
}

func TestBackupTargetConnectivityEditInvalidatesReadinessButMetadataEditPreservesIt(t *testing.T) {
	target, fsm := seedBackupTargetReadinessTestTarget(t)
	verified := time.Now().UTC()
	readyUntil := verified.Add(time.Hour)
	if err := ApplyBackupTargetNodeReadinessUpdateTxn(fsm.DB, &BackupTargetNodeReadinessUpdate{
		TargetID: target.ID, NodeID: "node-a",
		TargetFingerprint:   BackupTargetConnectivityFingerprint(target),
		ValidationSucceeded: true, LastVerifiedAt: verified, ReadyUntil: &readyUntil,
	}); err != nil {
		t.Fatalf("seed readiness: %v", err)
	}

	metadataOnly := *target
	metadataOnly.Name = "target-renamed"
	metadataOnly.Description = "new description"
	if err := UpsertBackupTargetTxn(fsm.DB, &metadataOnly); err != nil {
		t.Fatalf("metadata update: %v", err)
	}
	var count int64
	if err := fsm.DB.Model(&BackupTargetNodeReadiness{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("metadata edit readiness count=%d err=%v", count, err)
	}

	connectivityEdit := metadataOnly
	connectivityEdit.SSHHost = "root@new-backup"
	if err := UpsertBackupTargetTxn(fsm.DB, &connectivityEdit); err != nil {
		t.Fatalf("connectivity update: %v", err)
	}
	if err := fsm.DB.Model(&BackupTargetNodeReadiness{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("connectivity edit readiness count=%d err=%v", count, err)
	}
}

func TestBackupJobCommandRejectsStaleOrFailedTargetReadiness(t *testing.T) {
	target, fsm := seedBackupTargetReadinessTestTarget(t)
	verified := time.Now().UTC()
	readyUntil := verified.Add(time.Hour)
	job := BackupJob{
		ID: 81, Name: "job", TargetID: target.ID, RunnerNodeID: "node-a",
		Mode: BackupJobModeDataset, SourceDataset: "tank/source", CronExpr: "0 0 * * *",
	}
	failed := BackupTargetNodeReadinessUpdate{
		TargetID: target.ID, NodeID: "node-a",
		TargetFingerprint:   BackupTargetConnectivityFingerprint(target),
		ValidationSucceeded: false, LastVerifiedAt: verified, LastError: "dial failed",
	}
	payload, _ := json.Marshal(BackupJobCommandPayload{Job: job, TargetReadiness: &failed})
	err := applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: payload})
	if err == nil || !strings.Contains(err.Error(), "job_validation_failed") {
		t.Fatalf("failed receipt error = %v", err)
	}

	success := failed
	success.ValidationSucceeded = true
	success.LastError = ""
	success.ReadyUntil = &readyUntil
	success.NodeID = "node-b"
	payload, _ = json.Marshal(BackupJobCommandPayload{Job: job, TargetReadiness: &success})
	err = applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: payload})
	if err == nil || !strings.Contains(err.Error(), "job_scope_mismatch") {
		t.Fatalf("wrong-node receipt error = %v", err)
	}

	success.NodeID = "node-a"
	success.TargetFingerprint = strings.Repeat("f", 64)
	payload, _ = json.Marshal(BackupJobCommandPayload{Job: job, TargetReadiness: &success})
	err = applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: payload})
	if err == nil || !strings.Contains(err.Error(), "fingerprint_mismatch") {
		t.Fatalf("stale receipt error = %v", err)
	}
}

func TestDeleteBackupTargetRemovesReadiness(t *testing.T) {
	target, fsm := seedBackupTargetReadinessTestTarget(t)
	verified := time.Now().UTC()
	readyUntil := verified.Add(time.Hour)
	if err := ApplyBackupTargetNodeReadinessUpdateTxn(fsm.DB, &BackupTargetNodeReadinessUpdate{
		TargetID: target.ID, NodeID: "removed-node",
		TargetFingerprint:   BackupTargetConnectivityFingerprint(target),
		ValidationSucceeded: true, LastVerifiedAt: verified, ReadyUntil: &readyUntil,
	}); err != nil {
		t.Fatalf("seed readiness: %v", err)
	}
	if err := fsm.DB.Model(&BackupTarget{}).Where("id = ?", target.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable target: %v", err)
	}
	if err := DeleteBackupTargetTxn(fsm.DB, target.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	var count int64
	if err := fsm.DB.Model(&BackupTargetNodeReadiness{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("readiness count after delete=%d err=%v", count, err)
	}
}
