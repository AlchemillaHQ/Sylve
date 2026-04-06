// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestConfigureWireGuardDeviceWGCtrlFallbackSuccess(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	previousRuntimeOS := wireGuardRuntimeOS
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
		wireGuardRuntimeOS = previousRuntimeOS
	})
	wireGuardRuntimeOS = "linux"

	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		return errors.New("ioctl: bad address")
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "/usr/bin/wg", nil
	}

	privateKey := mustGenerateWireGuardPrivateKey(t)
	peerKey := mustGenerateWireGuardPrivateKey(t)
	peerAllowed := mustParseWireGuardCIDR(t, "10.210.0.2/32")

	var capturedConfig string
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command != "/usr/bin/wg" {
			return "", fmt.Errorf("unexpected command: %s", command)
		}
		if len(args) != 3 || args[0] != "setconf" || args[1] != wireGuardServerInterfaceName {
			return "", fmt.Errorf("unexpected wg args: %v", args)
		}

		content, err := os.ReadFile(args[2])
		if err != nil {
			return "", err
		}
		capturedConfig = string(content)
		return "", nil
	}

	peer := wgtypes.PeerConfig{
		PublicKey:         peerKey.PublicKey(),
		AllowedIPs:        []net.IPNet{peerAllowed},
		ReplaceAllowedIPs: true,
	}

	if err := configureWireGuardDevice(wireGuardServerInterfaceName, privateKey.String(), 51820, []wgtypes.PeerConfig{peer}); err != nil {
		t.Fatalf("expected fallback configure success, got error: %v", err)
	}

	if !strings.Contains(capturedConfig, "[Interface]") {
		t.Fatalf("expected setconf to include interface block, got: %s", capturedConfig)
	}
	if !strings.Contains(capturedConfig, "PrivateKey = "+privateKey.String()) {
		t.Fatal("expected setconf to include private key")
	}
	if !strings.Contains(capturedConfig, "[Peer]") {
		t.Fatal("expected setconf to include peer block")
	}
	if !strings.Contains(capturedConfig, "AllowedIPs = 10.210.0.2/32") {
		t.Fatal("expected setconf to include allowed ips")
	}
}

func TestConfigureWireGuardDeviceWGCtrlFallbackFailureIncludesBothErrors(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	previousRuntimeOS := wireGuardRuntimeOS
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
		wireGuardRuntimeOS = previousRuntimeOS
	})
	wireGuardRuntimeOS = "linux"

	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		return errors.New("ioctl: bad address")
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "/usr/bin/wg", nil
	}
	wireGuardRunCommand = func(_ string, _ ...string) (string, error) {
		return "", errors.New("setconf failed")
	}

	privateKey := mustGenerateWireGuardPrivateKey(t)
	peerKey := mustGenerateWireGuardPrivateKey(t)
	peerAllowed := mustParseWireGuardCIDR(t, "10.210.0.2/32")
	peer := wgtypes.PeerConfig{
		PublicKey:         peerKey.PublicKey(),
		AllowedIPs:        []net.IPNet{peerAllowed},
		ReplaceAllowedIPs: true,
	}

	err := configureWireGuardDevice(wireGuardServerInterfaceName, privateKey.String(), 51820, []wgtypes.PeerConfig{peer})
	if err == nil {
		t.Fatal("expected configure failure when wgctrl and setconf both fail")
	}

	errorText := err.Error()
	if !strings.Contains(errorText, "ioctl: bad address") {
		t.Fatalf("expected error to include wgctrl failure, got: %s", errorText)
	}
	if !strings.Contains(errorText, "setconf failed") {
		t.Fatalf("expected error to include setconf failure, got: %s", errorText)
	}
}

