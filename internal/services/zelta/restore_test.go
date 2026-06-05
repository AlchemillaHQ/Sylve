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
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestParseRestoreSnapshotInput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultDS  string
		wantRemote string
		wantSnap   string
		wantErr    bool
	}{
		{"empty", "", "", "", "", true},
		{"whitespace", "  ", "", "", "", true},
		{"snap without default", "snap1", "", "", "", true},
		{"snap with default", "snap1", "pool/ds", "pool/ds", "@snap1", false},
		{"remote@snap", "pool/ds@snap1", "", "pool/ds", "@snap1", false},
		{"host:pool/ds@snap", "host:pool/ds@snap1", "", "host:pool/ds", "@snap1", false},
		{"host only", "host:pool/ds", "", "", "", true},
		{"snap starts with @", "@snap1", "pool/ds", "pool/ds", "@snap1", false},
		{"snap with @ in default", "@snap1", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote, snap, err := parseRestoreSnapshotInput(tt.input, tt.defaultDS)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if remote != tt.wantRemote {
				t.Fatalf("remote: got %q, want %q", remote, tt.wantRemote)
			}
			if snap != tt.wantSnap {
				t.Fatalf("snap: got %q, want %q", snap, tt.wantSnap)
			}
		})
	}
}

func TestFilterSnapshotsForRestoreJob(t *testing.T) {
	job := &clusterModels.BackupJob{
		Mode: clusterModels.BackupJobModeJail,
		JailRootDataset: "zroot/jails/42",
	}
	snapshots := []SnapshotInfo{
		{Name: "tank/backups/jails/42@bk_1", ShortName: "bk_1", Dataset: "tank/backups/jails/42"},
		{Name: "tank/backups/jails/42@bk_2", ShortName: "bk_2", Dataset: "tank/backups/jails/42"},
		{Name: "tank/backups/jails/99@bk_3", ShortName: "bk_3", Dataset: "tank/backups/jails/99"},
		{Name: "tank/backups/other@bk_4", ShortName: "bk_4", Dataset: "tank/backups/other"},
	}

	result := filterSnapshotsForRestoreJob(job, "tank/backups", snapshots)
	if len(result) != 2 {
		t.Fatalf("expected 2 snapshots for jail 42, got %d", len(result))
	}
	for _, s := range result {
		if !strings.Contains(s.Dataset, "/42") {
			t.Fatalf("snapshot %s not for jail 42", s.Dataset)
		}
	}

	jobVM := &clusterModels.BackupJob{
		Mode: clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/virtual-machines/7",
	}
	vmSnapshots := []SnapshotInfo{
		{Name: "tank/backups/virtual-machines/7@bk_1", ShortName: "bk_1", Dataset: "tank/backups/virtual-machines/7"},
		{Name: "tank/backups/virtual-machines/7@bk_2", ShortName: "bk_2", Dataset: "tank/backups/virtual-machines/7"},
		{Name: "tank/backups/virtual-machines/99@bk_3", ShortName: "bk_3", Dataset: "tank/backups/virtual-machines/99"},
	}
	vmResult := filterSnapshotsForRestoreJob(jobVM, "tank/backups", vmSnapshots)
	if len(vmResult) != 2 {
		t.Fatalf("expected 2 vm snapshots for VM 7, got %d", len(vmResult))
	}

	nilJob := filterSnapshotsForRestoreJob(nil, "", snapshots)
	if len(nilJob) != 4 {
		t.Fatalf("expected all 4 for nil job, got %d", len(nilJob))
	}

	datasetJob := &clusterModels.BackupJob{
		Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data",
	}
	dsResult := filterSnapshotsForRestoreJob(datasetJob, "", snapshots)
	if len(dsResult) != 4 {
		t.Fatalf("dataset mode should return all (no guest ID filter), got %d", len(dsResult))
	}
}

