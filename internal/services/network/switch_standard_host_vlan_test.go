// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

func TestStandardSwitchHostInterfaceName(t *testing.T) {
	hostVLAN := 20
	tests := []struct {
		name string
		sw   networkModels.StandardSwitch
		want string
	}{
		{
			name: "ordinary switch uses bridge",
			sw:   networkModels.StandardSwitch{BridgeName: "vm-lan"},
			want: "vm-lan",
		},
		{
			name: "filtered layer two switch has no host interface",
			sw: networkModels.StandardSwitch{
				BridgeName: "vm-lan", VLANFiltering: true,
			},
		},
		{
			name: "filtered host VLAN uses bridge child",
			sw: networkModels.StandardSwitch{
				BridgeName: "vm-lan", VLANFiltering: true, HostVLAN: &hostVLAN,
			},
			want: "vm-lan.20",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := standardSwitchHostInterfaceName(test.sw); got != test.want {
				t.Fatalf("host interface = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReconcileFilteredStandardSwitchHostConfiguresManagedVLANInterface(t *testing.T) {
	hostVLAN := 20
	hostName := "vmhost01.20"
	created := false
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name != hostName {
				t.Fatalf("inspected interface %q, want %q", name, hostName)
			}
			if !created {
				return nil, errors.New("interface not found")
			}
			return &iface.Interface{
				Name: name, MTU: 8996,
				Groups:     []string{standardSwitchHostVLANGroup},
				VLANParent: "vmhost01", VLANTag: hostVLAN,
			}, nil
		},
		stopDhclient: func(string) error { return nil },
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			if strings.HasPrefix(full, "/sbin/ifconfig vlan create ") {
				created = true
			}
			return "", nil
		},
	})

	desired := networkModels.StandardSwitch{
		Name:          "host-network",
		BridgeName:    "vmhost01",
		MTU:           9000,
		VLANFiltering: true,
		HostVLAN:      &hostVLAN,
		NetworkManual: "192.0.2.10/24",
		GatewayManual: "192.0.2.1",
		DefaultRoute:  true,
		DisableIPv6:   true,
	}
	previous := networkModels.StandardSwitch{BridgeName: desired.BridgeName, VLANFiltering: true}
	if err := reconcileFilteredStandardSwitchHost(previous, desired); err != nil {
		t.Fatalf("reconcile filtered host: %v", err)
	}

	wantCommands := []string{
		"/sbin/ifconfig vlan create vlandev vmhost01 vlan 20 descr svm-host-vlan/vmhost01/20 name vmhost01.20 group svm-host-vlan up",
		"/sbin/ifconfig vmhost01.20 descr svm-host-vlan/vmhost01/20 mtu 8996 up",
		"/sbin/ifconfig vmhost01.20 inet 192.0.2.10/24",
		"/sbin/ifconfig vmhost01.20 inet6 no_radr -accept_rtadv ifdisabled",
		"/sbin/ifconfig vmhost01.20 up",
		"/sbin/route add -net 192.0.2.10/24 192.0.2.1",
		"/sbin/route add default 192.0.2.1",
	}
	for _, command := range wantCommands {
		if !slices.Contains(commands, command) {
			t.Fatalf("missing command %q; commands=%v", command, commands)
		}
	}
	for _, command := range commands {
		if strings.HasPrefix(command, "/sbin/ifconfig vmhost01 inet ") {
			t.Fatalf("host address was assigned to the filtered base bridge: %v", commands)
		}
	}
}

