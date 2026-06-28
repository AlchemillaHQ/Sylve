// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func zfsSnapshotExists(t *testing.T, client *gzfs.Client, fullSnap string) bool {
	t.Helper()
	ctx := context.Background()
	list, err := client.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false)
	if err != nil {
		t.Fatalf("ListByType snapshots: %v", err)
	}
	for _, ds := range list {
		if ds != nil && ds.Name == fullSnap {
			return true
		}
	}
	return false
}

func zfsTakeSnapshot(t *testing.T, client *gzfs.Client, dataset, snapName string) {
	t.Helper()
	ctx := context.Background()
	if _, err := client.ZFS.Snapshot(ctx, dataset, snapName, false); err != nil {
		t.Fatalf("zfs snapshot %s@%s: %v", dataset, snapName, err)
	}
}

// snapshotCleanup destroys all snapshots under the given dataset whose short
// name (after the last @) matches any of the cleanupPrefixes. It returns the
// count of snapshots destroyed. This mirrors the logic in replicateGuestDatasets.
func snapshotCleanup(ctx context.Context, client *gzfs.Client, dataset string, prefixes []string) (int, error) {
	snapList, err := client.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset, true)
	if err != nil {
		return 0, err
	}

	destroyed := 0
	for _, snap := range snapList {
		if snap == nil {
			continue
		}
		fullName := snap.Name
		atIdx := strings.LastIndex(fullName, "@")
		if atIdx < 0 {
			continue
		}
		shortName := fullName[atIdx+1:]
		if shortName == "" {
			continue
		}
		matched := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(shortName, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if err := snap.Destroy(ctx, false, false); err != nil {
			return destroyed, err
		}
		destroyed++
	}

	return destroyed, nil
}

// ---------------------------------------------------------------------------
// Unit tests (no ZFS required)
// ---------------------------------------------------------------------------

func TestParseMigrationSnapshotTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOK    bool
		wantAfter time.Time
	}{
		{"initial", "sylve-migrate-initial-1700000000", true, time.Unix(1700000000, 0)},
		{"final", "sylve-migrate-final-1700000001", true, time.Unix(1700000001, 0)},
		{"pre_migration", "sylve-migrate-pre-migration-1700000002", true, time.Unix(1700000002, 0)},
		{"zero_timestamp", "sylve-migrate-initial-0", true, time.Unix(0, 0)},
		{"no_timestamp", "sylve-migrate-initial", false, time.Time{}},
		{"empty", "", false, time.Time{}},
		{"random", "not-a-migration-snapshot", false, time.Time{}},
		{"future", "sylve-migrate-final-9999999999", true, time.Unix(9999999999, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseMigrationSnapshotTimestamp(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if ok && !got.Equal(tt.wantAfter) {
				t.Fatalf("time: got %v, want %v", got, tt.wantAfter)
			}
		})
	}
}

