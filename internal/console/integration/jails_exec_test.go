// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package integration

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func testConsoleJailExec(t *testing.T, suite *consoleIntegrationSuite, ctid uint) {
	t.Helper()
	t.Run("exec arguments", func(t *testing.T) {
		args := []string{"two words", "", "$(id)", ";", "*", "a'b\"c", "--help", "--json", "--ctid", "9999", "--", "--version"}
		command := newConsoleJailExecCommand(t, suite, ctid,
			append([]string{"/bin/sh", "-c", `printf '<%s>\n' "$@"`, "exec-test"}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run exec: %v\n%s", err, output)
		}
		var want strings.Builder
		for _, arg := range args {
			fmt.Fprintf(&want, "<%s>\n", arg)
		}
		if string(output) != want.String() {
			t.Fatalf("exec output = %q, want %q", output, want.String())
		}
	})

	t.Run("exec root and clean environment without a terminal", func(t *testing.T) {
		t.Setenv("SYLVE_JAIL_EXEC_HOST_ONLY", "must-not-leak")
		command := newConsoleJailExecCommand(t, suite, ctid, "/bin/sh", "-c",
			`test "$(/usr/bin/id -u)" = 0 && test "${SYLVE_JAIL_EXEC_HOST_ONLY+x}" != x && test ! -t 0 && test ! -t 1 && test ! -t 2`)
		if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
			t.Fatalf("run exec: %v\n%s", err, output)
		}
	})

	t.Run("exec streams and exit status", func(t *testing.T) {
		command := newConsoleJailExecCommand(t, suite, ctid, "/bin/sh", "-c",
			`cat; printf 'stderr\n' >&2; exit 37`)
		command.Stdin = strings.NewReader("stdin\n")
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 37 {
			t.Fatalf("exec error = %v, want exit status 37\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
		}
		if stdout.String() != "stdin\n" || stderr.String() != "stderr\n" {
			t.Fatalf("exec stdout = %q, stderr = %q", stdout.String(), stderr.String())
		}
	})

	t.Run("exec missing executable", func(t *testing.T) {
		missing := "/sylve-exec-missing-" + suite.runID
		command := newConsoleJailExecCommand(t, suite, ctid, missing)
		output, err := command.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || !strings.Contains(string(output), missing) {
			t.Fatalf("exec error = %v, want missing executable failure\n%s", err, output)
		}
	})

	t.Run("exec process and signals", func(t *testing.T) {
		command := newConsoleJailExecCommand(t, suite, ctid, "/bin/sh", "-c",
			`printf '%s\n' "$$"; exec /bin/sleep 60`)
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Start(); err != nil {
			t.Fatalf("start exec: %v", err)
		}
		pid, readErr := bufio.NewReader(stdout).ReadString('\n')
		signalErr := command.Process.Signal(syscall.SIGTERM)
		err = command.Wait()
		if readErr != nil || strings.TrimSpace(pid) != strconv.Itoa(command.Process.Pid) {
			t.Fatalf("jailed process PID = %q, want %d, error = %v\nstderr: %s", pid, command.Process.Pid, readErr, &stderr)
		}
		if signalErr != nil {
			t.Fatalf("signal exec: %v", signalErr)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("exec error = %v, want SIGTERM\nstderr: %s", err, &stderr)
		}
		status := exitErr.Sys().(syscall.WaitStatus)
		if !status.Signaled() || status.Signal() != syscall.SIGTERM || stderr.Len() != 0 {
			t.Fatalf("exec status = %v, want SIGTERM\nstderr: %s", status, &stderr)
		}
	})
}

func newConsoleJailExecCommand(t *testing.T, suite *consoleIntegrationSuite, ctid uint, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), consoleIntegrationCommandTimeout)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, suite.binaryPath,
		append([]string{"jails", "exec", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--"}, args...)...)
	// Run without a configuration file; local jail execution must not use the daemon.
	command.Dir = t.TempDir()
	return command
}