func TestReconcileFilteredStandardSwitchHostRollsBackOnlyAddedRoutes(t *testing.T) {
	hostVLAN := 20
	hostName := "vmhost01.20"
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name != hostName {
				t.Fatalf("inspected interface %q, want %q", name, hostName)
			}
			return &iface.Interface{
				Name: name, MTU: 1496,
				Groups:     []string{standardSwitchHostVLANGroup},
				VLANParent: "vmhost01", VLANTag: hostVLAN,
			}, nil
		},
		stopDhclient: func(string) error { return nil },
		runCommand: func(command string, args ...string) (string, error) {
			full := strings.Join(append([]string{command}, args...), " ")
			commands = append(commands, full)
			switch full {
			case "/sbin/route add default 192.0.2.1":
				return "File exists", errors.New("route already exists")
			case "/sbin/route -n get default":
				return "gateway: 192.0.2.1\ninterface: vmhost01.20\n", nil
			case "/sbin/route -6 add -net 2001:db8::10/64 2001:db8::1":
				return "", errors.New("IPv6 route failure")
			default:
				return "", nil
			}
		},
	})

	desired := networkModels.StandardSwitch{
		BridgeName:     "vmhost01",
		MTU:            1500,
		VLANFiltering:  true,
		HostVLAN:       &hostVLAN,
		NetworkManual:  "192.0.2.10/24",
		GatewayManual:  "192.0.2.1",
		Network6Manual: "2001:db8::10/64",
		Gateway6Manual: "2001:db8::1",
		DefaultRoute:   true,
	}
	err := reconcileFilteredStandardSwitchHost(
		networkModels.StandardSwitch{BridgeName: desired.BridgeName, VLANFiltering: true},
		desired,
	)
	if err == nil || !strings.Contains(err.Error(), "add host VLAN IPv6 network route") {
		t.Fatalf("route failure = %v", err)
	}
	if !slices.Contains(commands, "/sbin/route delete -net 192.0.2.10/24 192.0.2.1") {
		t.Fatalf("added IPv4 network route was not rolled back: %v", commands)
	}
	if slices.Contains(commands, "/sbin/route delete default 192.0.2.1") {
		t.Fatalf("pre-existing IPv4 default route was removed: %v", commands)
	}
}

func TestReconcileFilteredStandardSwitchHostTreatsRTSolFailureAsBestEffort(t *testing.T) {
	hostVLAN := 20
	hostName := "vmhost01.20"
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name, MTU: 1496,
				Groups:     []string{standardSwitchHostVLANGroup},
				VLANParent: "vmhost01", VLANTag: hostVLAN,
			}, nil
		},
		stopDhclient: func(string) error { return nil },
		runCommand:   func(string, ...string) (string, error) { return "", nil },
		solicitRouter: func(name string) error {
			if name != hostName {
				t.Fatalf("solicited interface %q, want %q", name, hostName)
			}
			return errors.New("no router advertisement yet")
		},
	})

	desired := networkModels.StandardSwitch{
		BridgeName: "vmhost01", MTU: 1500, VLANFiltering: true,
		HostVLAN: &hostVLAN, SLAAC: true,
	}
	if err := reconcileFilteredStandardSwitchHost(
		networkModels.StandardSwitch{BridgeName: desired.BridgeName, VLANFiltering: true},
		desired,
	); err != nil {
		t.Fatalf("transient router solicitation failure was fatal: %v", err)
	}
}

func TestDestroyStandardSwitchHostVLANRefusesUnownedInterface(t *testing.T) {
	hostVLAN := 20
	mutated := false
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name, VLANParent: "vmhost01", VLANTag: hostVLAN,
			}, nil
		},
		runCommand: func(string, ...string) (string, error) {
			mutated = true
			return "", nil
		},
	})

	err := destroyStandardSwitchHostVLAN(networkModels.StandardSwitch{
		BridgeName: "vmhost01", VLANFiltering: true, HostVLAN: &hostVLAN,
	})
	if !errors.Is(err, ErrStandardSwitchConflict) ||
		StandardSwitchErrorCode(err) != "standard_switch_host_vlan_interface_conflict" {
		t.Fatalf("unowned host VLAN error = %v, code=%q", err, StandardSwitchErrorCode(err))
	}
	if mutated {
		t.Fatal("unowned host VLAN interface was mutated")
	}
}

func TestHostVLANChangeRejectsDHCPServerReferences(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
	)
	sw := networkModels.StandardSwitch{Name: "filtered", BridgeName: "vmhost01"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	config := networkModels.DHCPConfig{Domain: "example.test", DNSServers: []string{}}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("seed DHCP config: %v", err)
	}
	if err := db.Model(&config).Association("StandardSwitches").Append(&sw); err != nil {
		t.Fatalf("associate standard switch with DHCP config: %v", err)
	}

	err := svc.checkStandardSwitchHostInterfaceUsage(sw.ID, "vmhost01.20")
	if !errors.Is(err, ErrStandardSwitchInUse) ||
		StandardSwitchErrorCode(err) != "standard_switch_in_use_by_dhcp_config" {
		t.Fatalf("host VLAN DHCP usage error = %v, code=%q", err, StandardSwitchErrorCode(err))
	}
}
