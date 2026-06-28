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

func TestLatestCommonBackupSnapshotNameFallback(t *testing.T) {
	prefix := "bk_jx"
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "", "2026-06-24T02:00:00Z"),
		mkSnap("p/d@bk_jx_b", "", "2026-06-25T02:00:00Z"),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_a", "", "2026-06-24T02:00:00Z"),
	}
	base, ok := latestCommonBackupSnapshot(local, remote, prefix)
	if !ok || base.ShortName != "@bk_jx_a" {
		t.Fatalf("expected common base @bk_jx_a, ok=%v base=%q", ok, base.ShortName)
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

func TestForeignTargetSnapshots(t *testing.T) {
	prefix := "bk_jx"
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "1", ""),
		mkSnap("p/d@bk_jx_b", "2", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_b", "2", ""),
		mkSnap("t/d@2026-06-26", "999", ""),
		mkSnap("t/d@manual-thing", "888", ""),
	}
	got := foreignTargetSnapshots(local, remote, prefix)
	sort.Strings(got)
	want := []string{"t/d@2026-06-26", "t/d@manual-thing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foreign mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestForeignTargetSnapshotsNeverTouchesBackupOrSourcePresent(t *testing.T) {
	prefix := "bk_jx"
	local := []SnapshotInfo{
		mkSnap("p/d@bk_jx_a", "1", ""),
		mkSnap("p/d@shared", "5", ""),
	}
	remote := []SnapshotInfo{
		mkSnap("t/d@bk_jx_a", "1", ""),
		mkSnap("t/d@shared", "5", ""),
	}
	got := foreignTargetSnapshots(local, remote, prefix)
	if len(got) != 0 {
		t.Fatalf("expected no foreign snapshots, got %v", got)
	}
}

func TestGenerationDatasetToken(t *testing.T) {
	if v, ok := generationDatasetToken("p/x/active_gen-zz"); !ok || v != 35*36+35 {
		t.Fatalf("zz token: ok=%v v=%d", ok, v)
	}
	if v, ok := generationDatasetToken("p/x/active_gen-100"); !ok || v != 1296 {
		t.Fatalf("100 token: ok=%v v=%d", ok, v)
	}
	if v, ok := generationDatasetToken("p/x/active_gen-zz-2"); !ok || v != 1295 {
		t.Fatalf("retry token: ok=%v v=%d", ok, v)
	}
	if _, ok := generationDatasetToken("p/x/active"); ok {
		t.Fatal("non-generation dataset should not parse")
	}
}

func TestStaleBackupGenerationsKeepNewestChronological(t *testing.T) {
	active := "p/x/active"
	lineage := []string{
		"p/x/active",
		"p/x/active_gen-zz",
		"p/x/active_gen-100",
	}
	stale := staleBackupGenerationDatasets(active, lineage, 1)
	if !reflect.DeepEqual(stale, []string{"p/x/active_gen-zz"}) {
		t.Fatalf("expected to destroy the older (zz) generation, got %v", stale)
	}
}

func TestStaleBackupGenerationsKeepN(t *testing.T) {
	active := "p/x/active"
	lineage := []string{
		"p/x/active",
		"p/x/active_gen-1",
		"p/x/active_gen-2",
		"p/x/active_gen-3",
		"p/x/active_gen-4",
	}
	stale := staleBackupGenerationDatasets(active, lineage, 2)
	sort.Strings(stale)
	want := []string{"p/x/active_gen-1", "p/x/active_gen-2"}
	if !reflect.DeepEqual(stale, want) {
		t.Fatalf("keepN mismatch: got=%v want=%v", stale, want)
	}
}

func TestStaleBackupGenerationsNoopWhenWithinKeep(t *testing.T) {
	active := "p/x/active"
	lineage := []string{"p/x/active", "p/x/active_gen-1", "p/x/active_gen-2"}
	if stale := staleBackupGenerationDatasets(active, lineage, 2); len(stale) != 0 {
		t.Fatalf("expected no-op, got %v", stale)
	}
}

func TestStaleBackupGenerationsNeverTouchesActiveOrUnrelated(t *testing.T) {
	active := "p/x/active"
	lineage := []string{
		"p/x/active",
		"p/x/active_gen-1",
		"p/x/active_gen-2",
		"p/x/active_gen-3",
		"p/x/somethingelse",
	}
	stale := staleBackupGenerationDatasets(active, lineage, 1)
	for _, ds := range stale {
		if ds == active || ds == "p/x/somethingelse" {
			t.Fatalf("must never destroy active or unrelated datasets, got %v", stale)
		}
		if !strings.HasPrefix(ds, active+"_gen-") {
			t.Fatalf("destroyed a non-generation dataset: %s", ds)
		}
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

func TestTrimTargetBackupGenerationsKeepNewestViaSSH(t *testing.T) {
	h := newFakeSSHHarness(t)
	parent := "tank/backups/jails/10/j-x"
	active := parent + "/active"

	h.SetScenario(fakeSSHScenario{
		Responses: map[string][]fakeSSHResponse{
			"zfs list -t filesystem -d 1 -Hp -o name " + parent: {
				{Stdout: strings.Join([]string{
					active,
					parent + "/active_gen-1",
					parent + "/active_gen-2",
					parent + "/active_gen-3",
					parent + "/active_gen-4",
				}, "\n") + "\n", ExitCode: 0},
			},
			"zfs destroy -r " + parent + "/active_gen-1": {{ExitCode: 0}},
			"zfs destroy -r " + parent + "/active_gen-2": {{ExitCode: 0}},
		},
	})

	s := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "tank/backups"}

	destroyed, err := s.trimTargetBackupGenerations(context.Background(), target, active, 2)
	if err != nil {
		t.Fatalf("trimTargetBackupGenerations: %v", err)
	}
	if destroyed != 2 {
		t.Fatalf("expected 2 generations destroyed, got %d", destroyed)
	}

	assertFakeSSHCallSequence(t, h.Calls(), []string{
		"zfs list -t filesystem -d 1 -Hp -o name " + parent,
		"zfs destroy -r " + parent + "/active_gen-2",
		"zfs destroy -r " + parent + "/active_gen-1",
	})
}

func TestTrimTargetBackupGenerationsNoopWhenWithinKeepViaSSH(t *testing.T) {
	h := newFakeSSHHarness(t)
	parent := "tank/backups/jails/10/j-x"
	active := parent + "/active"

	h.SetScenario(fakeSSHScenario{
		Responses: map[string][]fakeSSHResponse{
			"zfs list -t filesystem -d 1 -Hp -o name " + parent: {
				{Stdout: strings.Join([]string{
					active,
					parent + "/active_gen-1",
					parent + "/active_gen-2",
				}, "\n") + "\n", ExitCode: 0},
			},
		},
	})

	s := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", BackupRoot: "tank/backups"}

	destroyed, err := s.trimTargetBackupGenerations(context.Background(), target, active, 2)
	if err != nil {
		t.Fatalf("trimTargetBackupGenerations: %v", err)
	}
	if destroyed != 0 {
		t.Fatalf("expected 0 generations destroyed, got %d", destroyed)
	}

	assertFakeSSHCallSequence(t, h.Calls(), []string{
		"zfs list -t filesystem -d 1 -Hp -o name " + parent,
	})
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
