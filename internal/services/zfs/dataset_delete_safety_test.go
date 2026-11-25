// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

type datasetVMRuntime struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	status string
}

func (r datasetVMRuntime) GetLvDomain(uint) (*libvirtServiceInterfaces.LvDomain, error) {
	return &libvirtServiceInterfaces.LvDomain{Status: r.status}, nil
}

func TestDatasetUsagePreservesVMRuntimePolicy(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	vm := vmModels.VM{RID: 701, Name: "dataset-user"}
	if err := database.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}
	storage := vmModels.Storage{VMID: vm.ID, Dataset: vmModels.VMStorageDataset{GUID: "300", Name: "tank/data"}}
	if err := database.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database}
	for _, status := range []string{"Running", "Paused", "Stopped"} {
		t.Run(status, func(t *testing.T) {
			service.Libvirt = datasetVMRuntime{status: status}
			for _, failEarly := range []bool{false, true} {
				want := failEarly || status == "Running" || status == "Paused"
				if service.IsDatasetInUse("300", failEarly) != want {
					t.Fatal("dataset usage disagrees with VM runtime policy", status, failEarly)
				}
				if service.IsDatasetInUse("999", failEarly) {
					t.Fatal("unreferenced dataset was treated as VM storage")
				}
			}
		})
	}
}

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

func TestValidateCriticalDatasetDeletion(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "tank/sylve", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/virtual-machines", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/jails", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/downloads", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/containers", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/containers/images", want: "cannot_delete_critical_filesystem"},
		{name: "tank/sylve/containers/images/layer", want: "cannot_delete_container_image_layer"},
		{name: "tank/sylve/containers/images/layer@snapshot", want: "cannot_delete_container_image_layer"},
		{name: "tank/sylve/jails/101"},
		{name: "tank/sylve/containers/web"},
		{name: "tank/data/sylve"},
		{name: "tank/sylve-backup"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCriticalDatasetDeletion(&gzfs.Dataset{Name: test.name, Pool: "tank"})
			if test.want == "" {
				if err != nil {
					t.Fatalf("deletion was rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("deletion returned %v, want ErrConflict with %s", err, test.want)
			}
		})
	}
}

func TestIsSystemMountpoint(t *testing.T) {
	tests := map[string]bool{
		"/":            true,
		"/boot":        true,
		"/etc":         true,
		"/home":        true,
		"/root":        true,
		"/tmp":         true,
		"/usr":         true,
		"/usr/":        true,
		"/usr/ports":   true,
		"/var":         true,
		"/var/log":     true,
		"/home/hayzam": false,
		"/tmp/scratch": false,
		"/usr2":        false,
		"/variety":     false,
		"/zroot/data":  false,
		"none":         false,
		"legacy":       false,
		"-":            false,
		"":             false,
	}

	for mountpoint, want := range tests {
		if got := isSystemMountpoint(mountpoint); got != want {
			t.Errorf("isSystemMountpoint(%q) = %v, want %v", mountpoint, got, want)
		}
	}
}

func TestValidateSystemFilesystemDeletion(t *testing.T) {
	filesystem := func(name, mountpoint string) *gzfs.Dataset {
		return &gzfs.Dataset{Name: name, Mountpoint: mountpoint, Type: gzfs.DatasetTypeFilesystem}
	}
	tests := []struct {
		name     string
		affected []*gzfs.Dataset
		blocked  bool
	}{
		{
			name:     "boot environment container",
			affected: []*gzfs.Dataset{filesystem("zroot/ROOT", "none"), filesystem("zroot/ROOT/default", "/")},
			blocked:  true,
		},
		{
			name:     "operating system tree",
			affected: []*gzfs.Dataset{filesystem("zroot/var/log", "/var/log")},
			blocked:  true,
		},
		{
			name:     "home container",
			affected: []*gzfs.Dataset{filesystem("zroot/home", "/home"), filesystem("zroot/home/hayzam", "/home/hayzam")},
			blocked:  true,
		},
		{
			name:     "single home folder",
			affected: []*gzfs.Dataset{filesystem("zroot/home/hayzam", "/home/hayzam")},
		},
		{
			name:     "data file system",
			affected: []*gzfs.Dataset{filesystem("zroot/data", "/zroot/data"), filesystem("zroot/data/media", "/zroot/data/media")},
		},
		{
			name: "boot environment snapshot",
			affected: []*gzfs.Dataset{
				{Name: "zroot/ROOT/default@upgrade", Mountpoint: "/", Type: gzfs.DatasetTypeSnapshot},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSystemFilesystemDeletion(test.affected)
			if test.blocked && !errors.Is(err, ErrCannotDeleteSystemFilesystem) {
				t.Fatalf("deletion returned %v, want ErrCannotDeleteSystemFilesystem", err)
			}
			if !test.blocked && err != nil {
				t.Fatalf("deletion was rejected: %v", err)
			}
		})
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

	database := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
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
		{Name: "backup/two@snapshot", GUID: "guid-two"},
	})
	if err != nil {
		t.Fatalf("normalizeDatasetDeletionTargets returned an error: %v", err)
	}
	want := []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: "tank/one", GUID: "guid-one"},
		{Name: "tank/two@snapshot", GUID: "guid-two"},
		{Name: "backup/two@snapshot", GUID: "guid-two"},
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

func TestIntegrationBulkDeleteDatasetSnapshotKeepsSameNamedDescendantsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	parentName := pool + "/recursive-set"
	childName := parentName + "/child"
	zfstest.EnsureDataset(t, client, parentName)
	zfstest.EnsureDataset(t, client, childName)
	snapshot, err := client.ZFS.Snapshot(ctx, parentName, "set", true)
	if err != nil || snapshot == nil {
		t.Fatalf("create recursive snapshot: %v", err)
	}

	database := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	service := &Service{
		DB:        database,
		GZFS:      client,
		syncMutex: &sync.Mutex{},
	}

	target := []zfsServiceInterfaces.DatasetDeletionTarget{{Name: snapshot.Name, GUID: snapshot.GUID}}
	if err := service.BulkDeleteDataset(ctx, target); err != nil {
		t.Fatalf("bulk delete snapshot: %v", err)
	}
	if existing, err := client.ZFS.Get(ctx, snapshot.Name, false); err == nil && existing != nil {
		t.Fatalf("snapshot %s still exists after deletion", snapshot.Name)
	}
	if existing, err := client.ZFS.Get(ctx, childName+"@set", false); err != nil || existing == nil {
		t.Fatalf("same-named descendant snapshot was deleted: %v", err)
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

	database := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
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
