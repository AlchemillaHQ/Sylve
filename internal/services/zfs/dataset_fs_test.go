// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type editFilesystemRunner struct {
	datasetName  string
	guid         string
	setCalls     int
	inheritCalls []string
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
	case "inherit":
		r.inheritCalls = append(r.inheritCalls, strings.Join(args[1:], " "))
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
				DB:        testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{}),
				GZFS:      gzfs.NewClient(gzfs.Options{Runner: runner}),
				syncMutex: &sync.Mutex{},
			}

			err := service.EditFilesystem(t.Context(), runner.guid, test.properties, nil)
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

func TestEditFilesystemInheritsProperties(t *testing.T) {
	tests := []struct {
		name        string
		datasetName string
		properties  map[string]string
		inherit     []string
		wantErr     error
		wantSet     int
		wantInherit []string
	}{
		{
			name:        "inherit only",
			datasetName: "tank/data",
			inherit:     []string{"compression", "atime"},
			wantInherit: []string{"compression tank/data", "atime tank/data"},
		},
		{
			name:        "set and inherit",
			datasetName: "tank/data",
			properties:  map[string]string{"quota": "1G"},
			inherit:     []string{"compression"},
			wantSet:     1,
			wantInherit: []string{"compression tank/data"},
		},
		{
			name:        "same property set and inherited",
			datasetName: "tank/data",
			properties:  map[string]string{"compression": "lz4"},
			inherit:     []string{"compression"},
			wantErr:     ErrInvalidRequest,
		},
		{
			name:        "nothing to edit",
			datasetName: "tank/data",
			wantErr:     ErrInvalidRequest,
		},
		{
			name:        "managed mountpoint",
			datasetName: "tank/sylve/jails/100",
			inherit:     []string{"mountpoint"},
			wantErr:     ErrConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &editFilesystemRunner{datasetName: test.datasetName, guid: "123"}
			service := &Service{
				DB:        testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{}),
				GZFS:      gzfs.NewClient(gzfs.Options{Runner: runner}),
				syncMutex: &sync.Mutex{},
			}

			err := service.EditFilesystem(t.Context(), runner.guid, test.properties, test.inherit)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("EditFilesystem error = %v; want %v", err, test.wantErr)
				}
				if runner.setCalls != 0 || len(runner.inheritCalls) != 0 {
					t.Fatalf("rejected edit ran commands: set=%d inherit=%v", runner.setCalls, runner.inheritCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("EditFilesystem returned an error: %v", err)
			}
			if runner.setCalls != test.wantSet {
				t.Fatalf("zfs set calls = %d; want %d", runner.setCalls, test.wantSet)
			}
			if !reflect.DeepEqual(runner.inheritCalls, test.wantInherit) {
				t.Fatalf("zfs inherit calls = %v; want %v", runner.inheritCalls, test.wantInherit)
			}
		})
	}
}
