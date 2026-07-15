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
	"slices"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
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

func TestRestoreZeltaArgsRespectRecursion(t *testing.T) {
	remote := "root@backup:tank/backups/data@bk_1"
	local := "zroot/data.restoring"
	if got := restoreZeltaArgs(remote, local, false); !slices.Equal(got, []string{
		"backup", "--json", "--no-snapshot", "--depth", "1", remote, local,
	}) {
		t.Fatalf("non-recursive restore args = %#v", got)
	}
	if got := restoreZeltaArgs(remote, local, true); !slices.Equal(got, []string{
		"backup", "--json", "--no-snapshot", remote, local,
	}) {
		t.Fatalf("recursive restore args = %#v", got)
	}
}

func TestFilterSnapshotsForRestoreJob(t *testing.T) {
	job := &clusterModels.BackupJob{
		Mode:            clusterModels.BackupJobModeJail,
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
		Mode:          clusterModels.BackupJobModeVM,
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
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data",
	}
	dsResult := filterSnapshotsForRestoreJob(datasetJob, "", snapshots)
	if len(dsResult) != 4 {
		t.Fatalf("dataset mode should return all (no guest ID filter), got %d", len(dsResult))
	}
}

func TestListRemoteSnapshotsWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfstest.Pool(t)
	defer zfsCleanup()
	_ = gzfsClient

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/data")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/data").CombinedOutput()
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
		ID: 1, Name: "restore-test", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 1, Name: "restore-snap-list", Mode: "dataset", TargetID: 1,
		SourceDataset: poolName + "/source/data",
		CronExpr:      "0 0 * * *", Enabled: true,
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

	destSuffix := svc.backupDestSuffixForMode("dataset", "", poolName+"/source/data")
	remoteDS := poolName + "/target/" + destSuffix
	t.Logf("backup dest suffix: %q → remote dataset: %s", destSuffix, remoteDS)

	snaps, err := svc.listRemoteSnapshotsWithLineage(ctx, &loaded.Target, remoteDS)
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
		t.Fatalf("ListRemoteSnapshots: %v", err)
	}
	t.Logf("ListRemoteSnapshots (after filtering): %d snapshots", len(snapshots))
	if len(snapshots) == 0 {
		t.Fatal("committed backup disappeared from restore listing")
	}
	if !snapshots[len(snapshots)-1].Committed || snapshots[len(snapshots)-1].Legacy {
		t.Fatalf("new restore point was not commit-verified: %+v", snapshots[len(snapshots)-1])
	}

	interruptedName := backupSnapshotPrefixForJob(loaded.ID) + "_" + backupCommitProtocolToken + "_interrupted"
	if output, err := exec.CommandContext(ctx, zfsBin, "snapshot", remoteDS+"@"+interruptedName).CombinedOutput(); err != nil {
		t.Fatalf("create interrupted target snapshot: %v\n%s", err, output)
	}
	visibleAfterInterrupted, err := svc.ListRemoteSnapshots(ctx, &loaded)
	if err != nil {
		t.Fatalf("ListRemoteSnapshots after interrupted snapshot: %v", err)
	}
	for _, snapshot := range visibleAfterInterrupted {
		if snapshotShortName(snapshot) == "@"+interruptedName {
			t.Fatalf("uncommitted restore point was advertised: %+v", snapshot)
		}
	}
	if err := svc.runRestoreJob(ctx, &loaded, "@"+interruptedName, remoteDS); err == nil ||
		!strings.Contains(err.Error(), "not_committed") {
		t.Fatalf("uncommitted restore point error = %v", err)
	}
}

func TestRunRestoreJobDatasetWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, gzfsClient, zfsCleanup := zfstest.Pool(t)
	defer zfsCleanup()
	_ = gzfsClient

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/data")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/restore")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/data").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/restore").CombinedOutput()

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
		ID: 2, Name: "restore-target", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 2, Name: "restore-dataset", Mode: "dataset", TargetID: 2,
		SourceDataset: poolName + "/restore",
		CronExpr:      "0 0 * * *", Enabled: true,
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

