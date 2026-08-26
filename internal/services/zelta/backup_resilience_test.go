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
	"reflect"
	"sort"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func mkSnap(name, guid, creation string) SnapshotInfo {
	short := ""
	ds := ""
	if i := strings.LastIndex(name, "@"); i >= 0 {
		short = name[i:]
		ds = name[:i]
	}
	return SnapshotInfo{Name: name, ShortName: short, Dataset: ds, Guid: guid, Creation: creation}
}

func TestParseSnapshotInfoOutputWithGuid(t *testing.T) {
	out := "pool/ds@bk_j1_a\t1700000000\t1024\t2048\t111\n" +
		"pool/ds@bk_j1_b\t1700000100\t1024\t2048\t222"
	snaps := parseSnapshotInfoOutput(out)
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].Guid != "111" || snaps[1].Guid != "222" {
		t.Fatalf("guid not parsed: %q %q", snaps[0].Guid, snaps[1].Guid)
	}
	if snaps[0].ShortName != "@bk_j1_a" || snaps[0].Dataset != "pool/ds" {
		t.Fatalf("short/dataset wrong: %q %q", snaps[0].ShortName, snaps[0].Dataset)
	}
	if !strings.HasPrefix(snaps[0].Creation, "2023-11-14") {
		t.Fatalf("creation not converted from epoch: %q", snaps[0].Creation)
	}
}

func TestParseSnapshotInfoOutputLegacyFourColumns(t *testing.T) {
	out := "pool/ds@bk_1\t1700000000\t1024\t2048"
	snaps := parseSnapshotInfoOutput(out)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Guid != "" {
		t.Fatalf("expected empty guid, got %q", snaps[0].Guid)
	}
	if snaps[0].ShortName != "@bk_1" {
		t.Fatalf("short name: %q", snaps[0].ShortName)
	}
}

func TestSnapshotCreationTime(t *testing.T) {
	if _, ok := snapshotCreationTime(SnapshotInfo{Creation: ""}); ok {
		t.Fatal("empty creation should not parse")
	}
	if _, ok := snapshotCreationTime(SnapshotInfo{Creation: "2026-06-26T02:00:00Z"}); !ok {
		t.Fatal("RFC3339 should parse")
	}
	if tm, ok := snapshotCreationTime(SnapshotInfo{Creation: "1700000000"}); !ok || tm.IsZero() {
		t.Fatal("epoch fallback should parse")
	}
}

func TestLatestCommonBackupSnapshotByGUID(t *testing.T) {
	prefix := "bk_jx"
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "1", "2026-06-24T02:00:00Z"),
		mkSnap("p/d@bk_jx_b", "2", "2026-06-25T02:00:00Z"),
		mkSnap("p/d@bk_jx_c", "3", "2026-06-26T02:00:00Z"),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_b", "2", "2026-06-25T02:00:00Z"),
		mkSnap("t/d@2026-06-26", "999", "2026-06-26T06:06:00Z"),
	}
	base, ok := latestCommonBackupSnapshot(local, remote, prefix)
	if !ok {
		t.Fatal("expected a common base")
	}
	if base.Guid != "2" {
		t.Fatalf("expected common base guid 2 (b), got %q", base.Guid)
	}
}

func TestLatestCommonBackupSnapshotRequiresGUIDIdentity(t *testing.T) {
	prefix := "bk_jx"
	tests := []struct {
		name   string
		local  SnapshotInfo
		remote SnapshotInfo
	}{
		{
			name:   "same name different GUID",
			local:  mkSnap("p/d@bk_jx_a", "local-guid", ""),
			remote: mkSnap("t/d@bk_jx_a", "remote-guid", ""),
		},
		{
			name:   "both GUIDs missing",
			local:  mkSnap("p/d@bk_jx_a", "", ""),
			remote: mkSnap("t/d@bk_jx_a", "", ""),
		},
		{
			name:   "remote GUID missing",
			local:  mkSnap("p/d@bk_jx_a", "local-guid", ""),
			remote: mkSnap("t/d@bk_jx_a", "", ""),
		},
		{
			name:   "local GUID missing",
			local:  mkSnap("p/d@bk_jx_a", "", ""),
			remote: mkSnap("t/d@bk_jx_a", "remote-guid", ""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := latestCommonBackupSnapshot(
				[]SnapshotInfo{tt.local},
				[]SnapshotInfo{tt.remote},
				prefix,
			); ok {
				t.Fatal("snapshot name alone must not establish a common base")
			}
		})
	}
}