func TestBuildWireGuardSetconfConfigIncludesOptionalFields(t *testing.T) {
	privateKey := mustGenerateWireGuardPrivateKey(t)
	peerKey := mustGenerateWireGuardPrivateKey(t)
	psk := mustGenerateWireGuardPrivateKey(t)
	keepalive := 25 * time.Second

	config := buildWireGuardSetconfConfig(privateKey, 51820, []wgtypes.PeerConfig{
		{
			PublicKey:                   peerKey.PublicKey(),
			PresharedKey:                &psk,
			Endpoint:                    &net.UDPAddr{IP: net.ParseIP("198.51.100.10"), Port: 51820},
			AllowedIPs:                  []net.IPNet{mustParseWireGuardCIDR(t, "10.210.0.2/32"), mustParseWireGuardCIDR(t, "2001:db8::2/128")},
			PersistentKeepaliveInterval: &keepalive,
			ReplaceAllowedIPs:           true,
		},
	})

	expectedSnippets := []string{
		"[Interface]",
		"PrivateKey = " + privateKey.String(),
		"ListenPort = 51820",
		"[Peer]",
		"PublicKey = " + peerKey.PublicKey().String(),
		"PresharedKey = " + psk.String(),
		"Endpoint = 198.51.100.10:51820",
		"AllowedIPs = 10.210.0.2/32, 2001:db8::2/128",
		"PersistentKeepalive = 25",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(config, snippet) {
			t.Fatalf("expected setconf config to include %q, got: %s", snippet, config)
		}
	}
}

func TestExpandedWireGuardRouteCIDRsExpandsDefaultRoutes(t *testing.T) {
	got := expandedWireGuardRouteCIDRs([]string{"0.0.0.0/0", "::/0", "10.254.1.0/24"})
	expected := []string{
		"0.0.0.0/1",
		"10.254.1.0/24",
		"128.0.0.0/1",
		"8000::/1",
		"::/1",
	}

	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected expanded routes: got %v want %v", got, expected)
	}
}

func TestAddEndpointHostRouteUsesGatewayFromRouteGet(t *testing.T) {
	previousRunCommand := wireGuardRunCommand
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
	})

	var calls [][]string
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command != "/sbin/route" {
			return "", fmt.Errorf("unexpected command: %s", command)
		}

		full := append([]string{command}, args...)
		calls = append(calls, full)

		if len(args) >= 2 && args[len(args)-2] == "get" {
			return "gateway: 178.63.44.129\ninterface: em0\n", nil
		}
		return "", nil
	}

	if err := addEndpointHostRoute("94.207.111.109", 0); err != nil {
		t.Fatalf("expected host route add success, got error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 route calls (get+add), got %d", len(calls))
	}

	expectedAdd := "/sbin/route -n add -host 94.207.111.109 178.63.44.129"
	gotAdd := strings.Join(calls[1], " ")
	if gotAdd != expectedAdd {
		t.Fatalf("unexpected add command: got %q want %q", gotAdd, expectedAdd)
	}
}

func TestAddEndpointHostRouteUsesInterfaceWhenGatewayIsLink(t *testing.T) {
	previousRunCommand := wireGuardRunCommand
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
	})

	var calls [][]string
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command != "/sbin/route" {
			return "", fmt.Errorf("unexpected command: %s", command)
		}

		full := append([]string{command}, args...)
		calls = append(calls, full)

		if len(args) >= 2 && args[len(args)-2] == "get" {
			return "gateway: link#7\ninterface: em0\n", nil
		}
		return "", nil
	}

	if err := addEndpointHostRoute("94.207.111.109", 0); err != nil {
		t.Fatalf("expected host route add success, got error: %v", err)
	}

	expectedAdd := "/sbin/route -n add -host 94.207.111.109 -iface em0"
	gotAdd := strings.Join(calls[1], " ")
	if gotAdd != expectedAdd {
		t.Fatalf("unexpected add command: got %q want %q", gotAdd, expectedAdd)
	}
}

func TestAddRouteViaInterfaceIncludesFIB(t *testing.T) {
	previousRunCommand := wireGuardRunCommand
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
	})

	var call []string
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		call = append([]string{command}, args...)
		return "", nil
	}

	if err := addRouteViaInterface("10.254.1.0/24", "wgc2", 2); err != nil {
		t.Fatalf("expected route add success, got error: %v", err)
	}

	expected := "/usr/sbin/setfib -F 2 /sbin/route -n add -net 10.254.1.0/24 -iface wgc2"
	if got := strings.Join(call, " "); got != expected {
		t.Fatalf("unexpected route add command: got %q want %q", got, expected)
	}
}

