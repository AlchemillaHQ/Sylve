// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunZeltaCancelKillsChildProcesses(t *testing.T) {
	dir := t.TempDir()
	ZeltaInstallDir = dir
	t.Cleanup(func() { ZeltaInstallDir = "" })
	pidFile := filepath.Join(dir, "child.pid")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nsleep 60 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "zelta"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runZeltaWithEnvStreaming(ctx, nil, nil, "backup")
		done <- err
	}()

	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if time.Now().After(deadline) {
			t.Fatal("fake zelta did not start its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("zelta run did not return after cancel")
	}

	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d still running after cancel", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
