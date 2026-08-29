// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

type syncStubSet struct {
	ifaceGet                func(string) (*iface.Interface, error)
	createBridge            func(networkModels.StandardSwitch) error
	editBridge              func(networkModels.StandardSwitch, networkModels.StandardSwitch) error
	deleteBridge            func(networkModels.StandardSwitch) error
	runCommand              func(string, ...string) (string, error)
	runCommandAllowExitCode func(string, []int, ...string) (string, error)
	runCommandWithContext   func(context.Context, string, ...string) (string, error)
	stopDhclient            func(string) error
}

const testStandardSwitchMAC = "02:00:00:00:00:01"

func withTestStandardSwitchMAC(sw networkModels.StandardSwitch) networkModels.StandardSwitch {
	if sw.BridgeMACMode != "" {
		return sw
	}
	objectID := uint(1)
	sw.BridgeMACMode = networkModels.StandardSwitchMACModeObject
	sw.BridgeMACObjectID = &objectID
	sw.BridgeMACObject = &networkModels.Object{
		ID:      objectID,
		Type:    "Mac",
		Entries: []networkModels.ObjectEntry{{Value: testStandardSwitchMAC}},
	}
	return sw
}

func createTestStandardSwitchMACSource(t *testing.T, svc *Service) networkModels.StandardSwitchMACSource {
	t.Helper()
	if err := svc.DB.AutoMigrate(&networkModels.Object{}, &networkModels.ObjectEntry{}); err != nil {
		t.Fatalf("migrate test MAC object tables: %v", err)
	}
	object := networkModels.Object{
		Name:    "test-standard-switch-mac",
		Type:    "Mac",
		Entries: []networkModels.ObjectEntry{{Value: testStandardSwitchMAC}},
	}
	if err := svc.DB.Create(&object).Error; err != nil {
		t.Fatalf("create test MAC object: %v", err)
	}
	return networkModels.StandardSwitchMACSource{
		Mode:        networkModels.StandardSwitchMACModeObject,
		MACObjectID: object.ID,
	}
}

func setTestStandardSwitchMACSource(sw *networkModels.StandardSwitch, source networkModels.StandardSwitchMACSource) {
	sw.BridgeMACMode = source.Mode
	sw.BridgeMACSourcePort = source.Port
	if source.MACObjectID == 0 {
		sw.BridgeMACObjectID = nil
		return
	}
	objectID := source.MACObjectID
	sw.BridgeMACObjectID = &objectID
}

func stubSyncFunctions(t *testing.T, stubs syncStubSet) {
	t.Helper()

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origDelete := syncDeleteBridge
	origRun := syncRunCommand
	origRunAllowExitCode := syncRunCommandAllowExitCode
	origRunWithContext := syncRunCommandWithContext
	origStopDhclient := syncStopDhclient
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncDeleteBridge = origDelete
		syncRunCommand = origRun
		syncRunCommandAllowExitCode = origRunAllowExitCode
		syncRunCommandWithContext = origRunWithContext
		syncStopDhclient = origStopDhclient
	})

	simulatedInterfaces := map[string]*iface.Interface{}
	macOverrides := map[string]string{}
	ifaceGetDelegate := syncIfaceGet
	if stubs.ifaceGet != nil {
		ifaceGetDelegate = stubs.ifaceGet
	}
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		interfaceObj, exists := simulatedInterfaces[name]
		var err error
		if !exists {
			interfaceObj, err = ifaceGetDelegate(name)
		}
		if interfaceObj == nil || err != nil {
			return interfaceObj, err
		}
		copy := *interfaceObj
		if mac, overridden := macOverrides[name]; overridden {
			copy.Ether = mac
			copy.HWAddr = mac
		}
		return &copy, nil
	}
	if stubs.createBridge != nil {
		syncCreateBridge = stubs.createBridge
	}
	if stubs.editBridge != nil {
		syncEditBridge = stubs.editBridge
	}
	if stubs.deleteBridge != nil {
		syncDeleteBridge = stubs.deleteBridge
	}
	if stubs.runCommand != nil {
		syncRunCommand = func(command string, args ...string) (string, error) {
			output, err := stubs.runCommand(command, args...)
			if err != nil || command != "/sbin/ifconfig" {
				return output, err
			}
			if len(args) == 2 && args[0] == "bridge" && args[1] == "create" {
				name := strings.TrimSpace(output)
				if name != "" {
					simulatedInterfaces[name] = &iface.Interface{Name: name}
				}
			}
			if len(args) == 3 && args[1] == "name" {
				interfaceObj := simulatedInterfaces[args[0]]
				if interfaceObj == nil {
					interfaceObj = &iface.Interface{}
				}
				copy := *interfaceObj
				copy.Name = args[2]
				simulatedInterfaces[args[2]] = &copy
				delete(simulatedInterfaces, args[0])
			}
			if len(args) == 3 && args[1] == "ether" {
				macOverrides[args[0]] = args[2]
			}
			return output, err
		}
	}
	if stubs.runCommandAllowExitCode != nil {
		syncRunCommandAllowExitCode = stubs.runCommandAllowExitCode
	}
	if stubs.runCommandWithContext != nil {
		syncRunCommandWithContext = stubs.runCommandWithContext
	}
	if stubs.stopDhclient != nil {
		syncStopDhclient = stubs.stopDhclient
	}
}

func useTestDhclientRuntimeDir(t *testing.T) string {
	t.Helper()

	original := dhclientRuntimeDir
	dhclientRuntimeDir = filepath.Join(t.TempDir(), "dhclient")
	t.Cleanup(func() {
		dhclientRuntimeDir = original
	})
	return dhclientRuntimeDir
}

func skipDhclientNaturalExitGrace(t *testing.T) {
	t.Helper()

	original := dhclientNaturalExitGrace
	dhclientNaturalExitGrace = 0
	t.Cleanup(func() {
		dhclientNaturalExitGrace = original
	})
}

func commandIndex(commands []string, expected string) int {
	for index, command := range commands {
		if command == expected {
			return index
		}
	}
	return -1
}

func TestNormalizeIPv6GatewayForRouteAddsInterfaceScopeForLinkLocal(t *testing.T) {
	got := normalizeIPv6GatewayForRoute("fe80::1", "vm-abcd1")
	want := "fe80::1%vm-abcd1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeIPv6GatewayForRoutePreservesExistingScope(t *testing.T) {
	got := normalizeIPv6GatewayForRoute("fe80::1%igb0", "vm-abcd1")
	want := "fe80::1%igb0"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeIPv6GatewayForRouteKeepsGlobalAddressUnchanged(t *testing.T) {
	got := normalizeIPv6GatewayForRoute("2001:db8::1", "vm-abcd1")
	want := "2001:db8::1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDisableBridgeMemberOffloadsOnlyChangesEnabledCapabilities(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name,
				Capabilities: iface.Capabilities{
					Enabled: iface.Flags{Raw: ifcapTXCSUM | ifcapTXCSUMIPv6 | ifcapTSO4 | ifcapTSO6 | ifcapLRO | ifcapTOE4 | ifcapTOE6 | ifcapMEXTPG},
				},
			}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := disableBridgeMemberOffloads("testport0"); err != nil {
		t.Fatalf("disable bridge offloads: %v", err)
	}

	want := "/sbin/ifconfig testport0 -txcsum -txcsum6 -tso -lro -toe -mextpg"
	if len(commands) != 1 || commands[0] != want {
		t.Fatalf("offload commands = %v, want [%q]", commands, want)
	}
}

func TestDisableBridgeMemberOffloadsIsNoOpWhenAlreadyDisabled(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			t.Fatalf("unexpected command: %s %s", command, strings.Join(args, " "))
			return "", nil
		},
	})

	if err := disableBridgeMemberOffloads("testport0"); err != nil {
		t.Fatalf("already-disabled interface returned error: %v", err)
	}
}

func TestDisableBridgeMemberOffloadsSkipsTransientInterfaces(t *testing.T) {
	tests := []struct {
		name         string
		interfaceObj *iface.Interface
	}{
		{name: "tap_fixture0", interfaceObj: &iface.Interface{Driver: "tap"}},
		{name: "epair_fixture0a", interfaceObj: &iface.Interface{}},
		{name: "vnet_fixture0", interfaceObj: &iface.Interface{Groups: []string{"vnet"}}},
		{name: "bridge_fixture0", interfaceObj: &iface.Interface{Groups: []string{"bridge"}}},
	}

	interfaces := make(map[string]*iface.Interface, len(tests))
	for _, test := range tests {
		test.interfaceObj.Name = test.name
		test.interfaceObj.Capabilities.Enabled.Raw = ifcapTXCSUM | ifcapTXCSUMIPv6 | ifcapTSO4 | ifcapLRO | ifcapTOE4 | ifcapMEXTPG
		interfaces[test.name] = test.interfaceObj
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return interfaces[name], nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			t.Fatalf("unexpected command: %s %s", command, strings.Join(args, " "))
			return "", nil
		},
	})

	for _, test := range tests {
		if err := disableBridgeMemberOffloads(test.name); err != nil {
			t.Fatalf("transient interface %s returned error: %v", test.name, err)
		}
	}
}

