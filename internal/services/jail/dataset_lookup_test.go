// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/alchemillahq/gzfs"
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
			"tank/sylve/jails/105": map[string]any{
				"name":       "tank/sylve/jails/105",
				"pool":       "tank",
				"type":       string(gzfs.DatasetTypeFilesystem),
				"properties": map[string]any{"guid": map[string]any{"value": "100"}},
			},
		},
	})
}

func TestDatasetByGUIDUsesLookupHook(t *testing.T) {
	runner := &lookupRunner{}
	var looked []string
	service := &Service{
		GZFS: gzfs.NewClient(gzfs.Options{Runner: runner}),
		LookupDatasetByGUID: func(_ context.Context, guid string) (*gzfs.Dataset, error) {
			looked = append(looked, guid)
			return &gzfs.Dataset{Name: "tank/sylve/jails/105", GUID: guid}, nil
		},
	}

	dataset, err := service.datasetByGUID(t.Context(), "100")
	if err != nil || dataset == nil || dataset.Name != "tank/sylve/jails/105" {
		t.Fatalf("lookup = %#v, %v", dataset, err)
	}
	if !reflect.DeepEqual(looked, []string{"100"}) || len(runner.commands) != 0 {
		t.Fatalf("lookup bypassed the hook: hook=%v zfs=%v", looked, runner.commands)
	}
}

func TestDatasetByGUIDFallsBackWithoutLookupHook(t *testing.T) {
	runner := &lookupRunner{}
	service := &Service{GZFS: gzfs.NewClient(gzfs.Options{Runner: runner})}

	dataset, err := service.datasetByGUID(t.Context(), "100")
	if err != nil || dataset == nil || dataset.Name != "tank/sylve/jails/105" || dataset.GUID != "100" {
		t.Fatalf("lookup = %#v, %v", dataset, err)
	}
	if !reflect.DeepEqual(runner.commands, []string{"get", "list"}) {
		t.Fatalf("zfs commands = %v, want a full GUID scan", runner.commands)
	}
}