func TestConfigureWireGuardInterfaceAppliesFIB(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	fibCalls := 0
	fibValue := uint(0)
	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		addAddress: func(string, string) error { return nil },
		setMTU:     func(string, uint) error { return nil },
		setMetric:  func(string, uint) error { return nil },
		setFIB: func(_ string, fib uint) error {
			fibCalls++
			fibValue = fib
			return nil
		},
		up: func(string) error { return nil },
	}
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation
	addressCheckCalls := 0
	wireGuardInterfaceHasAddress = func(string, string) (bool, error) {
		addressCheckCalls++
		if addressCheckCalls == 1 {
			return false, nil
		}
		return true, nil
	}
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		t.Fatalf("unexpected shell fallback call: %s %v", command, args)
		return "", nil
	}

	if err := configureWireGuardInterface("wgc2", []string{"10.254.1.7/32"}, 1420, 0, 1); err != nil {
		t.Fatalf("expected configure success, got error: %v", err)
	}
	if fibCalls != 1 {
		t.Fatalf("expected one SetFIB call, got %d", fibCalls)
	}
	if fibValue != 1 {
		t.Fatalf("expected SetFIB to use fib=1, got %d", fibValue)
	}
}

func TestConfigureWireGuardDeviceFreeBSDSetconfPreferred(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	previousRuntimeOS := wireGuardRuntimeOS
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
		wireGuardRuntimeOS = previousRuntimeOS
	})

	wireGuardRuntimeOS = "freebsd"
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "/usr/bin/wg", nil
	}

	wgctrlCalls := 0
	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		wgctrlCalls++
		return nil
	}

	setconfCalls := 0
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command == "/usr/bin/wg" && len(args) >= 1 && args[0] == "setconf" {
			setconfCalls++
		}
		return "", nil
	}

	privateKey := mustGenerateWireGuardPrivateKey(t)
	if err := configureWireGuardDevice(wireGuardServerInterfaceName, privateKey.String(), 51820, nil); err != nil {
		t.Fatalf("expected freebsd configure success, got error: %v", err)
	}

	if setconfCalls == 0 {
		t.Fatal("expected freebsd configure to use wg setconf")
	}
	if wgctrlCalls != 0 {
		t.Fatalf("expected no wgctrl calls when setconf succeeds on freebsd, got %d", wgctrlCalls)
	}
}

func TestConfigureWireGuardDeviceFreeBSDSetconfFailureDoesNotUseWGCtrl(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	previousRuntimeOS := wireGuardRuntimeOS
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
		wireGuardRuntimeOS = previousRuntimeOS
	})

	wireGuardRuntimeOS = "freebsd"
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "/usr/bin/wg", nil
	}
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command == "/usr/bin/wg" && len(args) >= 1 && args[0] == "setconf" {
			return "", errors.New("setconf failed")
		}
		return "", nil
	}

	wgctrlCalls := 0
	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		wgctrlCalls++
		return nil
	}

	privateKey := mustGenerateWireGuardPrivateKey(t)
	err := configureWireGuardDevice(wireGuardServerInterfaceName, privateKey.String(), 51820, nil)
	if err == nil {
		t.Fatal("expected freebsd configure failure when wg setconf fails")
	}
	if !strings.Contains(err.Error(), "wg_setconf_error") {
		t.Fatalf("expected freebsd error to include wg_setconf_error, got: %v", err)
	}
	if wgctrlCalls != 0 {
		t.Fatalf("expected no wgctrl fallback calls on freebsd, got %d", wgctrlCalls)
	}
}

