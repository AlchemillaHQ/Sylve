// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestBatchedRestoreDiscoveryPreservesCommitFiltering(t *testing.T) {
	const (
		remoteRoot      = "backup/root"
		legacyName      = "bk_j1_legacy"
		interruptedName = "bk_j1_c1_interrupted"
		committedName   = "bk_j1_c1_committed"
	)
	manifest, err := buildBackupManifest(1, committedName, false, []backupManifestEntry{{
		Root:         "source/root",
		Type:         "filesystem",
		SnapshotGUID: "committed-guid",
	}})
	if err != nil {
		t.Fatal(err)
	}
	remoteNames := []string{
		remoteRoot + "@" + interruptedName,
		remoteRoot + "@" + committedName,
	}
	metadataBySnapshot := map[string]backupCommitMetadata{
		remoteRoot + "@" + committedName: newBackupCommitMetadata(manifest),
	}
	snapshots := []SnapshotInfo{
		{Name: remoteRoot + "@" + legacyName, ShortName: "@" + legacyName},
		{Name: remoteNames[0], ShortName: "@" + interruptedName},
		{Name: remoteNames[1], ShortName: "@" + committedName},
	}

	t.Run("job restore keeps legacy and committed", func(t *testing.T) {
		harness := newFakeSSHHarness(t)
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
		if len(filtered) != 2 || !filtered[0].Legacy || !filtered[1].Committed {
			t.Fatalf("unexpected restore points: %+v", filtered)
		}
	})

	t.Run("target VM restore rejects legacy and interrupted", func(t *testing.T) {
		harness := newFakeSSHHarness(t)
		harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))
		filtered, err := (&Service{}).filterRestorableTargetSnapshots(
			context.Background(),
			&clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "backup"},
			clusterModels.BackupJobModeVM,
			snapshots,
		)
		if err != nil {
			t.Fatalf("filter target restore points: %v", err)
		}
		if len(filtered) != 1 || snapshotShortName(filtered[0]) != "@"+committedName || !filtered[0].Committed {
			t.Fatalf("unexpected VM restore points: %+v", filtered)
		}
	})
}

func TestBackupRetentionInventoryRejectsManifestMismatch(t *testing.T) {
	harness := newFakeSSHHarness(t)
	snapshots, guidsByName, metadataBySnapshot, remoteNames := backupInventoryScaleFixture(t, 1)
	harness.SetScenario(backupCommitBatchScenario(t, remoteNames, metadataBySnapshot))
	shortName := strings.TrimPrefix(snapshotShortName(snapshots[0]), "@")
	guidsByName[shortName]["backup/root"] = "changed-guid"
	job := &clusterModels.BackupJob{
		ID: 1,
		Target: clusterModels.BackupTarget{
			SSHHost:    "user@target",
			BackupRoot: "backup",
		},
	}

	_, err := (&Service{}).backupRetentionEligibleSnapshotProofsFromInventories(
		context.Background(),
		job,
		"backup/root",
		snapshots,
		[]backupScope{{sourceDataset: "source/root", destSuffix: "root"}},
		map[string]remoteBackupManifestInventory{
			"backup/root": {
				root:                "backup/root",
				datasets:            map[string]string{"backup/root": "filesystem"},
				snapshotGUIDsByName: guidsByName,
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "backup_retention_manifest_mismatch") {
		t.Fatalf("expected fail-closed manifest mismatch, got %v", err)
	}
}