func TestParseAndCompareRestoreDatasetManifest(t *testing.T) {
	manifest, err := parseRestoreDatasetTree(
		"tank/backup\tfilesystem\n"+
			"tank/backup/jails\tfilesystem\n"+
			"tank/backup/virtual-machines/7/disk0\tvolume\n",
		"tank/backup",
	)
	if err != nil {
		t.Fatalf("parseRestoreDatasetTree: %v", err)
	}
	if len(manifest) != 3 {
		t.Fatalf("manifest length = %d, want 3", len(manifest))
	}
	if manifest[0].Suffix != "" || manifest[0].Type != "filesystem" {
		t.Fatalf("root manifest entry = %#v", manifest[0])
	}
	if manifest[2].Suffix != "virtual-machines/7/disk0" || manifest[2].Type != "volume" {
		t.Fatalf("volume manifest entry = %#v", manifest[2])
	}

	expected := []restoreDatasetManifestEntry{
		{Suffix: "", Type: "filesystem", SnapshotGUID: "100"},
		{Suffix: "child", Type: "filesystem", SnapshotGUID: "200"},
		{Suffix: "disk", Type: "volume", SnapshotGUID: "300"},
	}
	matching := []restoreDatasetManifestEntry{
		{Suffix: "disk", Type: "volume", SnapshotGUID: "300"},
		{Suffix: "", Type: "filesystem", SnapshotGUID: "100"},
		{Suffix: "child", Type: "filesystem", SnapshotGUID: "200"},
	}
	if problems := compareRestoreDatasetManifests(expected, matching); len(problems) != 0 {
		t.Fatalf("matching manifests reported problems: %v", problems)
	}

	mismatched := []restoreDatasetManifestEntry{
		{Suffix: "", Type: "filesystem", SnapshotGUID: "different"},
		{Suffix: "child", Type: "volume", SnapshotGUID: ""},
		{Suffix: "extra", Type: "filesystem", SnapshotGUID: "400"},
	}
	problems := strings.Join(compareRestoreDatasetManifests(expected, mismatched), "\n")
	for _, want := range []string{
		"missing_dataset=disk",
		"unexpected_dataset=extra",
		"dataset_type_mismatch=child",
		"selected_snapshot_missing=child",
		"snapshot_guid_mismatch=<root>",
	} {
		if !strings.Contains(problems, want) {
			t.Fatalf("manifest mismatch did not contain %q:\n%s", want, problems)
		}
	}
}

func TestVerifyNonrecursiveRestoreManifestRequiresExactRootRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping nonrecursive staging integrity test in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	staging := poolName + "/nonrecursive-staging"
	zfstest.EnsureDataset(t, client, staging)
	mustRunRestoreZFSTestCommand(t, "snapshot", staging+"@selected")

	service := &Service{GZFS: client}
	expected := []restoreDatasetManifestEntry{{
		Suffix:       "",
		Type:         "filesystem",
		SnapshotGUID: restoreZFSSnapshotGUID(t, staging, "selected"),
	}}
	if err := service.verifyRestoreManifest(
		context.Background(), staging, "@selected", expected, false,
	); err != nil {
		t.Fatalf("matching nonrecursive root failed verification: %v", err)
	}

	wrongGUID := append([]restoreDatasetManifestEntry(nil), expected...)
	wrongGUID[0].SnapshotGUID = "18446744073709551615"
	if err := service.verifyRestoreManifest(
		context.Background(), staging, "@selected", wrongGUID, false,
	); err == nil || !strings.Contains(err.Error(), "snapshot_guid_mismatch=<root>") {
		t.Fatalf("wrong root GUID error = %v", err)
	}

	mustRunRestoreZFSTestCommand(t, "destroy", staging+"@selected")
	if err := service.verifyRestoreManifest(
		context.Background(), staging, "@selected", expected, false,
	); err == nil || !strings.Contains(err.Error(), "selected_snapshot_missing=<root>") {
		t.Fatalf("missing root snapshot error = %v", err)
	}

	mustRunRestoreZFSTestCommand(t, "snapshot", staging+"@selected")
	expected[0].SnapshotGUID = restoreZFSSnapshotGUID(t, staging, "selected")
	child := staging + "/unexpected-child"
	zfstest.EnsureDataset(t, client, child)
	mustRunRestoreZFSTestCommand(t, "snapshot", child+"@selected")
	if err := service.verifyRestoreManifest(
		context.Background(), staging, "@selected", expected, false,
	); err == nil || !strings.Contains(err.Error(), "unexpected_dataset=unexpected-child") {
		t.Fatalf("unexpected nonrecursive child error = %v", err)
	}
}