func TestLatestCommonBackupSnapshotNoCommon(t *testing.T) {
	prefix := "bk_jx"
	local := []SnapshotInfo{mkSnap("p/d@bk_jx_a", "1", "2026-06-24T02:00:00Z")}
	remote := []SnapshotInfo{mkSnap("t/d@2026-06-26", "999", "2026-06-26T06:06:00Z")}
	if _, ok := latestCommonBackupSnapshot(local, remote, prefix); ok {
		t.Fatal("expected no common base")
	}
}

func TestLatestCommonBackupSnapshotEmpty(t *testing.T) {
	if _, ok := latestCommonBackupSnapshot(nil, nil, "bk_jx"); ok {
		t.Fatal("empty inputs should yield no common base")
	}
}

func TestLatestCommonBackupSnapshotsByDatasetAndJobPrefix(t *testing.T) {
	local := []SnapshotInfo{
		mkSnap("src/root@bk_ja_1", "r1", ""),
		mkSnap("src/root/child@bk_ja_1", "c1", ""),
		mkSnap("src/root@bk_jb_1", "rb1", ""),
		mkSnap("src/root/child@bk_jb_1", "cb1", ""),
		mkSnap("src/root@bk_ja_2", "r2", ""),
		mkSnap("src/root/child@bk_ja_2", "c2", ""),
		mkSnap("src/root@bk_ja_3", "r3", ""),
		mkSnap("src/root/child@bk_ja_3", "c3", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("dst/root@bk_ja_1", "r1", ""),
		mkSnap("dst/root/child@bk_ja_1", "c1", ""),
		mkSnap("dst/root@bk_jb_1", "rb1", ""),
		mkSnap("dst/root/child@bk_jb_1", "cb1", ""),
		mkSnap("dst/root@bk_ja_2", "r2", ""),
		// A reused short name with a different GUID is not a common base.
		mkSnap("dst/root/child@bk_ja_2", "different-c2", ""),
	}

	bases := latestCommonBackupSnapshotsByDataset(
		local,
		remote,
		"src/root",
		"dst/root",
		"bk_ja",
	)
	if got := bases["src/root"].Name; got != "src/root@bk_ja_2" {
		t.Fatalf("root base = %q, want bk_ja_2", got)
	}
	if got := bases["src/root/child"].Name; got != "src/root/child@bk_ja_1" {
		t.Fatalf("child base = %q, want bk_ja_1", got)
	}
	for _, base := range bases {
		if strings.Contains(base.Name, "@bk_jb_") {
			t.Fatalf("other job prefix selected as a base: %s", base.Name)
		}
	}
}

func TestLatestCommonBackupSnapshotsByDatasetRequiresGUIDs(t *testing.T) {
	local := []SnapshotInfo{
		mkSnap("src/root/local-missing@bk_ja_1", "", ""),
		mkSnap("src/root/remote-missing@bk_ja_1", "local-guid", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("dst/root/local-missing@bk_ja_1", "remote-guid", ""),
		mkSnap("dst/root/remote-missing@bk_ja_1", "", ""),
	}

	bases := latestCommonBackupSnapshotsByDataset(local, remote, "src/root", "dst/root", "bk_ja")
	if len(bases) != 0 {
		t.Fatalf("GUID-less snapshots selected as common bases: %+v", bases)
	}
}

func TestForeignTargetSnapshots(t *testing.T) {
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "1", ""),
		mkSnap("p/d@bk_jx_b", "2", ""),
		mkSnap("p/d@shared", "5", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_b", "2", ""),
		mkSnap("t/d@bk_jx_user", "777", ""),
		mkSnap("t/d@shared", "different-guid", ""),
		mkSnap("t/d@2026-06-26", "999", ""),
		mkSnap("t/d@manual-thing", "888", ""),
	}
	got := foreignTargetSnapshots(local, remote, "p/d", "t/d", nil)
	sort.Strings(got)
	want := []string{"t/d@2026-06-26", "t/d@bk_jx_user", "t/d@manual-thing", "t/d@shared"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foreign mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestForeignTargetSnapshotsNeverTouchesBackupOrSourcePresent(t *testing.T) {
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "1", ""),
		mkSnap("p/d@shared", "5", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_a", "1", ""),
		mkSnap("t/d@shared", "5", ""),
	}
	got := foreignTargetSnapshots(local, remote, "p/d", "t/d", nil)
	if len(got) != 0 {
		t.Fatalf("expected no foreign snapshots, got %v", got)
	}
}

func TestForeignTargetSnapshotsRequireMatchingDatasetSuffix(t *testing.T) {
	local := []SnapshotInfo{
		mkSnap("p/d/one@bk_jx_a", "guid-one", ""),
		mkSnap("p/d/two@bk_jx_a", "guid-two", ""),
	}
	remote := []SnapshotInfo{
		// The GUIDs exist on the source, but only at the opposite suffixes.
		mkSnap("t/d/one@bk_jx_a", "guid-two", ""),
		mkSnap("t/d/two@bk_jx_a", "guid-one", ""),
	}

	got := foreignTargetSnapshots(local, remote, "p/d", "t/d", nil)
	want := []string{"t/d/one@bk_jx_a", "t/d/two@bk_jx_a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suffix-swapped snapshots = %v, want %v", got, want)
	}
}

func TestForeignTargetSnapshotsAcceptOnlyExactProvenTargetSnapshot(t *testing.T) {
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_c1_old", "root-guid", ""),
		mkSnap("t/d/rogue@bk_jx_c1_old", "rogue-guid", ""),
	}
	proofs := map[string]string{
		"t/d@bk_jx_c1_old": "root-guid",
	}

	got := foreignTargetSnapshots(nil, remote, "p/d", "t/d", proofs)
	want := []string{"t/d/rogue@bk_jx_c1_old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("target proof classification = %v, want %v", got, want)
	}
}