func TestApplyWireGuardServerRuntimeFailureRollsBackInterface(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
	})

	runtime := newFakeWireGuardRuntime()
	wireGuardRunCommand = runtime.runCommand
	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		return errors.New("ioctl: bad address")
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "", errors.New("wg binary not available")
	}

	server := &networkModels.WireGuardServer{
		Enabled:    true,
		Port:       51820,
		Addresses:  []string{"10.210.0.1/24"},
		PrivateKey: mustGenerateWireGuardPrivateKey(t).String(),
	}

	svc := &Service{}
	err := svc.applyWireGuardServerRuntime(server)
	if err == nil {
		t.Fatal("expected apply failure when configure path fails")
	}

	if runtime.interfaceExists(wireGuardServerInterfaceName) {
		t.Fatal("expected server interface to be destroyed after failed apply")
	}
	if runtime.destroyCount(wireGuardServerInterfaceName) == 0 {
		t.Fatal("expected destroy to be called during server rollback")
	}
}

func TestApplyWireGuardClientRuntimeFailureRollsBackInterface(t *testing.T) {
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousResolveWGBinaryPath := wireGuardResolveWGBinaryPath
	previousRunCommand := wireGuardRunCommand
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardResolveWGBinaryPath = previousResolveWGBinaryPath
		wireGuardRunCommand = previousRunCommand
	})

	runtime := newFakeWireGuardRuntime()
	wireGuardRunCommand = runtime.runCommand
	wireGuardConfigureWithWGCtrl = func(_ string, _ wgtypes.Config) error {
		return errors.New("ioctl: bad address")
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "", errors.New("wg binary not available")
	}

	client := &networkModels.WireGuardClient{
		ID:            7,
		Enabled:       true,
		EndpointHost:  "198.51.100.10",
		EndpointPort:  51820,
		PrivateKey:    mustGenerateWireGuardPrivateKey(t).String(),
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{"10.10.0.0/16"},
		Addresses:     []string{"10.210.1.2/32"},
	}

	svc := &Service{}
	err := svc.applyWireGuardClientRuntime(client)
	if err == nil {
		t.Fatal("expected apply failure when configure path fails")
	}

	clientIface := wireGuardClientInterfaceName(client.ID)
	if runtime.interfaceExists(clientIface) {
		t.Fatal("expected client interface to be destroyed after failed apply")
	}
	if runtime.destroyCount(clientIface) == 0 {
		t.Fatal("expected destroy to be called during client rollback")
	}
}

func TestEnsureWireGuardInterfaceUsesNativeOpsWithoutShellFallback(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
	})

	calls := make([]string, 0, 4)
	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		exists: func(name string) (bool, error) {
			calls = append(calls, "exists:"+name)
			return false, nil
		},
		create: func(cloneType string) (string, error) {
			calls = append(calls, "create:"+cloneType)
			return "wg1", nil
		},
		rename: func(currentName string, newName string) error {
			calls = append(calls, "rename:"+currentName+"->"+newName)
			return nil
		},
	}
	wireGuardShouldFallbackInterface = func(error) bool {
		return true
	}
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		t.Fatalf("unexpected shell fallback call: %s %v", command, args)
		return "", nil
	}

	if err := ensureWireGuardInterface(wireGuardServerInterfaceName); err != nil {
		t.Fatalf("expected native ensure to succeed, got error: %v", err)
	}

	expected := []string{
		"exists:wgs0",
		"create:wg",
		"rename:wg1->wgs0",
	}
	if strings.Join(calls, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected native operation order: got %v want %v", calls, expected)
	}
}

func TestConfigureWireGuardInterfaceNativeUnsupportedFallsBackToShell(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		addAddress: func(string, string) error {
			return errWireGuardInterfaceOpsUnsupported
		},
		setMTU: func(string, uint) error {
			return errWireGuardInterfaceOpsUnsupported
		},
		setMetric: func(string, uint) error {
			return errWireGuardInterfaceOpsUnsupported
		},
		up: func(string) error {
			return errWireGuardInterfaceOpsUnsupported
		},
	}
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation
	addressCheckCalls := 0
	wireGuardInterfaceHasAddress = func(string, string) (bool, error) {
		addressCheckCalls++
		if addressCheckCalls == 1 {
			return false, nil
		}
		return true, nil
	}

	shellCalls := 0
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		shellCalls++
		if command != "/sbin/ifconfig" {
			t.Fatalf("unexpected shell command %s", command)
		}
		return "", nil
	}

	if err := configureWireGuardInterface("wgs0", []string{"10.210.0.1/24"}, 1420, 10, 0); err != nil {
		t.Fatalf("expected configure to fallback and succeed, got error: %v", err)
	}

	if shellCalls != 4 {
		t.Fatalf("expected 4 shell fallback calls, got %d", shellCalls)
	}
}