func TestRecursiveRestoreSnapshotCoverageRejectsIncompleteTreeWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)
	remoteRoot := poolName + "/backup/tree"
	missingChild := remoteRoot + "/child"
	destination := poolName + "/live"
	zfstest.EnsureDataset(t, client, missingChild)
	zfstest.EnsureDataset(t, client, destination+"/keep")
	mustRunRestoreZFSTestCommand(t, "snapshot", remoteRoot+"@selected")

	target := clusterModels.BackupTarget{
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: poolName + "/backup",
		Enabled:    true,
	}
	job := clusterModels.BackupJob{
		ID:            91,
		Name:          "incomplete-recursive-restore",
		Target:        target,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: destination,
		DestSuffix:    "tree",
		Recursive:     true,
	}
	svc := &Service{
		DB:                testutil.NewSQLiteTestDB(t),
		GZFS:              client,
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := svc.runRestoreJob(ctx, &job, "@selected", "")
	if err == nil {
		t.Fatal("expected incomplete recursive snapshot coverage to fail")
	}
	if !strings.Contains(err.Error(), "recursive_restore_snapshot_incomplete") ||
		!strings.Contains(err.Error(), missingChild) {
		t.Fatalf("unexpected coverage error: %v", err)
	}

	for dataset, wantExists := range map[string]bool{
		destination:                true,
		destination + "/keep":      true,
		destination + ".restoring": false,
	} {
		exists, existsErr := svc.localDatasetExists(context.Background(), dataset)
		if existsErr != nil {
			t.Fatalf("check %s: %v", dataset, existsErr)
		}
		if exists != wantExists {
			t.Fatalf("dataset %s exists=%v, want %v", dataset, exists, wantExists)
		}
	}
}

func TestRunNonrecursiveRestoreJobSelectedRootOnlyWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)
	remoteRoot := poolName + "/backup/tree"
	remoteChild := remoteRoot + "/child-without-selected-snapshot"
	destination := poolName + "/live"
	zfstest.EnsureDataset(t, client, remoteChild)
	zfstest.EnsureDataset(t, client, destination+"/old-child")

	remoteMountpoint := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"get", "-H", "-o", "value", "mountpoint", remoteRoot,
	))
	if remoteMountpoint == "" || remoteMountpoint == "legacy" || remoteMountpoint == "none" || remoteMountpoint == "-" {
		t.Fatalf("remote root has unusable mountpoint %q", remoteMountpoint)
	}
	if err := os.WriteFile(filepath.Join(remoteMountpoint, "selected-root-data"), []byte("root-only"), 0o600); err != nil {
		t.Fatalf("write remote root data: %v", err)
	}
	mustRunRestoreZFSTestCommand(t, "snapshot", remoteRoot+"@selected")
	selectedGUID := restoreZFSSnapshotGUID(t, remoteRoot, "selected")

	extractZeltaToTemp(t)
	database := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
	)
	target := clusterModels.BackupTarget{
		ID:         51,
		Name:       "nonrecursive-restore-target",
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: poolName + "/backup",
		Enabled:    true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("create backup target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID:            52,
		Name:          "nonrecursive-restore-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: destination,
		DestSuffix:    "tree",
		Recursive:     false,
		CronExpr:      "0 0 * * *",
		Enabled:       true,
	}
	if err := database.Create(&job).Error; err != nil {
		t.Fatalf("create backup job: %v", err)
	}
	if err := database.Preload("Target").First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload backup job: %v", err)
	}

	service := &Service{
		DB:                database,
		GZFS:              client,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := service.runRestoreJob(ctx, &job, "@selected", ""); err != nil {
		t.Fatalf("nonrecursive restore failed: %v", err)
	}

	if got := restoreZFSSnapshotGUID(t, destination, "selected"); got != selectedGUID {
		t.Fatalf("restored root snapshot GUID = %q, want %q", got, selectedGUID)
	}
	for _, child := range []string{
		destination + "/old-child",
		destination + "/child-without-selected-snapshot",
	} {
		if output, err := exec.Command("zfs", "list", "-H", "-o", "name", child).CombinedOutput(); err == nil {
			t.Fatalf("nonrecursive restore unexpectedly retained child %s: %s", child, output)
		}
	}
	destinationMountpoint := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"get", "-H", "-o", "value", "mountpoint", destination,
	))
	rootData, err := os.ReadFile(filepath.Join(destinationMountpoint, "selected-root-data"))
	if err != nil || string(rootData) != "root-only" {
		t.Fatalf("selected root data missing: data=%q err=%v", rootData, err)
	}
	if !restoreZFSSnapshotExists(t, remoteRoot, "selected") {
		t.Fatal("nonrecursive restore mutated the remote root snapshot")
	}
}

