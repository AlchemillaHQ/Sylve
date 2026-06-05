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

func TestFSMDispatcherBackupJobStateCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	// seed a job to update
	if err := db.Create(&BackupJob{
		ID: 1, Name: "state-test", TargetID: 10,
		Mode: BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 2 * * *", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	t.Run("update all fields", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		nextRun := now.Add(24 * time.Hour)
		raw, _ := json.Marshal(map[string]any{
			"jobId":      1,
			"lastRunAt":  now,
			"lastStatus": "success",
			"lastError":  "",
			"nextRunAt":  nextRun,
			"encrypted":  true,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}

		var job BackupJob
		db.First(&job, 1)
		if job.LastStatus != "success" {
			t.Fatalf("last_status mismatch: %q", job.LastStatus)
		}
		if job.Encrypted != true {
			t.Fatal("expected encrypted=true")
		}
		if job.LastRunAt == nil || !job.LastRunAt.Equal(now) {
			t.Fatalf("last_run_at mismatch: %v", job.LastRunAt)
		}
		if job.NextRunAt == nil || !job.NextRunAt.Equal(nextRun) {
			t.Fatalf("next_run_at mismatch: %v", job.NextRunAt)
		}
	})

	t.Run("update status=success", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "success"})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("status=success failed: %v", err)
		}
	})

	t.Run("update status=failed", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "failed"})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("status=failed failed: %v", err)
		}
	})

	t.Run("update status=running", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "running"})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("status=running failed: %v", err)
		}
	})

	t.Run("update status=blocked", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "blocked"})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("status=blocked failed: %v", err)
		}
	})

	t.Run("update trims and lowercases status", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "  Success  "})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update trimmed status failed: %v", err)
		}

		var job BackupJob
		db.First(&job, 1)
		if job.LastStatus != "success" {
			t.Fatalf("expected trimmed+lowered 'success', got: %q", job.LastStatus)
		}
	})

	t.Run("update empty status returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": ""})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for empty status, got nil")
		}
		if !strings.Contains(err.Error(), "last_status_required") {
			t.Fatalf("expected status required error, got: %v", err)
		}
	})

	t.Run("update invalid status returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 1, "lastStatus": "unknown"})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for unknown status, got nil")
		}
		if !strings.Contains(err.Error(), "invalid_last_status") {
			t.Fatalf("expected invalid status error, got: %v", err)
		}
	})

	t.Run("update jobId=0 returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"jobId": 0, "lastStatus": "success"})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for jobId=0, got nil")
		}
		if !strings.Contains(err.Error(), "invalid_job_id") {
			t.Fatalf("expected invalid job_id error, got: %v", err)
		}
	})

	t.Run("update nil nextRunAt persists nil", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobId": 1, "lastStatus": "success",
			"nextRunAt": nil,
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update with nil nextRunAt: %v", err)
		}

		var job BackupJob
		db.First(&job, 1)
		if job.NextRunAt != nil {
			t.Fatalf("expected nil nextRunAt, got %v", job.NextRunAt)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_state", Action: "update",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
