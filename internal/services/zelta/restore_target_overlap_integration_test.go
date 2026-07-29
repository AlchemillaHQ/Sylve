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
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

// TestOOBRestoreRemoteLineageOverlapIsFailSafeRealZFS exercises the remote
// operations that can overlap after the UI status read. ZFS keeps an active
// send valid across new snapshots and dataset renames, refuses to prune the
// snapshot while it is being sent, and Sylve resolves a renamed generation.
// If pruning wins before send starts, restore preflight fails before creating
// or replacing the local destination. These guarantees mean a remote lineage
// lease is not required solely for OOB restore safety.
func TestOOBRestoreRemoteLineageOverlapIsFailSafeRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping OOB restore overlap integration test in short mode")
	}
	requireLocalhostBackupSSH(t)

	poolName, gzfsClient, cleanup := zfstest.Pool(t)
	defer cleanup()

	backupRoot := poolName + "/target"
	lineageParent := backupRoot + "/dataset/j-5"
	activeDataset := lineageParent + "/active"
	rotatedDataset := activeDataset + "_gen-overlap"
	destinationDataset := poolName + "/restore/destination"
	zfstest.EnsureDataset(t, gzfsClient, activeDataset)

	mountpointOutput, err := exec.Command(
		"zfs", "get", "-H", "-o", "value", "mountpoint", activeDataset,
	).Output()
	if err != nil {
		t.Fatalf("read active dataset mountpoint: %v", err)
	}
	mountpoint := strings.TrimSpace(string(mountpointOutput))
	if mountpoint == "" || mountpoint == "none" || mountpoint == "legacy" {
		t.Fatalf("active dataset has unusable mountpoint %q", mountpoint)
	}

	payload := make([]byte, 8<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("generate incompressible send payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, "payload"), payload, 0o600); err != nil {
		t.Fatalf("write send payload: %v", err)
	}
	if output, err := exec.Command("sync").CombinedOutput(); err != nil {
		t.Fatalf("sync payload: %v: %s", err, output)
	}
	if output, err := exec.Command("zfs", "snapshot", activeDataset+"@selected").CombinedOutput(); err != nil {
		t.Fatalf("create selected snapshot: %v: %s", err, output)
	}

	// Run the real Zelta restore command with a test-only zfs wrapper. The
	// wrapper starts the real send, then withholds its stream until the gate is
	// opened. This deterministically holds the selected snapshot while the
	// target is mutated below.
	realZFS, err := exec.LookPath("zfs")
	if err != nil {
		t.Fatalf("find real zfs binary: %v", err)
	}
	zeltaDir := extractZeltaToTemp(t)
	wrapper := `#!/bin/sh
if [ "$1" = "send" ]; then
    "$SYLVE_OVERLAP_REAL_ZFS" "$@" | (
        : > "$SYLVE_OVERLAP_MARKER"
        while [ ! -f "$SYLVE_OVERLAP_GATE" ]; do sleep 0.05; done
        cat
    )
    exit $?
fi
exec "$SYLVE_OVERLAP_REAL_ZFS" "$@"
`
	if err := os.WriteFile(filepath.Join(zeltaDir, "bin", "zfs"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write gated zfs wrapper: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "send-started")
	gate := filepath.Join(t.TempDir(), "send-continue")
	receiveDataset := poolName + "/zelta-receive/overlap"
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/zelta-receive")
	type zeltaResult struct {
		output string
		err    error
	}
	result := make(chan zeltaResult, 1)
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	zeltaDone := false
	go func() {
		output, runErr := runZeltaWithEnv(
			sendCtx,
			[]string{
				"SYLVE_OVERLAP_REAL_ZFS=" + realZFS,
				"SYLVE_OVERLAP_MARKER=" + marker,
				"SYLVE_OVERLAP_GATE=" + gate,
			},
			"backup", "--json", "--no-snapshot",
			activeDataset+"@selected", receiveDataset,
		)
		result <- zeltaResult{output: output, err: runErr}
	}()
	defer func() {
		if !zeltaDone {
			_ = os.WriteFile(gate, nil, 0o600)
			sendCancel()
			select {
			case <-result:
			case <-time.After(5 * time.Second):
			}
		}
		sendCancel()
	}()

	markerDeadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case early := <-result:
			zeltaDone = true
			t.Fatalf("Zelta send exited before overlap gate: %v: %s", early.err, early.output)
		default:
		}
		if time.Now().After(markerDeadline) {
			t.Fatal("timed out waiting for gated Zelta send")
		}
		time.Sleep(25 * time.Millisecond)
	}
	// Give the real zfs send time to fill the withheld pipe and hold the
	// snapshot. The busy-prune assertion below proves the hold is active.
	time.Sleep(150 * time.Millisecond)

	if output, err := exec.Command("zfs", "snapshot", activeDataset+"@concurrent").CombinedOutput(); err != nil {
		t.Fatalf("snapshot creation during send must be supported: %v: %s", err, output)
	}
	if output, err := exec.Command("zfs", "rename", activeDataset, rotatedDataset).CombinedOutput(); err != nil {
		t.Fatalf("generation rename during send must be supported: %v: %s", err, output)
	}

	// A target-prune attempt cannot invalidate a snapshot already being sent.
	if output, err := exec.Command("zfs", "destroy", rotatedDataset+"@selected").CombinedOutput(); err == nil {
		t.Fatalf("prune unexpectedly destroyed a snapshot under active send: %s", output)
	} else if !strings.Contains(strings.ToLower(string(output)), "busy") {
		t.Fatalf("active-send prune failed for an unexpected reason: %v: %s", err, output)
	}

	target := &clusterModels.BackupTarget{
		ID: 5, Name: "overlap-target", SSHHost: "root@localhost", SSHPort: 22,
		BackupRoot: backupRoot, Enabled: true,
	}
	service := &Service{GZFS: gzfsClient}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Run discovery while another snapshot is added to the renamed generation.
	discoveryMutation := make(chan error, 1)
	go func() {
		output, snapshotErr := exec.Command("zfs", "snapshot", rotatedDataset+"@during-discovery").CombinedOutput()
		if snapshotErr != nil {
			discoveryMutation <- &commandOutputError{err: snapshotErr, output: string(output)}
			return
		}
		discoveryMutation <- nil
	}()
	snapshots, err := service.listRemoteSnapshotsWithLineage(ctx, target, activeDataset)
	if err != nil {
		t.Fatalf("lineage discovery during target mutation: %v", err)
	}
	if err := <-discoveryMutation; err != nil {
		t.Fatalf("concurrent discovery mutation: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatal("lineage discovery returned no snapshots after generation rename")
	}

	resolved, err := service.resolveRemoteDatasetForSnapshot(ctx, target, activeDataset, "@selected")
	if err != nil {
		t.Fatalf("resolve selected snapshot after generation rename: %v", err)
	}
	if resolved != rotatedDataset {
		t.Fatalf("selected snapshot resolved to %q, want %q", resolved, rotatedDataset)
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatalf("open Zelta send gate: %v", err)
	}
	select {
	case completed := <-result:
		zeltaDone = true
		if completed.err != nil {
			t.Fatalf("Zelta send did not survive generation rename: %v: %s", completed.err, completed.output)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for renamed Zelta send")
	}
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", receiveDataset).CombinedOutput(); err != nil {
		t.Fatalf("Zelta did not receive the stream after source rename: %v: %s", err, output)
	}

	// If prune wins before a later restore reaches send, preflight must fail
	// without creating or replacing the requested local destination.
	if output, err := exec.Command("zfs", "destroy", rotatedDataset+"@selected").CombinedOutput(); err != nil {
		t.Fatalf("prune selected snapshot after send: %v: %s", err, output)
	}
	_, err = service.runRestoreFromTargetSingleDataset(
		ctx,
		target,
		restoreFromTargetPayload{
			TargetID: target.ID, RemoteDataset: activeDataset, Snapshot: "@selected",
			DestinationDataset: destinationDataset,
		},
		nil,
		false,
		false,
		false,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "snapshot_not_found_on_target") {
		t.Fatalf("restore did not fail closed after pre-send prune: %v", err)
	}
	if output, existsErr := exec.Command("zfs", "list", "-H", "-o", "name", destinationDataset).CombinedOutput(); existsErr == nil {
		t.Fatalf("failed restore created destination %s: %s", destinationDataset, output)
	}
}

type commandOutputError struct {
	err    error
	output string
}

func (e *commandOutputError) Error() string {
	return e.err.Error() + ": " + strings.TrimSpace(e.output)
}
