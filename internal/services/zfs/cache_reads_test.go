// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/vmihailenco/msgpack/v5"
)

type liveSnapshotRunner struct {
	mu    sync.Mutex
	names []string
	lists int
	gate  chan struct{}
}

func (r *liveSnapshotRunner) Run(_ context.Context, _ io.Reader, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	if name != "zfs" || len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.lists++
	names := append([]string{}, r.names...)
	r.mu.Unlock()
	datasets := map[string]any{}
	for i, snapshot := range names {
		datasets[snapshot] = map[string]any{
			"name":       snapshot,
			"pool":       "tank",
			"type":       string(gzfs.DatasetTypeSnapshot),
			"properties": map[string]any{"guid": map[string]any{"value": fmt.Sprint(100 + i)}},
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"output_version": map[string]any{"command": "zfs list"},
		"datasets":       datasets,
	})
}

func (r *liveSnapshotRunner) listCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lists
}

func newCacheReadService(t *testing.T, runner gzfs.Runner) *Service {
	t.Helper()
	previous := db.CacheDB
	cache := db.SetupCache(&internal.SylveConfig{DataPath: t.TempDir()})
	t.Cleanup(func() {
		_ = cache.Close()
		db.CacheDB = previous
	})
	return &Service{
		DB:                        testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}),
		GZFS:                      gzfs.NewClient(gzfs.Options{Runner: runner}),
		pendingCacheInvalidations: map[string]uint64{},
	}
}

func stubSnapshotSummaryList(t *testing.T, output string) *int {
	t.Helper()
	previous := runSnapshotSummaryList
	calls := 0
	runSnapshotSummaryList = func(context.Context) (string, error) {
		calls++
		return output, nil
	}
	t.Cleanup(func() { runSnapshotSummaryList = previous })
	return &calls
}

func summaryNames(summaries []zfsServiceInterfaces.SnapshotSummary) []string {
	names := []string{}
	for _, summary := range summaries {
		names = append(names, summary.Name)
	}
	return names
}

func TestSnapshotSummariesReadLiveWhileStale(t *testing.T) {
	service := newCacheReadService(t, &liveSnapshotRunner{})
	calls := stubSnapshotSummaryList(t, "tank/data@new\t200\t1700000000\t0\t0\t-\t0\t7\n")
	clearStale := func() {
		t.Helper()
		if err := service.DB.Where("kind = ?", db.ZFSCacheKindSnapshot).Delete(&models.ZFSCacheInvalidation{}).Error; err != nil {
			t.Fatal(err)
		}
	}

	read := func() []string {
		t.Helper()
		summaries, err := service.snapshotSummariesForRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return summaryNames(summaries)
	}

	if got := read(); len(got) != 1 || got[0] != "tank/data@new" || *calls != 1 {
		t.Fatalf("missing cache: got %v after %d live reads", got, *calls)
	}
	if _, written := db.GetValue(snapshotSummaryCacheKey); written || !service.cacheStale(t.Context(), db.ZFSCacheKindSnapshot) {
		t.Fatalf("a missing cache was written by a read (%v) or not marked stale", written)
	}
	clearStale()
	if err := storeSnapshotSummaries([]zfsServiceInterfaces.SnapshotSummary{{Name: "tank/data@old", GUID: "100"}}, 60); err != nil {
		t.Fatal(err)
	}

	if got := read(); len(got) != 1 || got[0] != "tank/data@old" || *calls != 1 {
		t.Fatalf("current cache: got %v after %d live reads", got, *calls)
	}
	if err := db.InvalidateZFSCache(service.DB, db.ZFSCacheKindSnapshot); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 1 || got[0] != "tank/data@new" || *calls != 2 {
		t.Fatalf("stale cache: got %v after %d live reads", got, *calls)
	}
	var cached []zfsServiceInterfaces.SnapshotSummary
	raw, _ := db.GetValue(snapshotSummaryCacheKey)
	if err := msgpack.Unmarshal(raw, &cached); err != nil || summaryNames(cached)[0] != "tank/data@old" {
		t.Fatalf("a live read replaced the cache: %v, %v", summaryNames(cached), err)
	}

	clearStale()
	service.pendingCacheInvalidations[db.ZFSCacheKindSnapshot] = 1
	if got := read(); got[0] != "tank/data@new" || *calls != 3 {
		t.Fatalf("an unsaved invalidation did not force a live read: %v after %d", got, *calls)
	}
	delete(service.pendingCacheInvalidations, db.ZFSCacheKindSnapshot)
	if got := read(); got[0] != "tank/data@old" || *calls != 3 {
		t.Fatalf("refreshed cache was not used again: %v after %d", got, *calls)
	}
}

