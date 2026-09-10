// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package bridgevlan

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func integrationInterface(t *testing.T, kind string) string {
	t.Helper()
	output, err := exec.Command("/sbin/ifconfig", kind, "create").CombinedOutput()
	if err != nil {
		t.Fatalf("create %s: %v: %s", kind, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func destroyIntegrationInterface(name string) {
	_, _ = exec.Command("/sbin/ifconfig", name, "destroy").CombinedOutput()
}

func integrationCommand(t *testing.T, name string, arguments ...string) string {
	t.Helper()
	output, err := exec.Command(name, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s: %v: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func integrationEpairPeer(t *testing.T, first string) string {
	t.Helper()
	if !strings.HasSuffix(first, "a") {
		t.Fatalf("unexpected epair interface name %q", first)
	}
	return strings.TrimSuffix(first, "a") + "b"
}

func createIntegrationVNETJail(t *testing.T, name string) {
	t.Helper()
	integrationCommand(t, "/usr/sbin/jail", "-c",
		"name="+name,
		"path=/",
		"host.hostname="+name,
		"persist",
		"vnet",
		"allow.raw_sockets=1",
	)
}

func moveIntegrationInterfaceToJail(t *testing.T, member, jailName string) {
	t.Helper()
	integrationCommand(t, "/sbin/ifconfig", member, "vnet", jailName)
	integrationCommand(t, "/sbin/ifconfig", "-j", jailName, member, "up")
}

func integrationPing(jailName, address string) ([]byte, error) {
	return exec.Command(
		"/usr/sbin/jexec", jailName,
		"/sbin/ping", "-n", "-c", "1", "-W", "1000", "-t", "3", address,
	).CombinedOutput()
}

func TestIntegrationVLANBridgeControllerRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bridge VLAN integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("bridge VLAN integration test requires root")
	}
	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		t.Skipf("ifconfig is unavailable: %v", err)
	}

	bridge := integrationInterface(t, "bridge")
	first := integrationInterface(t, "epair")
	second := integrationInterface(t, "epair")
	t.Cleanup(func() {
		destroyIntegrationInterface(bridge)
		destroyIntegrationInterface(first)
		destroyIntegrationInterface(second)
	})

	controller := newController(newNativeBackend())
	if err := controller.backend.AddMember(bridge, first); err != nil {
		t.Fatalf("add member to unfiltered bridge: %v", err)
	}
	if err := controller.SetMemberPrivate(bridge, first, true); err != nil {
		t.Fatalf("set unfiltered bridge member private: %v", err)
	}
	state, err := controller.inspectMember(bridge, first)
	if err != nil {
		t.Fatalf("inspect unfiltered private member: %v", err)
	}
	if !state.Private {
		t.Fatalf("unfiltered bridge member private state was not applied: %#v", state)
	}
	if err := controller.SetMemberPrivate(bridge, first, false); err != nil {
		t.Fatalf("clear unfiltered bridge member private: %v", err)
	}

	if err := controller.PrepareManagedFilteredBridge(bridge); err != nil {
		t.Fatalf("prepare filtered bridge: %v", err)
	}
	native := 10
	policy := PortPolicy{Mode: ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{20, 30}}
	if err := controller.ConfigureMember(bridge, first, nil, policy); err != nil {
		t.Fatalf("configure member: %v", err)
	}
	state, err = controller.inspectMember(bridge, first)
	if err != nil {
		t.Fatalf("inspect member: %v", err)
	}
	normalized, _ := Normalize(policy)
	if err := verifyPolicyState(state, normalized); err != nil {
		t.Fatalf("member policy state: %v", err)
	}

	defaultVLAN := 40
	if err := controller.SetDefaultAccessVLAN(bridge, &defaultVLAN); err != nil {
		t.Fatalf("set default access VLAN: %v", err)
	}

	if err := controller.backend.AddMember(bridge, second); err != nil {
		t.Fatalf("add default-access member: %v", err)
	}
	inherited, err := controller.inspectMember(bridge, second)
	if err != nil {
		t.Fatalf("inspect inherited member: %v", err)
	}
	if inherited.PVID != defaultVLAN || len(inherited.TaggedVLANs) != 0 ||
		inherited.VLANProtocol != vlanProtocol8021Q || inherited.QinQ {
		t.Fatalf(
			"inherited member = %#v, want PVID %d, no tagged VLANs, 802.1Q, and Q-in-Q disabled",
			inherited,
			defaultVLAN,
		)
	}

	t.Logf("verified bridge %s and members %s/%s at %s", bridge, first, second, fmt.Sprint(time.Now().UnixNano()))
}

func TestIntegrationVLANBridgePacketIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bridge VLAN packet integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("bridge VLAN packet integration test requires root")
	}
	for _, binary := range []string{"/sbin/ifconfig", "/usr/sbin/jail", "/usr/sbin/jexec", "/sbin/ping"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is unavailable: %v", binary, err)
		}
	}

	bridge := integrationInterface(t, "bridge")
	access10 := integrationInterface(t, "epair")
	access20 := integrationInterface(t, "epair")
	trunk := integrationInterface(t, "epair")
	interfaces := []string{bridge, access10, access20, trunk}
	jailPrefix := fmt.Sprintf("sylve_vlan_%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	jails := []string{jailPrefix + "_a", jailPrefix + "_b", jailPrefix + "_t"}
	createdJails := make([]string, 0, len(jails))
	t.Cleanup(func() {
		for index := len(createdJails) - 1; index >= 0; index-- {
			_, _ = exec.Command("/usr/sbin/jail", "-r", createdJails[index]).CombinedOutput()
		}
		for index := len(interfaces) - 1; index >= 0; index-- {
			destroyIntegrationInterface(interfaces[index])
		}
	})

	controller := newController(newNativeBackend())
	if err := controller.PrepareManagedFilteredBridge(bridge); err != nil {
		t.Fatalf("prepare filtered bridge: %v", err)
	}
	accessVLAN10 := 10
	accessVLAN20 := 20
	if err := controller.ConfigureMember(bridge, access10, nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &accessVLAN10, TaggedVLANs: []int{},
	}); err != nil {
		t.Fatalf("configure VLAN 10 access member: %v", err)
	}
	if err := controller.ConfigureMember(bridge, access20, nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &accessVLAN20, TaggedVLANs: []int{},
	}); err != nil {
		t.Fatalf("configure VLAN 20 access member: %v", err)
	}
	if err := controller.ConfigureMember(bridge, trunk, nil, PortPolicy{
		Mode: ModeTrunk, TaggedVLANs: []int{10},
	}); err != nil {
		t.Fatalf("configure tagged VLAN 10 trunk member: %v", err)
	}
	for _, name := range interfaces {
		integrationCommand(t, "/sbin/ifconfig", name, "up")
	}

	for _, jailName := range jails {
		createIntegrationVNETJail(t, jailName)
		createdJails = append(createdJails, jailName)
	}

	access10Peer := integrationEpairPeer(t, access10)
	access20Peer := integrationEpairPeer(t, access20)
	trunkPeer := integrationEpairPeer(t, trunk)
	moveIntegrationInterfaceToJail(t, access10Peer, jails[0])
	moveIntegrationInterfaceToJail(t, access20Peer, jails[1])
	moveIntegrationInterfaceToJail(t, trunkPeer, jails[2])
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[0], access10Peer, "inet", "192.0.2.1/24", "up")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[1], access20Peer, "inet", "192.0.2.2/24", "up")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[2], "vlan", "create",
		"vlandev", trunkPeer, "vlan", "10", "name", "vlan10")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[2], "vlan10", "inet", "192.0.2.3/24", "up")

	if output, err := integrationPing(jails[0], "192.0.2.3"); err != nil {
		t.Fatalf("VLAN 10 access-to-trunk ping failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := integrationPing(jails[0], "192.0.2.2"); err == nil {
		t.Fatalf("VLAN 10 unexpectedly reached VLAN 20: %s", strings.TrimSpace(string(output)))
	}
}

