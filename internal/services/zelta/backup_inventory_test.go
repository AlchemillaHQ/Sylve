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

func backupCommitBatchTestOutput(
	t *testing.T,
	snapshots []string,
	metadataBySnapshot map[string]backupCommitMetadata,
) string {
	t.Helper()
	var output strings.Builder
	for _, snapshot := range snapshots {
		metadata, ok := metadataBySnapshot[snapshot]
		if !ok {
			for _, property := range backupCommitPropertyNames() {
				fmt.Fprintf(&output, "%s\t%s\t-\t-\n", snapshot, property)
			}
			continue
		}
		properties, err := backupCommitProperties(metadata)
		if err != nil {
			t.Fatalf("commit properties for %s: %v", snapshot, err)
		}
		for _, property := range properties {
			parts := strings.SplitN(property, "=", 2)
			fmt.Fprintf(&output, "%s\t%s\t%s\tlocal\n", snapshot, parts[0], parts[1])
		}
	}
	return output.String()
}

func backupCommitBatchTestCommand(snapshots []string) string {
	args := []string{
		"zfs", "get", "-H", "-p", "-o", "name,property,value,source",
		strings.Join(backupCommitPropertyNames(), ","),
	}
	args = append(args, snapshots...)
	return strings.Join(args, " ")
}

func backupInventoryScaleFixture(
	t *testing.T,
	count int,
) (
	[]SnapshotInfo,
	map[string]map[string]string,
	map[string]backupCommitMetadata,
	[]string,
) {
	t.Helper()
	const (
		jobID      = uint(1)
		sourceRoot = "source/root"
		remoteRoot = "backup/root"
	)
	snapshots := make([]SnapshotInfo, 0, count)
	guidsByName := make(map[string]map[string]string, count)
	metadataBySnapshot := make(map[string]backupCommitMetadata, count)
	remoteNames := make([]string, 0, count)
	for i := 0; i < count; i++ {
		shortName := fmt.Sprintf("%s_c1_%04d", backupSnapshotPrefixForJob(jobID), i)
		guid := fmt.Sprintf("guid-%04d", i)
		remoteName := remoteRoot + "@" + shortName
		manifest, err := buildBackupManifest(jobID, shortName, false, []backupManifestEntry{{
			Root:         sourceRoot,
			Type:         "filesystem",
			SnapshotGUID: guid,
		}})
		if err != nil {
			t.Fatalf("manifest %d: %v", i, err)
		}
		snapshots = append(snapshots, SnapshotInfo{
			Name:      remoteName,
			ShortName: "@" + shortName,
			Dataset:   remoteRoot,
			Guid:      guid,
		})
		guidsByName[shortName] = map[string]string{remoteRoot: guid}
		metadataBySnapshot[remoteName] = newBackupCommitMetadata(manifest)
		remoteNames = append(remoteNames, remoteName)
	}
	return snapshots, guidsByName, metadataBySnapshot, remoteNames
}

func backupCommitBatchScenario(
	t *testing.T,
	remoteNames []string,
	metadataBySnapshot map[string]backupCommitMetadata,
) fakeSSHScenario {
	t.Helper()
	responses := make(map[string][]fakeSSHResponse)
	for start := 0; start < len(remoteNames); start += backupCommitMetadataBatchSize {
		end := start + backupCommitMetadataBatchSize
		if end > len(remoteNames) {
			end = len(remoteNames)
		}
		batch := remoteNames[start:end]
		responses[backupCommitBatchTestCommand(batch)] = []fakeSSHResponse{{
			Stdout:   backupCommitBatchTestOutput(t, batch, metadataBySnapshot),
			ExitCode: 0,
		}}
	}
	return fakeSSHScenario{Responses: responses}
}

func TestFilterRestorableBackupSnapshotsBatchesCommitMetadata(t *testing.T) {
	harness := newFakeSSHHarness(t)
	snapshots, _, metadataBySnapshot, remoteNames := backupInventoryScaleFixture(t, 576)
	harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))
	job := &clusterModels.BackupJob{
		ID: 1,
		Target: clusterModels.BackupTarget{
			SSHHost:    "user@target",
			BackupRoot: "backup",
		},
	}

	filtered, err := (&Service{}).filterRestorableBackupSnapshots(
		context.Background(),
		job,
		snapshots,
	)
	if err != nil {
		t.Fatalf("filter restore points: %v", err)
	}
	if len(filtered) != len(snapshots) {
		t.Fatalf("filtered snapshots = %d, want %d", len(filtered), len(snapshots))
	}
	for _, snapshot := range filtered {
		if !snapshot.Committed || snapshot.Legacy {
			t.Fatalf("invalid committed flags: %+v", snapshot)
		}
	}
	if got, want := len(harness.Calls()), 5; got != want {
		t.Fatalf("SSH calls = %d, want %d bounded batches", got, want)
	}
}

func TestBackupRetentionProofsReuseManifestInventory(t *testing.T) {
	harness := newFakeSSHHarness(t)
	snapshots, guidsByName, metadataBySnapshot, remoteNames := backupInventoryScaleFixture(t, 576)
	harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))
	job := &clusterModels.BackupJob{
		ID: 1,
		Target: clusterModels.BackupTarget{
			SSHHost:    "user@target",
			BackupRoot: "backup",
		},
	}
	scopes := []backupScope{{sourceDataset: "source/root", destSuffix: "root"}}
	inventories := map[string]remoteBackupManifestInventory{
		"backup/root": {
			root:                "backup/root",
			datasets:            map[string]string{"backup/root": "filesystem"},
			snapshotGUIDsByName: guidsByName,
		},
	}

	proofs, err := (&Service{}).backupRetentionEligibleSnapshotProofsFromInventories(
		context.Background(),
		job,
		"backup/root",
		snapshots,
		scopes,
		inventories,
	)
	if err != nil {
		t.Fatalf("build retention proofs: %v", err)
	}
	if got, want := len(proofs.Source), len(snapshots); got != want {
		t.Fatalf("source proofs = %d, want %d", got, want)
	}
	if got, want := len(proofs.Target), len(snapshots); got != want {
		t.Fatalf("target proofs = %d, want %d", got, want)
	}
	calls := harness.Calls()
	if got, want := len(calls), 5; got != want {
		t.Fatalf("SSH calls = %d, want %d bounded metadata batches", got, want)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "zfs list ") {
			t.Fatalf("retention proof rebuilt remote inventory: %s", call)
		}
	}
}
