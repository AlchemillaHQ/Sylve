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

func TestFSMDispatcherBackupJobCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{}, &BackupEvent{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("create valid job dataset mode", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 1, Name: "daily-backup", TargetID: 10,
			Mode: BackupJobModeDataset, SourceDataset: "tank/data",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create", Data: raw,
		}); err != nil {
			t.Fatalf("create dataset job failed: %v", err)
		}

		var job BackupJob
		if err := db.First(&job, 1).Error; err != nil {
			t.Fatalf("fetch job: %v", err)
		}
		if job.Name != "daily-backup" || job.Mode != BackupJobModeDataset {
			t.Fatalf("job mismatch: name=%q mode=%q", job.Name, job.Mode)
		}
		if job.SourceDataset != "tank/data" {
			t.Fatalf("source dataset mismatch: %q", job.SourceDataset)
		}
		if !job.Enabled {
			t.Fatal("expected enabled=true")
		}
	})

	t.Run("create valid job jail mode", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 2, Name: "jail-backup", TargetID: 10,
			Mode: BackupJobModeJail, JailRootDataset: "tank/jails",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create", Data: raw,
		})
		if err != nil {
			t.Fatalf("create jail job failed: %v", err)
		}
	})

	t.Run("create valid job vm mode", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 3, Name: "vm-backup", TargetID: 10,
			Mode: BackupJobModeVM, SourceDataset: "tank/vms/vm-1",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create", Data: raw,
		})
		if err != nil {
			t.Fatalf("create vm job failed: %v", err)
		}
	})

	t.Run("create with invalid mode returns error", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 4, Name: "bad-mode", TargetID: 10,
			Mode: "invalid", SourceDataset: "tank/data",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for invalid mode, got nil")
		}
		if !strings.Contains(err.Error(), "invalid_backup_job_mode") {
			t.Fatalf("expected invalid mode error, got: %v", err)
		}
	})

	t.Run("create with empty mode returns error", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 5, Name: "empty-mode", TargetID: 10,
			Mode: "", SourceDataset: "tank/data",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for empty mode, got nil")
		}
	})

	t.Run("update change name and prune settings", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 1, Name: "daily-backup-updated", TargetID: 10,
			Mode: BackupJobModeDataset, SourceDataset: "tank/data-updated",
			PruneKeepLast: 30, PruneTarget: true,
			CronExpr: "0 3 * * *", Enabled: true,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}

		var job BackupJob
		db.First(&job, 1)
		if job.Name != "daily-backup-updated" {
			t.Fatalf("name not updated: %q", job.Name)
		}
		if job.PruneKeepLast != 30 {
			t.Fatalf("prune_keep_last not updated: %d", job.PruneKeepLast)
		}
		if !job.PruneTarget {
			t.Fatal("prune_target not updated to true")
		}
		if job.SourceDataset != "tank/data-updated" {
			t.Fatalf("source_dataset not updated: %q", job.SourceDataset)
		}
	})

	t.Run("update set enabled=false persists boolean false", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 1, Name: "daily-backup-updated", TargetID: 10,
			Mode: BackupJobModeDataset, SourceDataset: "tank/data-updated",
			CronExpr: "0 3 * * *", Enabled: false,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update with enabled=false failed: %v", err)
		}

		var job BackupJob
		db.First(&job, 1)
		if job.Enabled {
			t.Fatal("expected enabled=false after update, got true")
		}
	})

	t.Run("update with invalid mode returns error", func(t *testing.T) {
		raw, _ := json.Marshal(BackupJob{
			ID: 1, Name: "bad-update", TargetID: 10,
			Mode: "invalid", SourceDataset: "tank/data",
			CronExpr: "0 2 * * *", Enabled: true,
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for invalid mode on update, got nil")
		}
	})

	t.Run("delete existing job with no events", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"id": 3})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "delete", Data: raw,
		}); err != nil {
			t.Fatalf("delete failed: %v", err)
		}

		var count int64
		db.Model(&BackupJob{}).Where("id = ?", 3).Count(&count)
		if count != 0 {
			t.Fatalf("expected job deleted, got %d", count)
		}
	})

	t.Run("delete job with running event returns error", func(t *testing.T) {
		// seed a running event for job id=1
		if err := db.Create(&BackupEvent{
			JobID: ptr[uint](1), Status: "running",
		}).Error; err != nil {
			t.Fatalf("seed running event: %v", err)
		}

		deleteRaw, _ := json.Marshal(map[string]any{"id": 1})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "delete", Data: deleteRaw,
		})
		if err == nil {
			t.Fatal("expected error for running job, got nil")
		}
		if !strings.Contains(err.Error(), "backup_job_running") {
			t.Fatalf("expected running error, got: %v", err)
		}
	})

	t.Run("delete id=0 is no-op", func(t *testing.T) {
		deleteRaw, _ := json.Marshal(map[string]any{"id": 0})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "delete", Data: deleteRaw,
		}); err != nil {
			t.Fatalf("delete id=0 should be no-op: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job", Action: "create",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