func TestIntegrationVLANBridgeNativeAndDeniedTaggedTraffic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bridge VLAN native-trunk integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("bridge VLAN native-trunk integration test requires root")
	}
	for _, binary := range []string{"/sbin/ifconfig", "/usr/sbin/jail", "/usr/sbin/jexec", "/sbin/ping"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is unavailable: %v", binary, err)
		}
	}

	bridge := integrationInterface(t, "bridge")
	nativeAccess := integrationInterface(t, "epair")
	deniedAccess := integrationInterface(t, "epair")
	trunk := integrationInterface(t, "epair")
	interfaces := []string{bridge, nativeAccess, deniedAccess, trunk}
	jailPrefix := fmt.Sprintf("sylve_vlan_native_%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	jails := []string{jailPrefix + "_native", jailPrefix + "_denied", jailPrefix + "_trunk"}
	createdJails := make([]string, 0, len(jails))
	t.Cleanup(func() {
		for index := len(createdJails) - 1; index >= 0; index-- {
			_, _ = exec.Command("/usr/sbin/jail", "-r", createdJails[index]).CombinedOutput()
		}
		for index := len(interfaces) - 1; index >= 0; index-- {
			destroyIntegrationInterface(interfaces[index])
		}
	})

	controller := newController(newNativeBackend())
	if err := controller.PrepareManagedFilteredBridge(bridge); err != nil {
		t.Fatalf("prepare filtered bridge: %v", err)
	}
	nativeVLAN, deniedVLAN := 10, 30
	if err := controller.ConfigureMember(bridge, nativeAccess, nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &nativeVLAN, TaggedVLANs: []int{},
	}); err != nil {
		t.Fatalf("configure native VLAN access member: %v", err)
	}
	if err := controller.ConfigureMember(bridge, deniedAccess, nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &deniedVLAN, TaggedVLANs: []int{},
	}); err != nil {
		t.Fatalf("configure denied VLAN access member: %v", err)
	}
	if err := controller.ConfigureMember(bridge, trunk, nil, PortPolicy{
		Mode: ModeTrunk, UntaggedVLAN: &nativeVLAN, TaggedVLANs: []int{20},
	}); err != nil {
		t.Fatalf("configure native trunk member: %v", err)
	}
	for _, name := range interfaces {
		integrationCommand(t, "/sbin/ifconfig", name, "up")
	}

	for _, jailName := range jails {
		createIntegrationVNETJail(t, jailName)
		createdJails = append(createdJails, jailName)
	}
	nativePeer := integrationEpairPeer(t, nativeAccess)
	deniedPeer := integrationEpairPeer(t, deniedAccess)
	trunkPeer := integrationEpairPeer(t, trunk)
	moveIntegrationInterfaceToJail(t, nativePeer, jails[0])
	moveIntegrationInterfaceToJail(t, deniedPeer, jails[1])
	moveIntegrationInterfaceToJail(t, trunkPeer, jails[2])
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[0], nativePeer, "inet", "192.0.2.1/24", "up")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[1], deniedPeer, "inet", "198.51.100.1/24", "up")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[2], trunkPeer, "inet", "192.0.2.2/24", "up")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[2], "vlan", "create",
		"vlandev", trunkPeer, "vlan", fmt.Sprint(deniedVLAN), "name", "vlan30")
	integrationCommand(t, "/sbin/ifconfig", "-j", jails[2], "vlan30", "inet", "198.51.100.2/24", "up")

	if output, err := integrationPing(jails[0], "192.0.2.2"); err != nil {
		t.Fatalf("native VLAN access-to-trunk ping failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := integrationPing(jails[1], "198.51.100.2"); err == nil {
		t.Fatalf("disallowed tagged VLAN 30 unexpectedly crossed the trunk: %s", strings.TrimSpace(string(output)))
	}
}
