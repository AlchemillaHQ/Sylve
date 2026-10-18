// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestCommandStreamsOutputBeforeExit(t *testing.T) {
	e := New(Plan{})
	events, unsubscribe := e.Subscribe()
	defer unsubscribe()
	script := commandScript(t, "printf 'stdout progress'\nprintf '\\rstderr progress' >&2\nread -r reply\n[ \"$reply\" = continue ]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cmd := exec.CommandContext(ctx, script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var commandErr error
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = stdin.Close()
		cancel()
		<-done
	})
	go func() {
		commandErr = e.runCommand(cmd)
		close(done)
	}()
	stdout, stderr := false, false
	for !stdout || !stderr {
		select {
		case event := <-events:
			stdout = stdout || strings.Contains(event.Line, "stdout progress")
			stderr = stderr || strings.Contains(event.Line, "stderr progress")
		case <-done:
			t.Fatalf("command exited before progress was delivered: %v", commandErr)
		case <-ctx.Done():
			t.Fatal("command output was buffered until exit")
		}
	}
	if _, err := io.WriteString(stdin, "continue\n"); err != nil {
		t.Fatal(err)
	}
	<-done
	if commandErr != nil {
		t.Fatal(commandErr)
	}
}

func TestCommandStreamsLongOutput(t *testing.T) {
	e := New(Plan{})
	script := commandScript(t, "awk 'BEGIN { for (i = 0; i < 200000; i++) printf \"x\"; printf \"\\nfinished\\n\" }'\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.runCommand(exec.CommandContext(ctx, script)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(e.Snapshot().Logs, "\n"), "finished") {
		t.Fatal("long command output was truncated or blocked")
	}
}
