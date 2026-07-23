// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// ExecuteOperation sends a typed console operation to the running Sylve daemon.
// Operation failures are returned as real errors.
func ExecuteOperation(socketPath, operation string, payload any) (string, error) {
	return executeOperation(socketPath, operation, payload)
}

func executeOperation(socketPath, operation string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode %s request: %w", operation, err)
	}

	return executeRequest(socketPath, Request{
		Operation: operation,
		Payload:   encoded,
	})
}

func executeRequest(socketPath string, request Request) (string, error) {
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

	if err := enc.Encode(request); err != nil {
		return "", fmt.Errorf("send command: %w", err)
	}

	var resp Response
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
