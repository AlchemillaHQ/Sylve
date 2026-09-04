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

func TestListRemoteSnapshotsPageValidatesOnlyRequestedWindow(t *testing.T) {
	harness := newFakeSSHHarness(t)
	snapshots, _, metadataBySnapshot, remoteNames := backupInventoryScaleFixture(t, 576)

	var snapshotOutput strings.Builder
	for i := range snapshots {
		fmt.Fprintf(
			&snapshotOutput,
			"%s\t%d\t1\t1\t%s\tnone\n",
			snapshots[i].Name,
			1700000000+i,
			snapshots[i].Guid,
		)
	}
	pageNames := remoteNames[len(remoteNames)-DefaultSnapshotPageLimit:]
	harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs list -t filesystem -d 1 -Hp -o name backup": {{
			Stdout: "backup\nbackup/root\n",
		}},
		"zfs list -t snapshot -Hp -o name,creation,used,refer,guid,encryption -s creation backup/root": {{
			Stdout: snapshotOutput.String(),
		}},
		backupCommitBatchTestCommand(pageNames): {{
			Stdout: backupCommitBatchTestOutput(t, pageNames, metadataBySnapshot),
		}},
		"zfs list -t filesystem,volume -r -d 1 -Hp -o name backup/root": {{
			Stdout: "backup/root\n",
		}},
	}})

	job := &clusterModels.BackupJob{
		ID:         1,
		Mode:       clusterModels.BackupJobModeDataset,
		DestSuffix: "root",
		Target: clusterModels.BackupTarget{
			SSHHost:    "user@target",
			BackupRoot: "backup",
		},
	}
	page, err := (&Service{}).ListRemoteSnapshotsPage(
		context.Background(),
		job,
		SnapshotPageRequest{Limit: DefaultSnapshotPageLimit},
	)
	if err != nil {
		t.Fatalf("list snapshot page: %v", err)
	}
	if len(page.Items) != DefaultSnapshotPageLimit || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("unexpected page: items=%d hasMore=%v cursor=%q", len(page.Items), page.HasMore, page.NextCursor)
	}
	if page.Items[0].Name != pageNames[0] || page.Items[len(page.Items)-1].Name != pageNames[len(pageNames)-1] {
		t.Fatalf("unexpected page bounds: first=%q last=%q", page.Items[0].Name, page.Items[len(page.Items)-1].Name)
	}
	if got, want := len(harness.Calls()), 4; got != want {
		t.Fatalf("SSH calls = %d, want %d: %v", got, want, harness.Calls())
	}
	for _, call := range harness.Calls() {
		if strings.HasPrefix(call, "zfs get ") && strings.Contains(call, remoteNames[0]) {
			t.Fatalf("metadata validation escaped requested page: %s", call)
		}
	}
}