func TestConfigureWireGuardInterfaceNativePermissionErrorDoesNotFallback(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		addAddress: func(string, string) error {
			return syscall.EPERM
		},
	}
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation

	shellCalls := 0
	wireGuardRunCommand = func(string, ...string) (string, error) {
		shellCalls++
		return "", nil
	}

	err := configureWireGuardInterface("wgs0", []string{"10.210.0.1/24"}, 0, 0, 0)
	if err == nil {
		t.Fatal("expected configure failure for native permission error")
	}
	if shellCalls != 0 {
		t.Fatalf("expected no shell fallback calls, got %d", shellCalls)
	}
}

func TestConfigureWireGuardInterfaceAddressExistsOnInterfaceIsIdempotent(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		addAddress: func(string, string) error {
			return syscall.EEXIST
		},
		up: func(string) error {
			return nil
		},
	}
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation
	wireGuardInterfaceHasAddress = func(name string, hostCIDR string) (bool, error) {
		return true, nil
	}

	shellCalls := 0
	wireGuardRunCommand = func(string, ...string) (string, error) {
		shellCalls++
		return "", nil
	}

	if err := configureWireGuardInterface("wgs0", []string{"10.210.0.1/24"}, 0, 0, 0); err != nil {
		t.Fatalf("expected configure success when address already exists on interface, got: %v", err)
	}
	if shellCalls != 0 {
		t.Fatalf("expected no shell fallback calls, got %d", shellCalls)
	}
}

func TestConfigureWireGuardInterfaceAddressExistsErrorStillFailsWhenAddressMissing(t *testing.T) {
	previousInterfaceOps := wireGuardInterfaceOpsBackend
	previousRunCommand := wireGuardRunCommand
	previousFallback := wireGuardShouldFallbackInterface
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardInterfaceOpsBackend = previousInterfaceOps
		wireGuardRunCommand = previousRunCommand
		wireGuardShouldFallbackInterface = previousFallback
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	wireGuardInterfaceOpsBackend = fakeWireGuardInterfaceOps{
		addAddress: func(string, string) error {
			return syscall.EEXIST
		},
	}
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation
	wireGuardInterfaceHasAddress = func(name string, hostCIDR string) (bool, error) {
		return false, nil
	}

	shellCalls := 0
	wireGuardRunCommand = func(string, ...string) (string, error) {
		shellCalls++
		return "", nil
	}

	err := configureWireGuardInterface("wgs0", []string{"10.210.0.1/24"}, 0, 0, 0)
	if err == nil {
		t.Fatal("expected configure failure when address exists error is not idempotent on target interface")
	}
	if shellCalls != 0 {
		t.Fatalf("expected no shell fallback calls, got %d", shellCalls)
	}
}

func TestListManagedWireGuardInterfacesFiltersAndSorts(t *testing.T) {
	previousListInterfaces := wireGuardListInterfaces
	t.Cleanup(func() {
		wireGuardListInterfaces = previousListInterfaces
	})

	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "em0"},
			{Name: "wgc12"},
			{Name: "wgs0"},
			{Name: "wgc2"},
			{Name: "wg1"},
			{Name: "lo0"},
		}, nil
	}

	got, err := listManagedWireGuardInterfaces()
	if err != nil {
		t.Fatalf("expected managed interface listing to succeed, got: %v", err)
	}

	expected := []string{"wg1", "wgc12", "wgc2", "wgs0"}
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected managed interfaces: got %v want %v", got, expected)
	}
}

