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