func TestClearBridgeMemberLayer3StopsDHCPAndDeletesEveryAddress(t *testing.T) {
	var operations []string
	lookups := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			lookups++
			if lookups > 1 {
				return &iface.Interface{Name: name}, nil
			}
			return &iface.Interface{
				Name: name,
				IPv4: []iface.IPv4{
					{IP: net.ParseIP("192.0.2.10")},
					{IP: net.ParseIP("192.0.2.11")},
				},
				IPv6: []iface.IPv6{
					{IP: net.ParseIP("fe80::1")},
					{IP: net.ParseIP("2001:db8::10")},
				},
			}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			operations = append(operations, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		stopDhclient: func(name string) error {
			operations = append(operations, "stop-dhclient "+name)
			return nil
		},
	})

	if err := clearBridgeMemberLayer3("em0"); err != nil {
		t.Fatalf("clear bridge member layer 3: %v", err)
	}
	if len(operations) == 0 || operations[0] != "stop-dhclient em0" {
		t.Fatalf("DHCP must stop before address cleanup, operations: %v", operations)
	}
	for _, expected := range []string{
		"/sbin/ifconfig em0 inet6 -auto_linklocal -accept_rtadv",
		"/sbin/ifconfig em0 inet 192.0.2.10 delete",
		"/sbin/ifconfig em0 inet 192.0.2.11 delete",
		"/sbin/ifconfig em0 inet6 fe80::1%em0 delete",
		"/sbin/ifconfig em0 inet6 2001:db8::10 delete",
	} {
		if commandIndex(operations, expected) == -1 {
			t.Fatalf("missing %q in operations: %v", expected, operations)
		}
	}
}

func TestAddBridgeMemberClearsOnlyVLANMemberLayer3(t *testing.T) {
	var commands []string
	var stopped []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		stopDhclient: func(name string) error {
			stopped = append(stopped, name)
			return nil
		},
	})

	if err := addBridgeMember("testbridge0", "em0", 1500, 100, false); err != nil {
		t.Fatalf("add VLAN bridge member: %v", err)
	}
	if len(stopped) != 1 || stopped[0] != "em0.100" {
		t.Fatalf("stopped DHCP clients = %v, want only em0.100", stopped)
	}
	if commandIndex(commands, "/sbin/ifconfig em0.100 inet6 -auto_linklocal -accept_rtadv") == -1 {
		t.Fatalf("VLAN member was not prepared as layer 2: %v", commands)
	}
	for _, command := range commands {
		if strings.HasPrefix(command, "/sbin/ifconfig em0 inet ") ||
			strings.HasPrefix(command, "/sbin/ifconfig em0 inet6 ") {
			t.Fatalf("parent interface address configuration was changed: %v", commands)
		}
	}
}

func TestAddBridgeMemberDisablesOffloadsBeforeAttachment(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name,
				Capabilities: iface.Capabilities{
					Enabled: iface.Flags{Raw: ifcapTXCSUM | ifcapTXCSUMIPv6 | ifcapTSO4 | ifcapLRO},
				},
			}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		stopDhclient: func(string) error { return nil },
	})

	if err := addBridgeMember("testbridge0", "testport0", 0, 0, true); err != nil {
		t.Fatalf("add bridge member: %v", err)
	}

	offloadCommand := "/sbin/ifconfig testport0 -txcsum -txcsum6 -tso -lro"
	attachCommand := "/sbin/ifconfig testbridge0 addm testport0 up"
	offloadIndex, attachIndex := -1, -1
	for index, command := range commands {
		if command == offloadCommand {
			offloadIndex = index
		}
		if command == attachCommand {
			attachIndex = index
		}
	}
	if offloadIndex == -1 || attachIndex == -1 || offloadIndex >= attachIndex {
		t.Fatalf("offloads must be disabled before bridge attachment, commands: %v", commands)
	}
}

func TestNormalizeStandardSwitchAddressModes(t *testing.T) {
	modes := normalizeStandardSwitchAddressModes(standardSwitchAddressModes{
		network4ID:  1,
		network6ID:  2,
		gateway4ID:  3,
		gateway6ID:  4,
		dhcp:        true,
		disableIPv6: true,
		slaac:       true,
		manual: networkModels.StandardSwitchManualAddresses{
			Network4: "192.0.2.1/24",
			Gateway4: "192.0.2.254",
			Network6: "2001:db8::1/64",
			Gateway6: "2001:db8::fe",
		},
	})

	if modes.network4ID != 0 || modes.gateway4ID != 0 || modes.manual.Network4 != "" || modes.manual.Gateway4 != "" {
		t.Fatalf("DHCP normalization = %#v", modes)
	}
	if modes.network6ID != 0 || modes.gateway6ID != 0 || modes.manual.Network6 != "" || modes.manual.Gateway6 != "" || modes.slaac {
		t.Fatalf("disabled IPv6 normalization = %#v", modes)
	}
}

func TestNormalizeStandardSwitchAddressModesSLAACClearsIPv6Only(t *testing.T) {
	modes := normalizeStandardSwitchAddressModes(standardSwitchAddressModes{
		network4ID: 1,
		network6ID: 2,
		gateway4ID: 3,
		gateway6ID: 4,
		slaac:      true,
		manual: networkModels.StandardSwitchManualAddresses{
			Network4: "192.0.2.1/24",
			Gateway4: "192.0.2.254",
			Network6: "2001:db8::1/64",
			Gateway6: "2001:db8::fe",
		},
	})

	if modes.network4ID != 1 || modes.gateway4ID != 3 || modes.manual.Network4 == "" || modes.manual.Gateway4 == "" {
		t.Fatalf("SLAAC unexpectedly changed IPv4 state: %#v", modes)
	}
	if modes.network6ID != 0 || modes.gateway6ID != 0 || modes.manual.Network6 != "" || modes.manual.Gateway6 != "" || !modes.slaac {
		t.Fatalf("SLAAC normalization = %#v", modes)
	}
}

func TestNewStandardSwitchRejectsInvalidMTU(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	_, err := svc.NewStandardSwitch(
		"switch-invalid-mtu",
		90000,
		0,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil {
		t.Fatal("expected invalid_mtu error, got nil")
	}
	if !errors.Is(err, ErrInvalidStandardSwitch) || StandardSwitchErrorCode(err) != "invalid_standard_switch_mtu" {
		t.Fatalf("expected invalid_standard_switch_mtu error, got %q", err.Error())
	}
}

func TestNewStandardSwitchRejectsInvalidVLAN(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	_, err := svc.NewStandardSwitch(
		"switch-invalid-vlan",
		1500,
		5000,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil {
		t.Fatal("expected invalid_vlan error, got nil")
	}
	if !errors.Is(err, ErrInvalidStandardSwitch) || StandardSwitchErrorCode(err) != "invalid_standard_switch_vlan" {
		t.Fatalf("expected invalid_standard_switch_vlan error, got %q", err.Error())
	}
}

func TestNewStandardSwitchRejectsPortOverlapDeterministically(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	existing := networkModels.StandardSwitch{
		Name:       "existing",
		BridgeName: "vm-existing",
		VLAN:       10,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("failed to seed existing switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{
		Name:     "em0",
		SwitchID: existing.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed existing port: %v", err)
	}

	_, err := svc.NewStandardSwitch(
		"candidate",
		1500,
		10,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil {
		t.Fatal("expected port_overlap error, got nil")
	}

	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_port_conflict" {
		t.Fatalf("expected standard_switch_port_conflict, got %v", err)
	}
}

func TestSyncStandardSwitchesSyncCreatesWhenBridgeMissing(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s1", BridgeName: "vm-s1"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	createCalls := 0
	editCalls := 0

	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return nil, errors.New("interface not found")
	}
	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		createCalls++
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		editCalls++
		return nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("expected create bridge call once, got %d", createCalls)
	}
	if editCalls != 0 {
		t.Fatalf("expected no edit bridge calls, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncReconcilesInPlaceWhenBridgeExists(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s2", BridgeName: "vm-s2"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	createCalls := 0
	editCalls := 0

	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name}, nil
	}
	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		createCalls++
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		editCalls++
		return nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("expected no create bridge calls, got %d", createCalls)
	}
	if editCalls != 1 {
		t.Fatalf("expected edit bridge call once, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncPreservesNonDBMembers(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s3", BridgeName: "vm-s3"}
	setTestStandardSwitchMACSource(&sw, createTestStandardSwitchMACSource(t, svc))
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	origIfaceGet := syncIfaceGet
	origCreate := syncCreateBridge
	origEdit := syncEditBridge
	origRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = origIfaceGet
		syncCreateBridge = origCreate
		syncEditBridge = origEdit
		syncRunCommand = origRun
	})

	getCalls := 0
	currentMAC := ""
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		getCalls++
		switch getCalls {
		case 1:
			return &iface.Interface{
				Name:  name,
				Ether: currentMAC,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
					{Name: "tap0"},
				},
			}, nil
		default:
			return &iface.Interface{
				Name:  name,
				Ether: currentMAC,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		}
	}

	syncCreateBridge = func(sw networkModels.StandardSwitch) error {
		return nil
	}
	syncEditBridge = func(oldSw, newSw networkModels.StandardSwitch) error {
		return nil
	}

	var seenAddMember bool
	var seenBringUp bool
	syncRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/ifconfig" && len(args) == 3 && args[1] == "ether" {
			currentMAC = args[2]
		}
		full := append([]string{command}, args...)
		if strings.Join(full, " ") == "/sbin/ifconfig vm-s3 addm tap0 up" {
			seenAddMember = true
		}
		if strings.Join(full, " ") == "/sbin/ifconfig tap0 up" {
			seenBringUp = true
		}
		return "", nil
	}

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}

	if !seenAddMember {
		t.Fatalf("expected non-db member reattach command, got getCalls=%d", getCalls)
	}
	if !seenBringUp {
		t.Fatal("expected non-db member bring-up command")
	}
}

