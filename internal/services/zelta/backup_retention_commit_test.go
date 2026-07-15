// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestFilterBackupSnapshotsByExactManifestProof(t *testing.T) {
	t.Parallel()

	snapshots := []SnapshotInfo{
		{Name: "backup/root@bk_j1_c1_committed", ShortName: "@bk_j1_c1_committed", Guid: "root-guid"},
		{Name: "backup/root/child@bk_j1_c1_committed", ShortName: "@bk_j1_c1_committed", Guid: "child-guid"},
		{Name: "backup/root/rogue@bk_j1_c1_committed", ShortName: "@bk_j1_c1_committed", Guid: "rogue-guid"},
		{Name: "backup/root@bk_j1_c1_interrupted", ShortName: "@bk_j1_c1_interrupted", Guid: "other-guid"},
	}
	proofs := map[string]string{
		"backup/root@bk_j1_c1_committed":       "root-guid",
		"backup/root/child@bk_j1_c1_committed": "child-guid",
	}
	filtered := filterBackupSnapshotsByProof(snapshots, proofs)
	got := snapshotNames(filtered)
	want := []string{
		"backup/root@bk_j1_c1_committed",
		"backup/root/child@bk_j1_c1_committed",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retention snapshots = %v, want %v", got, want)
	}
}

func TestBackupRetentionPreservesLegacySnapshotsWithoutOwnershipProof(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	job := &clusterModels.BackupJob{ID: 1}
	proofs, err := svc.backupRetentionEligibleSnapshotProofs(
		context.Background(),
		job,
		"backup/root",
		[]SnapshotInfo{{Name: "backup/root@bk_j1_legacy", ShortName: "@bk_j1_legacy"}},
		[]backupScope{{sourceDataset: "pool/source", destSuffix: "root"}},
	)
	if err != nil {
		t.Fatalf("legacy retention classification: %v", err)
	}
	if len(proofs.Source) != 0 || len(proofs.Target) != 0 {
		t.Fatalf("legacy snapshots must not be eligible for automatic deletion: %+v", proofs)
	}
}

func TestAddBackupManifestRetentionProofsMapsExactSourceAndTargetPaths(t *testing.T) {
	t.Parallel()

	job := &clusterModels.BackupJob{
		ID: 1,
		Target: clusterModels.BackupTarget{
			BackupRoot: "backup",
		},
	}
	manifest := backupManifest{
		SnapshotName: "bk_j1_c1_point",
		Entries: []backupManifestEntry{
			{Root: "source/root", Suffix: "", SnapshotGUID: "root-guid"},
			{Root: "source/root", Suffix: "child", SnapshotGUID: "child-guid"},
		},
	}
	proofs := newBackupRetentionProofSet()
	if err := addBackupManifestRetentionProofs(
		&proofs,
		job,
		manifest,
		[]backupScope{{sourceDataset: "source/root", destSuffix: "job/root"}},
	); err != nil {
		t.Fatalf("add manifest proofs: %v", err)
	}

	wantSource := map[string]string{
		"source/root@bk_j1_c1_point":       "root-guid",
		"source/root/child@bk_j1_c1_point": "child-guid",
	}
	wantTarget := map[string]string{
		"backup/job/root@bk_j1_c1_point":       "root-guid",
		"backup/job/root/child@bk_j1_c1_point": "child-guid",
	}
	if !reflect.DeepEqual(proofs.Source, wantSource) || !reflect.DeepEqual(proofs.Target, wantTarget) {
		t.Fatalf("proofs = %+v, want source=%v target=%v", proofs, wantSource, wantTarget)
	}
}