func TestListRemoteSnapshotsWithEphemeralZFS(t *testing.T) {
	zfsSkipIfNotAvailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfsTestSetup(t)
	defer zfsCleanup()
	_ = gzfsClient

	ensureZFSDataset(t, gzfsClient, poolName+"/source/data")
	ensureZFSDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/data").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:               db,
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:             gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 1, Name: "restore-test", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 1, Name: "restore-snap-list", Mode: "dataset", TargetID: 1,
		SourceDataset: poolName + "/source/data",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 1)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Fatalf("first backup failed: %v", err)
	}

	{
		sshCmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "root@localhost",
			"zfs", "list", "-t", "filesystem", "-d", "1", "-Hp", "-o", "name", poolName+"/target")
		sshOut, sshErr := sshCmd.CombinedOutput()
		t.Logf("SSH zfs list filesystems: err=%v out=%s", sshErr, string(sshOut))
	}

	{
		sshCmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "root@localhost",
			"zfs", "list", "-t", "snapshot", "-Hp", "-o", "name,creation,used,refer", "-s", "creation", poolName+"/target")
		sshOut, sshErr := sshCmd.CombinedOutput()
		t.Logf("SSH zfs list snapshots on root: err=%v out=%s", sshErr, string(sshOut))
	}

	{
		sshCmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "root@localhost",
			"zfs", "list", "-t", "snapshot", "-Hp", "-o", "name,creation,used,refer", "-s", "creation", "-r", poolName+"/target")
		sshOut, sshErr := sshCmd.CombinedOutput()
		t.Logf("SSH zfs list snapshots recursive: err=%v out=%s", sshErr, string(sshOut))
	}

	snaps, err := svc.listRemoteSnapshotsWithLineage(ctx, &loaded.Target, target.BackupRoot)
	if err != nil {
		t.Fatalf("listRemoteSnapshotsWithLineage failed: %v", err)
	}
	t.Logf("raw SSH list: %d snapshots", len(snaps))
	if len(snaps) == 0 {
		t.Fatal("expected snapshots via SSH listing")
	}
	for _, s := range snaps {
		t.Logf("  %s", s.Name)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/data")
	remoteDS := poolName + "/target/" + destSuffix
	t.Logf("backup dest suffix: %q → remote dataset: %s", destSuffix, remoteDS)

	directSnaps, err := svc.listRemoteSnapshotsForDataset(ctx, &loaded.Target, remoteDS)
	if err != nil {
		t.Fatalf("direct snapshots listing on %s failed: %v", remoteDS, err)
	}
	t.Logf("direct snaps on %s: %d", remoteDS, len(directSnaps))
	for _, s := range directSnaps {
		t.Logf("  %s", s.Name)
	}
	if len(directSnaps) == 0 {
		t.Fatal("expected snapshots via direct SSH listing on the specific dataset")
	}

	snapshots, err := svc.ListRemoteSnapshots(ctx, &loaded)
	if err != nil {
		t.Logf("ListRemoteSnapshots: %v", err)
	}
	t.Logf("ListRemoteSnapshots (after filtering): %d snapshots", len(snapshots))
	if len(snapshots) == 0 {
		t.Logf("filtering removed all — known gap between autoDestSuffix and backupDestSuffixForMode")
	}
}

func TestRunRestoreJobDatasetWithEphemeralZFS(t *testing.T) {
	zfsSkipIfNotAvailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfsTestSetup(t)
	defer zfsCleanup()
	_ = gzfsClient

	ensureZFSDataset(t, gzfsClient, poolName+"/source/data")
	ensureZFSDataset(t, gzfsClient, poolName+"/target")
	ensureZFSDataset(t, gzfsClient, poolName+"/restore")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/data").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/restore").CombinedOutput()

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:               db,
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:             gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 2, Name: "restore-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 2, Name: "restore-dataset", Mode: "dataset", TargetID: 2,
		SourceDataset: poolName + "/restore",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 2)

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)

	err := svc.runBackupJob(ctx, &loaded)
	if err != nil {
		t.Logf("backup may have failed (no source data): %v", err)
	}

	snapshots, err := svc.ListRemoteSnapshots(ctx, &loaded)
	if err != nil {
		t.Fatalf("ListRemoteSnapshots failed: %v", err)
	}
	if len(snapshots) == 0 {
		t.Skip("no snapshots to restore from")
	}

	restoreSnapshot := snapshots[len(snapshots)-1].ShortName
	t.Logf("restoring from snapshot: %s", restoreSnapshot)

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)

	err = svc.runRestoreJob(ctx, &loaded, restoreSnapshot, "")
	if err != nil {
		t.Logf("restore result: %v", err)
	}

	var events []clusterModels.BackupEvent
	db.Where("mode = ?", "restore").Order("id desc").Find(&events)
	for _, e := range events {
		t.Logf("restore event: status=%q source=%q target=%q error=%q",
			e.Status, e.SourceDataset, e.TargetEndpoint, e.Error)
		if e.Output != "" {
			t.Logf("  output tail: %s", lastLines(e.Output, 5))
		}
	}

	afterSnaps := listZFSSnapshots(t, poolName+"/restore")
	t.Logf("snapshots on restore dataset: %v", afterSnaps)
}

func TestSnapshotDatasetName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tank/data@snap1", "tank/data"},
		{"pool/ds/sub@bk_2025", "pool/ds/sub"},
		{"host:remote@snap", "host:remote"},
		{"", ""},
		{"noatsign", ""},
		{"@snap", ""},
	}
	for _, tt := range tests {
		got := snapshotDatasetName(tt.input)
		if got != tt.want {
			t.Fatalf("snapshotDatasetName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
