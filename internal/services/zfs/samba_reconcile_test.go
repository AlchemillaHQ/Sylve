// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zfs

import (
	"reflect"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestDatasetGUIDsInTreesIncludesRecursiveDescendants(t *testing.T) {
	datasets := []*gzfs.Dataset{
		{Name: "tank", GUID: "pool"},
		{Name: "tank/shared", GUID: "shared"},
		{Name: "tank/shared/child", GUID: "child"},
		{Name: "tank/shared-other", GUID: "other"},
	}

	got := datasetGUIDsInTrees(datasets, []*gzfs.Dataset{datasets[1]})
	want := []string{"shared", "child"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("datasetGUIDsInTrees() = %v, want %v", got, want)
	}
}
