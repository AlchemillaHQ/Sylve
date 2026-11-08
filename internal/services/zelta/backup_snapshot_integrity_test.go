// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationBackupSnapshotFailsWhenRequestedSnapshotAlreadyExists(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	poolName, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	zfstest.EnsureDataset(t, client, poolName+"/source/child")
	zfstest.EnsureDataset(t, client, poolName+"/other")

	ctx := context.Background()
	if output, err := exec.CommandContext(ctx, "zfs", "snapshot", "-r", poolName+"/source@requested").CombinedOutput(); err != nil {
		t.Fatalf("seed requested snapshot: %v\n%s", err, string(output))
	}

	err := snapshotBackupScopes(ctx, []backupScope{
		{sourceDataset: poolName + "/other"},
		{sourceDataset: poolName + "/source"},
	}, "requested", true)
	if err == nil || !strings.HasPrefix(err.Error(), backupErrorSourceSnapshotFailed) {
		t.Fatalf("expected %s, got %v", backupErrorSourceSnapshotFailed, err)
	}
	if err := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", poolName+"/other@requested").Run(); err == nil {
		t.Fatal("a failed run left a snapshot on another scope")
	}
}
