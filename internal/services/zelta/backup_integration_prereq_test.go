// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"os/exec"
	"testing"
)

// requireLocalhostBackupSSH separates an unavailable integration environment
// from a product failure. Tests may skip here, before exercising Sylve; once
// this succeeds, every backup/restore assertion must fail rather than skip.
func requireLocalhostBackupSSH(t *testing.T) {
	t.Helper()
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skipf("localhost SSH integration prerequisite unavailable: %v", err)
	}
	output, err := exec.Command(
		ssh,
		"-n",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=3",
		"-o", "ConnectionAttempts=1",
		"root@localhost",
		"true",
	).CombinedOutput()
	if err != nil {
		t.Skipf("localhost SSH integration prerequisite unavailable: %v: %s", err, output)
	}
}
