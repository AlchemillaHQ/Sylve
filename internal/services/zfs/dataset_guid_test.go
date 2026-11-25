// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
)

type guidLookupRunner struct {
	onDisk   map[string]string
	commands []string
}

func (r *guidLookupRunner) Run(_ context.Context, _ io.Reader, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	if name != "zfs" || len(args) < 2 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	r.commands = append(r.commands, args[0])
	datasets := map[string]any{}
	add := func(dataset, guid string) {
		datasets[dataset] = map[string]any{
			"name":       dataset,
			"pool":       "tank",
			"type":       string(gzfs.DatasetTypeFilesystem),
			"properties": map[string]any{"guid": map[string]any{"value": guid}},
		}
	}
	switch args[0] {
	case "list":
		target := args[len(args)-2]
		if guid, ok := r.onDisk[target]; ok {
			add(target, guid)
		}
	case "get":
		for dataset, guid := range r.onDisk {
			add(dataset, guid)
		}
	default:
		return fmt.Errorf("unexpected zfs command: %v", args)
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"output_version": map[string]any{"command": "zfs " + args[0]},
		"datasets":       datasets,
	})
}

func cacheDatasets(t *testing.T, cached []*gzfs.Dataset) {
	t.Helper()
	encoded, err := MsgpackEncode(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetValue(datasetCacheKey(gzfs.DatasetTypeFilesystem), encoded, 60); err != nil {
		t.Fatal(err)
	}
}

func TestDatasetByGUIDUsesVerifiedCacheEntry(t *testing.T) {
	tests := []struct {
		name         string
		cached       []*gzfs.Dataset
		noCache      bool
		onDisk       map[string]string
		guid         string
		wantName     string
		wantCommands []string
	}{
		{
			name:         "cached and unchanged",
			cached:       []*gzfs.Dataset{{Name: "tank/data", GUID: "100"}, {Name: "tank/other", GUID: "200"}},
			onDisk:       map[string]string{"tank/data": "100", "tank/other": "200"},
			guid:         "100",
			wantName:     "tank/data",
			wantCommands: []string{"list"},
		},
		{
			name:         "renamed since caching",
			cached:       []*gzfs.Dataset{{Name: "tank/data", GUID: "100"}},
			onDisk:       map[string]string{"tank/renamed": "100"},
			guid:         "100",
			wantName:     "tank/renamed",
			wantCommands: []string{"list", "get", "list"},
		},
		{
			name:         "recreated with the same name",
			cached:       []*gzfs.Dataset{{Name: "tank/data", GUID: "100"}},
			onDisk:       map[string]string{"tank/data": "300"},
			guid:         "100",
			wantCommands: []string{"list", "get"},
		},
		{
			name:         "ambiguous cache",
			cached:       []*gzfs.Dataset{{Name: "tank/a", GUID: "100"}, {Name: "tank/b", GUID: "100"}},
			onDisk:       map[string]string{"tank/a": "100"},
			guid:         "100",
			wantName:     "tank/a",
			wantCommands: []string{"get", "list"},
		},
		{
			name:         "not cached",
			cached:       []*gzfs.Dataset{{Name: "tank/data", GUID: "100"}},
			onDisk:       map[string]string{"tank/data": "100", "tank/new": "400"},
			guid:         "400",
			wantName:     "tank/new",
			wantCommands: []string{"get", "list"},
		},
		{
			name:         "no cache database",
			noCache:      true,
			onDisk:       map[string]string{"tank/data": "100"},
			guid:         "100",
			wantName:     "tank/data",
			wantCommands: []string{"get", "list"},
		},
	}

	previous := db.CacheDB
	cache := db.SetupCache(&internal.SylveConfig{DataPath: t.TempDir()})
	t.Cleanup(func() {
		_ = cache.Close()
		db.CacheDB = previous
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.noCache {
				db.CacheDB = nil
				t.Cleanup(func() { db.CacheDB = cache })
			} else {
				cacheDatasets(t, test.cached)
			}
			runner := &guidLookupRunner{onDisk: test.onDisk}
			service := &Service{GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}

			dataset, err := service.DatasetByGUID(t.Context(), test.guid)
			if err != nil {
				t.Fatalf("DatasetByGUID returned an error: %v", err)
			}
			gotName := ""
			if dataset != nil {
				gotName = dataset.Name
				if dataset.GUID != test.guid {
					t.Fatalf("returned %s with GUID %s, want GUID %s", dataset.Name, dataset.GUID, test.guid)
				}
			}
			if gotName != test.wantName {
				t.Fatalf("dataset = %q, want %q", gotName, test.wantName)
			}
			if !reflect.DeepEqual(runner.commands, test.wantCommands) {
				t.Fatalf("zfs commands = %v, want %v", runner.commands, test.wantCommands)
			}
		})
	}
}