func TestFilterToleratedLegacyTargetSnapshotsRequiresCanonicalJobScope(t *testing.T) {
	job := &clusterModels.BackupJob{
		ID: 10,
		Target: clusterModels.BackupTarget{
			BackupRoot: "backup/root",
		},
	}
	const (
		sourceRoot = "src/root"
		targetRoot = "backup/root/jobs/a/active"
	)
	scopes := []backupScope{{sourceDataset: sourceRoot, destSuffix: "jobs/a/active"}}
	foreign := []string{
		targetRoot + "@bk_ja_legacy",
		targetRoot + "/child@bk_ja_legacy",
		targetRoot + "@bk_jb_legacy",
		targetRoot + "@bk_ja_c1_current",
		targetRoot + "@bk_ja_extra_c1_malformed",
		"backup/root/other@bk_ja_legacy",
	}

	got := filterToleratedLegacyTargetSnapshots(job, sourceRoot, targetRoot, scopes, foreign)
	want := []string{
		targetRoot + "@bk_jb_legacy",
		targetRoot + "@bk_ja_c1_current",
		targetRoot + "@bk_ja_extra_c1_malformed",
		"backup/root/other@bk_ja_legacy",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered foreign snapshots = %v, want %v", got, want)
	}

	tests := []struct {
		name       string
		sourceRoot string
		targetRoot string
		scopes     []backupScope
	}{
		{name: "wrong source", sourceRoot: "src/other", targetRoot: targetRoot, scopes: scopes},
		{name: "wrong target", sourceRoot: sourceRoot, targetRoot: "backup/root/jobs/b/active", scopes: scopes},
		{name: "missing scope", sourceRoot: sourceRoot, targetRoot: targetRoot},
		{name: "outside backup root", sourceRoot: sourceRoot, targetRoot: "other/root/jobs/a/active", scopes: scopes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterToleratedLegacyTargetSnapshots(job, tt.sourceRoot, tt.targetRoot, tt.scopes, foreign)
			if !reflect.DeepEqual(got, foreign) {
				t.Fatalf("non-canonical scope filtered snapshots: got %v, want %v", got, foreign)
			}
		})
	}
}

