// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func exactRestoreSnapshotListCommand(datasets []string, snapshot string) string {
	args := []string{"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid"}
	for _, dataset := range datasets {
		args = append(args, dataset+snapshot)
	}
	return strings.Join(args, " ")
}

func TestFilterRestorableTargetSnapshotsBatchesCommitMetadata(t *testing.T) {
	harness := newFakeSSHHarness(t)
	snapshots, _, metadataBySnapshot, remoteNames := backupInventoryScaleFixture(t, 257)
	harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))
	target := &clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"}

	filtered, err := (&Service{}).filterRestorableTargetSnapshots(
		context.Background(),
		target,
		clusterModels.BackupJobModeDataset,
		snapshots,
	)
	if err != nil {
		t.Fatalf("filter target restore points: %v", err)
	}
	if len(filtered) != len(snapshots) {
		t.Fatalf("filtered snapshots = %d, want %d", len(filtered), len(snapshots))
	}
	if got, want := len(harness.Calls()), 3; got != want {
		t.Fatalf("SSH calls = %d, want %d bounded batches", got, want)
	}
}

func TestLoadRemoteRestoreSnapshotInventoryListsOnlySelectedGeneration(t *testing.T) {
	harness := newFakeSSHHarness(t)
	const (
		remoteRoot = "backup/root"
		snapshot   = "@bk_j1_c1_selected"
	)
	datasets := make([]string, 0, 257)
	datasets = append(datasets, remoteRoot)
	for i := 0; i < 256; i++ {
		datasets = append(datasets, fmt.Sprintf("%s/child-%03d", remoteRoot, i))
	}

	var datasetOutput strings.Builder
	for _, dataset := range datasets {
		fmt.Fprintf(&datasetOutput, "%s\tfilesystem\n", dataset)
	}
	responses := map[string][]fakeSSHResponse{
		strings.Join(backupDatasetListArgs(remoteRoot, true), " "): {{
			Stdout:   datasetOutput.String(),
			ExitCode: 0,
		}},
	}
	for start := 0; start < len(datasets); start += backupCommitMetadataBatchSize {
		end := start + backupCommitMetadataBatchSize
		if end > len(datasets) {
			end = len(datasets)
		}
		var snapshotOutput strings.Builder
		for _, dataset := range datasets[start:end] {
			fmt.Fprintf(&snapshotOutput, "%s%s\tguid-%s\n", dataset, snapshot, dataset)
		}
		responses[exactRestoreSnapshotListCommand(datasets[start:end], snapshot)] = []fakeSSHResponse{{
			Stdout:   snapshotOutput.String(),
			ExitCode: 0,
		}}
	}
	harness.SetScenario(fakeSSHScenario{Responses: responses})

	inventory, err := (&Service{}).loadRemoteRestoreSnapshotInventory(
		context.Background(),
		&clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"},
		remoteRoot,
		snapshot,
		true,
	)
	if err != nil {
		t.Fatalf("load exact restore inventory: %v", err)
	}
	manifest, err := inventory.restoreManifest(snapshot, true)
	if err != nil {
		t.Fatalf("build restore manifest: %v", err)
	}
	if got, want := len(manifest), len(datasets); got != want {
		t.Fatalf("manifest entries = %d, want %d", got, want)
	}
	calls := harness.Calls()
	if got, want := len(calls), 4; got != want {
		t.Fatalf("SSH calls = %d, want %d", got, want)
	}
	for _, call := range calls {
		if strings.Contains(call, "-t snapshot") && strings.Contains(call, " -r ") {
			t.Fatalf("restore enumerated snapshot history: %s", call)
		}
	}
}

func TestResolveRemoteDatasetForSnapshotUsesExactSnapshotQueries(t *testing.T) {
	harness := newFakeSSHHarness(t)
	const snapshot = "@bk_j1_c1_selected"
	harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs list -t filesystem -d 1 -Hp -o name backup": {{
			Stdout: "backup\nbackup/root\nbackup/root_gen-a\nbackup/root_gen-b\n",
		}},
		"zfs list -H -p -t snapshot -o name,creation,used,refer,guid,encryption backup/root" + snapshot: {{
			Stderr:   "cannot open 'backup/root@bk_j1_c1_selected': dataset does not exist",
			ExitCode: 1,
		}},
		"zfs list -H -p -t snapshot -o name,creation,used,refer,guid,encryption backup/root_gen-a" + snapshot: {{
			Stdout: "backup/root_gen-a@bk_j1_c1_selected\t100\t1\t1\tguid-a\toff\n",
		}},
		"zfs list -H -p -t snapshot -o name,creation,used,refer,guid,encryption backup/root_gen-b" + snapshot: {{
			Stdout: "backup/root_gen-b@bk_j1_c1_selected\t200\t1\t1\tguid-b\toff\n",
		}},
	}})

	resolved, err := (&Service{}).resolveRemoteDatasetForSnapshot(
		context.Background(),
		&clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"},
		"backup/root",
		snapshot,
	)
	if err != nil {
		t.Fatalf("resolve exact restore snapshot: %v", err)
	}
	if resolved != "backup/root_gen-b" {
		t.Fatalf("resolved dataset = %q, want backup/root_gen-b", resolved)
	}
	for _, call := range harness.Calls() {
		if strings.Contains(call, "-t snapshot") && !strings.Contains(call, snapshot) {
			t.Fatalf("lineage resolution listed snapshot history: %s", call)
		}
	}
	if remoteSnapshotMissingError(fmt.Errorf("cannot open backup/root: permission denied")) {
		t.Fatal("permission failure was treated as a missing snapshot")
	}
}

func TestRecheckRemoteRestoreDatasetPlanRejectsChangedGUID(t *testing.T) {
	harness := newFakeSSHHarness(t)
	const (
		remoteRoot = "backup/root"
		snapshot   = "@bk_j1_c1_selected"
	)
	harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		strings.Join(backupDatasetListArgs(remoteRoot, true), " "): {{
			Stdout: remoteRoot + "\tfilesystem\n",
		}},
		exactRestoreSnapshotListCommand([]string{remoteRoot}, snapshot): {{
			Stdout: remoteRoot + snapshot + "\tchanged-guid\n",
		}},
	}})
	plan := remoteRestoreDatasetPlan{
		resolvedRemoteDataset: remoteRoot,
		snapshot:              snapshot,
		restoreRecursive:      true,
		expectedManifest: []restoreDatasetManifestEntry{{
			Type:         "filesystem",
			SnapshotGUID: "original-guid",
		}},
	}

	err := (&Service{}).recheckRemoteRestoreDatasetPlan(
		context.Background(),
		&clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"},
		plan,
	)
	if err == nil || !strings.Contains(err.Error(), "restore_snapshot_changed_after_preflight") {
		t.Fatalf("expected changed GUID rejection, got %v", err)
	}
}
