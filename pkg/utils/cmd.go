// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var execCommand = exec.Command
var execCommandContext = exec.CommandContext

func SetCommandForTest(fn func(string, ...string) *exec.Cmd) func() {
	original := execCommand
	execCommand = fn
	return func() { execCommand = original }
}

func SetCommandWithContextForTest(fn func(string, ...string) *exec.Cmd) func() {
	originalContext := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		fixture := fn(name, args...)
		cmd := exec.CommandContext(ctx, fixture.Path, fixture.Args[1:]...)
		cmd.Env, cmd.Dir, cmd.ExtraFiles, cmd.SysProcAttr = fixture.Env, fixture.Dir, fixture.ExtraFiles, fixture.SysProcAttr
		return cmd
	}
	return func() { execCommandContext = originalContext }
}

func RunCommandWithInputContext(ctx context.Context, input, command string, args ...string) (string, error) {
	cmd := execCommandContext(ctx, command, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.String(), ctx.Err()
		}
		return stdout.String(), fmt.Errorf("command_execution_failed: %w", err)
	}
	return stdout.String(), nil
}

func RunCommand(command string, args ...string) (string, error) {
	cmd := execCommand(command, args...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := out.String()

	if err != nil {
		return output, fmt.Errorf("command execution failed: %v, output: %s", err, output)
	}

	return output, nil
}

func RunCommandWithInput(command string, input string, args ...string) (string, error) {
	cmd := execCommand(command, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Stdin = strings.NewReader(input)

	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("command execution failed: %v, output: %s", err, out.String())
	}
	return out.String(), nil
}

func RunCommandWithContext(ctx context.Context, command string, args ...string) (string, error) {
	cmd := execCommandContext(ctx, command, args...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := out.String()

	if err != nil {
		return output, fmt.Errorf("command execution failed: %v, output: %s", err, output)
	}

	return output, nil
}

func RunCommandWithContextStreams(
	ctx context.Context,
	command string,
	args ...string,
) (string, string, error) {
	cmd := execCommandContext(ctx, command, args...)

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	if err != nil {
		return out.String(), errOut.String(), fmt.Errorf("command execution failed: %v", err)
	}

	return out.String(), errOut.String(), nil
}

type CommandResult struct {
	Output   string
	ExitCode int
}

func RunCommandWithEnvContext(ctx context.Context, env []string, command string, args ...string) (CommandResult, error) {
	cmd := execCommandContext(ctx, command, args...)
	if env != nil {
		cmd.Env = env
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	result := CommandResult{Output: out.String()}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}

	return result, err
}

func RunCommandAllowExitCode(command string, allowed []int, args ...string) (string, error) {
	cmd := execCommand(command, args...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := out.String()

	if err == nil {
		return output, nil
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return output, fmt.Errorf("command execution failed: %v, output: %s", err, output)
	}

	code := exitErr.ExitCode()
	for _, allowedCode := range allowed {
		if code == allowedCode {
			return output, nil
		}
	}

	return output, fmt.Errorf(
		"command execution failed: exit status %d, output: %s",
		code,
		output,
	)
}
