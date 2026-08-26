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

func TestBackupJobUpdateRejectsDeletedBeforeApplyAndRollsBackReadiness(t *testing.T) {
	db := newClusterModelTestDB(
		t,
		&BackupTarget{},
		&BackupTargetNodeReadiness{},
		&BackupJob{},
	)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	target := BackupTarget{
		ID: 91, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := BackupJob{
		ID: 92, Name: "before-delete", TargetID: target.ID, RunnerNodeID: "node-a",
		Mode: BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	verifiedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	readyUntil := verifiedAt.Add(10 * time.Minute)
	updatedJob := job
	updatedJob.Name = "must-not-apply"
	payload, err := json.Marshal(BackupJobCommandPayload{
		Job: updatedJob,
		TargetReadiness: &BackupTargetNodeReadinessUpdate{
			TargetID: target.ID, NodeID: job.RunnerNodeID,
			TargetFingerprint:   BackupTargetConnectivityFingerprint(&target),
			ValidationSucceeded: true,
			LastVerifiedAt:      verifiedAt,
			ReadyUntil:          &readyUntil,
		},
	})
	if err != nil {
		t.Fatalf("marshal prepared update: %v", err)
	}

	// Simulate a delete command committing after proposal validation but before
	// this prepared update reaches deterministic apply.
	if err := db.Delete(&BackupJob{}, job.ID).Error; err != nil {
		t.Fatalf("delete job before apply: %v", err)
	}

	err = applyFSMCommand(t, fsm, Command{
		Type: "backup_job", Action: "update", Data: payload,
	})
	if err == nil || !strings.Contains(err.Error(), "backup_job_not_found") {
		t.Fatalf("deleted-before-apply update error = %v", err)
	}

	var jobCount int64
	if err := db.Model(&BackupJob{}).Where("id = ?", job.ID).Count(&jobCount).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("failed update recreated deleted job")
	}

	var readinessCount int64
	if err := db.Model(&BackupTargetNodeReadiness{}).
		Where("target_id = ? AND node_id = ?", target.ID, job.RunnerNodeID).
		Count(&readinessCount).Error; err != nil {
		t.Fatalf("count readiness: %v", err)
	}
	if readinessCount != 0 {
		t.Fatalf("failed update committed %d readiness rows", readinessCount)
	}
}
