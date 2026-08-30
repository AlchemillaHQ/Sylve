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
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestIntegrationStandardSwitchEditRecreatesMissingBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standard switch integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("standard switch integration test requires root")
	}
	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		t.Skipf("required command /sbin/ifconfig is unavailable: %v", err)
	}

	bridgeName := fmt.Sprintf("sie%04x%04x", os.Getpid()&0xffff, time.Now().UnixNano()&0xffff)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}
	t.Cleanup(func() {
		_, _ = exec.Command("/sbin/ifconfig", bridgeName, "destroy").CombinedOutput()
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	sw := networkModels.StandardSwitch{
		Name:        "integration-missing-runtime",
		BridgeName:  bridgeName,
		MTU:         1500,
		DisableIPv6: true,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}

	if err := svc.EditStandardSwitch(
		sw.ID,
		9000,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		true,
		false,
		true,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	); err != nil {
		t.Fatalf("edit standard switch with missing runtime: %v", err)
	}

	bridge, err := iface.Get(bridgeName)
	if err != nil {
		t.Fatalf("inspect recreated bridge: %v", err)
	}
	if bridge == nil || bridge.MTU != 9000 {
		t.Fatalf("recreated bridge = %#v, want MTU 9000", bridge)
	}

	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, sw.ID).Error; err != nil {
		t.Fatalf("reload standard switch: %v", err)
	}
	if persisted.MTU != 9000 || !persisted.Private {
		t.Fatalf("persisted switch = %#v, want updated MTU/private state", persisted)
	}
}

func TestIntegrationStandardSwitchPortMACIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standard switch MAC integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("standard switch MAC integration test requires root")
	}
	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		t.Skipf("required command /sbin/ifconfig is unavailable: %v", err)
	}

	bridgeName := fmt.Sprintf("sim%04x%04x", os.Getpid()&0xffff, time.Now().UnixNano()&0xffff)
	createPort := func(label string) string {
		t.Helper()
		output, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
		if err != nil {
			t.Fatalf("create %s epair: %v", label, err)
		}
		name := strings.TrimSpace(output)
		if name == "" {
			t.Fatalf("create %s epair returned an empty interface name", label)
		}
		return name
	}

	portA := createPort("source A")
	portB := createPort("source B")
	t.Cleanup(func() {
		_, _ = exec.Command("/sbin/ifconfig", bridgeName, "destroy").CombinedOutput()
		_, _ = exec.Command("/sbin/ifconfig", portA, "destroy").CombinedOutput()
		_, _ = exec.Command("/sbin/ifconfig", portB, "destroy").CombinedOutput()
	})

	const (
		macA = "02:00:00:00:a1:01"
		macB = "02:00:00:00:b2:02"
	)
	for name, mac := range map[string]string{portA: macA, portB: macB} {
		if _, err := utils.RunCommand("/sbin/ifconfig", name, "ether", mac, "up"); err != nil {
			t.Fatalf("set %s MAC to %s: %v", name, mac, err)
		}
	}

	sw := networkModels.StandardSwitch{
		Name:                "integration-port-mac",
		BridgeName:          bridgeName,
		MTU:                 1500,
		DisableIPv6:         true,
		Ports:               []networkModels.NetworkPort{{Name: portB}, {Name: portA}},
		BridgeMACMode:       networkModels.StandardSwitchMACModePort,
		BridgeMACSourcePort: portA,
	}
	assertBridgeMAC := func(want string) {
		t.Helper()
		bridge, err := iface.Get(bridgeName)
		if err != nil {
			t.Fatalf("inspect bridge %s MAC: %v", bridgeName, err)
		}
		got, err := currentInterfaceMAC(bridge)
		if err != nil || got != want {
			t.Fatalf("bridge MAC=%q err=%v, want %q", got, err, want)
		}
	}

	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create standard switch with reversed member order: %v", err)
	}
	assertBridgeMAC(macA)

	if err := editStandardBridge(sw, sw); err != nil {
		t.Fatalf("reconcile standard switch members: %v", err)
	}
	assertBridgeMAC(macA)

	changed := sw
	changed.BridgeMACSourcePort = portB
	if err := editStandardBridge(sw, changed); err != nil {
		t.Fatalf("change standard switch MAC source: %v", err)
	}
	assertBridgeMAC(macB)
}

