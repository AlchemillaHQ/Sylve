// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestDatasetWithinRoot(t *testing.T) {
	if !datasetWithinRoot("tank/backups", "tank/backups/jails/42") {
		t.Fatal("child dataset should be within root")
	}
	if !datasetWithinRoot("tank/backups", "tank/backups") {
		t.Fatal("dataset equals root should be within")
	}
	if datasetWithinRoot("tank/backups", "tank/other/42") {
		t.Fatal("unrelated dataset should not be within root")
	}
	if datasetWithinRoot("", "tank/backups") {
		t.Fatal("empty root should return false")
	}
	if datasetWithinRoot("tank/backups", "") {
		t.Fatal("empty dataset should return false")
	}
}

func TestRelativeDatasetSuffix(t *testing.T) {
	if s := relativeDatasetSuffix("tank/backups", "tank/backups/jails/42"); s != "jails/42" {
		t.Fatalf("expected jails/42, got %q", s)
	}
	if s := relativeDatasetSuffix("tank/backups", "tank/backups"); s != "" {
		t.Fatalf("expected empty for root, got %q", s)
	}
}

func TestNormalizeDatasetPath(t *testing.T) {
	if p := normalizeDatasetPath("tank/backups/"); p != "tank/backups" {
		t.Fatalf("trailing slash stripped: %q", p)
	}
	if p := normalizeDatasetPath("  tank/backups  "); p != "tank/backups" {
		t.Fatalf("whitespace trimmed: %q", p)
	}
}

func TestIsValidRestoreDestinationDataset(t *testing.T) {
	if !isValidRestoreDestinationDataset("tank/data") {
		t.Fatal("valid pool/path should pass")
	}
	if isValidRestoreDestinationDataset("tank") {
		t.Fatal("pool-only (no slash) should fail")
	}
	if isValidRestoreDestinationDataset("") {
		t.Fatal("empty should fail")
	}
	if isValidRestoreDestinationDataset("tank/data@snap") {
		t.Fatal("@ in path should fail (not a dataset)")
	}
}

func TestNormalizeRestoreDestinationDataset(t *testing.T) {
	if p := normalizeRestoreDestinationDataset("tank/jails/42/"); p != "tank/jails/42" {
		t.Fatalf("expected tank/jails/42, got %q", p)
	}
	if p := normalizeRestoreDestinationDataset("/tank/jails/42"); p != "tank/jails/42" {
		t.Fatalf("leading slash stripped: %q", p)
	}
}

func TestClassifyDatasetLineage(t *testing.T) {
	lineage, outOfBand, base := classifyDatasetLineage("jails/42")
	if lineage != "active" || outOfBand {
		t.Fatalf("plain suffix: lineage=%q outOfBand=%v", lineage, outOfBand)
	}
	if base != "jails/42" {
		t.Fatalf("base: %q", base)
	}

	lineage, outOfBand, base = classifyDatasetLineage("jails/42_gen-5")
	if lineage != "rotated" || !outOfBand {
		t.Fatalf("rotated suffix: lineage=%q outOfBand=%v", lineage, outOfBand)
	}
	if base != "jails/42" {
		t.Fatalf("rotated base: %q", base)
	}

	lineage, outOfBand, base = classifyDatasetLineage("jails/42.pre_failover")
	if lineage != "other" || !outOfBand {
		t.Fatalf("other suffix: lineage=%q outOfBand=%v", lineage, outOfBand)
	}

	lineage, outOfBand, base = classifyDatasetLineage("")
	if lineage != "active" || outOfBand {
		t.Fatal("empty suffix should be active")
	}
}

func TestDatasetLineageRank(t *testing.T) {
	if r := datasetLineageRank("active"); r != 0 {
		t.Fatalf("active rank: %d", r)
	}
	if r := datasetLineageRank("rotated"); r != 1 {
		t.Fatalf("rotated rank: %d", r)
	}
	if r := datasetLineageRank("other"); r != 2 {
		t.Fatalf("other rank: %d", r)
	}
	if r := datasetLineageRank("unknown"); r != 2 {
		t.Fatalf("unknown rank should be 2: %d", r)
	}
}

