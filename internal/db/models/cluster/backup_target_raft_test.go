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

	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFSMDispatcherBackupTargetCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupTarget{}, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	target := BackupTarget{
		ID: 10, Name: "target-a", SSHHost: "user@host-a", SSHPort: 22, SSHKey: "key-a",
		BackupRoot: "pool-a/backups", Description: "first target",
	}
	createPayload, _ := json.Marshal(BackupTargetCreateV3{
		BackupTargetCreateV2: BackupTargetCreateV2{
			Target:              BackupTargetToReplicationPayload(target),
			ProposedFingerprint: BackupTargetConfigurationFingerprint(&target),
		},
		HostKey:    testutil.SSHHostKey(t),
		OccurredAt: time.Now().UTC(),
	})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "create_v3",
		Data:   createPayload,
	}); err != nil {
		t.Fatalf("backup target create apply failed: %v", err)
	}

	var created BackupTarget
	if err := db.First(&created, 10).Error; err != nil {
		t.Fatalf("failed to fetch created backup target: %v", err)
	}
	if created.Name != "target-a" || created.SSHHost != "user@host-a" || created.BackupRoot != "pool-a/backups" ||
		created.SSHKeyPath != "" || created.SSHKey != "key-a" {
		t.Fatalf("created backup target mismatch: %+v", created)
	}

	deletePayload, _ := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: 10})
	if err := applyFSMCommand(t, fsm, Command{
		Type:   "backup_target",
		Action: "delete_v2",
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
		Action: "delete_v2",
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
		Action: "create_v3",
		Data:   json.RawMessage(`"bad-payload"`),
	})
	if err == nil {
		t.Fatal("expected handler error for malformed backup target payload, got nil")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Fatalf("expected handler wrapped error, got: %v", err)
	}
}
