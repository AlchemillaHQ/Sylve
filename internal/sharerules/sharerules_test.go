// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package sharerules

import (
	"slices"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestDatasetBlockRefusesSystemAndSylveData(t *testing.T) {
	datasets := []*gzfs.Dataset{
		{Name: "zroot/ROOT/default", Mountpoint: "/"},
		{Name: "zroot/sylve", Mountpoint: "/zroot/sylve"},
		{Name: "zroot/sylve/virtual-machines", Mountpoint: "none"},
		{Name: "tank/data", Mountpoint: "/tank/data"},
	}
	roots := ProtectedRoots(datasets, "/srv/apps/sylve-data")
	for _, root := range []string{"/", "/zroot/sylve", "/srv/apps/sylve-data"} {
		if !slices.Contains(roots, root) {
			t.Fatalf("protected roots %v are missing %s", roots, root)
		}
	}

	for _, tc := range []struct {
		name, mountpoint, want string
	}{
		{"zroot/ROOT/default", "/", BlockBootEnvironment},
		{"tank/rootfs", "/", BlockRoot},
		{"zroot/var/log", "/var/log", BlockSystemPath},
		{"zroot/usr/src", "/usr/src", BlockSystemPath},
		{"zroot/tmp", "/tmp", BlockSystemPath},
		{"zroot/sylve/jails", "/zroot/sylve/jails", BlockSylveData},
		{"zroot", "/zroot", BlockContainsProtected},
		{"tank/apps", "/srv/apps", BlockContainsProtected},
		{"zroot/usr/home", "/usr/home", ""},
		{"zroot/home", "/home", ""},
		{"tank/data", "/tank/data", ""},
		{"tank", "/tank", ""},
		{"tank/legacy", "legacy", ""},
	} {
		got := DatasetBlock(&gzfs.Dataset{Name: tc.name, Mountpoint: tc.mountpoint}, roots)
		if got != tc.want {
			t.Errorf("%s at %s: got %q, want %q", tc.name, tc.mountpoint, got, tc.want)
		}
	}
}

func TestStateOrdersDisabledBeforeDatasetProblems(t *testing.T) {
	dataset := &gzfs.Dataset{Name: "tank/data"}
	for _, tc := range []struct {
		enabled bool
		dataset *gzfs.Dataset
		mounted bool
		want    string
	}{
		{false, nil, false, StateDisabled},
		{true, nil, false, StateDatasetMissing},
		{true, dataset, false, StateDatasetUnmounted},
		{true, dataset, true, StateActive},
	} {
		if got := State(tc.enabled, tc.dataset, tc.mounted); got != tc.want {
			t.Errorf("State(%t, %v, %t) = %q; want %q", tc.enabled, tc.dataset != nil, tc.mounted, got, tc.want)
		}
	}
}
