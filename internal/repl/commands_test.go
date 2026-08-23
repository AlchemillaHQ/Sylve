// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestExecuteLineExitAndQuitDoNotShutdown(t *testing.T) {
	testCases := []string{"exit", "quit"}

	for _, command := range testCases {
		t.Run(command, func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			ctx := &Context{QuitChan: signals}

			shouldContinue := ExecuteLine(ctx, command)
			if shouldContinue {
				t.Fatalf("expected command %q to end session", command)
			}

			select {
			case got := <-signals:
				t.Fatalf("expected no shutdown signal, got %v", got)
			default:
			}
		})
	}
}

func TestExecuteLineShutdownTriggersSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx := &Context{QuitChan: signals}

	shouldContinue := ExecuteLine(ctx, "shutdown")
	if shouldContinue {
		t.Fatalf("expected shutdown to end session")
	}

	select {
	case got := <-signals:
		if got != syscall.SIGTERM {
			t.Fatalf("expected SIGTERM, got %v", got)
		}
	default:
		t.Fatalf("expected shutdown signal")
	}
}

func TestExecuteLineWritesOutputToContextWriter(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	shouldContinue := ExecuteLine(ctx, "ping")
	if !shouldContinue {
		t.Fatalf("expected ping to keep session running")
	}
	if strings.TrimSpace(out.String()) != "pong" {
		t.Fatalf("expected pong output, got %q", out.String())
	}
}

func TestExecuteLineUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	shouldContinue := ExecuteLine(ctx, "wat")
	if !shouldContinue {
		t.Fatalf("expected unknown command to keep session running")
	}
	if !strings.Contains(out.String(), "Unknown command: 'wat'. Type 'help'.") {
		t.Fatalf("unexpected unknown command output: %q", out.String())
	}
}

func TestExecuteLineSwitchesIsRegistered(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	shouldContinue := ExecuteLine(ctx, "switches list")
	if !shouldContinue {
		t.Fatal("expected switches command to keep session running")
	}
	if !strings.Contains(out.String(), "Error fetching switches: network_service_unavailable") {
		t.Fatalf("unexpected switches output: %q", out.String())
	}
}

func TestExecuteLineObjectsIsRegistered(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	shouldContinue := ExecuteLine(ctx, "objects list network")
	if !shouldContinue {
		t.Fatal("expected objects command to keep session running")
	}
	if !strings.Contains(out.String(), "Error fetching objects: network_service_unavailable") {
		t.Fatalf("unexpected objects output: %q", out.String())
	}
}

func TestExecuteLineDownloadsIsRegistered(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	shouldContinue := ExecuteLine(ctx, "downloads list")
	if !shouldContinue {
		t.Fatal("expected downloads command to keep session running")
	}
	if !strings.Contains(out.String(), "Error fetching downloads: utilities_service_unavailable") {
		t.Fatalf("unexpected downloads output: %q", out.String())
	}
}

func TestSplitCommandLineHandlesQuotedArguments(t *testing.T) {
	parts, err := splitCommandLine(`downloads start "https://download.freebsd.org/releases/amd64/15.1-RELEASE/base.txz" --type base-rootfs --extract`)
	if err != nil {
		t.Fatalf("split command line: %v", err)
	}

	want := []string{
		"downloads",
		"start",
		"https://download.freebsd.org/releases/amd64/15.1-RELEASE/base.txz",
		"--type",
		"base-rootfs",
		"--extract",
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
}

func TestSplitCommandLineRejectsUnterminatedQuote(t *testing.T) {
	_, err := splitCommandLine(`downloads start "https://example.test/base.txz`)
	if err == nil || err.Error() != "unterminated quote" {
		t.Fatalf("expected unterminated quote error, got %v", err)
	}
}