func TestIsInterfaceMissingError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{err: nil, want: false},
		{err: fmt.Errorf("interface not found"), want: true},
		{err: fmt.Errorf("does not exist"), want: true},
		{err: fmt.Errorf("no such interface"), want: true},
		{err: fmt.Errorf("permission denied"), want: false},
	}

	for _, tt := range tests {
		if got := isInterfaceMissingError(tt.err); got != tt.want {
			t.Fatalf("isInterfaceMissingError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestSyncStandardSwitchesSyncReturnsUnexpectedIfaceError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s4", BridgeName: "vm-s4"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	createCalls := 0
	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return nil, errors.New("permission denied")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: get vm-s4: permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("expected no create calls, got %d", createCalls)
	}
	if editCalls != 0 {
		t.Fatalf("expected no edit calls, got %d", editCalls)
	}
}

func TestSyncStandardSwitchesSyncReturnsCreateError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s5", BridgeName: "vm-s5"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return errors.New("create failed")
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: failed_to_create vm-s5: create failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesContinuesAfterOneSwitchFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	switches := []networkModels.StandardSwitch{
		{Name: "broken", BridgeName: "vm-broken"},
		{Name: "healthy", BridgeName: "vm-healthy"},
	}
	for index := range switches {
		if err := db.Create(&switches[index]).Error; err != nil {
			t.Fatalf("seed switch %s: %v", switches[index].Name, err)
		}
	}

	var created []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			created = append(created, sw.BridgeName)
			if sw.BridgeName == "vm-broken" {
				return errors.New("create failed")
			}
			return nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil || !strings.Contains(err.Error(), "failed_to_create vm-broken") {
		t.Fatalf("expected vm-broken error, got %v", err)
	}
	createdSet := make(map[string]bool, len(created))
	for _, bridgeName := range created {
		createdSet[bridgeName] = true
	}
	if len(created) != 2 || !createdSet["vm-broken"] || !createdSet["vm-healthy"] {
		t.Fatalf("created bridges = %v, want both switches attempted", created)
	}
}

func TestSyncStandardSwitchesSyncReturnsEditError(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s6", BridgeName: "vm-s6"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return errors.New("edit failed")
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: failed_to_reconcile vm-s6: edit failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncSkipsReattachWhenAlreadyPresent(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s7", BridgeName: "vm-s7"}
	setTestStandardSwitchMACSource(&sw, createTestStandardSwitchMACSource(t, svc))
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	runCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name:  name,
					Ether: testStandardSwitchMAC,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name:  name,
				Ether: testStandardSwitchMAC,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
					{Name: "tap0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			runCalls++
			return "", nil
		},
	})

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if runCalls != 0 {
		t.Fatalf("expected no reattach commands when member already present, got %d", runCalls)
	}
}

func TestSyncStandardSwitchesSyncTreatsVLANSubinterfaceAsDBMember(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s8", BridgeName: "vm-s8", VLAN: 10}
	setTestStandardSwitchMACSource(&sw, createTestStandardSwitchMACSource(t, svc))
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0.10"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0.10"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := svc.SyncStandardSwitches(nil, "sync"); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}

	for _, cmd := range commands {
		if strings.Contains(cmd, "em0.10") {
			t.Fatalf("expected no reattach commands for DB VLAN member, got %q", cmd)
		}
	}

	var sawTapAttach bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-s8 addm tap0 up" {
			sawTapAttach = true
		}
	}
	if !sawTapAttach {
		t.Fatalf("expected non-db tap member to be reattached, commands: %v", commands)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorWhenPostReconcileLookupFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s9", BridgeName: "vm-s9"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return nil, errors.New("lookup failed")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: get vm-s9 after reconcile: lookup failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorOnMemberReattachFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s10", BridgeName: "vm-s10"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			if full == "/sbin/ifconfig vm-s10 addm tap0 up" {
				return "", errors.New("addm failed")
			}
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: add member tap0 to vm-s10: addm failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesSyncReturnsErrorOnMemberBringUpFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{Name: "s11", BridgeName: "vm-s11"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: sw.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			}
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
				},
			}, nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			return nil
		},
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			if full == "/sbin/ifconfig tap0 up" {
				return "", errors.New("up failed")
			}
			return "", nil
		},
	})

	err := svc.SyncStandardSwitches(nil, "sync")
	if err == nil {
		t.Fatal("expected sync error, got nil")
	}
	if !strings.Contains(err.Error(), "sync_standard_switches: bring up member tap0: up failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncStandardSwitchesCreateActionCallsCreateBridge(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)

	var got networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		createBridge: func(sw networkModels.StandardSwitch) error {
			got = sw
			return nil
		},
	})

	input := &networkModels.StandardSwitch{Name: "create-test", BridgeName: "vm-create-test"}
	if err := svc.SyncStandardSwitches(input, "create"); err != nil {
		t.Fatalf("expected create sync success, got %v", err)
	}
	if got.BridgeName != "vm-create-test" {
		t.Fatalf("expected create bridge to be called with vm-create-test, got %q", got.BridgeName)
	}
}

func TestSyncStandardSwitchesDeleteActionCallsDeleteBridge(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)

	var got networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		deleteBridge: func(sw networkModels.StandardSwitch) error {
			got = sw
			return nil
		},
	})

	input := &networkModels.StandardSwitch{Name: "delete-test", BridgeName: "vm-delete-test"}
	if err := svc.SyncStandardSwitches(input, "delete"); err != nil {
		t.Fatalf("expected delete sync success, got %v", err)
	}
	if got.BridgeName != "vm-delete-test" {
		t.Fatalf("expected delete bridge to be called with vm-delete-test, got %q", got.BridgeName)
	}
}

func TestSyncStandardSwitchesEditActionSwitchNotFound(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t, &networkModels.StandardSwitch{}, &networkModels.NetworkPort{})

	stubSyncFunctions(t, syncStubSet{
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			t.Fatal("edit bridge should not be called when switch is missing")
			return nil
		},
	})

	input := &networkModels.StandardSwitch{ID: 42, Name: "missing", BridgeName: "vm-missing"}
	err := svc.SyncStandardSwitches(input, "edit")
	if err == nil {
		t.Fatal("expected switch_not_found error, got nil")
	}
	if err.Error() != "switch_not_found" {
		t.Fatalf("expected switch_not_found, got %q", err.Error())
	}
}

func TestSyncStandardSwitchesEditActionLoadsCurrentSwitchAndPorts(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{}, &networkModels.NetworkPort{})

	current := networkModels.StandardSwitch{
		Name:       "current",
		BridgeName: "vm-current",
		MTU:        1500,
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: current.ID}).Error; err != nil {
		t.Fatalf("failed to seed switch port: %v", err)
	}

	previous := networkModels.StandardSwitch{
		ID:         current.ID,
		Name:       current.Name,
		BridgeName: current.BridgeName,
		MTU:        1400,
	}

	var gotOld networkModels.StandardSwitch
	var gotNew networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		editBridge: func(oldSw, newSw networkModels.StandardSwitch) error {
			gotOld = oldSw
			gotNew = newSw
			return nil
		},
	})

	if err := svc.SyncStandardSwitches(&previous, "edit"); err != nil {
		t.Fatalf("expected edit sync success, got %v", err)
	}
	if gotOld.MTU != 1400 {
		t.Fatalf("expected old switch MTU 1400, got %d", gotOld.MTU)
	}
	if gotNew.MTU != 1500 {
		t.Fatalf("expected new switch MTU 1500 from DB, got %d", gotNew.MTU)
	}
	if len(gotNew.Ports) != 1 || gotNew.Ports[0].Name != "em0" {
		t.Fatalf("expected DB preloaded ports, got %+v", gotNew.Ports)
	}
}

