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
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
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

func TestRunBackupJobPreservesLegacyTargetSnapshotDuringTopologyRotation(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping foreign-snapshot recovery integration test in short mode")
	}
	requireLocalhostBackupSSH(t)

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
		t.Fatalf("first backup failed after integration prerequisites passed: %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/foreign")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	// Pre-c1 backups were identified only by the per-job name prefix. Preserve
	// such a target-only point and let a new backup proceed.
	legacySnapshotName := backupSnapshotPrefixForJob(loaded.ID) + "_legacy"
	createZFSSnapshot(t, activeDS, legacySnapshotName)
	if !zfsDatasetExists(t, activeDS+"@"+legacySnapshotName) {
		t.Fatalf("failed to plant legacy snapshot")
	}

	// Force a topology rotation on the next run. The archived generation must
	// retain the legacy point; it is tolerated for preflight, never deleted.
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/foreign/added")
	if out, err := exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/foreign/added").CombinedOutput(); err != nil {
		t.Fatalf("set added child mountpoint: %v\n%s", err, out)
	}

	svc.queuedJobs = make(map[uint]struct{})
	svc.runningJobs = make(map[uint]struct{})
	svc.runningWorkloadOp = make(map[string]string)
	db.Model(&clusterModels.BackupJob{}).Where("id = ?", 10).Updates(map[string]interface{}{
		"recursive": true, "last_run_at": nil, "last_status": "", "last_error": "",
	})
	var loaded2 clusterModels.BackupJob
	db.Preload("Target").First(&loaded2, 10)
	if err := svc.runBackupJob(ctx, &loaded2); err != nil {
		t.Fatalf("second backup should tolerate the preserved legacy snapshot, got: %v\n--- last backup event output ---\n%s\n--- target snapshots ---\n%v",
			err, dumpLatestBackupEvent(t, db), listZFSSnapshots(t, activeDS))
	}

	gens := listActiveGenerations(t, activeDS)
	if len(gens) != 1 {
		t.Fatalf("expected one archived topology generation, got %v", gens)
	}
	if !zfsDatasetExists(t, gens[0]+"@"+legacySnapshotName) {
		t.Fatalf("legacy snapshot was not preserved on archived generation %s", gens[0])
	}
	if zfsDatasetExists(t, activeDS+"@"+legacySnapshotName) {
		t.Fatalf("legacy snapshot unexpectedly appeared on new active dataset %s", activeDS)
	}
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

func TestRunBackupJobRecursiveForeignSnapshotFailsClosed(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping recursive foreign-snapshot recovery integration test in short mode")
	}
	requireLocalhostBackupSSH(t)

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
		t.Fatalf("first recursive backup failed after integration prerequisites passed: %v", err)
	}

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/rchild")
	activeDS := poolName + "/target/" + destSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("active target dataset %s missing after first backup", activeDS)
	}

	childDS, ok := firstReplicatedChild(t, activeDS)
	if !ok {
		t.Fatalf("recursive backup did not replicate a child dataset under %s", activeDS)
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
	if err := svc.runBackupJob(ctx, &loaded2); err == nil || !strings.Contains(err.Error(), "backup_target_foreign_snapshots_present") {
		t.Fatalf("recursive backup should fail closed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if !zfsDatasetExists(t, childDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on child %s was deleted", childDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after recursive recovery, got %v", gens)
	}
}

func TestRunBackupJobVMForeignSnapshotFailsClosed(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping VM foreign-snapshot recovery integration test in short mode")
	}
	requireLocalhostBackupSSH(t)

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	vmSource := poolName + "/sylve/virtual-machines/100"
	vmChild := vmSource + "/disk0"
	zfstest.EnsureDataset(t, gzfsClient, vmSource)
	zfstest.EnsureDataset(t, gzfsClient, vmChild)
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	for _, ds := range []string{poolName + "/sylve", poolName + "/sylve/virtual-machines", vmSource, vmChild, poolName + "/target"} {
		exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", ds).CombinedOutput()
	}

	extractZeltaToTemp(t)

	db := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&vmModels.Network{},
		&vmModels.VMCPUPinning{},
	)
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
	vm := vmModels.VM{RID: 100, Name: "backup-integration-vm"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed registered VM: %v", err)
	}
	vmDataset := vmModels.VMStorageDataset{Pool: poolName, Name: vmChild, GUID: "backup-integration-vm-disk"}
	if err := db.Create(&vmDataset).Error; err != nil {
		t.Fatalf("seed registered VM dataset: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID:      vm.ID,
		Type:      vmModels.VMStorageTypeFilesystem,
		Pool:      poolName,
		Enable:    true,
		DatasetID: &vmDataset.ID,
	}).Error; err != nil {
		t.Fatalf("seed registered VM storage: %v", err)
	}
	job := clusterModels.BackupJob{
		ID: 14, Name: "vm-test", Mode: "vm", TargetID: 14,
		SourceDataset: vmSource,
		PruneKeepLast: 7, PruneTarget: true, Recursive: true,
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 14)
	if err := svc.runBackupJob(ctx, &loaded); err != nil {
		t.Fatalf("first VM backup failed after integration prerequisites passed: %v", err)
	}

	vmDestSuffix := svc.backupDestSuffixForVMSource("", vmSource)
	activeDS := poolName + "/target/" + vmDestSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("VM target dataset %s not found after first backup", activeDS)
	}
	if !zfsDatasetExists(t, activeDS+"/disk0") {
		t.Fatalf("recursive VM backup omitted child disk dataset %s", activeDS+"/disk0")
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
	if err := svc.runBackupJob(ctx, &loaded2); err == nil || !strings.Contains(err.Error(), "backup_target_foreign_snapshots_present") {
		t.Fatalf("VM backup should fail closed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if !zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on VM target %s was deleted", activeDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after VM recovery, got %v", gens)
	}
}

func TestRunBackupJobJailForeignSnapshotFailsClosed(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping jail foreign-snapshot recovery integration test in short mode")
	}
	requireLocalhostBackupSSH(t)

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

	db := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
		&jailModels.Jail{},
		&jailModels.Storage{},
	)
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
	jail := jailModels.Jail{CTID: 42, Name: "backup-integration-jail", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("seed registered jail: %v", err)
	}
	if err := db.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   poolName,
		GUID:   "backup-integration-jail-root",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("seed registered jail storage: %v", err)
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
		t.Fatalf("first jail backup failed after integration prerequisites passed: %v", err)
	}

	jailDestSuffix := svc.backupDestSuffixForJailSource("jails/42/j-test/active", jailRoot)
	activeDS := poolName + "/target/" + jailDestSuffix
	if !zfsDatasetExists(t, activeDS) {
		t.Fatalf("jail target dataset %s not found after first backup", activeDS)
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
	if err := svc.runBackupJob(ctx, &loaded2); err == nil || !strings.Contains(err.Error(), "backup_target_foreign_snapshots_present") {
		t.Fatalf("jail backup should fail closed, got: %v\n--- event ---\n%s", err, dumpLatestBackupEvent(t, db))
	}

	if !zfsDatasetExists(t, activeDS+"@2026-06-26") {
		t.Fatalf("foreign snapshot on jail target %s was deleted", activeDS)
	}
	if gens := listActiveGenerations(t, activeDS); len(gens) != 0 {
		t.Fatalf("expected no generations (no reseed) after jail recovery, got %v", gens)
	}
}
