// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/remoteexec"
)

// requireLocalhostBackupSSH separates an unavailable integration environment
// from a product failure. Tests may skip here, before exercising Sylve; once
// this succeeds, every backup/restore assertion must fail rather than skip.
func requireLocalhostBackupSSH(t *testing.T) string {
	t.Helper()
	return requireLocalhostSSHHostKey(t, "root@localhost", "")
}

func requireLocalhostSSHHostKey(t *testing.T, host, keyPath string) string {
	t.Helper()
	key, err := tryLocalhostSSHHostKey(t, host, keyPath)
	if err != nil {
		t.Skipf("localhost SSH integration prerequisite unavailable: %v", err)
	}
	return key
}

func tryLocalhostSSHHostKey(t *testing.T, host, keyPath string) (string, error) {
	t.Helper()
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return "", err
	}
	previousKeyDir := SSHKeyDirectory
	SSHKeyDirectory = t.TempDir()
	t.Cleanup(func() { SSHKeyDirectory = previousKeyDir })
	trust := remoteexec.SSHHostTrust{Scope: "backup", Target: "integration-prerequisite", Endpoint: host, Port: 22}
	knownHosts := filepath.Join(SSHKeyDirectory, "known_hosts")
	args, err := remoteexec.SSHHostKeyOptions(trust, knownHosts, keyPath, true, true)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "-n", "-o", "ConnectTimeout=3", "-o", "ConnectionAttempts=1")
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	output, err := exec.Command(ssh, append(args, host, "true")...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, output)
	}
	key, err := remoteexec.ReadLearnedSSHHostKey(knownHosts, trust)
	if err != nil {
		t.Fatalf("read localhost SSH host key: %v", err)
	}
	return key, nil
}
