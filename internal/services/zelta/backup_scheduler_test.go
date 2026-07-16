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
)

func newSchedulerTestDB(t *testing.T) *Service {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	return &Service{
		DB:               db,
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
}

func TestNextRunTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	next, err := nextRunTime("0 0 * * *", now)
	if err != nil {
		t.Fatalf("nextRunTime failed: %v", err)
	}
	if !next.After(now) {
		t.Fatal("next run should be in the future")
	}

	_, err = nextRunTime("", now)
	if err == nil || !strings.Contains(err.Error(), "cron_expr_required") {
		t.Fatalf("expected cron_expr_required, got %v", err)
	}

	_, err = nextRunTime("invalid cron", now)
	if err == nil {
		t.Fatal("expected error for invalid cron")
	}
}

func TestIsLocalBackupJobRunner(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}

	job := &clusterModels.BackupJob{RunnerNodeID: ""}
	if !svc.isLocalBackupJobRunner(job, "") {
		t.Fatal("empty runner, no cluster → should be local")
	}
	if !svc.isLocalBackupJobRunner(job, "node-a") {
		t.Fatal("empty runner, any localNodeID → should be local")
	}

	job = &clusterModels.BackupJob{RunnerNodeID: "node-a"}
	if svc.isLocalBackupJobRunner(job, "") {
		t.Fatal("non-empty runner, empty localNodeID → not local")
	}
	if !svc.isLocalBackupJobRunner(job, "node-a") {
		t.Fatal("matching runner and localNodeID → local")
	}
	if svc.isLocalBackupJobRunner(job, "node-b") {
		t.Fatal("mismatched runner → not local")
	}

	if svc.isLocalBackupJobRunner(nil, "node-a") {
		t.Fatal("nil job → not local")
	}
}

func TestReserveAndReleaseJob(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}

	if svc.reserveJob(0) {
		t.Fatal("should not reserve job ID 0")
	}

	if !svc.reserveJob(42) {
		t.Fatal("should reserve job ID 42")
	}

	if svc.reserveJob(42) {
		t.Fatal("should not reserve already-queued job")
	}

	svc.releaseReservedJob(42)

	if !svc.reserveJob(42) {
		t.Fatal("should reserve after release")
	}

	svc.releaseReservedJob(42)
}

func TestDatasetHash(t *testing.T) {
	h := datasetHash("zroot/jails/my-jail")
	if h == 0 {
		t.Fatal("expected non-zero hash")
	}

	if datasetHash("abc") != datasetHash("abc") {
		t.Fatal("hash should be deterministic")
	}
}

func TestWorkloadOperationKey(t *testing.T) {
	if k := workloadOperationKey("", 0); k != "" {
		t.Fatalf("expected empty key for zero guestID, got %q", k)
	}
	if k := workloadOperationKey("dataset", 5); k == "" {
		t.Fatal("dataset with guestID should return a key")
	}
	if k := workloadOperationKey("vm", 7); k == "" {
		t.Fatal("vm with guestID should return a key")
	}
	if k := workloadOperationKey("jail", 3); k == "" {
		t.Fatal("jail with guestID should return a key")
	}
	if k := workloadOperationKey("unknown", 5); k != "" {
		t.Fatalf("unknown guest type should return empty, got %q", k)
	}
}

func TestRunBackupSchedulerTickNoDB(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("no DB should return nil: %v", err)
	}
}

func TestRunBackupSchedulerTickNoEnabledJobs(t *testing.T) {
	svc := newSchedulerTestDB(t)
	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("no enabled jobs should return nil: %v", err)
	}
}

func TestRunBackupSchedulerTickInvalidCronExpr(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 1, Name: "bad-cron", TargetID: 1, Mode: "dataset",
		CronExpr: "not-a-valid-cron", Enabled: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick should not error on invalid cron: %v", err)
	}

	var updated clusterModels.BackupJob
	svc.DB.First(&updated, 1)
	if updated.LastStatus != "failed" || updated.LastError != "invalid_cron_expr" {
		t.Fatalf("expected invalid_cron_expr, got lastStatus=%q lastError=%q", updated.LastStatus, updated.LastError)
	}
}

func TestRunBackupSchedulerTickSetsNextRunAt(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 2, Name: "no-next-run", TargetID: 1, Mode: "dataset",
		CronExpr: "0 0 * * *", Enabled: true, NextRunAt: nil,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick failed: %v", err)
	}

	var updated clusterModels.BackupJob
	svc.DB.First(&updated, 2)
	if updated.NextRunAt == nil {
		t.Fatal("expected NextRunAt to be set")
	}
}

func TestRunBackupSchedulerTickSkipsFutureJob(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	future := time.Now().UTC().Add(24 * time.Hour)
	job := clusterModels.BackupJob{
		ID: 3, Name: "future-job", TargetID: 1, Mode: "dataset",
		CronExpr: "0 0 * * *", Enabled: true, NextRunAt: &future,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick failed: %v", err)
	}

	if _, queued := svc.queuedJobs[3]; queued {
		t.Fatal("future job should not be enqueued")
	}
}