func TestRunDhclientRecognizesLegacyBoundClientWithoutPIDFile(t *testing.T) {
	useTestDhclientRuntimeDir(t)
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name, IPv4: []iface.IPv4{{IP: net.ParseIP("192.0.2.10")}}}, nil
		},
		runCommandAllowExitCode: func(command string, _ []int, args ...string) (string, error) {
			if command == "/bin/pgrep" && strings.Join(args, " ") == "-f -x dhclient: vm-legacy" {
				return "321\n", nil
			}
			return "", nil
		},
		runCommandWithContext: func(context.Context, string, ...string) (string, error) {
			t.Fatal("legacy client must not be started a second time")
			return "", nil
		},
	})

	if err := runDhclient("vm-legacy", 10); err != nil {
		t.Fatalf("recognize legacy dhclient: %v", err)
	}
}

func TestDeleteDHCPStandardBridgeDestroysInterfaceBeforeLegacyProcessFallback(t *testing.T) {
	useTestDhclientRuntimeDir(t)
	skipDhclientNaturalExitGrace(t)
	var sequence []string
	stopped := false
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			sequence = append(sequence, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		runCommandAllowExitCode: func(command string, _ []int, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			sequence = append(sequence, full)
			if command == "/bin/pkill" {
				stopped = true
				return "", nil
			}
			if stopped {
				return "", nil
			}
			return "654\n", nil
		},
	})

	sw := networkModels.StandardSwitch{BridgeName: "vm-dhcp-delete", DHCP: true}
	if err := deleteStandardBridge(sw); err != nil {
		t.Fatalf("delete DHCP standard bridge: %v", err)
	}

	destroy := commandIndex(sequence, "/sbin/ifconfig vm-dhcp-delete destroy")
	legacyStop := commandIndex(sequence, "/bin/pkill -TERM -f -x dhclient: vm-dhcp-delete")
	if destroy == -1 || legacyStop == -1 || destroy >= legacyStop {
		t.Fatalf("bridge must be destroyed before process fallback, sequence: %v", sequence)
	}
	for _, command := range sequence {
		if strings.Contains(command, "/sbin/dhclient -r") {
			t.Fatalf("unsupported dhclient release command used: %v", sequence)
		}
	}
}

func TestEditStandardBridgeRecreatesWhenDisablingDHCPAndPreservesExtraMembers(t *testing.T) {
	var operations []string
	lookup := 0
	oldSw := networkModels.StandardSwitch{
		Name:       "dhcp-old",
		BridgeName: "vm-dhcp-edit",
		DHCP:       true,
		Ports:      []networkModels.NetworkPort{{Name: "em0"}},
	}
	newSw := networkModels.StandardSwitch{
		Name:          "dhcp-new",
		BridgeName:    "vm-dhcp-edit",
		NetworkManual: "192.0.2.10/24",
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			lookup++
			switch lookup {
			case 1:
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap0"},
					},
				}, nil
			case 2:
				return &iface.Interface{Name: name}, nil
			default:
				return &iface.Interface{Name: name}, nil
			}
		},
		deleteBridge: func(sw networkModels.StandardSwitch) error {
			operations = append(operations, "delete:"+sw.BridgeName)
			return nil
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			operations = append(operations, "create:"+sw.BridgeName)
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			operations = append(operations, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("disable DHCP: %v", err)
	}
	want := []string{
		"delete:vm-dhcp-edit",
		"create:vm-dhcp-edit",
		"/sbin/ifconfig vm-dhcp-edit addm tap0 up",
		"/sbin/ifconfig tap0 up",
	}
	if strings.Join(operations, "|") != strings.Join(want, "|") {
		t.Fatalf("DHCP transition operations = %v, want %v", operations, want)
	}
}

func TestCreateStandardBridgeAssignsHostLikeIPv4WithoutGateway(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge42\n", nil
			}
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:        "host-like-create",
		BridgeName:  "vm-host-create",
		DisableIPv6: true,
		NetworkObj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "10.80.0.254/24"}},
		},
	}

	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("expected create bridge success, got %v", err)
	}

	var sawAssign bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-host-create inet 10.80.0.254/24" {
			sawAssign = true
			break
		}
	}
	if !sawAssign {
		t.Fatalf("expected IPv4 assignment command, got commands: %v", commands)
	}
}

func TestCreateStandardBridgeSkipsSubnetBaseIPv4WithoutGateway(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge43\n", nil
			}
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:        "subnet-base-create",
		BridgeName:  "vm-subnet-create",
		DisableIPv6: true,
		NetworkObj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "10.80.0.0/24"}},
		},
	}

	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("expected create bridge success, got %v", err)
	}

	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-subnet-create inet 10.80.0.0/24" {
			t.Fatalf("expected no IPv4 assignment for subnet-base CIDR, got commands: %v", commands)
		}
	}
}

func TestEditStandardBridgeAssignsHostLikeIPv4WithoutGateway(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-host",
		BridgeName:  "vm-edit-host",
		DisableIPv6: true,
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-host",
		BridgeName:  "vm-edit-host",
		DisableIPv6: true,
		NetworkObj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "10.90.0.254/24"}},
		},
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	var sawAssign bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-edit-host inet 10.90.0.254/24" {
			sawAssign = true
			break
		}
	}
	if !sawAssign {
		t.Fatalf("expected IPv4 assignment command, got commands: %v", commands)
	}
}

func TestEditStandardBridgeSkipsSubnetBaseIPv4WithoutGateway(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-subnet",
		BridgeName:  "vm-edit-subnet",
		DisableIPv6: true,
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-subnet",
		BridgeName:  "vm-edit-subnet",
		DisableIPv6: true,
		NetworkObj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "10.90.0.0/24"}},
		},
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-edit-subnet inet 10.90.0.0/24" {
			t.Fatalf("expected no IPv4 assignment for subnet-base CIDR, got commands: %v", commands)
		}
	}
}

func TestEditStandardBridgeAddsIPv6WhenDisableIPv6FlipsFalse(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-ipv6-flip-on",
		BridgeName:  "vm-edit-ipv6-flip-on",
		DisableIPv6: true,
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-ipv6-flip-on",
		BridgeName:  "vm-edit-ipv6-flip-on",
		DisableIPv6: false,
		Network6Obj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:1::1/64"}},
		},
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	var sawIfDisabledClear, sawAssign bool
	var clearIdx, assignIdx int = -1, -1
	for i, cmd := range commands {
		if strings.Contains(cmd, "auto_linklocal") && strings.Contains(cmd, "-ifdisabled") {
			sawIfDisabledClear = true
			clearIdx = i
		}
		if cmd == "/sbin/ifconfig vm-edit-ipv6-flip-on inet6 2001:db8:1::1/64" {
			sawAssign = true
			assignIdx = i
		}
	}
	if !sawIfDisabledClear {
		t.Fatalf("expected -ifdisabled clear, got commands: %v", commands)
	}
	if !sawAssign {
		t.Fatalf("expected IPv6 assignment, got commands: %v", commands)
	}
	if clearIdx >= assignIdx {
		t.Fatalf("-ifdisabled must be cleared BEFORE IPv6 address assignment, got commands: %v", commands)
	}
}

func TestEditStandardBridgeDisablesIPv6WhenFlagFlipsTrue(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-ipv6-flip-off",
		BridgeName:  "vm-edit-ipv6-flip-off",
		DisableIPv6: false,
		Network6Obj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:2::1/64"}},
		},
		Gateway6AddressObj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:2::ff"}},
		},
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-ipv6-flip-off",
		BridgeName:  "vm-edit-ipv6-flip-off",
		DisableIPv6: true,
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	var sawDelAddr, sawDelRoute, sawIfDisabled bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-edit-ipv6-flip-off inet6 2001:db8:2::1/64 delete" {
			sawDelAddr = true
		}
		if cmd == "/sbin/route -6 delete -net 2001:db8:2::1/64 2001:db8:2::ff" {
			sawDelRoute = true
		}
		if cmd == "/sbin/ifconfig vm-edit-ipv6-flip-off inet6 -accept_rtadv ifdisabled" {
			sawIfDisabled = true
		}
	}
	if !sawDelAddr {
		t.Fatalf("expected old IPv6 address deletion, got commands: %v", commands)
	}
	if !sawDelRoute {
		t.Fatalf("expected old IPv6 route deletion, got commands: %v", commands)
	}
	if !sawIfDisabled {
		t.Fatalf("expected ifdisabled flag set, got commands: %v", commands)
	}
}

func TestEditStandardBridgeSkipsIPv6WhenStillDisabled(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-ipv6-still-off",
		BridgeName:  "vm-edit-ipv6-still-off",
		DisableIPv6: true,
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-ipv6-still-off",
		BridgeName:  "vm-edit-ipv6-still-off",
		DisableIPv6: true,
		Network6Obj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:3::1/64"}},
		},
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-edit-ipv6-still-off inet6 2001:db8:3::1/64" {
			t.Fatalf("expected no IPv6 assignment when disabled, got commands: %v", commands)
		}
	}
}

