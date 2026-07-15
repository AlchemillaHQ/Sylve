// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestActivateTargetGenerationRollsBackAmbiguousArchiveRenameRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping ambiguous target-generation rename test in short mode")
	}

	poolName, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	backupRoot := poolName + "/backup"
	active := backupRoot + "/active"
	selected := active + "_gen-selected"
	for _, dataset := range []string{active, selected} {
		zfstest.EnsureDataset(t, client, dataset)
	}
	activeGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t, "get", "-H", "-p", "-o", "value", "guid", active,
	))
	selectedGUID := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t, "get", "-H", "-p", "-o", "value", "guid", selected,
	))

	// Use a transport shim, not a fake ZFS binary: every command executes
	// against real FreeBSD ZFS. The first active->archive rename succeeds, then
	// the shim reports a transport error to model an ambiguous SSH result.
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	statePath := filepath.Join(dir, "archive-rename-executed")
	sshScript := `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|-i|-p) shift 2 ;;
    -*) shift ;;
    *) shift; break ;;
  esac
done
if [ "$#" -ge 4 ] && [ "$1" = "zfs" ] && [ "$2" = "rename" ] && \
   [ "$3" = "${ZELTA_TEST_FAIL_RENAME_FROM:-}" ] && \
   [ ! -e "${ZELTA_TEST_RENAME_STATE:?}" ]; then
  "$@"
  : > "${ZELTA_TEST_RENAME_STATE}"
  echo simulated_transport_error >&2
  exit 255
fi
exec "$@"
`
	if err := os.WriteFile(sshPath, []byte(sshScript), 0o755); err != nil {
		t.Fatalf("write SSH fault shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZELTA_TEST_FAIL_RENAME_FROM", active)
	t.Setenv("ZELTA_TEST_RENAME_STATE", statePath)

	service := &Service{GZFS: client}
	target := &clusterModels.BackupTarget{
		SSHHost:    "local-zfs-through-transport-shim",
		BackupRoot: backupRoot,
		Enabled:    true,
	}
	activation, err := service.activateTargetGenerationForRestore(
		context.Background(), target, active, selected,
	)
	if err == nil || !strings.Contains(err.Error(), "failed_to_rename_target_dataset") {
		t.Fatalf("ambiguous rename error = %v", err)
	}
	if strings.Contains(err.Error(), "remote_rollback_failed") {
		t.Fatalf("ambiguous rename rollback failed: %v", err)
	}
	if activation.changed() {
		t.Fatalf("activation remained marked changed after rollback: %+v", activation)
	}

	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t, "get", "-H", "-p", "-o", "value", "guid", active,
	)); got != activeGUID {
		t.Fatalf("active GUID after rollback = %q, want %q", got, activeGUID)
	}
	if got := strings.TrimSpace(mustRunRestoreZFSTestCommand(
		t, "get", "-H", "-p", "-o", "value", "guid", selected,
	)); got != selectedGUID {
		t.Fatalf("selected GUID after rollback = %q, want %q", got, selectedGUID)
	}
	for _, dataset := range listActiveGenerations(t, active) {
		if dataset != selected {
			t.Fatalf("unexpected archive generation remained after rollback: %s", dataset)
		}
	}
}
