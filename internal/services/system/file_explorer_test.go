// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package system

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestResolveFileExplorerGuardPathResolvesExistingSymlinkPrefix(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDirectory, alias); err != nil {
		t.Fatal(err)
	}

	got := resolveFileExplorerGuardPath(filepath.Join(alias, "new", "file.img"))
	want := filepath.Join(realDirectory, "new", "file.img")
	if got != want {
		t.Fatalf("resolved path=%q want=%q", got, want)
	}
}
