// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type editFilesystemRunner struct {
	datasetName string
	guid        string
	setCalls    int
}

func (r *editFilesystemRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
	name string,
	args ...string,
) error {
	if name != "zfs" || len(args) == 0 {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}

	switch args[0] {
	case "get", "list":
		return json.NewEncoder(stdout).Encode(map[string]any{
			"output_version": map[string]any{"command": "zfs " + args[0]},
			"datasets": map[string]any{
				r.datasetName: map[string]any{
					"name": r.datasetName,
					"pool": "tank",
					"type": string(gzfs.DatasetTypeFilesystem),
					"properties": map[string]any{
						"guid":       map[string]any{"value": r.guid},
						"mountpoint": map[string]any{"value": "/mnt/" + r.datasetName},
					},
				},
			},
		})
	case "set":
		r.setCalls++
		return nil
	default:
		return fmt.Errorf("unexpected zfs command: %v", args)
	}
}

func TestEditFilesystemProtectsManagedDatasetMountpoints(t *testing.T) {
	tests := []struct {
		name         string
		datasetName  string
		properties   map[string]string
		wantConflict bool
	}{
		{
			name:         "namespace root",
			datasetName:  "tank/sylve",
			properties:   map[string]string{"mountpoint": "/other"},
			wantConflict: true,
		},
		{
			name:         "namespace descendant",
			datasetName:  "tank/sylve/jails/100",
			properties:   map[string]string{"mountpoint": "/other"},
			wantConflict: true,
		},
		{
			name:        "similar name",
			datasetName: "tank/sylve-data",
			properties:  map[string]string{"mountpoint": "/other"},
		},
		{
			name:        "unrelated property",
			datasetName: "tank/sylve/jails/100",
			properties:  map[string]string{"compression": "zstd"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &editFilesystemRunner{datasetName: test.datasetName, guid: "123"}
			service := &Service{
				DB:        testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}),
				GZFS:      gzfs.NewClient(gzfs.Options{Runner: runner}),
				syncMutex: &sync.Mutex{},
			}

			err := service.EditFilesystem(t.Context(), runner.guid, test.properties)
			if test.wantConflict {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("EditFilesystem error = %v; want conflict", err)
				}
				if runner.setCalls != 0 {
					t.Fatalf("zfs set calls = %d; want 0", runner.setCalls)
				}
				return
			}

			if err != nil {
				t.Fatalf("EditFilesystem returned an error: %v", err)
			}
			if runner.setCalls != 1 {
				t.Fatalf("zfs set calls = %d; want 1", runner.setCalls)
			}
		})
	}
}
