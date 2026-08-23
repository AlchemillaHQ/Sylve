// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestValidateDatasetDeletionTargetsRejectsPoolRoot(t *testing.T) {
	root := &gzfs.Dataset{Name: "zroot", Pool: "zroot"}
	child := &gzfs.Dataset{Name: "zroot/data", Pool: "zroot"}

	if err := validateDatasetDeletionTargets(child); err != nil {
		t.Fatalf("ordinary child dataset was rejected: %v", err)
	}

	if err := validateDatasetDeletionTargets(child, root); !errors.Is(err, ErrCannotDeletePoolRootDataset) {
		t.Fatalf("pool root deletion was not rejected: %v", err)
	}
}

func TestIntegrationDatasetDeletionMethodsRejectPoolRootRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)

	ctx := context.Background()
	childName := pool + "/child"
	zfstest.EnsureDataset(t, client, childName)

	root, err := client.ZFS.Get(ctx, pool, false)
	if err != nil || root == nil {
		t.Fatalf("get pool root dataset: %v", err)
	}
	child, err := client.ZFS.Get(ctx, childName, false)
	if err != nil || child == nil {
		t.Fatalf("get child dataset: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})
	service := &Service{
		DB:        database,
		GZFS:      client,
		syncMutex: &sync.Mutex{},
	}

	tests := []struct {
		name   string
		delete func() error
	}{
		{
			name: "single filesystem delete",
			delete: func() error {
				return service.DeleteFilesystem(ctx, root.GUID)
			},
		},
		{
			name: "bulk delete by exact target",
			delete: func() error {
				return service.BulkDeleteDataset(ctx, []zfsServiceInterfaces.DatasetDeletionTarget{
					{Name: child.Name, GUID: child.GUID},
					{Name: root.Name, GUID: root.GUID},
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.delete(); !errors.Is(err, ErrCannotDeletePoolRootDataset) {
				t.Fatalf("pool root deletion was not rejected: %v", err)
			}

			for _, name := range []string{pool, childName} {
				dataset, err := client.ZFS.Get(ctx, name, false)
				if err != nil || dataset == nil {
					t.Fatalf("dataset %s was modified after rejected deletion: %v", name, err)
				}
			}
		})
	}
}

func TestNormalizeDatasetDeletionTargets(t *testing.T) {
	targets, err := normalizeDatasetDeletionTargets([]zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: " /tank/one/ ", GUID: " guid-one "},
		{Name: "tank/one", GUID: "guid-one"},
		{Name: "tank/two@snapshot", GUID: "guid-two"},
	})
	if err != nil {
		t.Fatalf("normalizeDatasetDeletionTargets returned an error: %v", err)
	}
	want := []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: "tank/one", GUID: "guid-one"},
		{Name: "tank/two@snapshot", GUID: "guid-two"},
	}
	if len(targets) != len(want) {
		t.Fatalf("normalized target count = %d, want %d", len(targets), len(want))
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("normalized target %d = %#v, want %#v", i, targets[i], want[i])
		}
	}

	invalid := [][]zfsServiceInterfaces.DatasetDeletionTarget{
		nil,
		{{Name: "tank/one"}},
		{{GUID: "guid-one"}},
		{{Name: "tank/one", GUID: "guid-one"}, {Name: "tank/one", GUID: "guid-two"}},
		{{Name: "tank/one", GUID: "guid-one"}, {Name: "tank/two", GUID: "guid-one"}},
	}
	for _, test := range invalid {
		if _, err := normalizeDatasetDeletionTargets(test); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid targets %#v returned %v, want ErrInvalidRequest", test, err)
		}
	}
}

func TestIndependentDatasetDeletionRoots(t *testing.T) {
	parent := &gzfs.Dataset{Name: "tank/data"}
	child := &gzfs.Dataset{Name: "tank/data/child"}
	snapshot := &gzfs.Dataset{Name: "tank/data@snapshot"}
	other := &gzfs.Dataset{Name: "tank/other"}

	roots := independentDatasetDeletionRoots([]*gzfs.Dataset{child, snapshot, other, parent})
	if len(roots) != 2 || roots[0] != other || roots[1] != parent {
		t.Fatalf("independent roots = %#v, want [other parent]", roots)
	}
}

func TestIntegrationBulkDeleteDatasetSnapshotByExactTargetRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	datasetName := pool + "/snapshots"
	zfstest.EnsureDataset(t, client, datasetName)
	snapshot, err := client.ZFS.Snapshot(ctx, datasetName, "bulk-delete", false)
	if err != nil || snapshot == nil {
		t.Fatalf("create snapshot: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})
	var notifiedGUIDs []string
	service := &Service{
		DB:        database,
		GZFS:      client,
		syncMutex: &sync.Mutex{},
		OnDatasetsDeleted: func(_ context.Context, guids []string) error {
			notifiedGUIDs = append(notifiedGUIDs, guids...)
			return nil
		},
	}

	staleTarget := []zfsServiceInterfaces.DatasetDeletionTarget{{
		Name: snapshot.Name,
		GUID: "stale-guid",
	}}
	if err := service.BulkDeleteDataset(ctx, staleTarget); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale snapshot identity returned %v, want ErrConflict", err)
	}
	if existing, err := client.ZFS.Get(ctx, snapshot.Name, false); err != nil || existing == nil {
		t.Fatalf("snapshot changed after rejected stale target: %v", err)
	}

	target := []zfsServiceInterfaces.DatasetDeletionTarget{{Name: snapshot.Name, GUID: snapshot.GUID}}
	if err := service.BulkDeleteDataset(ctx, target); err != nil {
		t.Fatalf("bulk delete snapshot: %v", err)
	}
	if existing, err := client.ZFS.Get(ctx, snapshot.Name, false); err == nil && existing != nil {
		t.Fatalf("snapshot %s still exists after deletion", snapshot.Name)
	}
	if len(notifiedGUIDs) != 1 || notifiedGUIDs[0] != snapshot.GUID {
		t.Fatalf("notified GUIDs = %#v, want [%s]", notifiedGUIDs, snapshot.GUID)
	}
}
