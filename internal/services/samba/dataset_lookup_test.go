// SPDX-License-Identifier: BSD-2-Clause

package samba

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/alchemillahq/gzfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
)

type lookupRunner struct {
	commands []string
}

func (r *lookupRunner) Run(_ context.Context, _ io.Reader, stdout io.Writer, _ io.Writer, name string, args ...string) error {
	if name != "zfs" || len(args) == 0 || (args[0] != "get" && args[0] != "list") {
		return fmt.Errorf("unexpected command: %s %v", name, args)
	}
	r.commands = append(r.commands, args[0])
	return json.NewEncoder(stdout).Encode(map[string]any{
		"output_version": map[string]any{"command": "zfs " + args[0]},
		"datasets": map[string]any{
			"tank/share": map[string]any{
				"name":       "tank/share",
				"pool":       "tank",
				"type":       string(gzfs.DatasetTypeFilesystem),
				"properties": map[string]any{"guid": map[string]any{"value": "100"}},
			},
		},
	})
}

type lookupZFS struct {
	zfsServiceInterfaces.ZfsServiceInterface
	guids []string
}

func (f *lookupZFS) DatasetByGUID(_ context.Context, guid string) (*gzfs.Dataset, error) {
	f.guids = append(f.guids, guid)
	return &gzfs.Dataset{Name: "tank/share", GUID: guid}, nil
}

func TestDatasetByGUIDUsesZFSService(t *testing.T) {
	runner := &lookupRunner{}
	zfs := &lookupZFS{}
	service := &Service{ZFS: zfs, GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}

	dataset, err := service.datasetByGUID(t.Context(), "100")
	if err != nil || dataset == nil || dataset.Name != "tank/share" {
		t.Fatalf("lookup = %#v, %v", dataset, err)
	}
	if !reflect.DeepEqual(zfs.guids, []string{"100"}) || len(runner.commands) != 0 {
		t.Fatalf("lookup bypassed the ZFS service: service=%v zfs=%v", zfs.guids, runner.commands)
	}
}

func TestDatasetByGUIDFallsBackWithoutZFSService(t *testing.T) {
	runner := &lookupRunner{}
	service := &Service{GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}

	dataset, err := service.datasetByGUID(t.Context(), "100")
	if err != nil || dataset == nil || dataset.Name != "tank/share" || dataset.GUID != "100" {
		t.Fatalf("lookup = %#v, %v", dataset, err)
	}
	if !reflect.DeepEqual(runner.commands, []string{"get", "list"}) {
		t.Fatalf("zfs commands = %v, want a full GUID scan", runner.commands)
	}
}
