// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"strings"
	"testing"
)

func TestInsertBackupJobTxnRejectsIDCollision(t *testing.T) {
	db := newClusterModelTestDB(t, &BackupJob{})
	existing := BackupJob{
		ID: 77, Name: "existing-job", TargetID: 1, RunnerNodeID: "node-a",
		Mode: BackupJobModeDataset, SourceDataset: "tank/existing",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing job: %v", err)
	}

	incoming := existing
	incoming.Name = "must-not-overwrite"
	incoming.SourceDataset = "tank/replacement"
	err := InsertBackupJobTxn(db, &incoming)
	if err == nil || !strings.Contains(err.Error(), "backup_job_id_conflict") {
		t.Fatalf("same-ID insert error = %v", err)
	}

	var stored BackupJob
	if err := db.First(&stored, existing.ID).Error; err != nil {
		t.Fatalf("reload existing job: %v", err)
	}
	if stored.Name != existing.Name || stored.SourceDataset != existing.SourceDataset {
		t.Fatalf("same-ID insert changed existing job: %+v", stored)
	}
}
