// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestZeltaBackupFailsWhenRequestedSnapshotAlreadyExists(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS snapshot-failure integration test in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	zfstest.EnsureDataset(t, client, poolName+"/source/child")
	zfstest.EnsureDataset(t, client, poolName+"/target")

	ctx := context.Background()
	for _, dataset := range []string{poolName + "/source", poolName + "/source/child", poolName + "/target"} {
		if output, err := exec.CommandContext(ctx, "zfs", "set", "mountpoint=legacy", dataset).CombinedOutput(); err != nil {
			t.Fatalf("set legacy mountpoint on %s: %v\n%s", dataset, err, string(output))
		}
	}
	if output, err := exec.CommandContext(ctx, "zfs", "snapshot", "-r", poolName+"/source@requested").CombinedOutput(); err != nil {
		t.Fatalf("seed requested snapshot: %v\n%s", err, string(output))
	}

	zeltaDir := extractZeltaToTemp(t)
	zeltaBin := filepath.Join(zeltaDir, "bin", "zelta")
	cmd := exec.CommandContext(
		ctx,
		zeltaBin,
		"backup",
		"--json",
		"--incremental",
		"--snapshot",
		"--snap-name", "requested",
		poolName+"/source",
		poolName+"/target/backup",
	)
	cmd.Env = append(
		os.Environ(),
		"ZELTA_SHARE="+filepath.Join(zeltaDir, "share", "zelta"),
		"PATH="+filepath.Join(zeltaDir, "bin")+":"+os.Getenv("PATH"),
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("backup unexpectedly succeeded with an existing requested snapshot:\n%s", string(output))
	}
	if !strings.Contains(string(output), "source_snapshot_creation_failed") {
		t.Fatalf("missing explicit snapshot failure marker: %v\n%s", err, string(output))
	}
	if zfsDatasetExists(t, poolName+"/target/backup") {
		t.Fatal("target dataset was created after source snapshot creation failed")
	}
}