func TestLocalRetentionProofPreservesSameNameNonrecursiveDescendant(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()
	root := poolName + "/source"
	child := root + "/rogue"
	zfstest.EnsureDataset(t, gzfsClient, child)

	snapshotName := "bk_j1_c1_committed"
	rootSnapshot := root + "@" + snapshotName
	childSnapshot := child + "@" + snapshotName
	for _, snapshot := range []string{rootSnapshot, childSnapshot} {
		if output, err := exec.Command("zfs", "snapshot", snapshot).CombinedOutput(); err != nil {
			t.Fatalf("create snapshot %s: %v: %s", snapshot, err, output)
		}
	}

	readGUID := func(snapshot string) string {
		output, err := exec.Command(
			"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", snapshot,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("read snapshot %s: %v: %s", snapshot, err, output)
		}
		guid, err := parseExactSnapshotGUID(string(output), snapshot)
		if err != nil {
			t.Fatalf("parse snapshot %s: %v", snapshot, err)
		}
		return guid
	}
	rootGUID := readGUID(rootSnapshot)
	childGUID := readGUID(childSnapshot)

	proofs := map[string]string{rootSnapshot: rootGUID}
	inventory := []SnapshotInfo{
		{Name: rootSnapshot, Guid: rootGUID},
		{Name: childSnapshot, Guid: childGUID},
	}
	candidates := snapshotNames(filterBackupSnapshotsByProof(inventory, proofs))
	if !reflect.DeepEqual(candidates, []string{rootSnapshot}) {
		t.Fatalf("proven candidates = %v, want only %s", candidates, rootSnapshot)
	}

	svc := &Service{GZFS: gzfsClient}
	if err := svc.destroyLocalBackupSnapshotsWithProof(
		context.Background(),
		candidates,
		map[string]string{rootSnapshot: "changed-guid"},
	); err == nil {
		t.Fatal("changed local snapshot GUID unexpectedly passed the final proof check")
	}
	if err := exec.Command("zfs", "list", "-H", "-t", "snapshot", rootSnapshot).Run(); err != nil {
		t.Fatalf("root snapshot was destroyed after proof mismatch: %v", err)
	}
	if err := svc.destroyLocalBackupSnapshotsWithProof(context.Background(), candidates, proofs); err != nil {
		t.Fatalf("destroy proven root snapshot: %v", err)
	}
	if err := exec.Command("zfs", "list", "-H", "-t", "snapshot", childSnapshot).Run(); err != nil {
		t.Fatalf("same-name descendant snapshot was destroyed: %v", err)
	}
}

func TestTargetRetentionProofPreservesSameNameNonrecursiveDescendant(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	requireLocalhostBackupSSH(t)

	poolName, _, cleanup := zfstest.Pool(t)
	defer cleanup()
	root := poolName + "/target"
	child := root + "/rogue"
	for _, dataset := range []string{root, child} {
		if output, err := exec.Command("zfs", "create", "-p", dataset).CombinedOutput(); err != nil {
			t.Fatalf("create dataset %s: %v: %s", dataset, err, output)
		}
	}

	snapshotName := "bk_j1_c1_committed"
	rootSnapshot := root + "@" + snapshotName
	childSnapshot := child + "@" + snapshotName
	for _, snapshot := range []string{rootSnapshot, childSnapshot} {
		if output, err := exec.Command("zfs", "snapshot", snapshot).CombinedOutput(); err != nil {
			t.Fatalf("create snapshot %s: %v: %s", snapshot, err, output)
		}
	}

	readGUID := func(snapshot string) string {
		output, err := exec.Command(
			"zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", snapshot,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("read snapshot %s: %v: %s", snapshot, err, output)
		}
		guid, err := parseExactSnapshotGUID(string(output), snapshot)
		if err != nil {
			t.Fatalf("parse snapshot %s: %v", snapshot, err)
		}
		return guid
	}
	rootGUID := readGUID(rootSnapshot)
	childGUID := readGUID(childSnapshot)

	proofs := map[string]string{rootSnapshot: rootGUID}
	inventory := []SnapshotInfo{
		{Name: rootSnapshot, Guid: rootGUID},
		{Name: childSnapshot, Guid: childGUID},
	}
	candidates := snapshotNames(filterBackupSnapshotsByProof(inventory, proofs))
	if !reflect.DeepEqual(candidates, []string{rootSnapshot}) {
		t.Fatalf("proven target candidates = %v, want only %s", candidates, rootSnapshot)
	}

	target := &clusterModels.BackupTarget{SSHHost: "root@localhost", BackupRoot: root}
	if err := (&Service{}).destroyTargetBackupSnapshotsWithProof(
		context.Background(),
		target,
		candidates,
		proofs,
	); err != nil {
		t.Fatalf("destroy proven target root snapshot: %v", err)
	}
	if err := exec.Command("zfs", "list", "-H", "-t", "snapshot", childSnapshot).Run(); err != nil {
		t.Fatalf("same-name target descendant snapshot was destroyed: %v", err)
	}
}
