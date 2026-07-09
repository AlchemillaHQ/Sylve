// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

const ConsoleSocketPath = "/var/run/sylve-console.sock"

type socketRequest struct {
	Command string `json:"command"`
}

type socketResponse struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ExecuteCommand sends a command to the running sylve daemon via its Unix socket
// and returns the formatted output. If the daemon is not running, returns an
// error with a helpful message.
func ExecuteCommand(command string) (string, error) {
	return executeCommand(ConsoleSocketPath, command)
}

func executeCommand(socketPath, command string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		if isSocketUnavailable(err) {
			return "", fmt.Errorf("sylve daemon is not running; start it first with 'sylve'")
		}
		return "", fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := enc.Encode(socketRequest{Command: command}); err != nil {
		return "", fmt.Errorf("send command: %w", err)
	}

	var resp socketResponse
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}

	return resp.Output, nil
}

func isSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ENOENT) || errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}

	return false
}