func TestBuildLocalRetentionKeepNPerDataset(t *testing.T) {
	prefix := "bk_jx"
	snaps := []SnapshotInfo{
		mkSnap("p/d@bk_jx_1", "1", ""),
		mkSnap("p/d@bk_jx_2", "2", ""),
		mkSnap("p/d@bk_jx_3", "3", ""),
		mkSnap("p/d@bk_jx_4", "4", ""),
		mkSnap("p/d@bk_jx_5", "5", ""),
	}
	got := buildLocalRetentionPruneCandidates(snaps, 2, nil, prefix)
	sort.Strings(got)
	want := []string{"p/d@bk_jx_1", "p/d@bk_jx_2", "p/d@bk_jx_3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keepN mismatch: got=%v want=%v", got, want)
	}
}

func TestBuildLocalRetentionIgnoresNonBackupSnapshots(t *testing.T) {
	prefix := "bk_jx"
	snaps := []SnapshotInfo{
		mkSnap("p/d@bk_jx_1", "1", ""),
		mkSnap("p/d@bk_jx_2", "2", ""),
		mkSnap("p/d@2026-06-26", "9", ""),
	}
	got := buildLocalRetentionPruneCandidates(snaps, 1, nil, prefix)
	if !reflect.DeepEqual(got, []string{"p/d@bk_jx_1"}) {
		t.Fatalf("should only consider bk_ snaps: got=%v", got)
	}
}

func TestBuildLocalRetentionProtectsBase(t *testing.T) {
	prefix := "bk_jx"
	snaps := []SnapshotInfo{
		mkSnap("p/d@bk_jx_1", "1", ""),
		mkSnap("p/d@bk_jx_2", "2", ""),
		mkSnap("p/d@bk_jx_3", "3", ""),
		mkSnap("p/d@bk_jx_4", "4", ""),
	}
	protect := map[string]struct{}{"p/d@bk_jx_1": {}}
	got := buildLocalRetentionPruneCandidates(snaps, 2, protect, prefix)
	for _, c := range got {
		if c == "p/d@bk_jx_1" {
			t.Fatalf("protected base must never be pruned, got %v", got)
		}
	}
	if !reflect.DeepEqual(got, []string{"p/d@bk_jx_2"}) {
		t.Fatalf("expected to prune only bk_jx_2, got %v", got)
	}
}

func TestBuildLocalRetentionKeepZeroNoFloorlessWipe(t *testing.T) {
	prefix := "bk_jx"
	snaps := []SnapshotInfo{
		mkSnap("p/d@bk_jx_1", "1", ""),
		mkSnap("p/d@bk_jx_2", "2", ""),
	}
	protect := map[string]struct{}{"p/d@bk_jx_2": {}}
	got := buildLocalRetentionPruneCandidates(snaps, 0, protect, prefix)
	if !reflect.DeepEqual(got, []string{"p/d@bk_jx_1"}) {
		t.Fatalf("keep0 should prune all but protected: got=%v", got)
	}
}

func TestBuildLocalRetentionPerDatasetGrouping(t *testing.T) {
	prefix := "bk_jx"
	snaps := []SnapshotInfo{
		mkSnap("p/a@bk_jx_1", "1", ""),
		mkSnap("p/a@bk_jx_2", "2", ""),
		mkSnap("p/a@bk_jx_3", "3", ""),
		mkSnap("p/b@bk_jx_1", "4", ""),
		mkSnap("p/b@bk_jx_2", "5", ""),
	}
	got := buildLocalRetentionPruneCandidates(snaps, 1, nil, prefix)
	sort.Strings(got)
	want := []string{"p/a@bk_jx_1", "p/a@bk_jx_2", "p/b@bk_jx_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouping mismatch: got=%v want=%v", got, want)
	}
}

