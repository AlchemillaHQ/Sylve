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
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

type backupRunFixture struct {
	t      *testing.T
	svc    *Service
	job    clusterModels.BackupJob
	source string
	active string
}

func newBackupRunFixture(t *testing.T, name string) *backupRunFixture {
	t.Helper()
	zfstest.SkipIfUnavailable(t)
	hostKey := requireLocalhostBackupSSH(t)
	poolName, client, cleanup := zfstest.SharedPool(t)
	t.Cleanup(cleanup)
	source := poolName + "/source/" + name
	zfstest.EnsureDataset(t, client, source+"/child")
	zfstest.EnsureDataset(t, client, poolName+"/target")
	for _, dataset := range []string{poolName + "/source", source, source + "/child", poolName + "/target"} {
		if output, err := exec.Command("zfs", "set", "mountpoint=legacy", dataset).CombinedOutput(); err != nil {
			t.Fatalf("set legacy mountpoint on %s: %v: %s", dataset, err, output)
		}
	}
	extractZeltaToTemp(t)

	svc := newRunBackupJobTestDB(t)
	svc.GZFS = client
	target := clusterModels.BackupTarget{
		ID: 71, Name: name, SSHHost: "root@localhost", BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	seedBackupTargetHostTrust(t, svc.DB, &target, hostKey)
	job := clusterModels.BackupJob{
		ID: 71, Name: name, Mode: clusterModels.BackupJobModeDataset, TargetID: target.ID,
		SourceDataset: source, Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	destSuffix := datasetDestSuffix("", source)
	return &backupRunFixture{
		t: t, svc: svc, job: job, source: source,
		active: remoteActiveDatasetForSuffix(target.BackupRoot, destSuffix),
	}
}

func (f *backupRunFixture) run() clusterModels.BackupEvent {
	f.t.Helper()
	f.svc.queuedJobs = make(map[uint]struct{})
	f.svc.runningJobs = make(map[uint]struct{})
	f.svc.runningWorkloadOp = make(map[string]string)
	var loaded clusterModels.BackupJob
	if err := f.svc.DB.Preload("Target").First(&loaded, f.job.ID).Error; err != nil {
		f.t.Fatal(err)
	}
	if err := f.svc.runBackupJob(context.Background(), &loaded); err != nil {
		f.t.Fatalf("backup failed: %v\n%s", err, dumpLatestBackupEvent(f.t, f.svc.DB))
	}
	var event clusterModels.BackupEvent
	if err := f.svc.DB.Order("id desc").First(&event).Error; err != nil {
		f.t.Fatal(err)
	}
	return event
}

func TestIntegrationRunBackupJobSkipsUnchangedSource(t *testing.T) {
	f := newBackupRunFixture(t, "skip")
	prefix := backupSnapshotPrefixForJob(f.job.ID)

	f.run()
	event := f.run()
	if event.Status != "success" || !strings.Contains(event.Output, "backup_unchanged_since: ") {
		t.Fatalf("unchanged run: status=%q output=%q", event.Status, event.Output)
	}
	for _, dataset := range []string{f.source, f.source + "/child", f.active, f.active + "/child"} {
		requireBackupSnapshotCount(t, dataset, prefix, 1)
	}

	changeDataset(t, f.source+"/child")
	event = f.run()
	if strings.Contains(event.Output, "backup_unchanged_since") {
		t.Fatalf("changed run was skipped: %q", event.Output)
	}
	for _, dataset := range []string{f.source, f.source + "/child", f.active, f.active + "/child"} {
		requireBackupSnapshotCount(t, dataset, prefix, 2)
	}

	latest := ""
	for _, snapshot := range listZFSSnapshots(t, f.active) {
		if strings.HasPrefix(snapshot, f.active+"@") {
			latest = snapshot
		}
	}
	if output, err := exec.Command("zfs", "inherit", backupCommitPropertyManifest, latest).CombinedOutput(); err != nil {
		t.Fatalf("clear commit on %s: %v: %s", latest, err, output)
	}
	event = f.run()
	if strings.Contains(event.Output, "backup_unchanged_since") {
		t.Fatalf("run skipped although the target lost the last commit: %q", event.Output)
	}
	requireBackupSnapshotCount(t, f.active, prefix, 3)
}

func TestIntegrationRunBackupJobClonesDivergedTargetFromSharedSnapshot(t *testing.T) {
	f := newBackupRunFixture(t, "rollback")
	prefix := backupSnapshotPrefixForJob(f.job.ID)

	f.run()
	for i := 0; i < 2; i++ {
		changeDataset(t, f.source)
		f.run()
	}
	var local []string
	for _, snapshot := range listZFSSnapshots(t, f.source) {
		if strings.HasPrefix(snapshot, f.source+"@") {
			local = append(local, snapshot)
		}
	}
	if len(local) != 3 {
		t.Fatalf("expected 3 backup snapshots, got %v", local)
	}
	for _, dataset := range []string{f.source, f.source + "/child"} {
		if output, err := exec.Command("zfs", "rollback", "-r", dataset+local[0][strings.Index(local[0], "@"):]).CombinedOutput(); err != nil {
			t.Fatalf("rollback %s: %v: %s", dataset, err, output)
		}
	}
	changeDataset(t, f.source)

	event := f.run()
	if !strings.Contains(event.Output, "backup_reseed_cloned_from: ") {
		t.Fatalf("diverged target was not cloned: %q", event.Output)
	}
	for _, dataset := range []string{f.active, f.active + "/child"} {
		requireBackupSnapshotCount(t, dataset, prefix, 2)
	}
	archives := listActiveGenerations(t, f.active)
	if len(archives) != 1 {
		t.Fatalf("expected one archive generation, got %v", archives)
	}
	origin, err := exec.Command("zfs", "get", "-H", "-o", "value", "origin", archives[0]).Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(origin)), f.active+"@") {
		t.Fatalf("archive %s origin = %q (%v), want a snapshot of %s", archives[0], origin, err, f.active)
	}
	requireBackupSnapshotCount(t, archives[0], prefix, 2)

	changeDataset(t, f.source)
	event = f.run()
	if strings.Contains(event.Output, "auto_archived_target_dataset") {
		t.Fatalf("follow-up run reseeded again: %q", event.Output)
	}
	requireBackupSnapshotCount(t, f.active, prefix, 3)
}

