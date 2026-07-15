// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"strings"
	"testing"
)

func TestBackupCommitProtocolNamesRemainJobScoped(t *testing.T) {
	t.Parallel()

	const jobID uint = 71
	name := backupSnapshotNameForJob(jobID)
	jobPrefix := backupSnapshotPrefixForJob(jobID)
	if !strings.HasPrefix(name, jobPrefix+"_"+backupCommitProtocolToken+"_") {
		t.Fatalf("protocol snapshot %q escaped job prefix %q", name, jobPrefix)
	}
	if !backupSnapshotRequiresCommit(jobID, name) {
		t.Fatalf("new protocol snapshot %q did not require a commit", name)
	}
	if backupSnapshotRequiresCommit(jobID+1, name) {
		t.Fatalf("snapshot %q was accepted for the wrong job", name)
	}
	if backupSnapshotRequiresCommit(jobID, jobPrefix+"_legacytoken") {
		t.Fatal("legacy per-job snapshot unexpectedly required a commit marker")
	}
	if !isBKSnapshotShortName("@"+name, jobPrefix) {
		t.Fatalf("protocol snapshot %q is no longer retained by the job ownership filter", name)
	}
}

func TestBackupCommitJobIDFromSnapshot(t *testing.T) {
	t.Parallel()

	const jobID uint = 987
	name := backupSnapshotPrefixForJob(jobID) + "_" + backupCommitProtocolToken + "_token"
	gotID, required, err := backupCommitJobIDFromSnapshot("@" + name)
	if err != nil || !required || gotID != jobID {
		t.Fatalf("parse protocol snapshot: id=%d required=%t err=%v", gotID, required, err)
	}
	if gotID, required, err := backupCommitJobIDFromSnapshot(backupSnapshotPrefixForJob(jobID) + "_legacy"); err != nil || required || gotID != 0 {
		t.Fatalf("legacy snapshot changed contract: id=%d required=%t err=%v", gotID, required, err)
	}
	if _, _, err := backupCommitJobIDFromSnapshot("bk_j0_" + backupCommitProtocolToken + "_token"); err == nil {
		t.Fatal("zero job token was accepted")
	}
}