func TestInferRestoreDatasetKind(t *testing.T) {
	kind, id := inferRestoreDatasetKind("jails/42")
	if kind != clusterModels.BackupJobModeJail || id != 42 {
		t.Fatalf("jail: kind=%q id=%d", kind, id)
	}

	kind, id = inferRestoreDatasetKind("virtual-machines/7")
	if kind != clusterModels.BackupJobModeVM || id != 7 {
		t.Fatalf("vm: kind=%q id=%d", kind, id)
	}

	kind, id = inferRestoreDatasetKind("jails/42_data")
	if kind != clusterModels.BackupJobModeJail || id != 42 {
		t.Fatalf("jail with suffix: kind=%q id=%d", kind, id)
	}

	kind, id = inferRestoreDatasetKind("tank/data/db")
	if kind != clusterModels.BackupJobModeDataset {
		t.Fatalf("generic: expected dataset, got %q", kind)
	}

	kind, id = inferRestoreDatasetKind("")
	if kind != clusterModels.BackupJobModeDataset {
		t.Fatalf("empty: expected dataset, got %q", kind)
	}
}

func TestExtractDatasetGuestID(t *testing.T) {
	if id := extractDatasetGuestID("42"); id != 42 {
		t.Fatalf("plain: %d", id)
	}
	if id := extractDatasetGuestID("42_data"); id != 42 {
		t.Fatalf("with underscore: %d", id)
	}
	if id := extractDatasetGuestID("7.disk0"); id != 7 {
		t.Fatalf("with dot: %d", id)
	}
	if id := extractDatasetGuestID(""); id != 0 {
		t.Fatalf("empty: %d", id)
	}
	if id := extractDatasetGuestID("abc"); id != 0 {
		t.Fatalf("non-numeric: %d", id)
	}
}

func TestRestoreLockIDFromDestination(t *testing.T) {
	id1 := restoreLockIDFromDestination("tank/jails/42")
	if id1 == 0 {
		t.Fatal("should return non-zero")
	}
	id2 := restoreLockIDFromDestination("tank/jails/42")
	if id1 != id2 {
		t.Fatal("same input should return same lock ID")
	}
	id3 := restoreLockIDFromDestination("tank/virtual-machines/7")
	if id1 == id3 {
		t.Fatal("different destination should return different lock ID")
	}
}

func TestVMDatasetRoot(t *testing.T) {
	if r := vmDatasetRoot("tank/virtual-machines/7"); r != "tank/virtual-machines/7" {
		t.Fatalf("simple vm: %q", r)
	}
	if r := vmDatasetRoot("tank/virtual-machines/7_disk0"); r != "tank/virtual-machines/7_disk0" {
		t.Fatalf("vm with disk suffix: %q", r)
	}
	if r := vmDatasetRoot("tank/some/data"); r != "tank/some/data" {
		t.Fatalf("non-vm: %q", r)
	}
}

func TestVMMetadataCandidateDatasets(t *testing.T) {
	candidates := vmMetadataCandidateDatasets("tank/virtual-machines/7")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (root == dataset), got %d", len(candidates))
	}
	if candidates[0] != "tank/virtual-machines/7" {
		t.Fatalf("expected full dataset: %q", candidates[0])
	}

	candidates = vmMetadataCandidateDatasets("tank/sylve/virtual-machines/7/zvol")
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates (zvol + root), got %d: %v", len(candidates), candidates)
	}
	if candidates[0] != "tank/sylve/virtual-machines/7/zvol" {
		t.Fatalf("first: %q", candidates[0])
	}
	if candidates[1] != "tank/sylve/virtual-machines/7" {
		t.Fatalf("second (root): %q", candidates[1])
	}
}

func TestVMDestinationAnchor(t *testing.T) {
	if a := vmDestinationAnchor("tank/sylve/virtual-machines/7"); a != "tank/sylve" {
		t.Fatalf("anchor should be tank/sylve, got %q", a)
	}
	if a := vmDestinationAnchor("tank/virtual-machines/7"); a == "" {
		t.Fatal("anchor for root pool should not be empty")
	} else {
		// virtual-machines at index 0 means no anchor prefix
		t.Logf("anchor: %q", a)
	}
}

func TestCanonicalVMDatasetRoot(t *testing.T) {
	if r := canonicalVMDatasetRoot("tank/virtual-machines/7", 7); r != "tank/virtual-machines/7" {
		t.Fatalf("canonical: %q", r)
	}
	if r := canonicalVMDatasetRoot("", 7); r != "" {
		t.Fatalf("empty: %q", r)
	}
}