func TestRunRecursiveRestoreJobSelectedSnapshotWithEphemeralZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ZFS restore integration in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	sshHost, sshKeyPath := requireRestoreLocalhostSSH(t)
	remoteRoot := poolName + "/backup/tree"
	remoteChild := remoteRoot + "/child"
	remoteVolume := remoteRoot + "/disk0"
	destination := poolName + "/live"
	zfstest.EnsureDataset(t, client, remoteChild)
	zfstest.EnsureVolume(t, client, remoteVolume, 8)
	zfstest.EnsureDataset(t, client, destination+"/old-child")

	mountpoint := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"get", "-H", "-o", "value", "mountpoint", remoteRoot,
	))
	if mountpoint == "" || mountpoint == "legacy" || mountpoint == "none" || mountpoint == "-" {
		t.Fatalf("remote root has unusable mountpoint %q", mountpoint)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, "before-selected"), []byte("selected-data"), 0o600); err != nil {
		t.Fatalf("write remote data before selected snapshot: %v", err)
	}
	mustRunRestoreZFSTestCommand(t, "snapshot", "-r", remoteRoot+"@selected")
	if err := os.WriteFile(filepath.Join(mountpoint, "after-selected"), []byte("later-data"), 0o600); err != nil {
		t.Fatalf("write remote data after selected snapshot: %v", err)
	}
	mustRunRestoreZFSTestCommand(t, "snapshot", "-r", remoteRoot+"@later")
	if err := os.WriteFile(filepath.Join(mountpoint, "after-later"), []byte("changed"), 0o600); err != nil {
		t.Fatalf("write remote data after later snapshot: %v", err)
	}

	beforeRemoteSnapshots := restoreZFSSnapshotInventory(t, remoteRoot)
	expectedGUIDs := map[string]string{
		"":      restoreZFSSnapshotGUID(t, remoteRoot, "selected"),
		"child": restoreZFSSnapshotGUID(t, remoteChild, "selected"),
		"disk0": restoreZFSSnapshotGUID(t, remoteVolume, "selected"),
	}

	extractZeltaToTemp(t)
	db := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
	)
	target := clusterModels.BackupTarget{
		ID:         41,
		Name:       "recursive-restore-target",
		SSHHost:    sshHost,
		SSHKeyPath: sshKeyPath,
		BackupRoot: poolName + "/backup",
		Enabled:    true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create backup target: %v", err)
	}
	job := clusterModels.BackupJob{
		ID:            42,
		Name:          "recursive-restore-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: destination,
		DestSuffix:    "tree",
		Recursive:     true,
		CronExpr:      "0 0 * * *",
		Enabled:       true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create backup job: %v", err)
	}
	if err := db.Preload("Target").First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload backup job: %v", err)
	}

	svc := &Service{
		DB:                db,
		GZFS:              client,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := svc.runRestoreJob(ctx, &job, "@selected", ""); err != nil {
		t.Fatalf("recursive restore failed: %v", err)
	}

	expectedTypes := map[string]string{"": "filesystem", "child": "filesystem", "disk0": "volume"}
	for suffix, expectedGUID := range expectedGUIDs {
		dataset := destination
		if suffix != "" {
			dataset += "/" + suffix
		}
		if got := restoreZFSSnapshotGUID(t, dataset, "selected"); got != expectedGUID {
			t.Fatalf("%s@selected GUID = %q, want %q", dataset, got, expectedGUID)
		}
		if restoreZFSSnapshotExists(t, dataset, "later") {
			t.Fatalf("later snapshot unexpectedly restored on %s", dataset)
		}
		if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(t, "list", "-H", "-o", "type", dataset)); got != expectedTypes[suffix] {
			t.Fatalf("%s type = %q, want %q", dataset, got, expectedTypes[suffix])
		}
	}

	destinationMountpoint := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"get", "-H", "-o", "value", "mountpoint", destination,
	))
	selectedData, err := os.ReadFile(filepath.Join(destinationMountpoint, "before-selected"))
	if err != nil || string(selectedData) != "selected-data" {
		t.Fatalf("selected-point data missing: data=%q err=%v", selectedData, err)
	}
	for _, name := range []string{"after-selected", "after-later"} {
		if _, err := os.Stat(filepath.Join(destinationMountpoint, name)); !os.IsNotExist(err) {
			t.Fatalf("post-selected file %q exists or could not be checked: %v", name, err)
		}
	}

	afterRemoteSnapshots := restoreZFSSnapshotInventory(t, remoteRoot)
	if afterRemoteSnapshots != beforeRemoteSnapshots {
		t.Fatalf(
			"restore mutated remote snapshot inventory\nbefore:\n%s\nafter:\n%s",
			beforeRemoteSnapshots,
			afterRemoteSnapshots,
		)
	}

	allDatasets := mustRunRestoreZFSTestCommand(
		t,
		"list", "-H", "-r", "-t", "filesystem,volume", "-o", "name", poolName,
	)
	if strings.Contains(allDatasets, ".restoring") || strings.Contains(allDatasets, "_restore-backup-") {
		t.Fatalf("restore artifacts remain after successful restore:\n%s", allDatasets)
	}
	for _, property := range restoreStagingPropertyNames {
		propertyState := strings.TrimSpace(mustRunRestoreZFSTestCommand(
			t,
			"get", "-H", "-o", "value,source", property, destination,
		))
		if strings.HasSuffix(propertyState, "\tlocal") {
			t.Fatalf("restore staging property %s remains local on promoted dataset: %q", property, propertyState)
		}
	}

	var event clusterModels.BackupEvent
	if err := db.Where("job_id = ? AND mode = ?", job.ID, "restore").Order("id desc").First(&event).Error; err != nil {
		t.Fatalf("load restore event: %v", err)
	}
	if event.Status != "success" {
		t.Fatalf("restore event status = %q error=%q", event.Status, event.Error)
	}
}

