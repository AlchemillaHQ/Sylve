// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/alchemillahq/gzfs"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestNativeHealthCheckBatchesProcessesAndKernelSettings(t *testing.T) {
	f := newNativeFixture(t)
	exports := exportsMarker + "\n/mnt/data -sec=sys\n"
	if err := f.driver.Publish(t.Context(), exports); err != nil {
		t.Fatal(err)
	}
	state := &nfsModels.NFSRuntime{Hash: exportsHash(exports), Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
	config := nfsModels.NFSConfig{Protocol: "v3", MinThreads: 1, MaxThreads: 4}
	if err := f.driver.Start(t.Context(), config, state, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.commands = nil
	if err := f.driver.Check(t.Context(), config, state); err != nil {
		t.Fatal(err)
	}
	// One ps, one sysctl (including thread bounds), and the two NFSv3 RPC probes.
	if len(f.commands) != 4 {
		t.Fatalf("native health check exceeded its command budget: %v", f.commands)
	}
	if !slices.Contains(f.commands, "/bin/ps -p 202,201 -o pid= -o lstart= -o ucomm=") {
		t.Fatalf("serving processes were not checked together: %v", f.commands)
	}
}

func TestNativeFilesystemInspectionIsOneNarrowNonrecursiveQuery(t *testing.T) {
	d := newNativeRuntime()
	response := gzfs.DatasetList{Datasets: map[string]*gzfs.Dataset{
		"tank/data":  testDataset("1", "tank/data", "/mnt/data"),
		"tank/other": testDataset("2", "tank/other", "/mnt/other "),
	}}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	d.run = func(_ context.Context, _ []string, command string, args ...string) (utils.CommandResult, error) {
		calls++
		want := []string{"list", "-p", "-j", "-o", "name,guid,type,mountpoint,mounted,sharenfs,keystatus", "-t", "filesystem", "tank/data", "tank/other"}
		if command != "/sbin/zfs" || !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected property query: %s %v", command, args)
		}
		return utils.CommandResult{Output: string(data)}, nil
	}
	datasets, err := d.filesystems(t.Context(), []string{"tank/data", "tank/other"})
	if err != nil || len(datasets) != 2 || calls != 1 {
		t.Fatalf("batched filesystem query failed: %#v, calls=%d, %v", datasets, calls, err)
	}
	if dataset := findDataset(datasets, "2"); dataset == nil || dataset.Mountpoint != "/mnt/other " {
		t.Fatalf("path whitespace was lost: %#v", dataset)
	}
}

func TestNativeFilesystemInspectionRejectsPartialOrUnexpectedResults(t *testing.T) {
	for _, test := range []string{"missing dataset", "unexpected dataset", "missing property", "invalid GUID", "duplicate GUID"} {
		t.Run(test, func(t *testing.T) {
			d := newNativeRuntime()
			dataset := testDataset("1", "tank/data", "/mnt/data")
			entries := map[string]*gzfs.Dataset{dataset.Name: dataset}
			switch test {
			case "missing dataset":
				clear(entries)
			case "unexpected dataset":
				entries = map[string]*gzfs.Dataset{"tank/other": testDataset("1", "tank/other", "/mnt/data")}
			case "missing property":
				delete(dataset.Properties, "keystatus")
			case "invalid GUID":
				dataset.Properties["guid"] = gzfs.ZFSProperty{Value: "01"}
			case "duplicate GUID":
				entries["tank/other"] = testDataset("1", "tank/other", "/mnt/other")
			}
			data, err := json.Marshal(gzfs.DatasetList{Datasets: entries})
			if err != nil {
				t.Fatal(err)
			}
			d.run = func(context.Context, []string, string, ...string) (utils.CommandResult, error) {
				return utils.CommandResult{Output: string(data)}, nil
			}
			if _, err := d.filesystems(t.Context(), []string{"tank/data"}); err == nil {
				t.Fatal("unsafe filesystem response was accepted")
			}
		})
	}
}

func TestPublicationRechecksGUIDAfterPreSaveDiscovery(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	service.inspectDatasets = func(context.Context, []string) ([]*gzfs.Dataset, error) {
		return []*gzfs.Dataset{testDataset("2", "tank/data", "/mnt/data")}, nil
	}
	share.ReadOnly = false
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); !errors.Is(err, ErrApplyFailed) || driver.running {
		t.Fatalf("stale pre-save identity was published: running=%t, %v", driver.running, err)
	}
}
