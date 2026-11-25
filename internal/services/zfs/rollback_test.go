// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestSameMembers(t *testing.T) {
	for _, test := range []struct {
		a, b []string
		want bool
	}{
		{nil, []string{}, true},
		{[]string{"1", "2"}, []string{"2", "1"}, true},
		{[]string{"1", "1"}, []string{"1"}, true},
		{[]string{"1"}, []string{"1", "2"}, false},
		{[]string{"1"}, []string{}, false},
	} {
		if got := sameMembers(test.a, test.b); got != test.want {
			t.Errorf("sameMembers(%v, %v) = %v, want %v", test.a, test.b, got, test.want)
		}
	}
}

func TestIntegrationRollbackSnapshotChecksNewerSnapshotsAndUseRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	name := pool + "/rollback"
	zfstest.EnsureDataset(t, client, name)
	take := func(label string) *gzfs.Dataset {
		t.Helper()
		snapshot, err := client.ZFS.Snapshot(ctx, name, label, false)
		if err != nil || snapshot == nil {
			t.Fatalf("snapshot %s: %v", label, err)
		}
		return snapshot
	}
	first, second, third := take("first"), take("second"), take("third")
	exists := func(snapshot *gzfs.Dataset) bool {
		t.Helper()
		found, err := client.ZFS.Get(ctx, snapshot.Name, false)
		return err == nil && found != nil
	}
	filesystem, err := client.ZFS.Get(ctx, name, false)
	if err != nil || filesystem == nil {
		t.Fatalf("get filesystem: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &clusterModels.ReplicationPolicy{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &vmModels.VMStorageDataset{},
		&containerModels.Network{}, &containerModels.Container{},
	)
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}

	newer, err := service.snapshotsNewerThan(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !sameMembers(newer, []string{second.GUID, third.GUID}) {
		t.Fatalf("newer snapshots = %v, want %s and %s", newer, second.GUID, third.GUID)
	}
	if newer, err := service.snapshotsNewerThan(ctx, third); err != nil || !reflect.DeepEqual(newer, []string{}) {
		t.Fatalf("latest snapshot has newer snapshots %v, %v", newer, err)
	}

	err = service.RollbackSnapshot(ctx, first.GUID, first.Name, true, []string{second.GUID})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("an outdated list of newer snapshots returned %v, want ErrConflict", err)
	}
	if !exists(second) || !exists(third) {
		t.Fatal("a refused rollback destroyed snapshots")
	}

	container := containerModels.Container{
		OCID: 1, Name: "web", Pool: pool, Image: "example", OS: "linux",
		Mounts: []containerModels.Mount{{Type: containerModels.MountDataset, Source: name, Target: "/data", GUID: filesystem.GUID}},
	}
	if err := database.Create(&container).Error; err != nil {
		t.Fatal(err)
	}
	err = service.RollbackSnapshot(ctx, first.GUID, first.Name, true, []string{second.GUID, third.GUID})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("rolling back a file system mounted in a container returned %v, want ErrConflict", err)
	}
	if !exists(second) || !exists(third) {
		t.Fatal("a refused rollback destroyed snapshots")
	}
	if err := database.Delete(&container).Error; err != nil {
		t.Fatal(err)
	}

	if err := service.RollbackSnapshot(ctx, first.GUID, first.Name, true, []string{third.GUID, second.GUID}); err != nil {
		t.Fatalf("rollback with the current list of newer snapshots failed: %v", err)
	}
	if !exists(first) || exists(second) || exists(third) {
		t.Fatalf("rollback left first=%v second=%v third=%v", exists(first), exists(second), exists(third))
	}
}

func TestIntegrationRollbackSnapshotRefusesSystemFilesystemRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	name := pool + "/rollback-os"
	if _, err := client.ZFS.CreateFilesystem(ctx, name, map[string]string{
		"canmount":   "off",
		"mountpoint": "/var/sylve-rollback-test",
	}); err != nil {
		t.Fatalf("create filesystem: %v", err)
	}
	first, err := client.ZFS.Snapshot(ctx, name, "first", false)
	if err != nil || first == nil {
		t.Fatalf("snapshot: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &clusterModels.ReplicationPolicy{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &vmModels.VMStorageDataset{},
	)
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}
	err = service.RollbackSnapshot(ctx, first.GUID, first.Name, false, []string{})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "system_filesystem") {
		t.Fatalf("rolling back an operating-system file system returned %v, want a conflict", err)
	}
}

func TestIntegrationRollbackSnapshotRefusesSylveManagedDatasetRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	name := pool + "/sylve/jails/105"
	zfstest.EnsureDataset(t, client, name)
	first, err := client.ZFS.Snapshot(ctx, name, "first", false)
	if err != nil || first == nil {
		t.Fatalf("snapshot: %v", err)
	}
	second, err := client.ZFS.Snapshot(ctx, name, "second", false)
	if err != nil || second == nil {
		t.Fatalf("snapshot: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &clusterModels.ReplicationPolicy{},
		&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &vmModels.VMStorageDataset{},
	)
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}
	err = service.RollbackSnapshot(ctx, first.GUID, first.Name, true, []string{second.GUID})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "managed_dataset") {
		t.Fatalf("rolling back a Sylve-managed dataset returned %v, want a conflict", err)
	}
	if kept, err := client.ZFS.Get(ctx, second.Name, false); err != nil || kept == nil {
		t.Fatalf("a refused rollback destroyed %s: %v", second.Name, err)
	}
}
