// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestValidateMigratedGuestRootsUsesExactRealZFSMultiRootManifest(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS migration manifest integration test in short mode")
	}

	poolA, client, cleanupA := zfstest.Pool(t)
	defer cleanupA()
	poolB, _, cleanupB := zfstest.Pool(t)
	defer cleanupB()

	const rid = uint(731)
	rootA := poolA + "/sylve/virtual-machines/731"
	rootB := poolB + "/sylve/virtual-machines/731"
	zfstest.EnsureDataset(t, client, rootA)
	zfstest.EnsureDataset(t, client, rootB)
	zfstest.EnsureDataset(t, client, poolA+"/sylve/virtual-machines/7310")

	service := &Service{GZFS: client}
	got, err := service.ValidateMigratedVMRoots(t.Context(), rid, []string{rootB, rootA})
	if err != nil {
		t.Fatalf("validate exact multi-root manifest: %v", err)
	}
	want := []string{rootA, rootB}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("validated roots = %v, want %v", got, want)
	}

	for _, invalid := range [][]string{
		{rootA, rootA},
		{rootA + "/child"},
		{poolA + "/sylve/virtual-machines/7310"},
		{poolA + "/sylve/jails/731"},
	} {
		if _, err := service.ValidateMigratedVMRoots(t.Context(), rid, invalid); err == nil {
			t.Fatalf("invalid manifest was accepted: %v", invalid)
		}
	}
}

func TestActivateMigratedDatasetRootsFailsOnAnyRoot(t *testing.T) {
	roots := []string{"pool-a/sylve/virtual-machines/41", "pool-b/sylve/virtual-machines/41"}
	visited := make([]string, 0, len(roots))
	err := activateMigratedDatasetRoots(t.Context(), roots, func(_ context.Context, root string) error {
		visited = append(visited, root)
		if strings.HasPrefix(root, "pool-b/") {
			return errors.New("readonly property failed")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "pool-b/sylve/virtual-machines/41") {
		t.Fatalf("second-root activation failure was not fatal: %v", err)
	}
	if !reflect.DeepEqual(visited, roots) {
		t.Fatalf("visited roots = %v, want %v", visited, roots)
	}
}

func TestGeneratedMigrationSnapshotMatcherPreservesUserPrefixes(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "sylve-migrate-initial-1700000000", want: true},
		{name: "sylve-migrate-final-1700000001", want: true},
		{name: "sylve-migrate-pre-migration-1700000002", want: true},
		{name: "sylve-migrated-user-1700000000", want: false},
		{name: "sylve-migrate-user-1700000000", want: false},
		{name: "sylve-migrate-final-not-a-time", want: false},
	} {
		if got := isGeneratedMigrationSnapshotName(test.name); got != test.want {
			t.Errorf("isGeneratedMigrationSnapshotName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestGeneratedMigrationSnapshotPathUsesExactRootBoundary(t *testing.T) {
	root := "zroot/sylve/virtual-machines/73"
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: root + "@sylve-migrate-initial-1700000000", want: true},
		{path: root + "/disk-0@sylve-migrate-final-1700000001", want: true},
		{path: root + "0@sylve-migrate-final-1700000001", want: false},
		{path: root + "/disk-0@sylve-migrate-user-1700000001", want: false},
		{path: root + "/disk-0@manual", want: false},
	} {
		if got := isGeneratedMigrationSnapshotPath(root, test.path); got != test.want {
			t.Errorf("isGeneratedMigrationSnapshotPath(%q, %q) = %t, want %t", root, test.path, got, test.want)
		}
	}
}

func TestMigrationSnapshotCleanupSurfacesFailureAndPreservesUserSnapshots(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS migration snapshot cleanup integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	root := pool + "/sylve/virtual-machines/732"
	zfstest.EnsureDataset(t, client, root+"/child")

	migrationSnapshot := root + "@sylve-migrate-final-1700000001"
	manualSnapshot := root + "@sylve-migrate-user-1700000001"
	for _, snapshot := range []string{migrationSnapshot, manualSnapshot} {
		if output, err := exec.Command("zfs", "snapshot", "-r", snapshot).CombinedOutput(); err != nil {
			t.Fatalf("create snapshot %s: %v\n%s", snapshot, err, output)
		}
	}

	const holdTag = "sylve-migration-cleanup-test"
	if output, err := exec.Command("zfs", "hold", holdTag, migrationSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("hold migration snapshot: %v\n%s", err, output)
	}

	service := &Service{GZFS: client}
	err := service.destroyMigratedDatasetSnapshots(t.Context(), []string{root})
	if err == nil || !strings.Contains(err.Error(), migrationSnapshot) {
		t.Fatalf("held snapshot cleanup result = %v", err)
	}
	if output, err := exec.Command("zfs", "list", "-H", "-t", "snapshot", migrationSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("held migration snapshot was removed: %v\n%s", err, output)
	}

	if output, err := exec.Command("zfs", "release", holdTag, migrationSnapshot).CombinedOutput(); err != nil {
		t.Fatalf("release migration snapshot hold: %v\n%s", err, output)
	}
	if err := service.destroyMigratedDatasetSnapshots(t.Context(), []string{root}); err != nil {
		t.Fatalf("retry migration snapshot cleanup: %v", err)
	}
	if output, err := exec.Command("zfs", "list", "-H", "-t", "snapshot", migrationSnapshot).CombinedOutput(); err == nil {
		t.Fatalf("migration snapshot survived cleanup retry: %s", output)
	}
	for _, snapshot := range []string{manualSnapshot, root + "/child@sylve-migrate-user-1700000001"} {
		if output, err := exec.Command("zfs", "list", "-H", "-t", "snapshot", snapshot).CombinedOutput(); err != nil {
			t.Fatalf("user snapshot %s was removed: %v\n%s", snapshot, err, output)
		}
	}
}