func mustGenerateWireGuardPrivateKey(t *testing.T) wgtypes.Key {
	t.Helper()

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("failed to generate wireguard key: %v", err)
	}

	return key
}

func mustParseWireGuardCIDR(t *testing.T, cidr string) net.IPNet {
	t.Helper()

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("failed to parse cidr %q: %v", cidr, err)
	}

	return *network
}

type fakeWireGuardRuntime struct {
	ifaces         map[string]bool
	ifaceCounter   int
	destroyCounter map[string]int
}

type fakeWireGuardInterfaceOps struct {
	exists     func(name string) (bool, error)
	create     func(cloneType string) (string, error)
	rename     func(currentName string, newName string) error
	destroy    func(name string) error
	addAddress func(name string, hostCIDR string) error
	setMTU     func(name string, mtu uint) error
	setMetric  func(name string, metric uint) error
	setFIB     func(name string, fib uint) error
	up         func(name string) error
}

func (f fakeWireGuardInterfaceOps) Exists(name string) (bool, error) {
	if f.exists != nil {
		return f.exists(name)
	}
	return false, errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) Create(cloneType string) (string, error) {
	if f.create != nil {
		return f.create(cloneType)
	}
	return "", errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) Rename(currentName string, newName string) error {
	if f.rename != nil {
		return f.rename(currentName, newName)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) Destroy(name string) error {
	if f.destroy != nil {
		return f.destroy(name)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) AddAddress(name string, hostCIDR string) error {
	if f.addAddress != nil {
		return f.addAddress(name, hostCIDR)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) SetMTU(name string, mtu uint) error {
	if f.setMTU != nil {
		return f.setMTU(name, mtu)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) SetMetric(name string, metric uint) error {
	if f.setMetric != nil {
		return f.setMetric(name, metric)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) SetFIB(name string, fib uint) error {
	if f.setFIB != nil {
		return f.setFIB(name, fib)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func (f fakeWireGuardInterfaceOps) Up(name string) error {
	if f.up != nil {
		return f.up(name)
	}
	return errWireGuardInterfaceOpsUnsupported
}

func newFakeWireGuardRuntime() *fakeWireGuardRuntime {
	return &fakeWireGuardRuntime{
		ifaces:         make(map[string]bool),
		destroyCounter: make(map[string]int),
	}
}

func (f *fakeWireGuardRuntime) interfaceExists(name string) bool {
	return f.ifaces[name]
}

func (f *fakeWireGuardRuntime) destroyCount(name string) int {
	return f.destroyCounter[name]
}

func (f *fakeWireGuardRuntime) runCommand(command string, args ...string) (string, error) {
	switch command {
	case "/sbin/ifconfig":
		return f.handleIfconfig(args...)
	case "/sbin/route":
		return "", nil
	default:
		return "", nil
	}
}

func (f *fakeWireGuardRuntime) handleIfconfig(args ...string) (string, error) {
	if len(args) == 1 {
		if f.ifaces[args[0]] {
			return "", nil
		}
		return "", errors.New("does not exist")
	}

	if len(args) == 2 && args[0] == "wg" && args[1] == "create" {
		f.ifaceCounter++
		name := fmt.Sprintf("wg%d", f.ifaceCounter)
		f.ifaces[name] = true
		return name + "\n", nil
	}

	if len(args) == 3 && args[1] == "name" {
		oldName := args[0]
		newName := args[2]
		if !f.ifaces[oldName] {
			return "", errors.New("does not exist")
		}
		delete(f.ifaces, oldName)
		f.ifaces[newName] = true
		return "", nil
	}

	if len(args) == 2 && args[1] == "destroy" {
		name := args[0]
		if !f.ifaces[name] {
			return "", errors.New("does not exist")
		}
		delete(f.ifaces, name)
		f.destroyCounter[name]++
		return "", nil
	}

	if len(args) >= 2 {
		iface := args[0]
		if !f.ifaces[iface] {
			return "", errors.New("does not exist")
		}
		return "", nil
	}

	return "", nil
}