func TestBuildTargetRetentionRecursivePerDatasetAndJobPrefix(t *testing.T) {
	prefix := "bk_ja"
	snaps := []SnapshotInfo{
		mkSnap("dst/root@bk_ja_1", "r1", ""),
		mkSnap("dst/root/child@bk_ja_1", "c1", ""),
		mkSnap("dst/root@bk_jb_1", "rb1", ""),
		mkSnap("dst/root/child@bk_jb_1", "cb1", ""),
		mkSnap("dst/root@bk_ja_2", "r2", ""),
		mkSnap("dst/root/child@bk_ja_2", "c2", ""),
		mkSnap("dst/root@bk_ja_3", "r3", ""),
		mkSnap("dst/root/child@bk_ja_3", "c3", ""),
	}
	safe := snapshotCandidateSet([]string{
		"dst/root@bk_ja_1",
		"dst/root@bk_ja_2",
		"dst/root/child@bk_ja_1",
		// The child's second snapshot is its protected incremental base.
	})

	got := buildBKRetentionPruneCandidates(snaps, 1, safe, prefix)
	sort.Strings(got)
	want := []string{
		"dst/root/child@bk_ja_1",
		"dst/root@bk_ja_1",
		"dst/root@bk_ja_2",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recursive target prune mismatch: got=%v want=%v", got, want)
	}
	for _, candidate := range got {
		if strings.Contains(candidate, "@bk_jb_") {
			t.Fatalf("other job prefix selected for pruning: %s", candidate)
		}
	}
}

func TestRemoteActiveDatasetForSuffix(t *testing.T) {
	got := remoteActiveDatasetForSuffix("zdata/backups/host", "zapp/sylve/jails/10/j-x/active")
	want := "zdata/backups/host/zapp/sylve/jails/10/j-x/active"
	if got != want {
		t.Fatalf("path mismatch: got=%q want=%q", got, want)
	}
	if remoteActiveDatasetForSuffix("", "a/b") != "a/b" {
		t.Fatal("empty root should return suffix")
	}
	if remoteActiveDatasetForSuffix("root", "") != "root" {
		t.Fatal("empty suffix should return root")
	}
}

func TestTargetDatasetExistsTolerantOfSSHBanner(t *testing.T) {
	h := newFakeSSHHarness(t)
	ds := "tank/backups/jails/10/j-x/active"
	h.SetScenario(fakeSSHScenario{
		Responses: map[string][]fakeSSHResponse{
			"zfs list -H -o name " + ds: {
				{Stdout: "Warning: Permanently added 'localhost' (ED25519) to the list of known hosts.\n" + ds + "\n", ExitCode: 0},
			},
		},
	})

	s := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "tank/backups"}

	exists, err := s.targetDatasetExists(context.Background(), target, ds)
	if err != nil {
		t.Fatalf("targetDatasetExists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected dataset to be reported present despite SSH banner")
	}
}

func TestTargetDatasetExistsMissing(t *testing.T) {
	h := newFakeSSHHarness(t)
	ds := "tank/backups/nope"
	h.SetScenario(fakeSSHScenario{
		Responses: map[string][]fakeSSHResponse{
			"zfs list -H -o name " + ds: {
				{Stderr: "cannot open '" + ds + "': dataset does not exist\n", ExitCode: 1},
			},
		},
	})

	s := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "tank/backups"}

	exists, err := s.targetDatasetExists(context.Background(), target, ds)
	if err != nil {
		t.Fatalf("expected nil error for a missing dataset, got: %v", err)
	}
	if exists {
		t.Fatalf("expected dataset to be reported missing")
	}
}