func mustRunRestoreZFSTestCommand(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("zfs", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("zfs %s: %v\noutput: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func restoreZFSSnapshotGUID(t *testing.T, dataset, snapshot string) string {
	t.Helper()
	return strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"get", "-H", "-p", "-o", "value", "guid", dataset+"@"+snapshot,
	))
}

func restoreZFSSnapshotExists(t *testing.T, dataset, snapshot string) bool {
	t.Helper()
	cmd := exec.Command("zfs", "list", "-H", "-t", "snapshot", dataset+"@"+snapshot)
	return cmd.Run() == nil
}

func restoreZFSSnapshotInventory(t *testing.T, dataset string) string {
	t.Helper()
	return strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t,
		"list", "-H", "-p", "-r", "-t", "snapshot", "-o", "name,guid", dataset,
	))
}

func requireRestoreLocalhostSSH(t *testing.T) (string, string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve root home for localhost SSH test: %v", err)
	}
	host := "root@127.0.0.1"
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		keyPath := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(keyPath); err != nil {
			continue
		}
		cmd := exec.Command(
			"ssh",
			"-n",
			"-i", keyPath,
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=3",
			host,
			"true",
		)
		if output, err := cmd.CombinedOutput(); err == nil {
			return host, keyPath
		} else {
			t.Logf("localhost SSH identity %s unavailable: %v (%s)", keyPath, err, strings.TrimSpace(string(output)))
		}
	}
	t.Skip("real root localhost SSH is required for remote restore integration")
	return "", ""
}