func TestEditStandardBridgeReplacesIPv6WhenNetworkChanges(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "old-edit-ipv6-replace",
		BridgeName:  "vm-edit-ipv6-replace",
		DisableIPv6: false,
		Network6Obj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:4::1/64"}},
		},
	}
	newSw := networkModels.StandardSwitch{
		Name:        "new-edit-ipv6-replace",
		BridgeName:  "vm-edit-ipv6-replace",
		DisableIPv6: false,
		Network6Obj: &networkModels.Object{
			Entries: []networkModels.ObjectEntry{{Value: "2001:db8:5::1/64"}},
		},
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	var sawDel, sawAdd bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-edit-ipv6-replace inet6 2001:db8:4::1/64 delete" {
			sawDel = true
		}
		if cmd == "/sbin/ifconfig vm-edit-ipv6-replace inet6 2001:db8:5::1/64" {
			sawAdd = true
		}
	}
	if !sawDel {
		t.Fatalf("expected old IPv6 deletion, got commands: %v", commands)
	}
	if !sawAdd {
		t.Fatalf("expected new IPv6 assignment, got commands: %v", commands)
	}
}

func TestValidateStandardSwitchManual(t *testing.T) {
	tests := []struct {
		name                         string
		net4Id, gw4Id, net6Id, gw6Id uint
		manual                       networkModels.StandardSwitchManualAddresses
		wantErr                      string
		wantNetwork4, wantGateway4   string
		wantNetwork6, wantGateway6   string
	}{
		{
			name:         "valid manual values are trimmed and returned",
			manual:       networkModels.StandardSwitchManualAddresses{Network4: "  10.0.0.1/24 ", Gateway4: " 10.0.0.254 ", Network6: "2001:db8::1/64", Gateway6: "fe80::1"},
			wantNetwork4: "10.0.0.1/24",
			wantGateway4: "10.0.0.254",
			wantNetwork6: "2001:db8::1/64",
			wantGateway6: "fe80::1",
		},
		{
			name:    "network4 object and manual are mutually exclusive",
			net4Id:  5,
			manual:  networkModels.StandardSwitchManualAddresses{Network4: "10.0.0.1/24"},
			wantErr: "standard_switch_network4_source_conflict",
		},
		{
			name:    "gateway4 object and manual are mutually exclusive",
			gw4Id:   5,
			manual:  networkModels.StandardSwitchManualAddresses{Gateway4: "10.0.0.254"},
			wantErr: "standard_switch_gateway4_source_conflict",
		},
		{
			name:    "network6 object and manual are mutually exclusive",
			net6Id:  5,
			manual:  networkModels.StandardSwitchManualAddresses{Network6: "2001:db8::1/64"},
			wantErr: "standard_switch_network6_source_conflict",
		},
		{
			name:    "gateway6 object and manual are mutually exclusive",
			gw6Id:   5,
			manual:  networkModels.StandardSwitchManualAddresses{Gateway6: "fe80::1"},
			wantErr: "standard_switch_gateway6_source_conflict",
		},
		{
			name:    "network4 manual without prefix is rejected",
			manual:  networkModels.StandardSwitchManualAddresses{Network4: "10.0.0.1"},
			wantErr: "invalid_standard_switch_network4_manual",
		},
		{
			name:    "network4 manual that is actually IPv6 is rejected",
			manual:  networkModels.StandardSwitchManualAddresses{Network4: "2001:db8::/64"},
			wantErr: "invalid_standard_switch_network4_manual",
		},
		{
			name:    "gateway4 manual that is a CIDR is rejected",
			manual:  networkModels.StandardSwitchManualAddresses{Gateway4: "10.0.0.0/24"},
			wantErr: "invalid_standard_switch_gateway4_manual",
		},
		{
			name:    "network6 manual that is actually IPv4 is rejected",
			manual:  networkModels.StandardSwitchManualAddresses{Network6: "10.0.0.0/24"},
			wantErr: "invalid_standard_switch_network6_manual",
		},
		{
			name:    "gateway6 manual that is IPv4 is rejected",
			manual:  networkModels.StandardSwitchManualAddresses{Gateway6: "10.0.0.1"},
			wantErr: "invalid_standard_switch_gateway6_manual",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateStandardSwitchManual(tt.net4Id, tt.gw4Id, tt.net6Id, tt.gw6Id, tt.manual)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got.Network4 != tt.wantNetwork4 || got.Gateway4 != tt.wantGateway4 ||
				got.Network6 != tt.wantNetwork6 || got.Gateway6 != tt.wantGateway6 {
				t.Fatalf("trimmed mismatch: got %+v", got)
			}
		})
	}
}

func TestStandardSwitchManualHelperFallback(t *testing.T) {
	swBoth := networkModels.StandardSwitch{
		NetworkObj:     &networkModels.Object{Entries: []networkModels.ObjectEntry{{Value: "10.0.0.1/24"}}},
		NetworkManual:  "172.16.0.1/24",
		Network6Manual: "2001:db8::1/64",
		GatewayManual:  "10.0.0.254",
		Gateway6Manual: "fe80::1",
	}
	if got := swBoth.Network(4); got != "10.0.0.1/24" {
		t.Fatalf("expected object value to win for Network(4), got %q", got)
	}
	if got := swBoth.Network(6); got != "2001:db8::1/64" {
		t.Fatalf("expected manual fallback for Network(6), got %q", got)
	}
	if got := swBoth.Gateway(4); got != "10.0.0.254" {
		t.Fatalf("expected manual fallback for Gateway(4), got %q", got)
	}
	if got := swBoth.Gateway(6); got != "fe80::1" {
		t.Fatalf("expected manual fallback for Gateway(6), got %q", got)
	}

	empty := networkModels.StandardSwitch{}
	if empty.Network(4) != "" || empty.Network(6) != "" || empty.Gateway(4) != "" || empty.Gateway(6) != "" {
		t.Fatalf("expected empty strings when neither object nor manual set")
	}
}

func TestNewStandardSwitchStoresManualAddresses(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	stubSyncFunctions(t, syncStubSet{
		createBridge: func(networkModels.StandardSwitch) error { return nil },
	})

	macSource := createTestStandardSwitchMACSource(t, svc)

	_, err := svc.NewStandardSwitch(
		"manual-store",
		1500,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		macSource,
		false,
		false,
		false,
		false,
		false,
		true,
		networkModels.StandardSwitchManualAddresses{
			Network4: "10.81.0.254/24",
			Gateway4: "10.81.0.1",
			Network6: "2001:db8:81::1/64",
			Gateway6: "fe80::1",
		},
	)
	if err != nil {
		t.Fatalf("expected create success, got %v", err)
	}

	var got networkModels.StandardSwitch
	if err := db.Where("name = ?", "manual-store").First(&got).Error; err != nil {
		t.Fatalf("failed to load created switch: %v", err)
	}

	if got.NetworkID != nil || got.GatewayAddressID != nil || got.Network6ID != nil || got.Gateway6AddressID != nil {
		t.Fatalf("expected no object FKs set for manual switch, got %+v", got)
	}
	if got.BridgeMACMode != networkModels.StandardSwitchMACModeObject || got.BridgeMACObjectID == nil || *got.BridgeMACObjectID != macSource.MACObjectID {
		t.Fatalf("bridge MAC object source not persisted: %+v", got)
	}
	if got.NetworkManual != "10.81.0.254/24" || got.GatewayManual != "10.81.0.1" ||
		got.Network6Manual != "2001:db8:81::1/64" || got.Gateway6Manual != "fe80::1" {
		t.Fatalf("manual columns not persisted: %+v", got)
	}
	if got.Network(4) != "10.81.0.254/24" || got.Gateway(6) != "fe80::1" {
		t.Fatalf("helpers did not resolve manual values: net4=%q gw6=%q", got.Network(4), got.Gateway(6))
	}
	if !got.DisableBridgeOffloads {
		t.Fatal("expected bridge offload policy to be persisted")
	}
}

func TestNewStandardSwitchRejectsObjectAndManualConflict(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	obj := networkModels.Object{
		Name:    "net-obj",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "10.0.0.0/24"}},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	_, err := svc.NewStandardSwitch(
		"conflict-sw",
		1500,
		0,
		obj.ID,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{Network4: "10.0.0.1/24"},
	)
	if err == nil {
		t.Fatal("expected mutual-exclusivity error, got nil")
	}
	if StandardSwitchErrorCode(err) != "standard_switch_network4_source_conflict" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditStandardSwitchObjectToManualClearsFK(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	obj := networkModels.Object{
		Name:    "net-obj-edit",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "10.0.0.1/24"}},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	sw := networkModels.StandardSwitch{
		Name:       "o2m",
		BridgeName: "vm-o2m",
		MTU:        1500,
		NetworkID:  &obj.ID,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
	})

	err := svc.EditStandardSwitch(
		sw.ID,
		1500,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		true,
		networkModels.StandardSwitchManualAddresses{Network4: "10.9.0.1/24"},
	)
	if err != nil {
		t.Fatalf("expected edit success, got %v", err)
	}

	var got networkModels.StandardSwitch
	if err := db.First(&got, sw.ID).Error; err != nil {
		t.Fatalf("failed to reload switch: %v", err)
	}
	if got.NetworkID != nil {
		t.Fatalf("expected NetworkID cleared, got %v", *got.NetworkID)
	}
	if got.NetworkManual != "10.9.0.1/24" {
		t.Fatalf("expected NetworkManual set, got %q", got.NetworkManual)
	}
	if !got.DisableBridgeOffloads {
		t.Fatal("expected bridge offload policy to be updated")
	}
}

