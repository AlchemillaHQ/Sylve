// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

type restoreVirtualizationEnabledStub struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
}

func (restoreVirtualizationEnabledStub) IsVirtualizationEnabled() bool { return true }

func backupCommitMetadataTestOutput(t *testing.T, metadata backupCommitMetadata) string {
	t.Helper()
	properties, err := backupCommitProperties(metadata)
	if err != nil {
		t.Fatalf("commit properties: %v", err)
	}
	var output strings.Builder
	for _, property := range properties {
		parts := strings.SplitN(property, "=", 2)
		output.WriteString(parts[0])
		output.WriteByte('\t')
		output.WriteString(parts[1])
		output.WriteString("\tlocal")
		output.WriteByte('\n')
	}
	return output.String()
}

func uncommittedBackupMetadataTestOutput() string {
	var output strings.Builder
	for _, property := range backupCommitPropertyNames() {
		output.WriteString(property)
		output.WriteString("\t-\t-\n")
	}
	return output.String()
}

func TestFilterRestorableTargetSnapshotsHidesUncommittedAndLegacyVM(t *testing.T) {
	const (
		remoteRoot        = "backup/root"
		legacyName        = "bk_j1_legacy"
		uncommittedName   = "bk_j1_c1_interrupted"
		committedName     = "bk_j1_c1_committed"
		metadataGetPrefix = "zfs get -H -p -o property,value,source "
	)

	manifest, err := buildBackupManifest(1, committedName, true, []backupManifestEntry{
		{Root: "source/root", Type: "filesystem", SnapshotGUID: "101"},
	})
	if err != nil {
		t.Fatal(err)
	}
	committedOutput := backupCommitMetadataTestOutput(t, newBackupCommitMetadata(manifest))
	properties := strings.Join(backupCommitPropertyNames(), ",")

	tests := []struct {
		name        string
		datasetKind string
		wantLegacy  bool
	}{
		{name: "dataset preserves legacy", datasetKind: clusterModels.BackupJobModeDataset, wantLegacy: true},
		{name: "jail preserves legacy", datasetKind: clusterModels.BackupJobModeJail, wantLegacy: true},
		{name: "VM requires committed root set", datasetKind: clusterModels.BackupJobModeVM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newFakeSSHHarness(t)
			harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
				metadataGetPrefix + properties + " " + remoteRoot + "@" + uncommittedName: {{
					Stdout:   uncommittedBackupMetadataTestOutput(),
					ExitCode: 0,
				}},
				metadataGetPrefix + properties + " " + remoteRoot + "@" + committedName: {{
					Stdout:   committedOutput,
					ExitCode: 0,
				}},
			}})

			snapshots := []SnapshotInfo{
				{Name: remoteRoot + "@" + legacyName, ShortName: "@" + legacyName, Dataset: remoteRoot},
				{Name: remoteRoot + "@" + uncommittedName, ShortName: "@" + uncommittedName, Dataset: remoteRoot},
				{Name: remoteRoot + "@" + committedName, ShortName: "@" + committedName, Dataset: remoteRoot},
			}
			filtered, err := (&Service{}).filterRestorableTargetSnapshots(
				context.Background(),
				&clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"},
				tt.datasetKind,
				snapshots,
			)
			if err != nil {
				t.Fatalf("filter restore points: %v", err)
			}

			wantCount := 1
			if tt.wantLegacy {
				wantCount = 2
			}
			if len(filtered) != wantCount {
				t.Fatalf("filtered snapshots = %+v, want count %d", filtered, wantCount)
			}
			for _, snapshot := range filtered {
				switch snapshotShortName(snapshot) {
				case "@" + uncommittedName:
					t.Fatalf("uncommitted c1 snapshot was advertised: %+v", snapshot)
				case "@" + legacyName:
					if !tt.wantLegacy || !snapshot.Legacy {
						t.Fatalf("legacy snapshot flags = %+v", snapshot)
					}
				case "@" + committedName:
					if !snapshot.Committed || snapshot.Legacy {
						t.Fatalf("committed snapshot flags = %+v", snapshot)
					}
				default:
					t.Fatalf("unexpected restore point: %+v", snapshot)
				}
			}
		})
	}
}

