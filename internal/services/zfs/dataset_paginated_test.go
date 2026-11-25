// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
)

func TestIsContainerImageDataset(t *testing.T) {
	tests := map[string]bool{
		"zroot/sylve/containers/images":                    true,
		"zroot/sylve/containers/images/ffca1922":           true,
		"zroot/sylve/containers/images/ffca1922@216844808": true,
		"tank/sylve/containers/images/layer@1":             true,
		"zroot/sylve/containers":                           false,
		"zroot/sylve/containers/web@daily":                 false,
		"zroot/sylve/containers/images-backup@x":           false,
		"zroot/data/sylve/containers/images/layer@1":       false,
		"zroot":      false,
		"zroot@snap": false,
	}
	for name, want := range tests {
		if got := isContainerImageDataset(name); got != want {
			t.Errorf("isContainerImageDataset(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFilterPaginatedDatasets(t *testing.T) {
	datasets := []*gzfs.Dataset{
		{Name: "zroot/data@manual"},
		{Name: "zroot/data@bk_job1"},
		{Name: "zroot/sylve/containers/images/ffca1922@216844808"},
		{Name: "zroot/media@daily"},
		nil,
	}
	names := func(filtered []*gzfs.Dataset) []string {
		out := []string{}
		for _, dataset := range filtered {
			out = append(out, dataset.Name)
		}
		return out
	}

	if got := names(filterPaginatedDatasets(datasets, "", nil)); !reflect.DeepEqual(got, []string{"zroot/data@manual", "zroot/data@bk_job1", "zroot/media@daily"}) {
		t.Fatalf("unfiltered = %v", got)
	}
	if got := names(filterPaginatedDatasets(datasets, "data", []string{"bk_j"})); !reflect.DeepEqual(got, []string{"zroot/data@manual"}) {
		t.Fatalf("search and exclusion = %v", got)
	}
	if got := names(filterPaginatedDatasets(datasets, "images", nil)); len(got) != 0 {
		t.Fatalf("container image layers matched a search: %v", got)
	}
}

func TestFilterPaginatedDatasetsNameFilters(t *testing.T) {
	builtins := []string{"bk_j", "svms_", "sjs_", "_template", "ha_"}
	hidden := []string{
		"zroot/data@bk_j1o8a29plw5_c1_mw802xn7",
		"zroot/sylve/virtual-machines/100/raw-1@svms_before-upgrade_1732400000000",
		"zroot/sylve/jails/105@sjs_clean_1732400000000",
		"zroot/sylve/jails/templates/base-1@sylve_template_105_1732400000000",
		"zroot/sylve/jails/105@sylve_template_restore_105_1732400000000",
		"zroot/sylve/virtual-machines/templates/1/raw-2@sylve_vm_template_100_1732400000000",
		"zroot/sylve/virtual-machines/101/raw-3@sylve_vm_template_restore_101_1732400000000",
		"zroot/data@ha_1732400000000",
		"zroot/data@BK_J1O8A29PLW5",
	}
	kept := []string{
		"zroot/data@manual",
		"zroot/ha_data@daily",
		"zroot/svms_archive@weekly",
		"zroot/foo_template@daily",
		"zroot/data@my_template",
		"zroot/data@pre-bk_j",
		"zroot/data@nightly-ha_",
		"zroot/bk_jobs",
	}
	datasets := []*gzfs.Dataset{}
	for _, name := range append(append([]string{}, hidden...), kept...) {
		datasets = append(datasets, &gzfs.Dataset{Name: name})
	}
	names := func(filtered []*gzfs.Dataset) []string {
		out := []string{}
		for _, dataset := range filtered {
			out = append(out, dataset.Name)
		}
		return out
	}

	if got := names(filterPaginatedDatasets(datasets, "", builtins)); !reflect.DeepEqual(got, kept) {
		t.Fatalf("built-in filters kept %v, want %v", got, kept)
	}
	custom := names(filterPaginatedDatasets(datasets, "", []string{"archive", "_template"}))
	for _, name := range custom {
		if name == "zroot/svms_archive@weekly" {
			t.Fatalf("custom filter did not match anywhere in the name: %v", custom)
		}
		if name == "zroot/sylve/jails/templates/base-1@sylve_template_105_1732400000000" {
			t.Fatalf("template filter missed a template snapshot: %v", custom)
		}
	}
	if len(custom) != len(datasets)-5 {
		t.Fatalf("custom and template filters hid the wrong rows: %v", custom)
	}
}

func TestAttachSnapshotDetails(t *testing.T) {
	created := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	datasets := []*gzfs.Dataset{
		{Name: "zroot/data@daily", GUID: "1", Type: gzfs.DatasetTypeSnapshot},
		{Name: "zroot/data@renamed", GUID: "2", Type: gzfs.DatasetTypeSnapshot, Properties: map[string]gzfs.ZFSProperty{"used": {Value: "10"}}},
		{Name: "zroot/data@missing", GUID: "3", Type: gzfs.DatasetTypeSnapshot},
		{Name: "zroot/data", GUID: "4", Type: gzfs.DatasetTypeFilesystem},
		nil,
	}
	summaries := []zfsServiceInterfaces.SnapshotSummary{
		{Name: "zroot/data@daily", GUID: "1", CreatedAt: created, Clones: []string{"zroot/a", "zroot/b"}, Holds: 2},
		{Name: "zroot/data@old-name", GUID: "2", CreatedAt: created},
		{Name: "zroot/data", GUID: "4", CreatedAt: created},
	}
	original := datasets[0]
	attachSnapshotDetails(datasets, summaries)
	if original.Properties != nil {
		t.Fatalf("attaching details changed the shared input dataset: %#v", original.Properties)
	}

	daily := datasets[0].Properties
	if daily["creation"].Value != strconv.FormatInt(created.Unix(), 10) || daily["clones"].Value != "zroot/a,zroot/b" || daily["userrefs"].Value != "2" {
		t.Fatalf("details not attached: %#v", daily)
	}
	if _, ok := datasets[1].Properties["creation"]; ok || datasets[1].Properties["used"].Value != "10" {
		t.Fatalf("a summary with a different name was attached: %#v", datasets[1].Properties)
	}
	if datasets[2].Properties != nil || datasets[3].Properties != nil {
		t.Fatalf("unmatched or non-snapshot datasets were changed: %#v %#v", datasets[2].Properties, datasets[3].Properties)
	}
}

func TestApplySortByCreation(t *testing.T) {
	snapshot := func(name, creation string) *gzfs.Dataset {
		dataset := &gzfs.Dataset{Name: name, Properties: map[string]gzfs.ZFSProperty{}}
		if creation != "" {
			dataset.Properties["creation"] = gzfs.ZFSProperty{Value: creation}
		}
		return dataset
	}
	build := func() []*gzfs.Dataset {
		return []*gzfs.Dataset{
			snapshot("a@1", "300"),
			snapshot("b@1", "100"),
			snapshot("c@1", "200"),
			snapshot("d@1", "100"),
			snapshot("e@1", ""),
		}
	}
	names := func(datasets []*gzfs.Dataset) []string {
		out := []string{}
		for _, dataset := range datasets {
			out = append(out, dataset.Name)
		}
		return out
	}

	ascending := build()
	applySort(ascending, "creation", "asc")
	if got, want := names(ascending), []string{"e@1", "b@1", "d@1", "c@1", "a@1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ascending = %v, want %v", got, want)
	}
	descending := build()
	applySort(descending, "creation", "desc")
	if got, want := names(descending), []string{"a@1", "c@1", "b@1", "d@1", "e@1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("descending = %v, want %v", got, want)
	}
	unknown := build()
	applySort(unknown, "bogus", "asc")
	if got, want := names(unknown), names(build()); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown field reordered rows: %v", got)
	}
}
