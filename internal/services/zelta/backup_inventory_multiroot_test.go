// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestBackupRetentionProofsScaleLinearlyAcrossRoots(t *testing.T) {
	harness := newFakeSSHHarness(t)
	const (
		generationCount = 257
		rootCount       = 4
	)
	job := &clusterModels.BackupJob{
		ID: 1,
		Target: clusterModels.BackupTarget{
			SSHHost:    "user@target",
			BackupRoot: "backup",
		},
	}
	scopes := make([]backupScope, 0, rootCount)
	inventories := make(map[string]remoteBackupManifestInventory, rootCount)
	for rootIndex := 0; rootIndex < rootCount; rootIndex++ {
		sourceRoot := fmt.Sprintf("source/root-%d", rootIndex)
		remoteRoot := fmt.Sprintf("backup/root-%d", rootIndex)
		scopes = append(scopes, backupScope{
			sourceDataset: sourceRoot,
			destSuffix:    fmt.Sprintf("root-%d", rootIndex),
		})
		inventories[remoteRoot] = remoteBackupManifestInventory{
			root:                remoteRoot,
			datasets:            map[string]string{remoteRoot: "filesystem"},
			snapshotGUIDsByName: make(map[string]map[string]string, generationCount),
		}
	}

	coordinatorSnapshots := make([]SnapshotInfo, 0, generationCount)
	remoteNames := make([]string, 0, generationCount)
	metadataBySnapshot := make(map[string]backupCommitMetadata, generationCount)
	for generation := 0; generation < generationCount; generation++ {
		shortName := fmt.Sprintf("%s_c1_%04d", backupSnapshotPrefixForJob(job.ID), generation)
		entries := make([]backupManifestEntry, 0, rootCount)
		for rootIndex, scope := range scopes {
			remoteRoot := fmt.Sprintf("backup/root-%d", rootIndex)
			guid := fmt.Sprintf("guid-%d-%04d", rootIndex, generation)
			inventory := inventories[remoteRoot]
			inventory.snapshotGUIDsByName[shortName] = map[string]string{remoteRoot: guid}
			inventories[remoteRoot] = inventory
			entries = append(entries, backupManifestEntry{
				Root:         scope.sourceDataset,
				Type:         "filesystem",
				SnapshotGUID: guid,
			})
		}
		manifest, err := buildBackupManifest(job.ID, shortName, false, entries)
		if err != nil {
			t.Fatalf("manifest %d: %v", generation, err)
		}
		remoteName := "backup/root-0@" + shortName
		coordinatorSnapshots = append(coordinatorSnapshots, SnapshotInfo{
			Name:      remoteName,
			ShortName: "@" + shortName,
			Guid:      fmt.Sprintf("guid-0-%04d", generation),
		})
		remoteNames = append(remoteNames, remoteName)
		metadataBySnapshot[remoteName] = newBackupCommitMetadata(manifest)
	}
	harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))

	proofs, err := (&Service{}).backupRetentionEligibleSnapshotProofsFromInventories(
		context.Background(),
		job,
		"backup/root-0",
		coordinatorSnapshots,
		scopes,
		inventories,
	)
	if err != nil {
		t.Fatalf("build multi-root retention proofs: %v", err)
	}
	if got, want := len(proofs.Source), generationCount*rootCount; got != want {
		t.Fatalf("source proofs = %d, want %d", got, want)
	}
	if got, want := len(proofs.Target), generationCount*rootCount; got != want {
		t.Fatalf("target proofs = %d, want %d", got, want)
	}
	if got, want := len(harness.Calls()), 3; got != want {
		t.Fatalf("SSH calls = %d, want %d metadata batches", got, want)
	}
}
