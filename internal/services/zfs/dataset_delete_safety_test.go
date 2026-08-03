// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
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

func TestDatasetDeletionMethodsRejectPoolRootRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

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
			name: "bulk delete by GUID",
			delete: func() error {
				return service.BulkDeleteDataset(ctx, []string{child.GUID, root.GUID})
			},
		},
		{
			name: "bulk delete by name",
			delete: func() error {
				return service.BulkDeleteDatasetByNames(ctx, []string{child.Name, root.Name})
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
