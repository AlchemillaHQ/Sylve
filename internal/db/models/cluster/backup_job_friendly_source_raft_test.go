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

func TestFSMDispatcherBackupJobFriendlySourceCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	// seed jobs
	seedJobs := []BackupJob{
		{ID: 1, Name: "job-1", TargetID: 10, Mode: BackupJobModeDataset,
			SourceDataset: "tank/a", CronExpr: "* * * * *", Enabled: true, FriendlySrc: "old-src"},
		{ID: 2, Name: "job-2", TargetID: 10, Mode: BackupJobModeDataset,
			SourceDataset: "tank/b", CronExpr: "* * * * *", Enabled: true, FriendlySrc: "old-src"},
		{ID: 3, Name: "job-3", TargetID: 10, Mode: BackupJobModeDataset,
			SourceDataset: "tank/c", CronExpr: "* * * * *", Enabled: true, FriendlySrc: "keep-me"},
	}
	for _, j := range seedJobs {
		if err := db.Create(&j).Error; err != nil {
			t.Fatalf("seed job %d: %v", j.ID, err)
		}
	}

	t.Run("update valid jobIds with friendlySrc", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{1, 2},
			"friendlySrc": "new-src",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}

		var job1 BackupJob
		db.First(&job1, 1)
		if job1.FriendlySrc != "new-src" {
			t.Fatalf("job-1 not updated: %q", job1.FriendlySrc)
		}
		var job2 BackupJob
		db.First(&job2, 2)
		if job2.FriendlySrc != "new-src" {
			t.Fatalf("job-2 not updated: %q", job2.FriendlySrc)
		}
		var job3 BackupJob
		db.First(&job3, 3)
		if job3.FriendlySrc != "keep-me" {
			t.Fatalf("job-3 should not be updated: %q", job3.FriendlySrc)
		}
	})

	t.Run("update empty friendlySrc returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{1},
			"friendlySrc": "",
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for empty friendlySrc, got nil")
		}
		if !strings.Contains(err.Error(), "friendly_src_required") {
			t.Fatalf("expected friendly src required error, got: %v", err)
		}
	})

	t.Run("update whitespace-only friendlySrc returns error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{1},
			"friendlySrc": "   ",
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		})
		if err == nil {
			t.Fatal("expected error for whitespace-only friendlySrc, got nil")
		}
		if !strings.Contains(err.Error(), "friendly_src_required") {
			t.Fatalf("expected friendly src required error, got: %v", err)
		}
	})

	t.Run("update empty jobIds list is no-op", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{},
			"friendlySrc": "should-not-matter",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("empty jobIds should be no-op: %v", err)
		}
	})

	t.Run("update jobIds with a 0 filters it out", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{0, 3},
			"friendlySrc": "filtered-zero",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update with zero-id: %v", err)
		}

		var job3 BackupJob
		db.First(&job3, 3)
		if job3.FriendlySrc != "filtered-zero" {
			t.Fatalf("job-3 not updated (0 should be filtered): %q", job3.FriendlySrc)
		}
	})

	t.Run("update duplicate jobIds deduplicates", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{1, 1, 1},
			"friendlySrc": "dedup-test",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("update with dupes: %v", err)
		}
	})

	t.Run("update non-existent jobId no error", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"jobIds":      []uint{999},
			"friendlySrc": "no-match",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update", Data: raw,
		}); err != nil {
			t.Fatalf("non-existent jobId should not error: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "backup_job_friendly_source", Action: "update",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
