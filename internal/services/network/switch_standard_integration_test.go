// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package network

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

func TestIntegrationStandardSwitchDHClientLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dhclient integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("dhclient integration test requires root")
	}
	for _, command := range []string{"/bin/pgrep", "/bin/pkill", "/sbin/dhclient", "/sbin/ifconfig"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("required command %s is unavailable: %v", command, err)
		}
	}

	bridgeName := fmt.Sprintf("sid%04x%04x", os.Getpid()&0xffff, time.Now().UnixNano()&0xffff)
	leasePath := "/var/db/dhclient.leases." + bridgeName
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}
	if _, err := os.Stat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("integration lease path %s is unavailable: %v", leasePath, err)
	}

	originalRuntimeDir := dhclientRuntimeDir
	dhclientRuntimeDir = filepath.Join(t.TempDir(), "dhclient")
	t.Cleanup(func() { dhclientRuntimeDir = originalRuntimeDir })
	pidPath := dhclientPIDPath(bridgeName)
	t.Cleanup(func() {
		_, _ = exec.Command("/sbin/ifconfig", bridgeName, "destroy").CombinedOutput()
		mainPattern, privilegedPattern := dhclientProcessPatterns(bridgeName)
		_, _ = exec.Command("/bin/pkill", "-TERM", "-f", "-x", mainPattern, privilegedPattern).CombinedOutput()
		if stopped, _ := waitForDhclientProcesses(bridgeName, 2*time.Second); !stopped {
			_, _ = exec.Command("/bin/pkill", "-KILL", "-f", "-x", mainPattern, privilegedPattern).CombinedOutput()
		}
		_ = os.Remove(pidPath)
		_ = os.Remove(leasePath)
	})

	sw := networkModels.StandardSwitch{Name: "dhclient-integration", BridgeName: bridgeName, DHCP: true, DisableIPv6: true}
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create DHCP standard switch: %v", err)
	}
	firstPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second)

	if err := runDhclient(bridgeName, 10); err != nil {
		t.Fatalf("reconcile managed dhclient: %v", err)
	}
	if secondPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second); secondPID != firstPID {
		t.Fatalf("managed dhclient PID changed from %d to %d", firstPID, secondPID)
	}
	if err := stopDhclient(bridgeName); err != nil {
		t.Fatalf("stop managed dhclient: %v", err)
	}
	if _, err := iface.Get(bridgeName); err != nil {
		t.Fatalf("bridge disappeared while stopping dhclient: %v", err)
	}

	if err := runDhclient(bridgeName, 10); err != nil {
		t.Fatalf("restart dhclient: %v", err)
	}
	legacyPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second)
	if err := os.Remove(pidPath); err != nil {
		t.Fatalf("remove PID file to emulate legacy client: %v", err)
	}
	if err := runDhclient(bridgeName, 10); err != nil {
		t.Fatalf("reconcile legacy dhclient: %v", err)
	}
	mainPattern, _ := dhclientProcessPatterns(bridgeName)
	output, err := syncRunCommandAllowExitCode("/bin/pgrep", []int{1}, "-f", "-x", mainPattern)
	if err != nil {
		t.Fatalf("list legacy dhclient processes: %v", err)
	}
	if pids := strings.Fields(output); len(pids) != 1 || pids[0] != strconv.Itoa(legacyPID) {
		t.Fatalf("legacy dhclient PIDs = %v, want [%d]", pids, legacyPID)
	}

	if err := deleteStandardBridge(sw); err != nil {
		t.Fatalf("delete DHCP standard switch: %v", err)
	}
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("bridge after delete error = %v, want missing interface", err)
	}
	if running, err := dhclientProcessesRunning(bridgeName); err != nil || running {
		t.Fatalf("dhclient state after delete = running:%t error:%v", running, err)
	}
}

func waitForIntegrationDHClientPID(t *testing.T, bridgeName string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		running, managed, err := dhclientRunning(bridgeName)
		if err == nil && running && managed {
			return readIntegrationDHClientPID(t, dhclientPIDPath(bridgeName))
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("wait for managed dhclient on %s: running:%t managed:%t error:%v", bridgeName, running, managed, err)
		}
		time.Sleep(dhclientStopPollInterval)
	}
}

func readIntegrationDHClientPID(t *testing.T, path string) int {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dhclient PID file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse dhclient PID %q: %v", strings.TrimSpace(string(contents)), err)
	}
	return pid
}
