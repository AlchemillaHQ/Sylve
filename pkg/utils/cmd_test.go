// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func fakeExecCommandContext(ctx context.Context, command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	for i := range args {
		if args[i] == "--" && i+1 < len(args) {
			switch args[i+1] {
			case "success":
				fmt.Fprint(os.Stdout, "Hello, world!\n")
				os.Exit(0)

			case "failure":
				fmt.Fprint(os.Stderr, "something went wrong\n")
				os.Exit(1)

			case "stdin-echo":
				input, err := io.ReadAll(os.Stdin)
				if err != nil {
					fmt.Fprint(os.Stderr, "failed to read stdin\n")
					os.Exit(1)
				}
				fmt.Fprint(os.Stdout, string(input))
				os.Exit(0)

			case "exit2":
				fmt.Fprint(os.Stderr, "exit status 2\n")
				os.Exit(2)

			case "sleep":
				time.Sleep(5 * time.Second)
				fmt.Fprint(os.Stdout, "finished sleeping\n")
				os.Exit(0)
			}
		}
	}

	os.Exit(2)
}

func TestRunCommand_Success(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommand("success")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "Hello, world!\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCommand_Failure(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommand("failure")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if output != "something went wrong\n" {
		t.Fatalf("unexpected output: %q", output)
	}
	if !strings.Contains(err.Error(), "command execution failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRunCommandWithInput_Success(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommandWithInput("stdin-echo", "hello from stdin\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "hello from stdin\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCommandWithInput_Failure(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommandWithInput("failure", "ignored input")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if output != "something went wrong\n" {
		t.Fatalf("unexpected output: %q", output)
	}
	if !strings.Contains(err.Error(), "command execution failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRunCommandWithContext_Success(t *testing.T) {
	original := execCommandContext
	execCommandContext = fakeExecCommandContext
	defer func() { execCommandContext = original }()

	output, err := RunCommandWithContext(context.Background(), "success")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "Hello, world!\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCommandWithContext_Cancelled(t *testing.T) {
	original := execCommandContext
	execCommandContext = fakeExecCommandContext
	defer func() { execCommandContext = original }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	output, err := RunCommandWithContext(ctx, "sleep")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if output != "" {
		t.Fatalf("expected empty output, got %q", output)
	}
	if !strings.Contains(err.Error(), "command execution failed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRunCommandAllowExitCode_Success(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommandAllowExitCode("success", []int{2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "Hello, world!\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCommandAllowExitCode_AllowedFailure(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommandAllowExitCode("exit2", []int{2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "exit status 2\n" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunCommandAllowExitCode_DisallowedFailure(t *testing.T) {
	original := execCommand
	execCommand = fakeExecCommand
	defer func() { execCommand = original }()

	output, err := RunCommandAllowExitCode("exit2", []int{1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if output != "exit status 2\n" {
		t.Fatalf("unexpected output: %q", output)
	}
	if !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