func TestIntegrationStandardSwitchRebindsExistingDefaultRouteToBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standard switch integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("standard switch integration test requires root")
	}
	for _, command := range []string{"/sbin/ifconfig", "/sbin/route", "/sbin/sysctl", "/usr/bin/netstat", "/usr/sbin/setfib"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("required command %s is unavailable: %v", command, err)
		}
	}

	const (
		networkAddress = "198.18.254.2/24"
		gatewayAddress = "198.18.254.1"
	)
	testFIB := unusedStandardSwitchIntegrationFIB(t, "198.18.254.0/24")
	suffix := fmt.Sprintf("%04x%04x", os.Getpid()&0xffff, time.Now().UnixNano()&0xffff)
	portName := "srp" + suffix
	bridgeName := "srb" + suffix
	for _, name := range []string{portName, bridgeName} {
		if _, err := iface.Get(name); err == nil || !isInterfaceMissingError(err) {
			t.Fatalf("integration interface name %s is unavailable: %v", name, err)
		}
	}

	runInFIB := func(command string, args ...string) (string, error) {
		fibArgs := append([]string{strconv.Itoa(testFIB), command}, args...)
		return utils.RunCommand("/usr/sbin/setfib", fibArgs...)
	}
	t.Cleanup(func() {
		_, _ = runInFIB("/sbin/route", "delete", "default", gatewayAddress)
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	createdPort, err := utils.RunCommand("/sbin/ifconfig", "epair", "create", "name", portName)
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	if strings.TrimSpace(createdPort) != portName {
		t.Fatalf("created integration epair %q, want %q", strings.TrimSpace(createdPort), portName)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", portName, "fib", strconv.Itoa(testFIB)); err != nil {
		t.Fatalf("assign integration port to FIB %d: %v", testFIB, err)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", portName, "inet", networkAddress, "up"); err != nil {
		t.Fatalf("address integration port: %v", err)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", portName, "inet", "198.18.254.3/24", "alias"); err != nil {
		t.Fatalf("add secondary IPv4 address to integration port: %v", err)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", portName, "inet6", "-ifdisabled", "auto_linklocal"); err != nil {
		t.Fatalf("enable IPv6 on integration port: %v", err)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", portName, "inet6", "2001:db8:254::2/64", "-no_dad"); err != nil {
		t.Fatalf("add IPv6 address to integration port: %v", err)
	}
	if _, err := runInFIB("/sbin/route", "add", "default", gatewayAddress); err != nil {
		t.Fatalf("seed integration default route: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			if command == "/sbin/route" {
				return runInFIB(command, args...)
			}

			output, commandErr := utils.RunCommand(command, args...)
			if commandErr != nil {
				return output, commandErr
			}
			if command == "/sbin/ifconfig" && len(args) == 3 && args[1] == "name" && args[2] == bridgeName {
				if _, fibErr := utils.RunCommand("/sbin/ifconfig", bridgeName, "fib", strconv.Itoa(testFIB)); fibErr != nil {
					return output, fmt.Errorf("assign integration bridge to FIB %d: %w", testFIB, fibErr)
				}
			}
			return output, nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:                "integration-route-migration",
		BridgeName:          bridgeName,
		MTU:                 1500,
		NetworkManual:       networkAddress,
		GatewayManual:       gatewayAddress,
		DefaultRoute:        true,
		DisableIPv6:         true,
		Ports:               []networkModels.NetworkPort{{Name: portName}},
		BridgeMACMode:       networkModels.StandardSwitchMACModePort,
		BridgeMACSourcePort: portName,
	}
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create standard switch over addressed port: %v", err)
	}
	bridge, err := iface.Get(bridgeName)
	if err != nil {
		t.Fatalf("inspect bridge MAC after member attach: %v", err)
	}
	sourcePort, err := iface.Get(portName)
	if err != nil {
		t.Fatalf("inspect source port MAC after member attach: %v", err)
	}
	bridgeMAC, bridgeMACErr := currentInterfaceMAC(bridge)
	sourceMAC, sourceMACErr := currentInterfaceMAC(sourcePort)
	if bridgeMACErr != nil || sourceMACErr != nil || bridgeMAC != sourceMAC {
		t.Fatalf("bridge MAC=%q err=%v, source MAC=%q err=%v", bridgeMAC, bridgeMACErr, sourceMAC, sourceMACErr)
	}

	defaultRoute, err := runInFIB("/sbin/route", "-n", "get", "default")
	if err != nil {
		t.Fatalf("inspect migrated default route: %v", err)
	}
	routeInterface, found := routeGetField(defaultRoute, "interface")
	if !found || routeInterface != bridgeName {
		t.Fatalf("default route interface = %q (found %t), want %q; route: %s", routeInterface, found, bridgeName, defaultRoute)
	}
	member, err := iface.Get(portName)
	if err != nil {
		t.Fatalf("inspect migrated bridge member: %v", err)
	}
	if len(member.IPv4) != 0 || len(member.IPv6) != 0 {
		t.Fatalf("bridge member retained layer-3 addresses: IPv4=%v IPv6=%v", member.IPv4, member.IPv6)
	}
}

