// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func backupSnapshotForExactDataset(t *testing.T, dataset, prefix string) string {
	t.Helper()
	for _, snapshot := range listZFSSnapshots(t, dataset) {
		if strings.HasPrefix(snapshot, dataset+"@"+prefix+"_") {
			return snapshot
		}
	}
	t.Fatalf("backup snapshot for %s with prefix %s not found", dataset, prefix)
	return ""
}

func requireBackupSnapshotCount(t *testing.T, dataset, prefix string, want int) {
	t.Helper()
	snapshots := listZFSSnapshots(t, dataset)
	if got := countSnapshotsForDatasetWithPrefix(snapshots, dataset, prefix); got != want {
		t.Fatalf("backup snapshot count for %s = %d, want %d; snapshots=%v", dataset, got, want, snapshots)
	}
}

func TestIntegrationRunBackupJobMultiPoolPruneFailureIsReportedAndRetryable(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	requireLocalhostBackupSSH(t)

	poolA, client := zfstest.DedicatedPool(t)
	poolB, _ := zfstest.DedicatedPool(t)
	const rid = uint(9301)
	sourceA := fmt.Sprintf("%s/sylve/virtual-machines/%d", poolA, rid)
	sourceB := fmt.Sprintf("%s/sylve/virtual-machines/%d", poolB, rid)
	diskA := sourceA + "/disk0"
	diskB := sourceB + "/disk1"
	targetRoot := poolA + "/backup"

	zfstest.EnsureDataset(t, client, sourceA)
	zfstest.EnsureDataset(t, client, sourceB)
	zfstest.EnsureVolume(t, client, diskA, 4)
	zfstest.EnsureVolume(t, client, diskB, 4)
	zfstest.EnsureDataset(t, client, targetRoot)
	for _, dataset := range []string{
		poolA + "/sylve",
		poolA + "/sylve/virtual-machines",
		sourceA,
		poolB + "/sylve",
		poolB + "/sylve/virtual-machines",
		sourceB,
		targetRoot,
	} {
		if output, err := exec.Command("zfs", "set", "mountpoint=legacy", dataset).CombinedOutput(); err != nil {
			t.Fatalf("set legacy mountpoint on %s: %v: %s", dataset, err, output)
		}
	}

	extractZeltaToTemp(t)
	svc := newRunBackupJobTestDB(t)
	svc.GZFS = client
	target := clusterModels.BackupTarget{
		ID: 93, Name: "multi-pool-prune-error", SSHHost: "root@localhost",
		BackupRoot: targetRoot, Enabled: true,
	}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed backup target: %v", err)
	}
	vm := vmModels.VM{RID: rid, Name: "multi-pool-prune-error"}
	if err := svc.DB.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	for index, fixture := range []struct {
		pool string
		name string
	}{{poolA, diskA}, {poolB, diskB}} {
		dataset := vmModels.VMStorageDataset{
			Pool: fixture.pool, Name: fixture.name, GUID: fmt.Sprintf("multi-pool-disk-%d", index),
		}
		if err := svc.DB.Create(&dataset).Error; err != nil {
			t.Fatalf("seed VM storage dataset %d: %v", index, err)
		}
		storage := vmModels.Storage{
			VMID: vm.ID, Name: fmt.Sprintf("disk-%d", index), Type: vmModels.VMStorageTypeZVol,
			Pool: fixture.pool, Enable: true, DatasetID: &dataset.ID,
		}
		if err := svc.DB.Create(&storage).Error; err != nil {
			t.Fatalf("seed VM storage %d: %v", index, err)
		}
	}

	job := clusterModels.BackupJob{
		ID: 93, Name: "multi-pool-prune-error", Mode: clusterModels.BackupJobModeVM,
		TargetID: target.ID, SourceDataset: sourceA, Recursive: true,
		PruneTarget: true, CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("seed backup job: %v", err)
	}
	run := func() error {
		svc.queuedJobs = make(map[uint]struct{})
		svc.runningJobs = make(map[uint]struct{})
		svc.runningWorkloadOp = make(map[string]string)
		var loaded clusterModels.BackupJob
		if err := svc.DB.Preload("Target").First(&loaded, job.ID).Error; err != nil {
			t.Fatalf("load backup job: %v", err)
		}
		return svc.runBackupJob(context.Background(), &loaded)
	}

	if err := run(); err != nil {
		t.Fatalf("seed backup failed: %v", err)
	}
	prefix := backupSnapshotPrefixForJob(job.ID)
	heldSnapshot := backupSnapshotForExactDataset(t, sourceA, prefix)
	const holdTag = "sylve-prune-error-test"
	if output, err := exec.Command("zfs", "hold", holdTag, heldSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("hold first-pool snapshot %s: %v: %s", heldSnapshot, err, output)
	}
	held := true
	t.Cleanup(func() {
		if held {
			_, _ = exec.Command("zfs", "release", holdTag, heldSnapshot).CombinedOutput()
		}
	})
	if err := svc.DB.Model(&clusterModels.BackupJob{}).Where("id = ?", job.ID).Update("prune_keep_last", 1).Error; err != nil {
		t.Fatalf("enable keep-last retention: %v", err)
	}
	time.Sleep(time.Second)

	err := run()
	if err == nil || !strings.Contains(err.Error(), "backup_prune_destroy_failed") {
		t.Fatalf("held first-pool snapshot prune error = %v", err)
	}
	updated := fetchJob(t, svc.DB, job.ID)
	if updated.LastStatus != "failed" ||
		!strings.Contains(updated.LastError, "backup_prune_destroy_failed") ||
		!strings.Contains(updated.LastError, heldSnapshot) {
		t.Fatalf("job result after prune failure: status=%q error=%q", updated.LastStatus, updated.LastError)
	}
	var failedEvent clusterModels.BackupEvent
	if err := svc.DB.Order("id desc").First(&failedEvent).Error; err != nil {
		t.Fatalf("load failed backup event: %v", err)
	}
	if failedEvent.Status != "failed" ||
		!strings.Contains(failedEvent.Error, "backup_prune_destroy_failed") ||
		!strings.Contains(failedEvent.Error, heldSnapshot) ||
		!strings.Contains(failedEvent.Output, "backup_prune_destroy_failed") ||
		!strings.Contains(failedEvent.Output, heldSnapshot) {
		t.Fatalf("prune failure event: status=%q error=%q output=%q", failedEvent.Status, failedEvent.Error, failedEvent.Output)
	}

	remoteA := remoteActiveDatasetForSuffix(targetRoot, svc.backupDestSuffixForVMSource("", sourceA))
	remoteB := remoteActiveDatasetForSuffix(targetRoot, svc.backupDestSuffixForVMSource("", sourceB))
	for _, dataset := range []string{sourceA, diskA} {
		requireBackupSnapshotCount(t, dataset, prefix, 2)
	}
	for _, dataset := range []string{sourceB, diskB} {
		requireBackupSnapshotCount(t, dataset, prefix, 1)
	}
	for _, dataset := range []string{remoteA, remoteA + "/disk0", remoteB, remoteB + "/disk1"} {
		requireBackupSnapshotCount(t, dataset, prefix, 2)
	}

	if output, err := exec.Command("zfs", "release", holdTag, heldSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("release first-pool snapshot %s: %v: %s", heldSnapshot, err, output)
	}
	held = false
	time.Sleep(time.Second)
	if err := run(); err != nil {
		t.Fatalf("retry after releasing snapshot hold failed: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, svc.DB))
	}
	for _, dataset := range []string{sourceA, diskA, sourceB, diskB, remoteA, remoteA + "/disk0", remoteB, remoteB + "/disk1"} {
		requireBackupSnapshotCount(t, dataset, prefix, 1)
	}
}
