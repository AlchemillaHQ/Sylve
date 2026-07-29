// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestCleanupOrphanBackupEventsPreservesActiveRestoreObservability(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupEvent{})
	now := time.Now().UTC()
	missingJobID := uint(99)
	events := []clusterModels.BackupEvent{
		{JobID: &missingJobID, Mode: "restore", Status: "queued", StartedAt: now},
		{JobID: &missingJobID, Mode: "restore", Status: "running", StartedAt: now},
		{JobID: &missingJobID, Mode: "restore", Status: "failed", StartedAt: now, CompletedAt: &now},
	}
	if err := database.Create(&events).Error; err != nil {
		t.Fatalf("create events: %v", err)
	}
	if err := CleanupOrphanBackupEvents(database); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var remaining []clusterModels.BackupEvent
	if err := database.Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining events: %v", err)
	}
	if len(remaining) != 2 || remaining[0].Status != "queued" || remaining[1].Status != "running" {
		t.Fatalf("active restore events were pruned: %+v", remaining)
	}
}

func TestBackupEventHardCapNeverDeletesQueuedOrRunningEvents(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupEvent{})
	now := time.Now().UTC()
	events := make([]clusterModels.BackupEvent, 0, BackupEventMaxRows+3)
	for i := 0; i < BackupEventMaxRows+1; i++ {
		completed := now.Add(-time.Duration(i) * time.Second)
		events = append(events, clusterModels.BackupEvent{
			Mode: "restore", Status: "success", StartedAt: completed, CompletedAt: &completed,
		})
	}
	events = append(events,
		clusterModels.BackupEvent{Mode: "restore", Status: "queued", StartedAt: now.Add(-time.Hour)},
		clusterModels.BackupEvent{Mode: "restore", Status: "running", StartedAt: now.Add(-time.Hour)},
	)
	if err := database.CreateInBatches(&events, 500).Error; err != nil {
		t.Fatalf("create events: %v", err)
	}
	if err := EnforceBackupEventRetention(database, now); err != nil {
		t.Fatalf("enforce retention: %v", err)
	}

	var activeCount int64
	if err := database.Model(&clusterModels.BackupEvent{}).
		Where("status IN ?", []string{"queued", "running"}).
		Count(&activeCount).Error; err != nil {
		t.Fatalf("count active events: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("active event count=%d, want 2", activeCount)
	}
	var total int64
	if err := database.Model(&clusterModels.BackupEvent{}).Count(&total).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if total != BackupEventMaxRows {
		t.Fatalf("retained rows=%d, want %d", total, BackupEventMaxRows)
	}
}
