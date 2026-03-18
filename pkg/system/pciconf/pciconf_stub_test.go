// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package pciconf

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	origStdout := os.Stdout
	origStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		t.Fatalf("failed to create stderr pipe: %v", err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW

	fn()

	os.Stdout = origStdout
	os.Stderr = origStderr

	_ = stdoutW.Close()
	_ = stderrW.Close()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	if _, err := io.Copy(&outBuf, stdoutR); err != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		t.Fatalf("failed to read stdout: %v", err)
	}
	if _, err := io.Copy(&errBuf, stderrR); err != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		t.Fatalf("failed to read stderr: %v", err)
	}

	_ = stdoutR.Close()
	_ = stderrR.Close()

	return outBuf.String(), errBuf.String()
}

func TestGetPCIDevicesStub(t *testing.T) {
	devices, err := GetPCIDevices()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if devices == nil {
		t.Fatal("expected non-nil slice")
	}

	if len(devices) != 0 {
		t.Fatalf("expected empty slice, got %d devices", len(devices))
	}
}

func TestPrintPCIDevicesStubNoOutput(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		PrintPCIDevices()
	})

	if stdout != "" {
		t.Fatalf("expected no stdout output, got %q", stdout)
	}

	if stderr != "" {
		t.Fatalf("expected no stderr output, got %q", stderr)
	}
}