func TestDatasetsForReadLiveWhileStaleOrMissing(t *testing.T) {
	runner := &liveSnapshotRunner{names: []string{"tank/data@first"}}
	service := newCacheReadService(t, runner)
	stubSnapshotSummaryList(t, "tank/data@first\t100\t1700000000\t0\t0\t-\t0\t7\n")
	read := func(datasetType gzfs.DatasetType) string {
		t.Helper()
		datasets, err := service.datasetsForRead(t.Context(), datasetType)
		if err != nil || len(datasets) != 1 {
			t.Fatalf("datasetsForRead = %v, %v", datasets, err)
		}
		return datasets[0].Name
	}

	if got := read(gzfs.DatasetTypeSnapshot); got != "tank/data@first" || runner.listCount() != 1 {
		t.Fatalf("cache miss: got %s after %d lists", got, runner.listCount())
	}
	if _, written := db.GetValue(datasetCacheKey(gzfs.DatasetTypeSnapshot)); written || !service.cacheStale(t.Context(), db.ZFSCacheKindSnapshot) {
		t.Fatalf("a missing cache was written by a read (%v) or not marked stale", written)
	}
	if err := service.processCacheInvalidations(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("background refresh failed: %v", err)
	}
	if service.cacheStale(t.Context(), db.ZFSCacheKindSnapshot) || runner.listCount() != 2 {
		t.Fatalf("background refresh did not complete: %d lists", runner.listCount())
	}

	runner.names = []string{"tank/data@second"}
	if got := read(gzfs.DatasetTypeSnapshot); got != "tank/data@first" || runner.listCount() != 2 {
		t.Fatalf("the refreshed cache was not used: got %s after %d lists", got, runner.listCount())
	}
	if got := read(gzfs.DatasetTypeAll); got != "tank/data@second" || runner.listCount() != 3 {
		t.Fatalf("an uncached listing was not read live: got %s after %d lists", got, runner.listCount())
	}
	if service.cacheStale(t.Context(), db.ZFSCacheKindGenericDataset) {
		t.Fatal("an uncached listing marked the file system cache stale")
	}
	if err := db.InvalidateZFSCache(service.DB, db.ZFSCacheKindGenericDataset); err != nil {
		t.Fatal(err)
	}
	if got := read(gzfs.DatasetTypeSnapshot); got != "tank/data@first" || runner.listCount() != 3 {
		t.Fatalf("a file system change made snapshot reads live: got %s after %d lists", got, runner.listCount())
	}
	if err := db.InvalidateZFSCache(service.DB, db.ZFSCacheKindSnapshot); err != nil {
		t.Fatal(err)
	}
	if got := read(gzfs.DatasetTypeSnapshot); got != "tank/data@second" || runner.listCount() != 4 {
		t.Fatalf("stale cache: got %s after %d lists", got, runner.listCount())
	}
}

func TestGetPaginatedDatasetsShowsChangesWhileStale(t *testing.T) {
	runner := &liveSnapshotRunner{names: []string{"tank/data@new"}}
	service := newCacheReadService(t, runner)
	stubSnapshotSummaryList(t, "tank/data@new\t100\t1700000000\t4096\t8192\ttank/clone\t1\t7\n")
	encoded, err := MsgpackEncode([]*gzfs.Dataset{{Name: "tank/data@old", GUID: "900", Type: gzfs.DatasetTypeSnapshot}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetValue(datasetCacheKey(gzfs.DatasetTypeSnapshot), encoded, 60); err != nil {
		t.Fatal(err)
	}
	if err := storeSnapshotSummaries([]zfsServiceInterfaces.SnapshotSummary{{Name: "tank/data@old", GUID: "900"}}, 60); err != nil {
		t.Fatal(err)
	}
	if err := db.InvalidateZFSCache(service.DB, db.ZFSCacheKindSnapshot); err != nil {
		t.Fatal(err)
	}

	response, err := service.GetPaginatedDatasets(t.Context(), &zfsServiceInterfaces.PaginatedDatasetsRequest{
		DatasetType: gzfs.DatasetTypeSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Name != "tank/data@new" {
		t.Fatalf("stale list was served: %#v", response.Data)
	}
	properties := response.Data[0].Properties
	if properties["creation"].Value != "1700000000" || properties["clones"].Value != "tank/clone" || properties["userrefs"].Value != "1" {
		t.Fatalf("live details were not attached: %#v", properties)
	}
}

func TestConcurrentStaleReadsShareResultsSafely(t *testing.T) {
	runner := &liveSnapshotRunner{names: []string{"tank/data@new"}, gate: make(chan struct{})}
	service := newCacheReadService(t, runner)
	stubSnapshotSummaryList(t, "tank/data@new\t100\t1700000000\t0\t0\t-\t0\t7\n")
	if err := db.InvalidateZFSCache(service.DB, db.ZFSCacheKindSnapshot); err != nil {
		t.Fatal(err)
	}

	const readers = 8
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := service.GetPaginatedDatasets(t.Context(), &zfsServiceInterfaces.PaginatedDatasetsRequest{
				DatasetType: gzfs.DatasetTypeSnapshot,
			})
			if err == nil && (len(response.Data) != 1 || response.Data[0].Properties["creation"].Value != "1700000000") {
				err = fmt.Errorf("unexpected page: %#v", response.Data)
			}
			errs <- err
		}()
	}
	close(runner.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if lists := runner.listCount(); lists < 1 || lists > readers {
		t.Fatalf("live lists = %d", lists)
	}
}
