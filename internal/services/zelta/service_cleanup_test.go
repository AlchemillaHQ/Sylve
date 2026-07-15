// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
)

func TestCleanupStaleEventsSkipsActiveAndRecentlyHeartbeatingEvents(t *testing.T) {
	db := newZeltaServiceTestDB(t, &clusterModels.BackupEvent{})
	service := NewService(db, nil, nil, nil, nil, nil, nil)

	now := time.Now().UTC()
	staleTime := now.Add(-time.Hour)
	recentTime := now.Add(-time.Minute)

	jobRecent := uint(101)
	jobActive := uint(102)
	jobStale := uint(103)

	recentEvent := clusterModels.BackupEvent{
		JobID:          &jobRecent,
		Mode:           "backup",
		Status:         "running",
		SourceDataset:  "pool/src/recent",
		TargetEndpoint: "target/recent",
		StartedAt:      staleTime,
	}
	if err := db.Create(&recentEvent).Error; err != nil {
		t.Fatalf("failed to create recent event: %v", err)
	}
	if err := db.Model(&recentEvent).UpdateColumns(map[string]any{
		"started_at": staleTime,
		"updated_at": recentTime,
	}).Error; err != nil {
		t.Fatalf("failed to set recent event timestamps: %v", err)
	}

	activeEvent := clusterModels.BackupEvent{
		JobID:          &jobActive,
		Mode:           "backup",
		Status:         "running",
		SourceDataset:  "pool/src/active",
		TargetEndpoint: "target/active",
		StartedAt:      staleTime,
	}
	if err := db.Create(&activeEvent).Error; err != nil {
		t.Fatalf("failed to create active event: %v", err)
	}
	if err := db.Model(&activeEvent).UpdateColumns(map[string]any{
		"started_at": staleTime,
		"updated_at": staleTime,
	}).Error; err != nil {
		t.Fatalf("failed to set active event timestamps: %v", err)
	}

	staleEvent := clusterModels.BackupEvent{
		JobID:          &jobStale,
		Mode:           "backup",
		Status:         "running",
		SourceDataset:  "pool/src/stale",
		TargetEndpoint: "target/stale",
		StartedAt:      staleTime,
	}
	if err := db.Create(&staleEvent).Error; err != nil {
		t.Fatalf("failed to create stale event: %v", err)
	}
	if err := db.Model(&staleEvent).UpdateColumns(map[string]any{
		"started_at": staleTime,
		"updated_at": staleTime,
	}).Error; err != nil {
		t.Fatalf("failed to set stale event timestamps: %v", err)
	}

	service.acquireJob(jobActive)
	defer service.releaseJob(jobActive)

	if err := service.CleanupStaleEvents(context.Background(), 15*time.Minute); err != nil {
		t.Fatalf("cleanup stale events failed: %v", err)
	}

	if err := db.First(&recentEvent, recentEvent.ID).Error; err != nil {
		t.Fatalf("failed to reload recent event: %v", err)
	}
	if recentEvent.Status != "running" {
		t.Fatalf("expected recent event to remain running, got %q", recentEvent.Status)
	}

	if err := db.First(&activeEvent, activeEvent.ID).Error; err != nil {
		t.Fatalf("failed to reload active event: %v", err)
	}
	if activeEvent.Status != "running" {
		t.Fatalf("expected active event to remain running, got %q", activeEvent.Status)
	}

	if err := db.First(&staleEvent, staleEvent.ID).Error; err != nil {
		t.Fatalf("failed to reload stale event: %v", err)
	}
	if staleEvent.Status != "interrupted" {
		t.Fatalf("expected stale event to be interrupted, got %q", staleEvent.Status)
	}
	if staleEvent.Error != "process_crashed_or_restarted" {
		t.Fatalf("expected stale event error to be process_crashed_or_restarted, got %q", staleEvent.Error)
	}
	if staleEvent.CompletedAt == nil {
		t.Fatal("expected stale event to have completed_at set")
	}
}