func TestParseSnapshotInfoOutput(t *testing.T) {
	output := "pool/data@bk_1\t1749000000\t100M\t50M\npool/data@bk_2\t1749100000\t200M\t100M\n"
	snaps := parseSnapshotInfoOutput(output)
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].ShortName != "@bk_1" {
		t.Fatalf("short name: %q", snaps[0].ShortName)
	}
	if snaps[0].Dataset != "pool/data" {
		t.Fatalf("dataset: %q", snaps[0].Dataset)
	}

	if got := parseSnapshotInfoOutput(""); len(got) != 0 {
		t.Fatal("empty output should return empty")
	}
}

func TestCollapseSnapshotsByShortName(t *testing.T) {
	snaps := []SnapshotInfo{
		{Name: "pool/a@bk_1", ShortName: "@bk_1", Creation: "100", Dataset: "pool/a"},
		{Name: "pool/b@bk_1", ShortName: "@bk_1", Creation: "200", Dataset: "pool/b"},
		{Name: "pool/a@bk_2", ShortName: "@bk_2", Creation: "300", Dataset: "pool/a"},
	}
	collapsed := collapseSnapshotsByShortName(snaps)
	if len(collapsed) != 2 {
		t.Fatalf("expected 2 collapsed, got %d", len(collapsed))
	}
	if got := collapseSnapshotsByShortName(nil); len(got) != 0 {
		t.Fatal("nil should return empty")
	}
}

func TestFilterBackupSnapshots(t *testing.T) {
	snaps := []SnapshotInfo{
		{Name: "pool/a@bk_1", ShortName: "@bk_1"},
		{Name: "pool/a@ha_2025", ShortName: "@ha_2025"},
		{Name: "pool/a@manual", ShortName: "@manual"},
	}
	filtered := filterBackupSnapshots(snaps)
	if len(filtered) != 1 || filtered[0].ShortName != "@bk_1" {
		t.Fatalf("should only keep bk_ prefixed, got %d", len(filtered))
	}
}

func TestSnapshotShortName(t *testing.T) {
	if n := snapshotShortName(SnapshotInfo{Name: "pool@snap", ShortName: "@snap"}); n != "@snap" {
		t.Fatalf("explicit short name: %q", n)
	}
	if n := snapshotShortName(SnapshotInfo{Name: "pool@snap"}); n != "@snap" {
		t.Fatalf("derived: %q", n)
	}
	if n := snapshotShortName(SnapshotInfo{Name: "pool"}); n != "pool" {
		t.Fatalf("no @ sign: %q", n)
	}
	if n := snapshotShortName(SnapshotInfo{Name: ""}); n != "" {
		t.Fatalf("empty: %q", n)
	}
}

func TestIsBackupSnapshotShortName(t *testing.T) {
	if !isBackupSnapshotShortName("bk_2025-01-01") {
		t.Fatal("bk_ prefix should be backup")
	}
	if isBackupSnapshotShortName("ha_2025-01-01") {
		t.Fatal("ha_ prefix should not be backup")
	}
	if isBackupSnapshotShortName("") {
		t.Fatal("empty should not be backup")
	}
}

func TestFilterSnapshotsForBackupJob(t *testing.T) {
	snaps := []SnapshotInfo{
		{Name: "pool/a@bk_ja_2025-01-01", ShortName: "@bk_ja_2025-01-01"},
		{Name: "pool/a@bk_jk_2025-01-01", ShortName: "@bk_jk_2025-01-01"},
		{Name: "pool/a@bk_ja_2025-01-02", ShortName: "@bk_ja_2025-01-02"},
	}
	filtered := filterSnapshotsForBackupJob(snaps, 10)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 for job 10, got %d", len(filtered))
	}
	filtered = filterSnapshotsForBackupJob(nil, 10)
	if len(filtered) != 0 {
		t.Fatal("nil should return empty")
	}
}

func TestDatasetDepth(t *testing.T) {
	if d := datasetDepth("pool"); d != 1 {
		t.Fatalf("pool: %d", d)
	}
	if d := datasetDepth("pool/data/sub"); d != 3 {
		t.Fatalf("nested: %d", d)
	}
}

func TestRemoteDatasetForJob(t *testing.T) {
	target := &clusterModels.BackupTarget{BackupRoot: "tank/backups", SSHHost: "root@localhost", SSHPort: 22}
	job := &clusterModels.BackupJob{
		ID: 10, Name: "test-job", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "zroot/data/db", TargetID: 1,
	}
	job.Target = *target

	remote := remoteDatasetForJob(job)
	if remote == "" {
		t.Fatal("remote dataset should not be empty")
	}
}