func TestEditStandardSwitchManualToObjectClearsManual(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	obj := networkModels.Object{
		Name:    "net-obj-m2o",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "10.0.0.1/24"}},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	sw := networkModels.StandardSwitch{
		Name:          "m2o",
		BridgeName:    "vm-m2o",
		MTU:           1500,
		NetworkManual: "10.5.0.1/24",
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
	})

	err := svc.EditStandardSwitch(
		sw.ID,
		1500,
		0,
		obj.ID,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err != nil {
		t.Fatalf("expected edit success, got %v", err)
	}

	var got networkModels.StandardSwitch
	if err := db.First(&got, sw.ID).Error; err != nil {
		t.Fatalf("failed to reload switch: %v", err)
	}
	if got.NetworkManual != "" {
		t.Fatalf("expected NetworkManual cleared when switching to object, got %q", got.NetworkManual)
	}
	if got.NetworkID == nil || *got.NetworkID != obj.ID {
		t.Fatalf("expected NetworkID set to object, got %v", got.NetworkID)
	}
}

func TestCreateStandardBridgeAppliesManualIPv4(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge99\n", nil
			}
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:          "manual-apply4",
		BridgeName:    "vm-manual-apply4",
		DisableIPv6:   true,
		NetworkManual: "10.81.0.254/24",
	}

	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("expected create bridge success, got %v", err)
	}

	var sawAssign bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-manual-apply4 inet 10.81.0.254/24" {
			sawAssign = true
			break
		}
	}
	if !sawAssign {
		t.Fatalf("expected manual IPv4 assignment, got commands: %v", commands)
	}
}

func TestCreateStandardBridgeAppliesManualIPv6ScopedLinkLocalGateway(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge100\n", nil
			}
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:           "manual-apply6",
		BridgeName:     "vm-manual-apply6",
		Network6Manual: "2001:db8::1/64",
		Gateway6Manual: "fe80::1",
	}

	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("expected create bridge success, got %v", err)
	}

	var sawScopedRoute bool
	for _, cmd := range commands {
		if cmd == "/sbin/route -6 add -net 2001:db8::1/64 fe80::1%vm-manual-apply6" {
			sawScopedRoute = true
			break
		}
	}
	if !sawScopedRoute {
		t.Fatalf("expected scoped manual link-local IPv6 route, got commands: %v", commands)
	}
}

func TestEditStandardBridgePreservesNonDatabaseMembers(t *testing.T) {
	var commands []string
	getCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			getCalls++
			if getCalls == 1 {
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "custom0"},
					},
				}, nil
			}
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			return "", nil
		},
	})

	oldSw := networkModels.StandardSwitch{
		Name:       "svm-vlan-preserve",
		BridgeName: "vm-svm-vlan-preserve",
	}
	newSw := networkModels.StandardSwitch{
		Name:       "svm-vlan-preserve",
		BridgeName: "vm-svm-vlan-preserve",
	}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("expected edit bridge success, got %v", err)
	}

	var sawMemberAttach bool
	for _, cmd := range commands {
		if cmd == "/sbin/ifconfig vm-svm-vlan-preserve addm custom0 up" {
			sawMemberAttach = true
			break
		}
	}
	if !sawMemberAttach {
		t.Fatalf("expected non-database member to be reattached, got commands: %v", commands)
	}
}

func TestNewStandardSwitchRollsBackDatabaseWhenRuntimeCreateFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			return errors.New("runtime create failed")
		},
	})

	_, err := svc.NewStandardSwitch(
		"runtime-failure",
		1500,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		true,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil || !strings.Contains(err.Error(), "runtime create failed") {
		t.Fatalf("expected runtime create failure, got %v", err)
	}

	var switchCount, portCount int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&switchCount).Error; err != nil {
		t.Fatalf("count switches: %v", err)
	}
	if err := db.Model(&networkModels.NetworkPort{}).Count(&portCount).Error; err != nil {
		t.Fatalf("count ports: %v", err)
	}
	if switchCount != 0 || portCount != 0 {
		t.Fatalf("runtime failure left database rows: switches=%d ports=%d", switchCount, portCount)
	}
}

func TestEditStandardSwitchRollsBackDatabaseAndRestoresRuntime(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{
		Name:       "rollback-edit",
		BridgeName: "vm-rollback-edit",
		MTU:        1500,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}

	var deletedRuntime networkModels.StandardSwitch
	var restoredRuntime networkModels.StandardSwitch
	var commands []string
	bridgeLookups := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case sw.BridgeName:
				bridgeLookups++
				if bridgeLookups == 1 {
					return &iface.Interface{
						Name:          name,
						BridgeMembers: []iface.BridgeMember{{Name: "tap0"}},
					}, nil
				}
				return &iface.Interface{Name: name}, nil
			case "tap0":
				return &iface.Interface{Name: name}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			return errors.New("runtime edit failed")
		},
		deleteBridge: func(current networkModels.StandardSwitch) error {
			deletedRuntime = current
			return nil
		},
		createBridge: func(previous networkModels.StandardSwitch) error {
			restoredRuntime = previous
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	err := svc.EditStandardSwitch(
		sw.ID,
		9000,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		false,
		false,
		false,
		true,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil || !strings.Contains(err.Error(), "runtime edit failed") {
		t.Fatalf("expected runtime edit failure, got %v", err)
	}

	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, sw.ID).Error; err != nil {
		t.Fatalf("reload switch: %v", err)
	}
	if persisted.MTU != 1500 {
		t.Fatalf("database update was not rolled back: MTU=%d", persisted.MTU)
	}
	if deletedRuntime.MTU != 9000 {
		t.Fatalf("expected attempted runtime state to be cleaned, got MTU=%d", deletedRuntime.MTU)
	}
	if restoredRuntime.MTU != 1500 {
		t.Fatalf("expected previous runtime state to be restored, got MTU=%d", restoredRuntime.MTU)
	}
	if len(commands) != 2 || commands[0] != "/sbin/ifconfig vm-rollback-edit addm tap0 up" ||
		commands[1] != "/sbin/ifconfig tap0 up" {
		t.Fatalf("expected extra bridge member to be restored, commands=%v", commands)
	}
}

func TestEditStandardSwitchCreatesUpdatedRuntimeWhenBridgeIsMissing(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{
		Name:        "missing-runtime-edit",
		BridgeName:  "vm-missing-edit",
		MTU:         1500,
		DisableIPv6: true,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}

	var created []networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(candidate networkModels.StandardSwitch) error {
			created = append(created, candidate)
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("missing runtime must be created, not edited")
			return nil
		},
	})

	err := svc.EditStandardSwitch(
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
		true,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err != nil {
		t.Fatalf("edit missing runtime: %v", err)
	}
	if len(created) != 1 || created[0].MTU != 9000 || !created[0].Private {
		t.Fatalf("created runtime = %#v, want updated MTU/private state", created)
	}

	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, sw.ID).Error; err != nil {
		t.Fatalf("reload switch: %v", err)
	}
	if persisted.MTU != 9000 || !persisted.Private {
		t.Fatalf("persisted switch = %#v, want updated MTU/private state", persisted)
	}
}

func TestEditStandardSwitchMissingRuntimeFailureRestoresPreviousWithoutDelete(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	sw := networkModels.StandardSwitch{
		Name:        "missing-runtime-rollback",
		BridgeName:  "vm-missing-rb",
		MTU:         1500,
		DisableIPv6: true,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}

	var created []networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(candidate networkModels.StandardSwitch) error {
			created = append(created, candidate)
			if len(created) == 1 {
				return errors.New("runtime create failed")
			}
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("missing runtime must be created, not edited")
			return nil
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			t.Fatal("failed create cleanup must not delete pre-existing routes")
			return nil
		},
	})

	err := svc.EditStandardSwitch(
		sw.ID,
		9000,
		0,
		0,
		0,
		0,
		0,
		[]string{},
		createTestStandardSwitchMACSource(t, svc),
		false,
		false,
		true,
		false,
		false,
		true,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err == nil || !strings.Contains(err.Error(), "runtime create failed") {
		t.Fatalf("expected runtime create failure, got %v", err)
	}
	if len(created) != 2 || created[0].MTU != 9000 || created[1].MTU != 1500 {
		t.Fatalf("create attempts = %#v, want updated then previous runtime", created)
	}

	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, sw.ID).Error; err != nil {
		t.Fatalf("reload switch: %v", err)
	}
	if persisted.MTU != 1500 {
		t.Fatalf("database update was not rolled back: MTU=%d", persisted.MTU)
	}
}

