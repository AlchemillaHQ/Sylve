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
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func createZFSSnapshot(t *testing.T, dataset, snap string) {
	t.Helper()
	cmd := exec.Command("zfs", "snapshot", fmt.Sprintf("%s@%s", dataset, snap))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zfs snapshot %s@%s: %v\n%s", dataset, snap, err, string(out))
	}
}

func destroyZFSSnapshot(t *testing.T, fullName string) {
	t.Helper()
	cmd := exec.Command("zfs", "destroy", fullName)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("zfs destroy %s: %v\n%s", fullName, err, string(out))
	}
}

func zfsDatasetExists(t *testing.T, name string) bool {
	t.Helper()
	cmd := exec.Command("zfs", "list", "-H", "-o", "name", name)
	err := cmd.Run()
	return err == nil
}

func TestRunBackupJobPruneAfterBackup(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping prune integration test in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfstest.Pool(t)
	defer zfsCleanup()
	_ = gzfsClient

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/backup")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/backup").CombinedOutput()
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
		ID: 2, Name: "prune-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 2, Name: "prune-test", Mode: "dataset", TargetID: 2,
		SourceDataset: poolName + "/source/backup",
		CronExpr:      "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	for i := 0; i < 4; i++ {
		svc.queuedJobs = make(map[uint]struct{})
		svc.runningJobs = make(map[uint]struct{})
		svc.runningWorkloadOp = make(map[string]string)

		var loaded clusterModels.BackupJob
		db.Preload("Target").First(&loaded, 2)
		if err := svc.runBackupJob(ctx, &loaded); err != nil {
			t.Logf("backup %d failed: %v", i+1, err)
		}

		time.Sleep(1 * time.Second)
	}

	beforeSnapshots := listZFSSnapshots(t, poolName+"/source/backup")
	bkBefores := 0
	for _, s := range beforeSnapshots {
		if strings.Contains(s, "@bk_j") {
			bkBefores++
		}
	}
	t.Logf("before prune: %d bk snapshots (from %d runs)", bkBefores, 4)
	if bkBefores <= 2 {
		t.Skipf("expected > 2 bk snapshots from seeding runs (got %d); backup env (root@localhost ssh) likely not set up", bkBefores)
	}

	job.PruneKeepLast = 2
	svc.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", 2).Update("prune_keep_last", 2)

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)

	var loadedFinal clusterModels.BackupJob
	db.Preload("Target").First(&loadedFinal, 2)

	if err := svc.runBackupJob(ctx, &loadedFinal); err != nil {
		t.Fatalf("final backup (triggering prune) failed: %v", err)
	}

	afterSnapshots := listZFSSnapshots(t, poolName+"/source/backup")
	bkAfter := 0
	for _, s := range afterSnapshots {
		if strings.Contains(s, "@bk_j") {
			bkAfter++
			t.Logf("  remaining bk snap: %s", s)
		}
	}
	t.Logf("after prune: %d bk snapshots (keep_last=2)", bkAfter)
	if bkAfter != 2 {
		t.Fatalf("expected exactly 2 bk snapshots after Keep-2 prune (Fix B), got %d", bkAfter)
	}
}

func TestRunBackupJobAutoReseedOnDivergedTarget(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping auto-reseed integration test in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfstest.Pool(t)
	defer zfsCleanup()
	_ = gzfsClient

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/reseed")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/reseed").CombinedOutput()
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
		ID: 3, Name: "reseed-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 3, Name: "reseed-test", Mode: "dataset", TargetID: 3,
		SourceDataset: poolName + "/source/reseed",
		PruneKeepLast: 7, PruneTarget: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 3)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Skipf("first backup failed (localhost ssh/zelta env not set up): %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/reseed")
	targetDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, targetDS) {
		t.Fatalf("target dataset %s does not exist after first backup", targetDS)
	}

	destroyedCommon := 0
	for _, snap := range listZFSSnapshots(t, targetDS) {
		if strings.Contains(snap, "@bk_j") {
			destroyZFSSnapshot(t, snap)
			destroyedCommon++
		}
	}
	if destroyedCommon == 0 {
		t.Fatalf("expected at least one bk_ snapshot on target after first backup")
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 3).Updates(map[string]interface{}{
		"last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 3)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("reseed-fallback backup should ultimately succeed, got: %v\n--- last backup event output ---\n%s\n--- target snapshots ---\n%v",
			err, dumpLatestBackupEvent(t, db), listZFSSnapshots(t, targetDS))
	}

	gens := listActiveGenerations(t, targetDS)
	if len(gens) != 1 {
		t.Fatalf("expected exactly 1 generation archive from a single reseed, got %v", gens)
	}

	if !zfsDatasetExists(t, targetDS) {
		t.Fatalf("target dataset %s should exist after reseed", targetDS)
	}
	freshBk := false
	for _, snap := range listZFSSnapshots(t, targetDS) {
		if strings.Contains(snap, "@bk_j") {
			freshBk = true
			break
		}
	}
	if !freshBk {
		t.Fatalf("target %s should have a fresh bk_ snapshot after reseed", targetDS)
	}
}
