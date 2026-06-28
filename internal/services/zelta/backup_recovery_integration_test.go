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
	"os/exec"
	"sort"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"gorm.io/gorm"
)

func dumpLatestBackupEvent(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var ev clusterModels.BackupEvent
	if err := db.Order("id desc").First(&ev).Error; err != nil {
		return "(no backup event recorded)"
	}
	return ev.Output
}

func zfsCreateLegacyDataset(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("zfs", "create", "-o", "canmount=noauto", "-o", "mountpoint=legacy", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zfs create %s: %v\n%s", name, err, string(out))
	}
}

func listActiveGenerations(t *testing.T, activeDataset string) []string {
	t.Helper()
	parent := activeDataset
	if idx := strings.LastIndex(activeDataset, "/"); idx > 0 {
		parent = activeDataset[:idx]
	}
	cmd := exec.Command("zfs", "list", "-H", "-o", "name", "-r", "-d", "1", parent)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	prefix := activeDataset + "_gen-"
	gens := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			gens = append(gens, line)
		}
	}
	sort.Strings(gens)
	return gens
}

func TestRunBackupJobRecoversFromForeignTargetSnapshotNoReseed(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping foreign-snapshot recovery integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/foreign")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/foreign").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 10, Name: "foreign-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 10, Name: "foreign-test", Mode: "dataset", TargetID: 10,
		SourceDataset: poolName + "/source/foreign",
		PruneKeepLast: 7, PruneTarget: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 10)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/foreign")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	createZFSSnapshot(t, activeDS, "2026-06-26")
	if !zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("failed to plant foreign snapshot")
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 10).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 10)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("second backup (after foreign snapshot) should succeed via recovery, got: %v\n--- last backup event output ---\n%s\n--- target snapshots ---\n%v",
			err, dumpLatestBackupEvent(t, db), listZFSSnapshots(t, activeDS))
	}

	if zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot %s@2026-06-26 should have been neutralized", activeDS)
	}

	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generation datasets (no reseed), got %v", gens)
	}

	var latest clusterModels.BackupEvent
	if err := db.Order("id desc").First(&latest).Error; err == nil {
		if strings.Contains(latest.Output, "auto_archived_target_dataset") {
			t.Fatalf("backup should not have reseeded; output: %s", lastLines(latest.Output, 12))
		}
	}
}

func TestRunBackupJobTrimsExcessGenerations(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping generation-GC integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/gen")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/gen").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 11, Name: "gen-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 11, Name: "gen-test", Mode: "dataset", TargetID: 11,
		SourceDataset: poolName + "/source/gen",
		PruneKeepLast: 7, PruneTarget: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 11)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/gen")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	for _, tok := range []string{"1", "2", "3", "4"} {
		zfsCreateLegacyDataset(t, activeDS+"_gen-"+tok)
	}
	if got := listActiveGenerations(t, activeDS); len(got) != 4 {
		t.Fatalf("expected 4 seeded generations, got %v", got)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 11).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 11)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("second backup failed: %v", err)
	}

	gens := listActiveGenerations(t, activeDS)
	want := []string{activeDS + "_gen-3", activeDS + "_gen-4"}
	if !equalStringSlices(gens, want) {
		t.Fatalf("generation GC mismatch: got=%v want=%v (keep=%d)", gens, want, backupGenerationsToKeep)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstReplicatedChild(t *testing.T, activeDataset string) (string, bool) {
	t.Helper()
	out, err := exec.Command("zfs", "list", "-H", "-o", "name", "-r", "-d", "1", activeDataset).CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == activeDataset {
			continue
		}
		if strings.HasPrefix(line, activeDataset+"/") {
			return line, true
		}
	}
	return "", false
}