func TestDeleteStandardBridgeDestroysEveryManagedVLAN(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name, Groups: []string{"svm-vlan"}}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		BridgeName: "vm-vlan-delete",
		VLAN:       100,
		Ports: []networkModels.NetworkPort{
			{Name: "em0"},
			{Name: "em1"},
		},
	}
	if err := deleteStandardBridge(sw); err != nil {
		t.Fatalf("delete standard bridge: %v", err)
	}

	for _, expected := range []string{
		"/sbin/ifconfig em0.100 destroy",
		"/sbin/ifconfig em1.100 destroy",
	} {
		found := false
		for _, command := range commands {
			if command == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in commands: %v", expected, commands)
		}
	}
}

func TestCreateStandardBridgeDefaultsLegacyZeroMTU(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge101\n", nil
			}
			return "", nil
		},
	})

	sw := networkModels.StandardSwitch{
		Name:        "legacy-mtu",
		BridgeName:  "vm-legacy-mtu",
		DisableIPv6: true,
	}
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create standard bridge: %v", err)
	}

	for _, command := range commands {
		if command == "/sbin/ifconfig vm-legacy-mtu mtu 1500" {
			return
		}
	}
	t.Fatalf("expected legacy zero MTU to normalize to 1500, commands: %v", commands)
}

func TestCreateStandardBridgeToleratesExistingDefaultRoute(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			switch full {
			case "/sbin/ifconfig bridge create":
				return "bridge301\n", nil
			case "/sbin/route add default 192.0.2.254":
				return "add net default: gateway 192.0.2.254: route already in table", errors.New("exit status 1")
			case "/sbin/route -n get default":
				return "route to: 0.0.0.0\ngateway: 192.0.2.254\ninterface: vm-existing-def\n", nil
			default:
				return "", nil
			}
		},
	})

	sw := networkModels.StandardSwitch{
		Name:          "existing-default",
		BridgeName:    "vm-existing-def",
		MTU:           1500,
		NetworkManual: "192.0.2.1/24",
		GatewayManual: "192.0.2.254",
		DefaultRoute:  true,
		DisableIPv6:   true,
	}
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("existing default route must be idempotent: %v", err)
	}
	if commandIndex(commands, "/sbin/route add default 192.0.2.254") == -1 {
		t.Fatalf("default route was not attempted, commands: %v", commands)
	}
}

func TestCreateStandardBridgeRejectsExistingDefaultRouteOnWrongInterface(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			switch full {
			case "/sbin/ifconfig bridge create":
				return "bridge305\n", nil
			case "/sbin/route add default 192.0.2.254":
				return "route already in table", errors.New("exit status 1")
			case "/sbin/route -n get default":
				return "route to: 0.0.0.0\ngateway: 192.0.2.254\ninterface: em0\n", nil
			default:
				return "", nil
			}
		},
	})

	sw := networkModels.StandardSwitch{
		Name:          "wrong-default-interface",
		BridgeName:    "vm-wrong-def-if",
		MTU:           1500,
		NetworkManual: "192.0.2.1/24",
		GatewayManual: "192.0.2.254",
		DefaultRoute:  true,
		DisableIPv6:   true,
	}
	sw = withTestStandardSwitchMAC(sw)
	err := createStandardBridge(sw)
	if err == nil || !strings.Contains(err.Error(), "default route already exists on interface em0") {
		t.Fatalf("expected wrong-interface default route error, got %v", err)
	}
}

func TestCreateStandardBridgeInstallsRoutesAfterMemberAddressRemoval(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if full == "/sbin/ifconfig bridge create" {
				return "bridge304\n", nil
			}
			return "", nil
		},
		stopDhclient: func(string) error { return nil },
	})

	sw := networkModels.StandardSwitch{
		Name:          "route-after-member",
		BridgeName:    "vm-route-member",
		MTU:           1500,
		NetworkManual: "192.0.2.1/24",
		GatewayManual: "192.0.2.254",
		DefaultRoute:  true,
		DisableIPv6:   true,
		Ports:         []networkModels.NetworkPort{{Name: "em0"}},
	}
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("create standard bridge: %v", err)
	}

	macIndex := commandIndex(commands, "/sbin/ifconfig vm-route-member ether 02:00:00:00:00:01")
	attachIndex := commandIndex(commands, "/sbin/ifconfig vm-route-member addm em0 up")
	networkRouteIndex := commandIndex(commands, "/sbin/route add -net 192.0.2.1/24 192.0.2.254")
	defaultRouteIndex := commandIndex(commands, "/sbin/route add default 192.0.2.254")
	if macIndex == -1 || attachIndex == -1 || macIndex >= attachIndex {
		t.Fatalf("bridge MAC must be fixed before member attachment, commands: %v", commands)
	}
	if networkRouteIndex <= attachIndex || defaultRouteIndex <= attachIndex {
		t.Fatalf("routes must be installed after member attachment, commands: %v", commands)
	}
}

func TestEditStandardBridgeInstallsRoutesAfterMemberAddressRemoval(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		stopDhclient: func(string) error { return nil },
	})

	oldSw := networkModels.StandardSwitch{
		Name:        "route-before-edit",
		BridgeName:  "vm-route-edit",
		MTU:         1500,
		DisableIPv6: true,
	}
	newSw := oldSw
	newSw.Name = "route-after-edit"
	newSw.NetworkManual = "192.0.2.1/24"
	newSw.GatewayManual = "192.0.2.254"
	newSw.DefaultRoute = true
	newSw.Ports = []networkModels.NetworkPort{{Name: "em0"}}

	oldSw = withTestStandardSwitchMAC(oldSw)
	newSw = withTestStandardSwitchMAC(newSw)
	if err := editStandardBridge(oldSw, newSw); err != nil {
		t.Fatalf("edit standard bridge: %v", err)
	}

	attachIndex := commandIndex(commands, "/sbin/ifconfig vm-route-edit addm em0 up")
	networkRouteIndex := commandIndex(commands, "/sbin/route add -net 192.0.2.1/24 192.0.2.254")
	defaultRouteIndex := commandIndex(commands, "/sbin/route add default 192.0.2.254")
	if attachIndex == -1 || networkRouteIndex <= attachIndex || defaultRouteIndex <= attachIndex {
		t.Fatalf("routes must be installed after member attachment, commands: %v", commands)
	}
}

func TestCreateStandardBridgeDoesNotDeletePreexistingRouteDuringCleanup(t *testing.T) {
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			switch full {
			case "/sbin/ifconfig bridge create":
				return "bridge302\n", nil
			case "/sbin/route add -net 192.0.2.1/24 192.0.2.254",
				"/sbin/route add default 192.0.2.254":
				return "route already in table", errors.New("exit status 1")
			case "/sbin/route -n get default":
				return "route to: 0.0.0.0\ngateway: 192.0.2.254\ninterface: vm-existing-cleanup\n", nil
			case "/sbin/route -6 add -net 2001:db8::1/64 2001:db8::fe":
				return "", errors.New("IPv6 route failed")
			default:
				return "", nil
			}
		},
	})

	sw := networkModels.StandardSwitch{
		Name:           "existing-route-cleanup",
		BridgeName:     "vm-existing-cleanup",
		MTU:            1500,
		NetworkManual:  "192.0.2.1/24",
		GatewayManual:  "192.0.2.254",
		Network6Manual: "2001:db8::1/64",
		Gateway6Manual: "2001:db8::fe",
		DefaultRoute:   true,
	}
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err == nil || !strings.Contains(err.Error(), "IPv6 route failed") {
		t.Fatalf("expected later bridge failure, got %v", err)
	}
	for _, command := range commands {
		if strings.HasPrefix(command, "/sbin/route delete") {
			t.Fatalf("cleanup deleted a route it did not add: %v", commands)
		}
	}
}

func TestCreateStandardBridgeRejectsConflictingDefaultRoute(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			switch full {
			case "/sbin/ifconfig bridge create":
				return "bridge303\n", nil
			case "/sbin/route add default 192.0.2.254":
				return "route already in table", errors.New("exit status 1")
			case "/sbin/route -n get default":
				return "route to: 0.0.0.0\ngateway: 198.51.100.1\ninterface: em0\n", nil
			default:
				return "", nil
			}
		},
	})

	sw := networkModels.StandardSwitch{
		Name:          "conflicting-default",
		BridgeName:    "vm-conflict-def",
		MTU:           1500,
		NetworkManual: "192.0.2.1/24",
		GatewayManual: "192.0.2.254",
		DefaultRoute:  true,
		DisableIPv6:   true,
	}
	sw = withTestStandardSwitchMAC(sw)
	err := createStandardBridge(sw)
	if err == nil || !strings.Contains(err.Error(), "default route already exists via 198.51.100.1") {
		t.Fatalf("expected conflicting default route error, got %v", err)
	}
}