func TestIntegrationRunRestoreJobCopiesLocalSnapshot(t *testing.T) {
	f := newBackupRunFixture(t, "localrestore")
	pool, _, _ := strings.Cut(f.source, "/")
	for _, dataset := range []string{pool + "/source", f.source, f.source + "/child"} {
		if output, err := exec.Command("zfs", "inherit", "mountpoint", dataset).CombinedOutput(); err != nil {
			t.Fatalf("inherit mountpoint on %s: %v: %s", dataset, err, output)
		}
	}
	f.run()
	changeDataset(t, f.source)
	f.run()

	var points []string
	for _, snapshot := range listZFSSnapshots(t, f.source) {
		if strings.HasPrefix(snapshot, f.source+"@") {
			points = append(points, snapshot[strings.Index(snapshot, "@"):])
		}
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 backup points, got %v", points)
	}
	wantGUID := restoreZFSSnapshotGUID(t, f.source+"/child", strings.TrimPrefix(points[0], "@"))

	var job clusterModels.BackupJob
	if err := f.svc.DB.Preload("Target").First(&job, f.job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.svc.runRestoreJob(context.Background(), &job, points[0], ""); err != nil {
		t.Fatalf("restore failed: %v\n%s", err, dumpLatestBackupEvent(t, f.svc.DB))
	}
	var event clusterModels.BackupEvent
	if err := f.svc.DB.Order("id desc").First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(event.Output, "restore_source_local: "+f.source+points[0]) {
		t.Fatalf("restore did not use the local snapshot: %q", event.Output)
	}
	if got := restoreZFSSnapshotGUID(t, f.source+"/child", strings.TrimPrefix(points[0], "@")); got != wantGUID {
		t.Fatalf("restored child snapshot GUID = %q, want %q", got, wantGUID)
	}
	if restoreZFSSnapshotExists(t, f.source, strings.TrimPrefix(points[1], "@")) {
		t.Fatalf("restore kept the later point %s", points[1])
	}
}

func TestIntegrationRunBackupJobPrunesOldArchivedGenerations(t *testing.T) {
	f := newBackupRunFixture(t, "gens")
	f.run()
	changeDataset(t, f.source)
	f.run()
	first := ""
	for _, snapshot := range listZFSSnapshots(t, f.source) {
		if strings.HasPrefix(snapshot, f.source+"@") {
			first = snapshot[strings.Index(snapshot, "@"):]
			break
		}
	}
	diverge := func() {
		for _, dataset := range []string{f.source, f.source + "/child"} {
			if output, err := exec.Command("zfs", "rollback", "-r", dataset+first).CombinedOutput(); err != nil {
				t.Fatalf("rollback %s: %v: %s", dataset, err, output)
			}
		}
		changeDataset(t, f.source)
		f.run()
		changeDataset(t, f.source)
		f.run()
	}
	diverge()
	diverge()
	archives := listActiveGenerations(t, f.active)
	if len(archives) != 2 {
		t.Fatalf("expected 2 archived generations, got %v", archives)
	}

	if err := f.svc.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", f.job.ID).
		Update("prune_keep_generations", 1).Error; err != nil {
		t.Fatal(err)
	}
	event := f.run()
	if !strings.Contains(event.Output, "backup_generation_pruned: "+archives[0]) {
		t.Fatalf("oldest archive was not pruned: %q", event.Output)
	}
	if remaining := listActiveGenerations(t, f.active); len(remaining) != 1 || remaining[0] != archives[1] {
		t.Fatalf("remaining archives = %v, want [%s]", remaining, archives[1])
	}
	changeDataset(t, f.source)
	if event := f.run(); event.Status != "success" {
		t.Fatalf("backup after pruning an archive: %s %q", event.Status, event.Error)
	}
}