func TestBackupEventHeartbeatUpdatesTimestamp(t *testing.T) {
	db := newZeltaServiceTestDB(t, &clusterModels.BackupEvent{})
	service := NewService(db, nil, nil, nil, nil, nil, nil)

	event := clusterModels.BackupEvent{
		Mode:           "backup",
		Status:         "running",
		SourceDataset:  "pool/src",
		TargetEndpoint: "target/dst",
		StartedAt:      time.Now().UTC().Add(-time.Hour),
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatalf("failed to create event: %v", err)
	}

	oldUpdatedAt := time.Now().UTC().Add(-time.Hour)
	if err := db.Model(&event).UpdateColumns(map[string]any{
		"started_at": oldUpdatedAt,
		"updated_at": oldUpdatedAt,
	}).Error; err != nil {
		t.Fatalf("failed to set event timestamps: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopHeartbeat := service.startBackupEventHeartbeat(ctx, event.ID, 10*time.Millisecond)
	defer stopHeartbeat()

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		var current clusterModels.BackupEvent
		if err := db.First(&current, event.ID).Error; err != nil {
			t.Fatalf("failed to reload heartbeat event: %v", err)
		}
		if current.UpdatedAt.After(oldUpdatedAt) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var current clusterModels.BackupEvent
	if err := db.First(&current, event.ID).Error; err != nil {
		t.Fatalf("failed to reload heartbeat event after timeout: %v", err)
	}
	t.Fatalf("expected heartbeat to update timestamp beyond %s, got %s", oldUpdatedAt, current.UpdatedAt)
}

func TestReconcileBackupRunAuditsFinalizesCompletedRun(t *testing.T) {
	database := newZeltaServiceTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	telemetry := newZeltaServiceTestDB(t, &infoModels.AuditRecord{})
	service := NewService(database, telemetry, nil, nil, nil, nil, nil)
	completedAt := time.Now().UTC()
	target := clusterModels.BackupTarget{ID: 1, Name: "target", SSHHost: "host", BackupRoot: "pool/backups"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID:         101,
		Name:       "failed backup",
		TargetID:   target.ID,
		Mode:       clusterModels.BackupJobModeDataset,
		CronExpr:   "0 * * * *",
		LastRunAt:  &completedAt,
		LastStatus: "failed",
		LastError:  "backup_target_unreachable",
	}
	if err := database.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	staleAudit := infoModels.AuditRecord{
		AsyncJobID:   &job.ID,
		AsyncJobType: "backup_job_run",
		Status:       "pending",
		Action:       "{}",
	}
	if err := telemetry.Create(&staleAudit).Error; err != nil {
		t.Fatalf("create stale audit: %v", err)
	}
	if err := telemetry.Model(&staleAudit).UpdateColumn("created_at", completedAt.Add(-time.Second)).Error; err != nil {
		t.Fatalf("set stale audit timestamp: %v", err)
	}

	freshAudit := infoModels.AuditRecord{
		AsyncJobID:   &job.ID,
		AsyncJobType: "backup_job_run",
		Status:       "pending",
		Action:       "{}",
	}
	if err := telemetry.Create(&freshAudit).Error; err != nil {
		t.Fatalf("create fresh audit: %v", err)
	}
	if err := telemetry.Model(&freshAudit).UpdateColumn("created_at", completedAt.Add(time.Second)).Error; err != nil {
		t.Fatalf("set fresh audit timestamp: %v", err)
	}

	if err := service.ReconcileBackupRunAudits(); err != nil {
		t.Fatalf("reconcile backup audits: %v", err)
	}
	if err := telemetry.First(&staleAudit, staleAudit.ID).Error; err != nil {
		t.Fatalf("reload stale audit: %v", err)
	}
	if staleAudit.Status != "failed" || staleAudit.Error != "backup_target_unreachable" {
		t.Fatalf("stale audit was not finalized: %+v", staleAudit)
	}
	if err := telemetry.First(&freshAudit, freshAudit.ID).Error; err != nil {
		t.Fatalf("reload fresh audit: %v", err)
	}
	if freshAudit.Status != "pending" {
		t.Fatalf("fresh audit was incorrectly finalized: %+v", freshAudit)
	}
}

func TestJobReservationPreventsDuplicateQueueing(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil)

	if !service.reserveJob(42) {
		t.Fatal("expected first reservation to succeed")
	}
	if service.reserveJob(42) {
		t.Fatal("expected duplicate reservation to fail while queued")
	}
	if !service.beginJob(42) {
		t.Fatal("expected queued job to transition to running")
	}
	if service.reserveJob(42) {
		t.Fatal("expected reservation to fail while running")
	}

	service.releaseJob(42)

	if !service.reserveJob(42) {
		t.Fatal("expected reservation to succeed again after release")
	}
	service.releaseReservedJob(42)
}
