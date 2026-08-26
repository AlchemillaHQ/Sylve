// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var errSelfRestartRequested = errors.New("self_restart_requested")

type executableResolver func() (string, error)
type processExec func(string, []string, []string) error

func requestSelfRestart(requests chan<- struct{}) {
	select {
	case requests <- struct{}{}:
	default:
	}
}

func reexecCurrentProcess() error {
	return reexecProcess(os.Executable, syscall.Exec, os.Args, os.Environ())
}

func reexecProcess(
	resolveExecutable executableResolver,
	exec processExec,
	args []string,
	environment []string,
) error {
	executable, err := resolveExecutable()
	if err != nil {
		return fmt.Errorf("resolve executable for self-restart: %w", err)
	}

	if err := exec(executable, args, environment); err != nil {
		return fmt.Errorf("re-exec Sylve: %w", err)
	}

	return nil
}
