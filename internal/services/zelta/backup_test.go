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
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestClassifyBackupOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   backupOutputKind
	}{
		{"up to date", "source and target: up-to-date", backupOutputUpToDate},
		{"up to date alt", "No datasets to backup, source and target are up-to-date", backupOutputUpToDate},
		{"no source", "error: No source: tank/data does not exist", backupOutputBlockedNoSource},
		{"no source snapshot", "error: No source snapshot found", backupOutputBlockedNoSourceSnapshot},
		{"source snapshot creation failed", "source_snapshot_creation_failed: tank/data@bk_j1_x", backupOutputBlockedSourceSnapshot},
		{"source snapshot verification failed", "source_snapshot_verification_failed: tank/data@bk_j1_x", backupOutputBlockedSourceSnapshot},
		{"target diverged", "error: Target has diverged from source", backupOutputBlockedTargetDiverged},
		{"target diverged alt", "Target diverged after last sync", backupOutputBlockedTargetDiverged},
		{"target local writes", "error: Target has local writes preventing sync", backupOutputBlockedTargetLocalWrites},
		{"no snapshot diverged", "No snapshot; target diverged", backupOutputBlockedNoSnapshotDiverged},
		{"no common snapshot", "No common snapshot (diverged)", backupOutputBlockedNoCommonSnapshot},
		{"unknown", "something completely unexpected happened", backupOutputUnknown},
		{"empty", "", backupOutputUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyBackupOutput(tt.output); got != tt.want {
				t.Fatalf("classifyBackupOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestBackupOutputKindErrorCode(t *testing.T) {
	tests := []struct {
		kind backupOutputKind
		want string
	}{
		{backupOutputBlockedNoSource, backupErrorSourceMissing},
		{backupOutputBlockedNoSourceSnapshot, backupErrorSourceSnapshotMissing},
		{backupOutputBlockedSourceSnapshot, backupErrorSourceSnapshotFailed},
		{backupOutputBlockedTargetLocalWrites, backupErrorTargetLocalWrites},
		{backupOutputBlockedTargetDiverged, backupErrorTargetDiverged},
		{backupOutputBlockedNoSnapshotDiverged, backupErrorTargetDiverged},
		{backupOutputBlockedNoCommonSnapshot, backupErrorTargetDiverged},
		{backupOutputUpToDate, ""},
		{backupOutputUnknown, ""},
	}
	for _, tt := range tests {
		if got := tt.kind.errorCode(); got != tt.want {
			t.Fatalf("%q.errorCode() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestSnapshotCandidateSet(t *testing.T) {
	s := snapshotCandidateSet([]string{"pool/a@snap1", "pool/a@snap2", "pool/b@snap1"})
	if len(s) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(s), s)
	}
	if _, ok := s["pool/a@snap1"]; !ok {
		t.Fatal("should contain pool/a@snap1")
	}
	if got := snapshotCandidateSet(nil); len(got) != 0 {
		t.Fatal("nil should return empty")
	}
}

func TestIsJobAlreadyRunningErr(t *testing.T) {
	if isJobAlreadyRunningErr(nil) {
		t.Fatal("nil should not be already_running")
	}
	if isJobAlreadyRunningErr(errReplicationPolicyTransitionAlreadyRunning) {
		t.Log("transition error contains already_running, detected correctly")
	} else {
		t.Fatal("transition error should be detected as already_running")
	}
}

func TestBuildBKRetentionPruneCandidates(t *testing.T) {
	snapshots := []SnapshotInfo{
		{Name: "pool/ds@bk_2025-01-01", ShortName: "@bk_2025-01-01", Dataset: "pool/ds"},
		{Name: "pool/ds@bk_2025-01-02", ShortName: "@bk_2025-01-02", Dataset: "pool/ds"},
		{Name: "pool/ds@bk_2025-01-03", ShortName: "@bk_2025-01-03", Dataset: "pool/ds"},
	}
	candidates := buildBKRetentionPruneCandidates(snapshots, 1, nil, "bk")
	if len(candidates) != 2 {
		t.Fatalf("keep 1 of 3 → 2 prune candidates, got %d: %v", len(candidates), candidates)
	}

	candidates = buildBKRetentionPruneCandidates(snapshots, 5, nil, "bk")
	if len(candidates) != 0 {
		t.Fatalf("keep 5 of 3 → 0 prune candidates, got %d", len(candidates))
	}

	candidates = buildBKRetentionPruneCandidates(nil, 1, nil, "bk")
	if len(candidates) != 0 {
		t.Fatal("nil snapshots should return empty")
	}
}

func TestFilterHaSnapshots(t *testing.T) {
	output := "pool/ds@ha_2025-01-01\npool/ds@ha_2025-01-02\npool/ds@not-ha\n"
	snaps := filterHaSnapshots(output, "pool/ds")
	if len(snaps) != 2 {
		t.Fatalf("expected 2 ha_ snaps, got %d: %v", len(snaps), snaps)
	}
	if snap := filterHaSnapshots("", "pool/ds"); len(snap) != 0 {
		t.Fatal("empty output")
	}
}

func TestDatasetLineageBaseSuffixForDataset(t *testing.T) {
	if s := datasetLineageBaseSuffixForDataset("tank/backups", "tank/backups/jails/42_gen-5"); s != "jails/42" {
		t.Fatalf("rotated: %q", s)
	}
	if s := datasetLineageBaseSuffixForDataset("tank/backups", "tank/backups/data"); s != "data" {
		t.Fatalf("plain: %q", s)
	}
}

func TestShouldAutoRotateBackupErrorCode(t *testing.T) {
	if !shouldAutoRotateBackupErrorCode(backupErrorTargetDiverged) {
		t.Fatal("target diverged should auto-rotate")
	}
	if !shouldAutoRotateBackupErrorCode(backupErrorTargetLocalWrites) {
		t.Fatal("target local writes should auto-rotate")
	}
	if shouldAutoRotateBackupErrorCode(backupErrorSourceMissing) {
		t.Fatal("source missing should not auto-rotate")
	}
	if shouldAutoRotateBackupErrorCode("") {
		t.Fatal("empty should not auto-rotate")
	}
}

func TestShouldRenameTargetAfterRotateFailure(t *testing.T) {
	if !shouldRenameTargetAfterRotateFailure("target is not a replica of the source") {
		t.Fatal("not replica message should trigger rename")
	}
	if !shouldRenameTargetAfterRotateFailure("to perform a full backup, rename the target dataset or sync to an empty target") {
		t.Fatal("rename suggestion should trigger rename")
	}
	if shouldRenameTargetAfterRotateFailure("some other error occurred") {
		t.Fatal("unrelated error should not trigger rename")
	}
}

func TestIsBKSnapshotShortName(t *testing.T) {
	if !isBKSnapshotShortName("bk_2025-01-01_12.00.00", "bk") {
		t.Fatal("bk prefix should match")
	}
	if !isBKSnapshotShortName("@bk_2025-01-01_12.00.00", "bk") {
		t.Fatal("@bk prefix should match after trimming @")
	}
	if isBKSnapshotShortName("ha_2025-01-01", "bk") {
		t.Fatal("ha prefix should not match bk")
	}
	if !isBKSnapshotShortName("bk_2025-01-01", "") {
		t.Fatal("empty prefix should default to bk_")
	}
	if !isBKSnapshotShortName("custom_pref_2025", "custom_pref") {
		t.Fatal("custom prefix should match")
	}
}

func TestBackupSnapshotPrefixForJob(t *testing.T) {
	if got := backupSnapshotPrefixForJob(0); got != "bk" {
		t.Fatalf("zero jobID: got %q", got)
	}
	got := backupSnapshotPrefixForJob(100)
	if should := "bk_j" + compactIDToken(100); got != should {
		t.Fatalf("expected %q, got %q", should, got)
	}
}

func TestCompactIDToken(t *testing.T) {
	if got := compactIDToken(0); got != "0" {
		t.Fatalf("zero: got %q", got)
	}
	if got := compactIDToken(10); got != "a" {
		t.Fatalf("10 = a in base36: got %q", got)
	}
	if got := compactIDToken(36); got != "10" {
		t.Fatalf("36 = 10 in base36: got %q", got)
	}
}

func TestTargetGenerationDatasetCandidate(t *testing.T) {
	tok := "abc123"

	candidate := targetGenerationDatasetCandidate("pool/dataset", tok, 0)
	if candidate != "pool/dataset_gen-abc123" {
		t.Fatalf("first attempt: got %q", candidate)
	}

	candidate = targetGenerationDatasetCandidate("pool/dataset", tok, 2)
	if candidate != "pool/dataset_gen-abc123-2" {
		t.Fatalf("third attempt: got %q", candidate)
	}

	candidate = targetGenerationDatasetCandidate("", tok, 0)
	if candidate != "" {
		t.Fatalf("empty active: got %q", candidate)
	}

	candidate = targetGenerationDatasetCandidate("pool/dataset", "", 0)
	if candidate == "" {
		t.Fatal("empty token should use timestamp")
	}
}

func TestAutoDestSuffix(t *testing.T) {
	got := autoDestSuffix("zroot/jails/my-jail")
	if !strings.Contains(got, "my-jail") {
		t.Fatalf("jail dataset should contain my-jail, got %q", got)
	}

	got = autoDestSuffix("zroot/virtual-machines/my-vm")
	if !strings.Contains(got, "my-vm") {
		t.Fatalf("vm dataset should contain my-vm, got %q", got)
	}

	got = autoDestSuffix("tank/data/db")
	if got == "" {
		t.Fatal("generic dataset should produce a suffix")
	}
}

func TestParseHumanSizeBytes(t *testing.T) {
	val, ok := parseHumanSizeBytes("1.5", "K", "")
	if !ok || val != 1500 {
		t.Fatalf("1.5K: got %d ok=%v", val, ok)
	}

	val, ok = parseHumanSizeBytes("20", "M", "")
	if !ok || val != 20000000 {
		t.Fatalf("20M: got %d ok=%v", val, ok)
	}

	val, ok = parseHumanSizeBytes("2.5", "G", "i")
	if !ok || val != 2684354560 {
		t.Fatalf("2.5Gi: got %d ok=%v", val, ok)
	}

	val, ok = parseHumanSizeBytes("abc", "K", "")
	if ok {
		t.Fatal("non-numeric should fail")
	}
}

func TestParseTotalBytesFromOutput(t *testing.T) {
	output := `{"replicationSize": "1048576"}`
	result := parseTotalBytesFromOutput(output)
	if result == nil || *result != 1048576 {
		t.Fatalf("replicationSize: got %v", result)
	}

	output = "syncing: 20 MiB"
	result = parseTotalBytesFromOutput(output)
	if result == nil {
		t.Fatal("syncing 20 MiB should parse")
	}
	if *result < 10000000 {
		t.Fatalf("syncing 20 MiB should be ~20MB, got %d", *result)
	}

	result = parseTotalBytesFromOutput("")
	if result != nil {
		t.Fatal("empty output should return nil")
	}
}

func TestGetBackupEventProgressReportsFinalizingTransfer(t *testing.T) {
	svc := newRunBackupJobTestDB(t)
	event := clusterModels.BackupEvent{
		Mode:   "jail",
		Status: "running",
		Output: "{\"replicationSize\": \"100000000\"}\nbackup_phase: finalizing\n",
	}
	if err := svc.DB.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}

	progress, err := svc.GetBackupEventProgress(context.Background(), event.ID)
	if err != nil {
		t.Fatalf("get progress: %v", err)
	}
	if progress.Phase != "finalizing" {
		t.Fatalf("phase = %q, want finalizing", progress.Phase)
	}
	if progress.TotalBytes == nil || *progress.TotalBytes != 100000000 {
		t.Fatalf("total bytes = %v, want 100000000", progress.TotalBytes)
	}
	if progress.MovedBytes == nil || *progress.MovedBytes != 100000000 {
		t.Fatalf("moved bytes = %v, want 100000000", progress.MovedBytes)
	}
	if progress.ProgressPercent == nil || *progress.ProgressPercent != 100 {
		t.Fatalf("progress percent = %v, want 100", progress.ProgressPercent)
	}
}

func TestJailDestSuffixForSource(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		jailRoot   string
		want       string
	}{
		{"lineage tail remapped under jail root", "jails/10/j-exsj2r/active", "zapp/sylve/jails/10", "zapp/sylve/jails/10/j-exsj2r/active"},
		{"empty suffix returns jail root", "", "zapp/sylve/jails/10", "zapp/sylve/jails/10"},
		{"suffix equals jail root", "zapp/sylve/jails/10", "zapp/sylve/jails/10", "zapp/sylve/jails/10"},
		{"job- lineage tail remapped", "jails/10/job-abc/active", "zapp/sylve/jails/10", "zapp/sylve/jails/10/job-abc/active"},
		{"empty jail root returns configured", "anything", "", "anything"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jailDestSuffixForSource(tc.configured, tc.jailRoot); got != tc.want {
				t.Fatalf("jailDestSuffixForSource(%q, %q) = %q, want %q", tc.configured, tc.jailRoot, got, tc.want)
			}
		})
	}
}

func TestVMDestSuffixForSource(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		vmSource   string
		want       string
	}{
		{"empty suffix returns vm root", "", "pool/sylve/virtual-machines/100", "pool/sylve/virtual-machines/100"},
		{"empty suffix preserves child rel", "", "pool/sylve/virtual-machines/100/disk0", "pool/sylve/virtual-machines/100/disk0"},
		{"lineage tail remapped under vm root", "virtual-machines/100/j-x/active", "pool/sylve/virtual-machines/100", "pool/sylve/virtual-machines/100/j-x/active"},
		{"lineage tail remapped with child rel", "virtual-machines/100/j-x/active", "pool/sylve/virtual-machines/100/disk0", "pool/sylve/virtual-machines/100/j-x/active/disk0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmDestSuffixForSource(tc.configured, tc.vmSource); got != tc.want {
				t.Fatalf("vmDestSuffixForSource(%q, %q) = %q, want %q", tc.configured, tc.vmSource, got, tc.want)
			}
		})
	}
}