func unusedStandardSwitchIntegrationFIB(t *testing.T, network string) int {
	t.Helper()

	addToAllFIBs, err := exec.Command("/sbin/sysctl", "-n", "net.add_addr_allfibs").CombinedOutput()
	if err != nil {
		t.Skipf("cannot inspect net.add_addr_allfibs: %v", err)
	}
	if strings.TrimSpace(string(addToAllFIBs)) != "0" {
		t.Skip("route migration integration test requires net.add_addr_allfibs=0 for isolation")
	}

	fibOutput, err := exec.Command("/sbin/sysctl", "-n", "net.fibs").CombinedOutput()
	if err != nil {
		t.Skipf("cannot inspect net.fibs: %v", err)
	}
	fibCount, err := strconv.Atoi(strings.TrimSpace(string(fibOutput)))
	if err != nil || fibCount < 2 {
		t.Skipf("route migration integration test requires an additional FIB; net.fibs=%q", strings.TrimSpace(string(fibOutput)))
	}

	for fib := 1; fib < fibCount; fib++ {
		output, commandErr := exec.Command(
			"/usr/sbin/setfib",
			strconv.Itoa(fib),
			"/usr/bin/netstat",
			"-rn",
			"-f",
			"inet",
		).CombinedOutput()
		if commandErr != nil {
			continue
		}
		routes := string(output)
		if !strings.Contains(routes, "default") && !strings.Contains(routes, network) {
			return fib
		}
	}

	t.Skip("no unused non-default FIB is available for the route migration integration test")
	return 0
}

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
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create DHCP standard switch: %v", err)
	}
	firstPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second)

	if err := runDhclient(bridgeName, 10); err != nil {
		t.Fatalf("reconcile managed dhclient: %v", err)
	}
	if secondPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second); secondPID == firstPID {
		t.Fatalf("unbound managed dhclient PID did not change from %d", firstPID)
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
	restartedPID := waitForIntegrationDHClientPID(t, bridgeName, 2*time.Second)
	if restartedPID == legacyPID {
		t.Fatalf("unbound legacy dhclient PID did not change from %d", legacyPID)
	}
	mainPattern, _ := dhclientProcessPatterns(bridgeName)
	output, err := syncRunCommandAllowExitCode("/bin/pgrep", []int{1}, "-f", "-x", mainPattern)
	if err != nil {
		t.Fatalf("list restarted dhclient processes: %v", err)
	}
	if pids := strings.Fields(output); len(pids) != 1 || pids[0] != strconv.Itoa(restartedPID) {
		t.Fatalf("restarted dhclient PIDs = %v, want [%d]", pids, restartedPID)
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
