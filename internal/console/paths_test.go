// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"path/filepath"
	"testing"
)

func TestPathsUseConfiguredDataDirectory(t *testing.T) {
	dataPath := filepath.Join("/var", "db", "sylve")
	if got, want := SocketPath(dataPath), filepath.Join(dataPath, "run", "console.sock"); got != want {
		t.Fatalf("socket path = %q, want %q", got, want)
	}
	if got, want := HistoryPath(dataPath), filepath.Join(dataPath, "repl", "history"); got != want {
		t.Fatalf("history path = %q, want %q", got, want)
	}
}
