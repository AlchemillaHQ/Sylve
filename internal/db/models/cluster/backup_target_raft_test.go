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
)

func TestFSMDispatcherBackupTargetCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	createPayload, _ := json.Marshal(BackupTargetReplicationPayload{
		ID:               10,
		Name:             "target-a",
		SSHHost:          "user@host-a",
		SSHPort:          22,
		SSHKeyPath:       "/tmp/key-a",
		SSHKey:           "key-a",
		BackupRoot:       "pool-a/backups",
		CreateBackupRoot: false,
		Description:      "first target",
		Enabled:          true,
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "create",
		Data:   createPayload,
	}); err != nil {
		t.Fatalf("backup target create apply failed: %v", err)
	}

	var created BackupTarget
	if err := db.First(&created, 10).Error; err != nil {
		t.Fatalf("failed to fetch created backup target: %v", err)
	}
	if created.Name != "target-a" || created.SSHHost != "user@host-a" || created.BackupRoot != "pool-a/backups" {
		t.Fatalf("created backup target mismatch: %+v", created)
	}

	updatePayload, _ := json.Marshal(BackupTargetReplicationPayload{
		ID:               10,
		Name:             "target-a-updated",
		SSHHost:          "user@host-b",
		SSHPort:          2222,
		SSHKeyPath:       "/tmp/key-b",
		SSHKey:           "key-b",
		BackupRoot:       "pool-b/backups",
		CreateBackupRoot: true,
		Description:      "updated target",
		Enabled:          false,
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "update",
		Data:   updatePayload,
	}); err != nil {
		t.Fatalf("backup target update apply failed: %v", err)
	}

	var updated BackupTarget
	if err := db.First(&updated, 10).Error; err != nil {
		t.Fatalf("failed to fetch updated backup target: %v", err)
	}
	if updated.Name != "target-a-updated" || updated.SSHPort != 2222 || updated.Enabled != false {
		t.Fatalf("updated backup target mismatch: %+v", updated)
	}

	deletePayload, _ := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: 10})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "delete",
		Data:   deletePayload,
	}); err != nil {
		t.Fatalf("backup target delete apply failed: %v", err)
	}

	var count int64
	if err := db.Model(&BackupTarget{}).Where("id = ?", 10).Count(&count).Error; err != nil {
		t.Fatalf("failed to count deleted backup target: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected deleted backup target to be removed, found %d row(s)", count)
	}
}

func TestFSMDispatcherBackupTargetDeleteDeniedWhenInUse(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	if err := db.Create(&BackupTarget{
		ID:         20,
		Name:       "target-in-use",
		SSHHost:    "user@host",
		SSHPort:    22,
		BackupRoot: "pool/in-use",
		Enabled:    true,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup target: %v", err)
	}

	if err := db.Create(&BackupJob{
		ID:            1,
		Name:          "job-1",
		TargetID:      20,
		Mode:          BackupJobModeDataset,
		SourceDataset: "tank/source",
		CronExpr:      "* * * * *",
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup job: %v", err)
	}

	deletePayload, _ := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: 20})
	err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "delete",
		Data:   deletePayload,
	})
	if err == nil {
		t.Fatal("expected delete-in-use error, got nil")
	}
	if !strings.Contains(err.Error(), "target_in_use_by_backup_jobs") {
		t.Fatalf("expected target_in_use_by_backup_jobs error, got: %v", err)
	}

	var count int64
	if err := db.Model(&BackupTarget{}).Where("id = ?", 20).Count(&count).Error; err != nil {
		t.Fatalf("failed to count backup target after failed delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected backup target to remain after failed delete, found %d row(s)", count)
	}
}

func TestFSMDispatcherBackupTargetMalformedPayload(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "create",
		Data:   json.RawMessage(`"bad-payload"`),
	})
	if err == nil {
		t.Fatal("expected handler error for malformed backup target payload, got nil")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Fatalf("expected handler wrapped error, got: %v", err)
	}
}