func TestStandardSwitchDeletePreflightDetectsSambaInterfaceUsage(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &sambaModels.SambaSettings{})
	if err := db.Create(&sambaModels.SambaSettings{Interfaces: "lo0, vm-samba"}).Error; err != nil {
		t.Fatalf("seed Samba settings: %v", err)
	}

	err := svc.checkStandardSwitchExternalUsage("vm-samba")
	if !errors.Is(err, ErrStandardSwitchInUse) {
		t.Fatalf("expected in-use error, got %v", err)
	}
	if code := StandardSwitchErrorCode(err); code != "standard_switch_in_use_by_samba" {
		t.Fatalf("unexpected error code: %q", code)
	}
}

func TestCreateStandardBridgeToleratesPortIPv6CleanupPermissionDenied(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			if full == "/sbin/ifconfig bridge create" {
				return "bridge200\n", nil
			}
			if full == "/sbin/ifconfig em0 inet6 -auto_linklocal -accept_rtadv" {
				return "", fmt.Errorf("command execution failed: exit status 1, " +
					"output: ifconfig: ioctl (SIOCDIFADDR): permission denied")
			}
			return "", nil
		},
		stopDhclient: func(string) error { return nil },
	})

	sw := networkModels.StandardSwitch{
		Name:        "jail-port",
		BridgeName:  "vm-jail-port",
		DisableIPv6: true,
		Ports:       []networkModels.NetworkPort{{Name: "em0"}},
	}
	sw = withTestStandardSwitchMAC(sw)
	if err := createStandardBridge(sw); err != nil {
		t.Fatalf("expected create to tolerate IPv6 cleanup permission denied, got %v", err)
	}
}

func TestValidateStandardSwitchMACSourceContract(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	validObject := networkModels.Object{
		Name:    "valid-bridge-mac",
		Type:    "Mac",
		Entries: []networkModels.ObjectEntry{{Value: "02:00:00:00:00:31"}},
	}
	multipleObject := networkModels.Object{
		Name: "multiple-bridge-macs",
		Type: "Mac",
		Entries: []networkModels.ObjectEntry{
			{Value: "02:00:00:00:00:32"},
			{Value: "02:00:00:00:00:33"},
		},
	}
	zeroObject := networkModels.Object{
		Name:    "zero-bridge-mac",
		Type:    "Mac",
		Entries: []networkModels.ObjectEntry{{Value: "00:00:00:00:00:00"}},
	}
	for _, object := range []*networkModels.Object{&validObject, &multipleObject, &zeroObject} {
		if err := db.Create(object).Error; err != nil {
			t.Fatalf("seed MAC object %q: %v", object.Name, err)
		}
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case "em0":
				return &iface.Interface{Name: name, Ether: "02:00:00:00:00:21"}, nil
			case "bad0":
				return &iface.Interface{Name: name, Ether: "01:00:5e:00:00:01"}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
	})

	tests := []struct {
		name   string
		source networkModels.StandardSwitchMACSource
		ports  []string
		code   string
	}{
		{name: "missing mode", code: "standard_switch_mac_source_required"},
		{name: "auto is not a mode", source: networkModels.StandardSwitchMACSource{Mode: "auto"}, code: "invalid_standard_switch_mac_mode"},
		{name: "port required", source: networkModels.StandardSwitchMACSource{Mode: "port"}, ports: []string{"em0"}, code: "standard_switch_mac_port_required"},
		{name: "port and object conflict", source: networkModels.StandardSwitchMACSource{Mode: "port", Port: "em0", MACObjectID: validObject.ID}, ports: []string{"em0"}, code: "standard_switch_mac_source_conflict"},
		{name: "source port must be selected", source: networkModels.StandardSwitchMACSource{Mode: "port", Port: "em0"}, ports: []string{"em1"}, code: "standard_switch_mac_port_not_selected"},
		{name: "source port must exist", source: networkModels.StandardSwitchMACSource{Mode: "port", Port: "missing0"}, ports: []string{"missing0"}, code: "standard_switch_mac_port_not_found"},
		{name: "source port MAC must be unicast", source: networkModels.StandardSwitchMACSource{Mode: "port", Port: "bad0"}, ports: []string{"bad0"}, code: "invalid_standard_switch_mac_address"},
		{name: "object required", source: networkModels.StandardSwitchMACSource{Mode: "object"}, code: "standard_switch_mac_object_required"},
		{name: "object and port conflict", source: networkModels.StandardSwitchMACSource{Mode: "object", Port: "em0", MACObjectID: validObject.ID}, code: "standard_switch_mac_source_conflict"},
		{name: "object must exist", source: networkModels.StandardSwitchMACSource{Mode: "object", MACObjectID: 999999}, code: "invalid_standard_switch_mac_object"},
		{name: "object must contain one MAC", source: networkModels.StandardSwitchMACSource{Mode: "object", MACObjectID: multipleObject.ID}, code: "invalid_standard_switch_mac_object"},
		{name: "object MAC must be valid", source: networkModels.StandardSwitchMACSource{Mode: "object", MACObjectID: zeroObject.ID}, code: "invalid_standard_switch_mac_address"},
		{name: "valid object supports a portless switch", source: networkModels.StandardSwitchMACSource{Mode: "object", MACObjectID: validObject.ID}},
		{name: "valid selected port", source: networkModels.StandardSwitchMACSource{Mode: "port", Port: "em0"}, ports: []string{"em0"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.validateStandardSwitchMACSource(test.source, test.ports)
			if test.code == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidStandardSwitch) || StandardSwitchErrorCode(err) != test.code {
				t.Fatalf("error=%v code=%q, want %q", err, StandardSwitchErrorCode(err), test.code)
			}
		})
	}
}

func TestDesiredStandardSwitchPortMACDoesNotDependOnPortOrder(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case "em0":
				return &iface.Interface{Name: name, Ether: "02:00:00:00:00:51"}, nil
			case "em1":
				return &iface.Interface{Name: name, Ether: "02:00:00:00:00:52"}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
	})

	sw := networkModels.StandardSwitch{
		BridgeMACMode:       networkModels.StandardSwitchMACModePort,
		BridgeMACSourcePort: "em1",
		Ports: []networkModels.NetworkPort{
			{Name: "em0"},
			{Name: "em1"},
		},
	}
	first, err := desiredStandardSwitchMAC(sw)
	if err != nil {
		t.Fatalf("resolve MAC with first port order: %v", err)
	}
	sw.Ports[0], sw.Ports[1] = sw.Ports[1], sw.Ports[0]
	second, err := desiredStandardSwitchMAC(sw)
	if err != nil {
		t.Fatalf("resolve MAC with reversed port order: %v", err)
	}
	if first != "02:00:00:00:00:52" || second != first {
		t.Fatalf("port order changed effective MAC: first=%q second=%q", first, second)
	}
}

func TestRunDhclientRestartsRunningClientWithoutAddress(t *testing.T) {
	useTestDhclientRuntimeDir(t)
	skipDhclientNaturalExitGrace(t)

	running := true
	stopSignals := 0
	var launches []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		runCommandAllowExitCode: func(command string, _ []int, args ...string) (string, error) {
			switch command {
			case "/bin/pgrep":
				if running {
					return "3172\n", nil
				}
				return "", nil
			case "/bin/pkill":
				stopSignals++
				running = false
				return "", nil
			default:
				return "", nil
			}
		},
		runCommandWithContext: func(_ context.Context, command string, args ...string) (string, error) {
			launches = append(launches, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := runDhclient("vm-unbound", 10); err != nil {
		t.Fatalf("restart unbound dhclient: %v", err)
	}
	if stopSignals != 1 {
		t.Fatalf("dhclient stop signals=%d want 1", stopSignals)
	}
	expectedLaunch := "/sbin/dhclient -b -p " + dhclientPIDPath("vm-unbound") + " vm-unbound"
	if len(launches) != 1 || launches[0] != expectedLaunch {
		t.Fatalf("dhclient launches=%v want [%q]", launches, expectedLaunch)
	}
}

func TestNewStandardSwitchPersistsSelectedPortMACSource(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == "em0" {
				return &iface.Interface{Name: name, Ether: "02:00:00:00:00:61"}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error { return nil },
	})

	id, err := svc.NewStandardSwitch(
		"port-mac-source",
		1500,
		0,
		0,
		0,
		0,
		0,
		[]string{"em0"},
		networkModels.StandardSwitchMACSource{Mode: networkModels.StandardSwitchMACModePort, Port: "em0"},
		true,
		false,
		true,
		false,
		false,
		false,
		networkModels.StandardSwitchManualAddresses{},
	)
	if err != nil {
		t.Fatalf("create switch with port MAC source: %v", err)
	}

	var stored networkModels.StandardSwitch
	if err := db.Preload("Ports").First(&stored, id).Error; err != nil {
		t.Fatalf("load switch with port MAC source: %v", err)
	}
	if stored.BridgeMACMode != networkModels.StandardSwitchMACModePort || stored.BridgeMACSourcePort != "em0" || stored.BridgeMACObjectID != nil {
		t.Fatalf("port MAC source not persisted: %+v", stored)
	}
	if len(stored.Ports) != 1 || stored.Ports[0].Name != "em0" {
		t.Fatalf("selected port not persisted with MAC source: %+v", stored.Ports)
	}
}