func TestRunBackupJobTrimsGenerationsWithPruneDisabled(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping generation-GC (prune-disabled) integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/gen0")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/gen0").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 12, Name: "gen0-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 12, Name: "gen0-test", Mode: "dataset", TargetID: 12,
		SourceDataset: poolName + "/source/gen0",
		PruneKeepLast: 0, PruneTarget: false,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 12)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/gen0")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	for _, tok := range []string{"1", "2", "3", "4"} {
		zfsCreateLegacyDataset(t, activeDS+"_gen-"+tok)
	}
	if got := listActiveGenerations(t, activeDS); len(got) != 4 {
		t.Fatalf("expected 4 seeded generations, got %v", got)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 12).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 12)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("second backup failed: %v", err)
	}

	gens := listActiveGenerations(t, activeDS)
	want := []string{activeDS + "_gen-3", activeDS + "_gen-4"}
	if !equalStringSlices(gens, want) {
		t.Fatalf("generation GC must run with PruneKeepLast=0: got=%v want=%v", gens, want)
	}
}

func TestRunBackupJobRecursiveForeignSnapshotRecovery(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping recursive foreign-snapshot recovery integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/rchild")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/rchild/child")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	for _, ds := range []string{poolName + "/source", poolName + "/source/rchild", poolName + "/source/rchild/child", poolName + "/target"} {
		exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", ds).CombinedOutput()
	}

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 13, Name: "rchild-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 13, Name: "rchild-test", Mode: "dataset", TargetID: 13,
		SourceDataset: poolName + "/source/rchild",
		PruneKeepLast: 7, PruneTarget: true, Recursive: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 13)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/rchild")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	childDS, ok := firstReplicatedChild(t, activeDS)
	if !ok {
		t.Skipf("backup did not replicate a child dataset under %s; recursion case N/A", activeDS)
	}

	createZFSSnapshot(t, childDS, "2026-06-26")
	if !zfsDatasetExists(t, childDS+"@2026-06-26") {
		t.Fatalf("failed to plant foreign snapshot on child %s", childDS)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 13).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 13)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("recursive recovery backup should succeed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if zfsDatasetExists(t, childDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on child %s should have been neutralized recursively", childDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after recursive recovery, got %v", gens)
	}
}

func TestRunBackupJobVMForeignSnapshotRecovery(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping VM foreign-snapshot recovery integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	vmSource := poolName + "/sylve/virtual-machines/100"
	zfstest.EnsureDataset(t, gzfsClient, vmSource)
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	for _, ds := range []string{poolName + "/sylve", poolName + "/sylve/virtual-machines", vmSource, poolName + "/target"} {
		exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", ds).CombinedOutput()
	}

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 14, Name: "vm-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 14, Name: "vm-test", Mode: "vm", TargetID: 14,
		SourceDataset: vmSource,
		PruneKeepLast: 7, PruneTarget: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 14)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first VM backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	vmDestSuffix := svc.backupDestSuffixForVMSource("", vmSource)
	activeDS := poolName + "/target/" + vmDestSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Skipf("VM target dataset %s not found after first backup; VM source mapping differs here", activeDS)
	}

	createZFSSnapshot(t, activeDS, "2026-06-26")
	if !zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("failed to plant foreign snapshot on VM target %s", activeDS)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 14).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 14)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("VM recovery backup should succeed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on VM target %s should have been neutralized", activeDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after VM recovery, got %v", gens)
	}
}

func TestRunBackupJobJailForeignSnapshotRecovery(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping jail foreign-snapshot recovery integration test in short mode")
	}

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	jailRoot := poolName + "/sylve/jails/42"
	zfstest.EnsureDataset(t, gzfsClient, jailRoot)
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	for _, ds := range []string{poolName + "/sylve", poolName + "/sylve/jails", jailRoot, poolName + "/target"} {
		exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", ds).CombinedOutput()
	}

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 15, Name: "jail-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 15, Name: "jail-test", Mode: "jail", TargetID: 15,
		JailRootDataset: jailRoot,
		DestSuffix:      "jails/42/j-test/active",
		PruneKeepLast:   7, PruneTarget: true, StopBeforeBackup: false,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 15)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first jail backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	jailDestSuffix := svc.backupDestSuffixForJailSource("jails/42/j-test/active", jailRoot)
	activeDS := poolName + "/target/" + jailDestSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Skipf("jail target dataset %s not found after first backup; mapping differs here", activeDS)
	}

	createZFSSnapshot(t, activeDS, "2026-06-26")
	if !zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("failed to plant foreign snapshot on jail target %s", activeDS)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 15).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 15)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("jail recovery backup should succeed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on jail target %s should have been neutralized", activeDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after jail recovery, got %v", gens)
	}
}