func TestExtractGuestFromDatasetPath(t *testing.T) {
	tests := []struct {
		dataset  string
		wantType string
		wantID   uint
	}{
		{"zroot/sylve/virtual-machines/103", "vm", 103},
		{"tank/sylve/virtual-machines/42/data", "vm", 42},
		{"pool/sylve/jails/7", "jail", 7},
		{"pool/sylve/jails/99/root", "jail", 99},
		{"just/a/path", "", 0},
		{"virtual-machines/abc", "", 0}, // non-numeric ID
		{"pool/virtual-machines/1/no/sylve", "vm", 1},
		{"", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.dataset, func(t *testing.T) {
			gt, id := extractGuestFromDatasetPath(tt.dataset)
			if gt != tt.wantType {
				t.Fatalf("type: got %q, want %q", gt, tt.wantType)
			}
			if id != tt.wantID {
				t.Fatalf("id: got %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestGuestKey(t *testing.T) {
	tests := []struct {
		guestType string
		guestID   uint
		want      string
	}{
		{"vm", 103, "vm-103"},
		{"jail", 42, "jail-42"},
		{"", 1, ""},               // empty type
		{"vm", 0, ""},             // zero ID
		{"  vm  ", 103, "vm-103"}, // trimmed
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := guestKey(tt.guestType, tt.guestID)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMigrationSnapshotPrefix(t *testing.T) {
	if migrationSnapPrefix != "sylve-migrate" {
		t.Fatalf("got %q, want sylve-migrate", migrationSnapPrefix)
	}
}

// ---------------------------------------------------------------------------
// ZFS-backed integration tests
// ---------------------------------------------------------------------------

// TestSnapshotCleanupDestroysTargetedPrefixes verifies that the snapshot
// cleanup logic only destroys snapshots matching the specified prefixes
// and leaves all other snapshots intact.
func TestSnapshotCleanupDestroysTargetedPrefixes(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	vmRoot := pool + "/sylve/virtual-machines/103"
	zfstest.EnsureDataset(t, client, vmRoot)

	// Create snapshots with various prefixes
	zfsTakeSnapshot(t, client, vmRoot, "ha_abc123")
	zfsTakeSnapshot(t, client, vmRoot, "ha_xyz789")
	zfsTakeSnapshot(t, client, vmRoot, "ha_def456")
	zfsTakeSnapshot(t, client, vmRoot, "bk_20250101")
	zfsTakeSnapshot(t, client, vmRoot, "bk_j2s_20250101")
	zfsTakeSnapshot(t, client, vmRoot, "sylve-migrate-initial-1700000000")
	zfsTakeSnapshot(t, client, vmRoot, "sylve-migrate-final-1700000001")
	zfsTakeSnapshot(t, client, vmRoot, "sylve-migrate-pre-migration-1700000002")
	zfsTakeSnapshot(t, client, vmRoot, "manual-backup")
	zfsTakeSnapshot(t, client, vmRoot, "my-snapshot")

	prefixes := []string{"ha_", "bk_", migrationSnapPrefix}
	destroyed, err := snapshotCleanup(ctx, client, vmRoot, prefixes)
	if err != nil {
		t.Fatalf("snapshotCleanup: %v", err)
	}

	// 3 ha_ + 2 bk_ (bk_ + bk_j2s_) + 3 sylve-migrate = 8 destroyed
	if destroyed != 8 {
		t.Fatalf("expected 8 destroyed snapshots, got %d", destroyed)
	}

	// Verify destroyed snapshots are gone
	for _, snap := range []string{
		vmRoot + "@ha_abc123",
		vmRoot + "@ha_xyz789",
		vmRoot + "@ha_def456",
		vmRoot + "@bk_20250101",
		vmRoot + "@bk_j2s_20250101",
		vmRoot + "@sylve-migrate-initial-1700000000",
		vmRoot + "@sylve-migrate-final-1700000001",
		vmRoot + "@sylve-migrate-pre-migration-1700000002",
	} {
		if zfsSnapshotExists(t, client, snap) {
			t.Fatalf("snapshot %s should have been destroyed", snap)
		}
	}

	// Verify preserved snapshots still exist
	for _, snap := range []string{
		vmRoot + "@manual-backup",
		vmRoot + "@my-snapshot",
	} {
		if !zfsSnapshotExists(t, client, snap) {
			t.Fatalf("snapshot %s should have been preserved", snap)
		}
	}
}

// TestSnapshotCleanupChildDatasets verifies that snapshots on child datasets
// (e.g. zvol, data subdirs) are also caught by the cleanup.
func TestSnapshotCleanupChildDatasets(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	vmRoot := pool + "/sylve/virtual-machines/103"
	zfstest.EnsureDataset(t, client, vmRoot)

	// Create a child volume (like a disk zvol)
	zvol := vmRoot + "/zvol-9"
	zfstest.EnsureVolume(t, client, zvol, 50)

	// Create a child filesystem (like data subdir)
	dataDS := vmRoot + "/data"
	zfstest.EnsureDataset(t, client, dataDS)

	// Snapshots on the root dataset
	zfsTakeSnapshot(t, client, vmRoot, "ha_parent")
	zfsTakeSnapshot(t, client, vmRoot, "my-safe-snap")

	// Snapshots on the zvol child
	zfsTakeSnapshot(t, client, zvol, "ha_child_zvol")
	zfsTakeSnapshot(t, client, zvol, "bk_backup_zvol")

	// Snapshots on the data child
	zfsTakeSnapshot(t, client, dataDS, "sylve-migrate-initial-1700000000")
	zfsTakeSnapshot(t, client, dataDS, "important-user-snap")

	prefixes := []string{"ha_", "bk_", migrationSnapPrefix}
	destroyed, err := snapshotCleanup(ctx, client, vmRoot, prefixes)
	if err != nil {
		t.Fatalf("snapshotCleanup: %v", err)
	}

	// Should destroy: ha_parent, ha_child_zvol, bk_backup_zvol, sylve-migrate-initial = 4
	if destroyed != 4 {
		t.Fatalf("expected 4 destroyed snapshots, got %d", destroyed)
	}

	// Verify child snapshots are gone
	for _, snap := range []string{
		zvol + "@ha_child_zvol",
		zvol + "@bk_backup_zvol",
		dataDS + "@sylve-migrate-initial-1700000000",
	} {
		if zfsSnapshotExists(t, client, snap) {
			t.Fatalf("snapshot %s should have been destroyed", snap)
		}
	}

	// Verify safe snapshots survive
	for _, snap := range []string{
		vmRoot + "@my-safe-snap",
		dataDS + "@important-user-snap",
	} {
		if !zfsSnapshotExists(t, client, snap) {
			t.Fatalf("snapshot %s should have been preserved", snap)
		}
	}
}

// TestSnapshotCleanupScopedToGuest verifies that cleanup only touches
// snapshots belonging to the specified guest dataset and does NOT affect
// other guests on the same pool.
func TestSnapshotCleanupScopedToGuest(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	// Guest 103 - the one being migrated
	vm103 := pool + "/sylve/virtual-machines/103"
	zfstest.EnsureDataset(t, client, vm103)
	zfsTakeSnapshot(t, client, vm103, "ha_g103")
	zfsTakeSnapshot(t, client, vm103, "my-snap-103")

	// Guest 104 - a different guest, must NOT be touched
	vm104 := pool + "/sylve/virtual-machines/104"
	zfstest.EnsureDataset(t, client, vm104)
	zfsTakeSnapshot(t, client, vm104, "ha_g104")
	zfsTakeSnapshot(t, client, vm104, "bk_g104")
	zfsTakeSnapshot(t, client, vm104, "my-snap-104")

	// Also create a jail on the same pool, must NOT be touched
	jail42 := pool + "/sylve/jails/42"
	zfstest.EnsureDataset(t, client, jail42)
	zfsTakeSnapshot(t, client, jail42, "ha_jail42")
	zfsTakeSnapshot(t, client, jail42, "bk_jail42")

	// Only clean VM 103
	prefixes := []string{"ha_", "bk_", migrationSnapPrefix}
	destroyed, err := snapshotCleanup(ctx, client, vm103, prefixes)
	if err != nil {
		t.Fatalf("snapshotCleanup: %v", err)
	}

	// Only ha_g103 on VM 103 should be destroyed
	if destroyed != 1 {
		t.Fatalf("expected 1 destroyed snapshot on VM 103, got %d", destroyed)
	}

	// Verify VM 103 cleanup
	if zfsSnapshotExists(t, client, vm103+"@ha_g103") {
		t.Fatal("ha_g103 should be destroyed")
	}
	if !zfsSnapshotExists(t, client, vm103+"@my-snap-103") {
		t.Fatal("my-snap-103 should be preserved")
	}

	// Verify VM 104 is completely untouched
	if !zfsSnapshotExists(t, client, vm104+"@ha_g104") {
		t.Fatal("ha_g104 should NOT have been touched")
	}
	if !zfsSnapshotExists(t, client, vm104+"@bk_g104") {
		t.Fatal("bk_g104 should NOT have been touched")
	}
	if !zfsSnapshotExists(t, client, vm104+"@my-snap-104") {
		t.Fatal("my-snap-104 should NOT have been touched")
	}

	// Verify jail is untouched
	if !zfsSnapshotExists(t, client, jail42+"@ha_jail42") {
		t.Fatal("ha_jail42 should NOT have been touched")
	}
	if !zfsSnapshotExists(t, client, jail42+"@bk_jail42") {
		t.Fatal("bk_jail42 should NOT have been touched")
	}
}

// TestSnapshotCleanupEmptyDataset verifies cleanup handles a dataset
// with no snapshots gracefully.
func TestSnapshotCleanupEmptyDataset(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	vmRoot := pool + "/sylve/virtual-machines/103"
	zfstest.EnsureDataset(t, client, vmRoot)

	prefixes := []string{"ha_", "bk_", migrationSnapPrefix}
	destroyed, err := snapshotCleanup(ctx, client, vmRoot, prefixes)
	if err != nil {
		t.Fatalf("snapshotCleanup on empty dataset: %v", err)
	}
	if destroyed != 0 {
		t.Fatalf("expected 0 destroyed, got %d", destroyed)
	}
}

// TestSnapshotCleanupIsDatasetNotFound verifies the error detection helper.
func TestIsDatasetNotFound(t *testing.T) {
	if isDatasetNotFound(nil) {
		t.Fatal("nil should not be dataset-not-found")
	}
	if !isDatasetNotFound(fmt.Errorf("dataset does not exist")) {
		t.Fatal("'dataset does not exist' should match")
	}
	if !isDatasetNotFound(fmt.Errorf("cannot open 'tank/foo': dataset does not exist")) {
		t.Fatal("wrapped 'dataset does not exist' should match")
	}
	if !isDatasetNotFound(fmt.Errorf("no such pool 'badpool'")) {
		t.Fatal("'no such' should match")
	}
	if isDatasetNotFound(fmt.Errorf("connection refused")) {
		t.Fatal("unrelated error should not match")
	}
}
