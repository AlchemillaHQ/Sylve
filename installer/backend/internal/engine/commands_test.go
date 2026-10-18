// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func commandScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "command")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandStdinIsNotLogged(t *testing.T) {
	e := New(Plan{})
	script := commandScript(t, "read -r password\n[ \"$password\" = 'secret-value' ] || exit 1\nprintf 'password accepted\\n'\n")
	if err := e.cmdStdin("secret-value", script); err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(e.Snapshot().Logs, "\n")
	if strings.Contains(logs, "secret-value") || !strings.Contains(logs, "<password>") || !strings.Contains(logs, "password accepted") {
		t.Fatalf("unexpected password command logs: %s", logs)
	}
}

func TestCommandFailureKeepsExitStatusAndOutput(t *testing.T) {
	e := New(Plan{})
	script := commandScript(t, "printf 'command failed\\n' >&2\nexit 7\n")
	err := e.cmd(script)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 || !strings.HasPrefix(err.Error(), script+":") {
		t.Fatalf("unexpected command error: %v", err)
	}
	if !strings.Contains(strings.Join(e.Snapshot().Logs, "\n"), "command failed") {
		t.Fatal("failed command output was lost")
	}
}
