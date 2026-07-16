// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package system

import "testing"

func TestFileExplorerPathOverlaps(t *testing.T) {
	root := "/zroot/sylve/jails/42"
	for _, tt := range []struct {
		path string
		want bool
	}{
		{path: "/zroot/sylve/jails/42/etc/rc.conf", want: true},
		{path: "/zroot/sylve/jails", want: true},
		{path: "/zroot/sylve/jails/420/etc/rc.conf", want: false},
		{path: "/zroot/sylve/other", want: false},
	} {
		if got := fileExplorerPathOverlaps(tt.path, root); got != tt.want {
			t.Errorf("fileExplorerPathOverlaps(%q, %q) = %t, want %t", tt.path, root, got, tt.want)
		}
	}
}
