// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	consolepath "github.com/alchemillahq/sylve/internal/console"
)

func TestReplHistoryPersistsAndDeduplicates(t *testing.T) {
	path := consolepath.HistoryPath(t.TempDir())
	history := recordReplHistory(path, nil, "  vms list  ")
	history = recordReplHistory(path, history, "vms list")
	history = recordReplHistory(path, history, "downloads list")

	want := []string{"vms list", "downloads list"}
	if !reflect.DeepEqual(history, want) {
		t.Fatalf("history = %#v, want %#v", history, want)
	}
	if got := loadReplHistory(path); !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded history = %#v, want %#v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat history: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history permissions = %o, want 600", info.Mode().Perm())
	}

	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat history directory: %v", err)
	}
	if directory.Mode().Perm() != 0o700 {
		t.Fatalf("history directory permissions = %o, want 700", directory.Mode().Perm())
	}
}

func TestReplHistoryRetainsMostRecentEntries(t *testing.T) {
	var history []string
	for i := 0; i <= maxReplHistoryEntries; i++ {
		var added bool
		history, added = addReplHistory(history, fmt.Sprintf("command %d", i))
		if !added {
			t.Fatalf("command %d was not added", i)
		}
	}

	if len(history) != maxReplHistoryEntries {
		t.Fatalf("history length = %d, want %d", len(history), maxReplHistoryEntries)
	}
	if history[0] != "command 1" {
		t.Fatalf("oldest history entry = %q, want command 1", history[0])
	}
}

func TestTUIModelsLoadConfiguredHistory(t *testing.T) {
	path := consolepath.HistoryPath(t.TempDir())
	recordReplHistory(path, nil, "vms list")

	local := initialTUI(&Context{HistoryPath: path})
	if !reflect.DeepEqual(local.history, []string{"vms list"}) {
		t.Fatalf("local history = %#v", local.history)
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	remote := newRemoteModel(clientConn, path)
	if !reflect.DeepEqual(remote.history, []string{"vms list"}) {
		t.Fatalf("remote history = %#v", remote.history)
	}
}
