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
	"fmt"
	"os/exec"
	"strings"
)

var execCommand = exec.Command

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
	cmd := exec.Command(command, args...)
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
	cmd := exec.CommandContext(ctx, command, args...)

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

func RunCommandAllowExitCode(command string, allowed []int, args ...string) (string, error) {
	cmd := exec.Command(command, args...)

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