func TestValidateVMRestoreSnapshotCompatibility(t *testing.T) {
	t.Parallel()

	job1 := uint(1)
	job2 := uint(2)
	tests := []struct {
		name      string
		snapshot  string
		jobID     *uint
		wantError string
	}{
		{name: "missing", wantError: "restore_vm_committed_snapshot_required"},
		{name: "legacy", snapshot: "@bk_j1_legacy", wantError: "restore_vm_legacy_snapshot_unsupported"},
		{name: "malformed c1", snapshot: "@bk_j0_c1_bad", wantError: "restore_vm_snapshot_commit_invalid"},
		{name: "committed protocol", snapshot: "@bk_j1_c1_valid"},
		{name: "job-bound legacy", snapshot: "@bk_j1_legacy", jobID: &job1},
		{name: "job-bound legacy mismatch", snapshot: "@bk_j1_legacy", jobID: &job2, wantError: "restore_vm_snapshot_job_mismatch"},
		{name: "job-bound committed mismatch", snapshot: "@bk_j1_c1_valid", jobID: &job2, wantError: "restore_vm_snapshot_job_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMRestoreSnapshot(tt.snapshot, tt.jobID)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestRunRestoreFromTargetVMRejectsLegacyBeforeRuntimeOrReceive(t *testing.T) {
	t.Parallel()

	err := (&Service{}).runRestoreFromTargetVM(
		context.Background(),
		&clusterModels.BackupTarget{BackupRoot: "backup/root"},
		restoreFromTargetPayload{Snapshot: "@bk_j1_legacy"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "restore_vm_legacy_snapshot_unsupported") {
		t.Fatalf("legacy VM restore error = %v", err)
	}
}

func TestLegacyVMRestoreMissingRootSnapshotFailsBeforeStaging(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS legacy VM restore preflight test in short mode")
	}
	requireLocalhostBackupSSH(t)

	poolA, clientA, cleanupA := zfstest.Pool(t)
	defer cleanupA()
	poolB, _, cleanupB := zfstest.Pool(t)
	defer cleanupB()

	backupRoot := poolA + "/target"
	remoteA := backupRoot + "/" + poolA + "/sylve/virtual-machines/42/j-1/active"
	remoteB := backupRoot + "/" + poolB + "/sylve/virtual-machines/42/j-1/active"
	otherJobRoot := backupRoot + "/" + poolA + "/sylve/virtual-machines/42/j-0/active"
	for _, dataset := range []string{remoteA, remoteB, otherJobRoot} {
		zfstest.EnsureDataset(t, clientA, dataset)
	}
	const snapshot = "bk_j1_legacy"
	createZFSSnapshot(t, remoteA, snapshot)
	destinationA := poolA + "/sylve/virtual-machines/42"

	database := testutil.NewSQLiteTestDB(t, &clusterModels.BackupEvent{})
	service := &Service{
		DB:                        database,
		VM:                        restoreVirtualizationEnabledStub{},
		GZFS:                      clientA,
		runningRestoreDestination: map[string]struct{}{destinationA: {}},
		runningWorkloadOp:         make(map[string]string),
	}
	target := &clusterModels.BackupTarget{
		ID:         1,
		SSHHost:    "root@localhost",
		SSHPort:    22,
		BackupRoot: backupRoot,
		Enabled:    true,
	}
	jobID := uint(1)
	err := service.runRestoreFromTargetVM(
		context.Background(),
		target,
		restoreFromTargetPayload{
			RemoteDataset:      remoteA,
			Snapshot:           "@" + snapshot,
			DestinationDataset: destinationA,
		},
		&jobID,
	)
	if err == nil || !strings.Contains(err.Error(), "snapshot_not_found_on_target") ||
		!strings.Contains(err.Error(), "dataset="+remoteB) {
		t.Fatalf("partial legacy VM restore error = %v", err)
	}

	for _, destination := range []string{
		destinationA,
		poolB + "/sylve/virtual-machines/42",
	} {
		if zfsDatasetExists(t, destination+".restoring") {
			t.Fatalf("staging dataset was mutated before all roots passed preflight: %s.restoring", destination)
		}
	}
}
