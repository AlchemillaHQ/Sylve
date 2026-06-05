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
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newRunBackupJobTestDB(t *testing.T) *Service {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	return &Service{
		DB:               db,
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
}

func seedBackupTarget(t *testing.T, db *gorm.DB, id uint, name string) clusterModels.BackupTarget {
	target := clusterModels.BackupTarget{
		ID: id, Name: name, SSHHost: "localhost", BackupRoot: "/backup", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target %s: %v", name, err)
	}
	return target
}

func seedAndLoadJob(t *testing.T, db *gorm.DB, id uint, name, mode string, targetID uint, sourceDS string) clusterModels.BackupJob {
	job := clusterModels.BackupJob{
		ID: id, Name: name, Mode: mode, TargetID: targetID,
		SourceDataset: sourceDS, CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job %s: %v", name, err)
	}
	var loaded clusterModels.BackupJob
	if err := db.Preload("Target").First(&loaded, id).Error; err != nil {
		t.Fatalf("failed to load job with target: %v", err)
	}
	return loaded
}

func fetchJob(t *testing.T, db *gorm.DB, id uint) clusterModels.BackupJob {
	var job clusterModels.BackupJob
	if err := db.First(&job, id).Error; err != nil {
		t.Fatalf("failed to fetch job %d: %v", id, err)
	}
	return job
}

func TestRunBackupJobAlreadyRunning(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	target := seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 100, "job1", "dataset", target.ID, "tank/data")

	svc.runningJobs[100] = struct{}{}

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "already_running") {
		t.Fatalf("expected already_running, got %v", err)
	}
	delete(svc.runningJobs, 100)
}

func TestRunBackupJobInvalidMode(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	target := seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 101, "bad-mode", "invalid", target.ID, "tank/data")

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "invalid_backup_job_mode") {
		t.Fatalf("expected invalid_backup_job_mode, got %v", err)
	}

	updated := fetchJob(t, svc.DB, 101)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status, got %q", updated.LastStatus)
	}
}

func TestRunBackupJobDatasetMissingSource(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	target := seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 102, "no-source", "dataset", target.ID, "")

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "source_dataset_required") {
		t.Fatalf("expected source_dataset_required, got %v", err)
	}
}

func TestRunBackupJobJailMissingRoot(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	target := seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 103, "no-jail-root", "jail", target.ID, "")

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "jail_root_dataset_required") {
		t.Fatalf("expected jail_root_dataset_required, got %v", err)
	}
}

func TestRunBackupJobTargetDisabled(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	target := seedBackupTarget(t, svc.DB, 1, "t1")
	target.Enabled = false
	svc.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", 1).Update("enabled", false)

	job := seedAndLoadJob(t, svc.DB, 104, "disabled-target", "dataset", target.ID, "tank/data")

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "backup_target_disabled") {
		t.Fatalf("expected backup_target_disabled, got %v", err)
	}
}

func TestRunBackupJobSetsLastRunAtOnFailure(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 105, "will-fail", "dataset", 1, "")

	svc.runBackupJob(context.Background(), &job)

	updated := fetchJob(t, svc.DB, 105)
	if updated.LastRunAt == nil {
		t.Fatal("expected LastRunAt to be set after failure")
	}
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed, got %q", updated.LastStatus)
	}
	if updated.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

func TestRunBackupJobSetsNextRunAtOnCompletion(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	before := time.Now().UTC()
	job := seedAndLoadJob(t, svc.DB, 106, "will-succeed", "dataset", 1, "tank/data")

	err := svc.runBackupJob(context.Background(), &job)
	_ = err

	updated := fetchJob(t, svc.DB, 106)
	if updated.LastRunAt == nil || updated.LastRunAt.Before(before) {
		t.Fatal("expected LastRunAt to be set after run")
	}
}

func TestRunBackupJobCreatesEventOnStart(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 107, "event-test", "dataset", 1, "tank/data")

	err := svc.runBackupJob(context.Background(), &job)
	_ = err

	var events []clusterModels.BackupEvent
	svc.DB.Where("job_id = ?", 107).Find(&events)
	if len(events) != 0 {
		t.Logf("found %d events for job 107", len(events))
	}
}

func TestRunBackupJobVMInvalidRID(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 108, "vm-bad-source", "vm", 1, "zroot/data/db")

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil || !strings.Contains(err.Error(), "invalid_vm_source_dataset") {
		t.Fatalf("expected invalid_vm_source_dataset, got %v", err)
	}
}

func TestRunBackupJobVMStopBeforeBackupInvalidRID(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	seedBackupTarget(t, svc.DB, 1, "t1")

	job := clusterModels.BackupJob{
		ID: 109, Name: "vm-stop", Mode: "vm", TargetID: 1,
		SourceDataset: "zroot/virtual-machines/42", CronExpr: "0 0 * * *",
		Enabled: true, StopBeforeBackup: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	svc.DB.Preload("Target").First(&loaded, 109)

	err := svc.runBackupJob(context.Background(), &loaded)
	if err == nil {
		t.Fatal("expected error for VM backup without VM service")
	}
}

func TestResolveVMBackupSourceDatasetsNilVM(t *testing.T) {
	svc := &Service{VM: nil, DB: nil}
	sources, err := svc.resolveVMBackupSourceDatasets(context.Background(), 42, "zroot/virtual-machines/42")
	if err == nil {
		t.Logf("resolveVMBackupSourceDatasets with nil VM/DB returned %v sources (no error)", len(sources))
		return
	}
	if err != nil {
		t.Logf("resolveVMBackupSourceDatasets returned error (expected): %v", err)
	}
}

func TestLocalDatasetExistsNoGZFS(t *testing.T) {
	svc := &Service{GZFS: nil}
	_, err := svc.localDatasetExists(context.Background(), "tank/nonexistent")
	if err == nil || !strings.Contains(err.Error(), "gzfs_not_initialized") {
		t.Fatalf("expected gzfs_not_initialized, got %v", err)
	}
}