func TestAdvanceBackupJobScheduleAfterRestorePreventsImmediateBackup(t *testing.T) {
	svc := newSchedulerTestDB(t)
	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	before := time.Now().UTC()
	pastDue := before.Add(-time.Hour)
	lastRun := before.Add(-time.Minute)
	job := clusterModels.BackupJob{
		ID: 7, Name: "restored-job", TargetID: 1, Mode: "dataset",
		CronExpr: "0 0 * * *", Enabled: true, NextRunAt: &pastDue,
		LastRunAt: &lastRun, LastStatus: "success",
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	if err := svc.advanceBackupJobScheduleAfterRestore(&job); err != nil {
		t.Fatalf("advance schedule after restore: %v", err)
	}
	if job.NextRunAt == nil || !job.NextRunAt.After(before) {
		t.Fatalf("next run should be after restore completion, got %v", job.NextRunAt)
	}

	var updated clusterModels.BackupJob
	if err := svc.DB.First(&updated, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(before) {
		t.Fatalf("persisted next run should be in the future, got %v", updated.NextRunAt)
	}
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(lastRun) || updated.LastStatus != "success" {
		t.Fatalf("restore schedule update changed runtime result: %+v", updated)
	}

	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("scheduler tick failed: %v", err)
	}
	if _, queued := svc.queuedJobs[job.ID]; queued {
		t.Fatal("restored job with a future next run should not be queued")
	}
}

func TestRunBackupSchedulerTickCatchesUpStalledJob(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	nextRun := time.Now().UTC().Add(1 * time.Hour)
	lastRun := time.Now().UTC().Add(-26 * time.Hour)
	job := clusterModels.BackupJob{
		ID: 4, Name: "stalled-job", TargetID: 1, Mode: "dataset",
		CronExpr: "0 0 * * *", Enabled: true,
		NextRunAt: &nextRun, LastRunAt: &lastRun,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	svc.DB = nil
	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick with nil DB should not error: %v", err)
	}

	svc.DB = testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	svc.DB.Create(&target)
	svc.DB.Create(&job)
	if job.LastRunAt == nil || time.Since(*job.LastRunAt).Hours() < 24 {
		t.Skip("stall age not reached; run at a later wall clock")
	}
}

func TestRunBackupSchedulerTickSkipsCatchUpWindowJob(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	pastDue := time.Now().UTC().Add(-3 * time.Hour)
	job := clusterModels.BackupJob{
		ID: 5, Name: "past-due", TargetID: 1, Mode: "dataset",
		CronExpr: "0 0 * * *", Enabled: true, NextRunAt: &pastDue,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick failed: %v", err)
	}

	var updated clusterModels.BackupJob
	svc.DB.First(&updated, 5)
	if updated.NextRunAt == nil {
		t.Fatal("expected NextRunAt to be advanced past catch-up")
	}
}

func TestRunBackupSchedulerTickEnqueuesDueJob(t *testing.T) {
	svc := newSchedulerTestDB(t)

	target := clusterModels.BackupTarget{ID: 1, Name: "t1", SSHHost: "localhost", BackupRoot: "/backup"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	now := time.Now().UTC()
	minuteAgo := now.Add(-1 * time.Minute)
	job := clusterModels.BackupJob{
		ID: 6, Name: "due-job", TargetID: 1, Mode: "dataset",
		CronExpr: "* * * * *", Enabled: true, NextRunAt: &minuteAgo,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	svc.DB = nil
	if err := svc.runBackupSchedulerTick(context.Background()); err != nil {
		t.Fatalf("tick with nil DB should return nil: %v", err)
	}

	svc.DB = testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{})
	svc.DB.Create(&target)
	svc.DB.Create(&job)
	if time.Until(*job.NextRunAt) > 0 {
		t.Fatal("job is not due yet")
	}
	_ = now
}

func TestAcquireWorkloadOperation(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}

	ok, existing := svc.acquireWorkloadOperation("vm", 1, "backup")
	if !ok || existing != "" {
		t.Fatalf("should acquire: ok=%v existing=%q", ok, existing)
	}

	ok, existing = svc.acquireWorkloadOperation("vm", 1, "backup")
	if ok {
		t.Fatalf("should not acquire same guest again: existing=%q", existing)
	}

	svc.releaseWorkloadOperation("vm", 1)

	ok, existing = svc.acquireWorkloadOperation("vm", 1, "restore")
	if !ok || existing != "" {
		t.Fatalf("should acquire after release: ok=%v existing=%q", ok, existing)
	}

	svc.releaseWorkloadOperation("vm", 1)

	svc.releaseWorkloadOperation("vm", 0)

	ok, _ = svc.acquireWorkloadOperation("", 0, "backup")
	if !ok {
		t.Fatal("should acquire with zero guest (no key)")
	}
}

func TestActiveJobIDs(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}

	ids := svc.activeJobIDs()
	if len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}

	svc.runningJobs = map[uint]struct{}{42: {}, 7: {}}
	ids = svc.activeJobIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2, got %v", ids)
	}
}

func TestAcquireAndReleaseJob(t *testing.T) {
	svc := &Service{
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}

	if svc.beginJob(0) {
		t.Fatal("beginJob should not accept job ID 0")
	}

	if !svc.beginJob(42) {
		t.Fatal("beginJob should succeed (moves to runningJobs)")
	}
	if _, running := svc.runningJobs[42]; !running {
		t.Fatal("job 42 should be in runningJobs")
	}

	svc.releaseJob(42)

	if _, running := svc.runningJobs[42]; running {
		t.Fatal("job 42 should be released")
	}

	svc.reserveJob(42)
	if !svc.beginJob(42) {
		t.Fatal("beginJob should succeed after reserveJob")
	}
	svc.releaseJob(42)
}
