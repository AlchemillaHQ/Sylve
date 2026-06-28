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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func extractZeltaToTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ZeltaInstallDir = dir
	t.Cleanup(func() { ZeltaInstallDir = "" })
	if err := EnsureZeltaInstalled(); err != nil {
		t.Fatalf("failed to extract zelta: %v", err)
	}
	return dir
}

func TestZeltaBinaryExtractsAndRuns(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	dir := extractZeltaToTemp(t)

	bin := filepath.Join(dir, "bin", "zelta")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("zelta binary not found at %s: %v", bin, err)
	}

	cmd := exec.Command(bin, "--help")
	cmd.Env = append(os.Environ(),
		"ZELTA_SHARE="+filepath.Join(dir, "share", "zelta"),
	)
	out, _ := cmd.CombinedOutput()
	if len(out) == 0 && cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		t.Logf("zelta --help produced no output but ran (exit: %d)", cmd.ProcessState.ExitCode())
	}
}

func TestZeltaBackupWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping full ZFS integration test in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfstest.Pool(t)
	defer zfsCleanup()

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/data")
	ctx := context.Background()

	zfsBin, err := exec.LookPath("zfs")
	if err != nil {
		t.Fatalf("could not find zfs binary: %v", err)
	}

	setProp := exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source")
	if out, err := setProp.CombinedOutput(); err != nil {
		t.Fatalf("zfs set mountpoint=legacy: %v\noutput: %s", err, string(out))
	}
	setProp = exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/data")
	if out, err := setProp.CombinedOutput(); err != nil {
		t.Fatalf("zfs set mountpoint=legacy data: %v\noutput: %s", err, string(out))
	}

	zeltaDir := extractZeltaToTemp(t)
	zeltaBin := filepath.Join(zeltaDir, "bin", "zelta")

	zeltaShare := filepath.Join(zeltaDir, "share", "zelta")
	snapName := "bk_" + time.Now().UTC().Format("2006-01-02_15.04.05")

	cmd := exec.CommandContext(ctx, zeltaBin,
		"snapshot",
		"--snap-name", snapName,
		poolName+"/source/data",
	)
	cmd.Env = append(os.Environ(),
		"ZELTA_SHARE="+zeltaShare,
		"PATH="+filepath.Join(zeltaDir, "bin")+":"+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zelta snapshot failed: %v\noutput: %s", err, string(out))
	}
	t.Logf("zelta snapshot output: %s", strings.TrimSpace(string(out)))

	listSnaps := exec.CommandContext(ctx, zfsBin, "list", "-H", "-o", "name", "-t", "snapshot", "-r", poolName+"/source")
	snapOut, err := listSnaps.CombinedOutput()
	if err != nil {
		t.Fatalf("zfs list snapshots failed: %v", err)
	}
	snapLines := strings.Split(strings.TrimSpace(string(snapOut)), "\n")
	found := false
	for _, line := range snapLines {
		if strings.Contains(line, snapName) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("snapshot %s not found in:\n%s", snapName, string(snapOut))
	}

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")
	setProp = exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target")
	setProp.CombinedOutput()
	_ = gzfsClient
}

func TestRunBackupJobWithEmbeddedZelta(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping full ZFS integration test in short mode")
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
		ID: 1, Name: "local-test", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 1, Name: "ephemeral-test", Mode: "dataset", TargetID: 1,
		SourceDataset: poolName + "/source/backup",
		CronExpr:      "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 1)

	err := svc.runBackupJob(ctx, &loaded)
	if err != nil {
		t.Logf("runBackupJob completed with error (expected if no SSH to localhost): %v", err)
	}

	updated := fetchJob(t, db, 1)
	if updated.LastRunAt != nil {
		t.Logf("backup ran at %v with status=%q error=%q",
			updated.LastRunAt, updated.LastStatus, updated.LastError)
	}

	var events []clusterModels.BackupEvent
	db.Find(&events)
	t.Logf("created %d backup events", len(events))
	for i, e := range events {
		t.Logf("  event[%d]: status=%q source=%q target=%q output=%d bytes",
			i, e.Status, e.SourceDataset, e.TargetEndpoint, len(e.Output))
		if e.Output != "" {
			t.Logf("  event[%d] output tail: %s", i, lastLines(e.Output, 3))
		}
	}

	snapshots := listZFSSnapshots(t, poolName+"/source/backup")
	t.Logf("ZFS snapshots on source: %v", snapshots)
	snapshots = listZFSSnapshots(t, poolName+"/target")
	t.Logf("ZFS snapshots on target: %v", snapshots)
}

func listZFSSnapshots(t *testing.T, dataset string) []string {
	t.Helper()
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "zfs", "list", "-H", "-o", "name", "-t", "snapshot", "-r", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := make([]string, 0)
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
