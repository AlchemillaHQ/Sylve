// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

type poolListingRunner struct {
	present map[string]map[string]string
	fail    error
	listed  []string
}

func (r *poolListingRunner) Run(ctx context.Context, _ io.Reader, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name != "zfs" || len(args) < 2 || args[0] != "list" {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	pool := args[len(args)-2]
	r.listed = append(r.listed, pool)
	if r.fail != nil {
		return r.fail
	}
	datasets := map[string]any{}
	for dataset, guid := range r.present[pool] {
		datasets[dataset] = map[string]any{
			"name":       dataset,
			"pool":       pool,
			"type":       string(gzfs.DatasetTypeFilesystem),
			"properties": map[string]any{"guid": map[string]any{"value": guid}},
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"output_version": map[string]any{"command": "zfs list"},
		"datasets":       datasets,
	})
}

func TestReconcileFailedDeletionCleansUpOnlyRemovedDatasets(t *testing.T) {
	affected := []*gzfs.Dataset{
		{Name: "tank/gone", GUID: "1", Pool: "tank", Type: gzfs.DatasetTypeFilesystem},
		{Name: "tank/kept", GUID: "2", Pool: "tank", Type: gzfs.DatasetTypeFilesystem},
		{Name: "tank/kept@gone", GUID: "3", Type: gzfs.DatasetTypeSnapshot},
		{Name: "backup/kept", GUID: "4", Pool: "backup", Type: gzfs.DatasetTypeFilesystem},
		{GUID: "5", Type: gzfs.DatasetTypeFilesystem},
		nil,
	}
	present := map[string]map[string]string{
		"tank":   {"tank": "10", "tank/kept": "2"},
		"backup": {"backup": "20", "backup/kept": "4"},
	}
	destroyErr := errors.New("cannot destroy 'tank/kept': dataset is busy")
	notifyErr := errors.New("samba unavailable")

	tests := []struct {
		name         string
		listFails    bool
		notifyErr    error
		wantNotified []string
		wantNotify   bool
	}{
		{name: "removed datasets are reconciled", wantNotified: []string{"1", "3"}, wantNotify: true},
		{name: "reconcile failures are reported", notifyErr: notifyErr, wantNotified: []string{"1", "3"}, wantNotify: true},
		{name: "nothing is cleaned up when the check fails", listFails: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &poolListingRunner{present: present}
			if test.listFails {
				runner.fail = errors.New("pool is unavailable")
			}
			var notified []string
			called := false
			service := &Service{
				DB:                        testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}),
				GZFS:                      gzfs.NewClient(gzfs.Options{Runner: runner}),
				pendingCacheInvalidations: map[string]uint64{},
				OnDatasetsDeleted: func(_ context.Context, guids []string) error {
					called = true
					notified = append(notified, guids...)
					return test.notifyErr
				},
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := service.reconcileFailedDeletion(ctx, affected, destroyErr)
			if !errors.Is(err, destroyErr) {
				t.Fatalf("error = %v, want the destroy error", err)
			}
			if test.notifyErr != nil && !errors.Is(err, test.notifyErr) {
				t.Fatalf("error = %v, want the reconcile error too", err)
			}
			if test.notifyErr == nil && err != destroyErr {
				t.Fatalf("error = %v, want the destroy error unchanged", err)
			}
			if called != test.wantNotify {
				t.Fatalf("dependent services notified = %v, want %v", called, test.wantNotify)
			}
			sort.Strings(notified)
			if test.wantNotify && !reflect.DeepEqual(notified, test.wantNotified) {
				t.Fatalf("notified GUIDs = %v, want %v", notified, test.wantNotified)
			}
			for _, kind := range []string{db.ZFSCacheKindGenericDataset, db.ZFSCacheKindSnapshot} {
				if !service.cacheStale(t.Context(), kind) {
					t.Fatalf("%s cache was not marked stale", kind)
				}
			}
		})
	}
}

func TestIntegrationBulkDeleteDatasetPartialFailureReconcilesRemovedDatasetsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	goneName := pool + "/partial-gone"
	heldName := pool + "/partial-held"
	zfstest.EnsureDataset(t, client, goneName)
	zfstest.EnsureDataset(t, client, heldName)
	held, err := client.ZFS.Snapshot(ctx, heldName, "keep", false)
	if err != nil || held == nil {
		t.Fatalf("create snapshot: %v", err)
	}
	const holdTag = "sylve-partial-delete-test"
	if output, err := exec.Command("zfs", "hold", holdTag, held.Name).CombinedOutput(); err != nil {
		t.Fatalf("hold snapshot: %v: %s", err, output)
	}
	defer func() {
		if output, err := exec.Command("zfs", "release", holdTag, held.Name).CombinedOutput(); err != nil {
			t.Errorf("release hold: %v: %s", err, output)
		}
	}()
	gone, err := client.ZFS.Get(ctx, goneName, false)
	if err != nil || gone == nil {
		t.Fatalf("get filesystem: %v", err)
	}

	var notified []string
	service := &Service{
		DB:        testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{}, &models.ZFSCacheInvalidation{}),
		GZFS:      client,
		syncMutex: &sync.Mutex{},
		OnDatasetsDeleted: func(_ context.Context, guids []string) error {
			notified = append(notified, guids...)
			return nil
		},
	}

	err = service.BulkDeleteDataset(ctx, []zfsServiceInterfaces.DatasetDeletionTarget{
		{Name: gone.Name, GUID: gone.GUID},
		{Name: held.Name, GUID: held.GUID},
	})
	if err == nil {
		t.Fatal("deleting a held snapshot did not fail")
	}
	if existing, err := client.ZFS.Get(ctx, goneName, false); err == nil && existing != nil {
		t.Fatalf("%s was not deleted before the failure", goneName)
	}
	if existing, err := client.ZFS.Get(ctx, held.Name, false); err != nil || existing == nil {
		t.Fatalf("held snapshot is missing: %v", err)
	}
	if !reflect.DeepEqual(notified, []string{gone.GUID}) {
		t.Fatalf("notified GUIDs = %v, want only the deleted filesystem %s", notified, gone.GUID)
	}
	if !service.cacheStale(ctx, db.ZFSCacheKindGenericDataset) || !service.cacheStale(ctx, db.ZFSCacheKindSnapshot) {
		t.Fatal("caches were not marked stale after a partial deletion")
	}
}
