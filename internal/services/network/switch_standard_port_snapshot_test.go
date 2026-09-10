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
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

func TestCaptureFilteredStandardSwitchPortClaimsSnapshotsMutatedState(t *testing.T) {
	runtimeDir := useTestDhclientRuntimeDir(t)
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("create DHCP runtime directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "dhclient.em0.pid"), []byte("123\n"), 0o600); err != nil {
		t.Fatalf("write managed DHCP PID: %v", err)
	}
	originalIP := net.ParseIP("192.0.2.10")
	interfaceObj := &iface.Interface{
		Name: "em0", MTU: 9000,
		Flags:        iface.Flags{Desc: []string{"UP"}},
		Capabilities: iface.Capabilities{Enabled: iface.Flags{Raw: ifcapTXCSUM | ifcapTSO4}},
		ND6:          iface.ND6{Flags: iface.Flags{Desc: []string{"AUTO_LINKLOCAL", "ACCEPT_RTADV"}}},
		IPv4:         []iface.IPv4{{IP: originalIP, Netmask: "255.255.255.0"}},
		IPv6:         []iface.IPv6{{IP: net.ParseIP("2001:db8::10"), PrefixLength: 64, AutoConf: true}},
	}
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name != interfaceObj.Name {
				t.Fatalf("unexpected interface lookup: %s", name)
			}
			return interfaceObj, nil
		},
		runCommandAllowExitCode: func(command string, allowed []int, args ...string) (string, error) {
			if command != "/bin/pgrep" {
				t.Fatalf("unexpected process command: %s %v", command, args)
			}
			return "123\n", nil
		},
	})

	snapshots, err := captureFilteredStandardSwitchPortClaims([]string{" em0 ", "em0"})
	if err != nil {
		t.Fatalf("capture filtered port: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %#v, want one deduplicated port", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.Name != "em0" || snapshot.MTU != 9000 || !snapshot.WasUp ||
		snapshot.EnabledCapabilities != ifcapTXCSUM|ifcapTSO4 ||
		!snapshot.AutoLinkLocal || !snapshot.AcceptRTAdv ||
		!snapshot.DHCPRunning || !snapshot.DHCPManaged || !snapshot.DHCPDefaultRoute {
		t.Fatalf("incomplete claimed-port snapshot: %#v", snapshot)
	}

	interfaceObj.IPv4[0].IP[0] ^= 0xff
	if !snapshot.IPv4[0].IP.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("snapshot retained mutable interface IP storage: %s", snapshot.IPv4[0].IP)
	}
}

func TestRestoreStandardSwitchPortRuntimeRestoresMutatedState(t *testing.T) {
	current := &iface.Interface{Name: "em0", MTU: 1500}
	commands := make([]string, 0)
	solicitations := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name != current.Name {
				t.Fatalf("unexpected interface lookup: %s", name)
			}
			return current, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
		solicitRouter: func(name string) error {
			if name != current.Name {
				t.Fatalf("unexpected router solicitation: %s", name)
			}
			solicitations++
			return nil
		},
	})

	snapshot := standardSwitchPortRuntimeSnapshot{
		Name:                current.Name,
		MTU:                 9000,
		WasUp:               true,
		EnabledCapabilities: ifcapTXCSUM | ifcapTSO4 | ifcapLRO,
		AutoLinkLocal:       true,
		AcceptRTAdv:         true,
		IPv4: []iface.IPv4{{
			IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0",
		}},
		IPv6: []iface.IPv6{
			{IP: net.ParseIP("fe80::10"), PrefixLength: 64},
			{IP: net.ParseIP("2001:db8::10"), PrefixLength: 64},
			{IP: net.ParseIP("2001:db8::20"), PrefixLength: 64, AutoConf: true},
		},
		DHCPRunning: true,
	}

	if err := restoreStandardSwitchPortRuntime(snapshot); err != nil {
		t.Fatalf("restore claimed port: %v", err)
	}
	want := []string{
		"/sbin/ifconfig em0 mtu 9000",
		"/sbin/ifconfig em0 txcsum tso4 lro",
		"/sbin/ifconfig em0 up",
		"/sbin/ifconfig em0 inet6 auto_linklocal accept_rtadv",
		"/sbin/ifconfig em0 inet 192.0.2.10/24 alias",
		"/sbin/ifconfig em0 inet6 2001:db8::10/64 alias",
		"/sbin/dhclient -b em0",
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("restore commands = %v, want %v", commands, want)
	}
	if solicitations != 1 {
		t.Fatalf("router solicitations = %d, want 1", solicitations)
	}
}

func TestRestoreStandardSwitchPortRuntimePreservesDownState(t *testing.T) {
	commands := make([]string, 0)
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name, MTU: 1500}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := restoreStandardSwitchPortRuntime(standardSwitchPortRuntimeSnapshot{
		Name: "em0", MTU: 1500, WasUp: false,
	}); err != nil {
		t.Fatalf("restore down port: %v", err)
	}
	if !slices.Equal(commands, []string{
		"/sbin/ifconfig em0 up",
		"/sbin/ifconfig em0 inet6 -auto_linklocal -accept_rtadv",
		"/sbin/ifconfig em0 down",
	}) {
		t.Fatalf("down-state restore commands = %v", commands)
	}
}

func TestRestoreFilteredPortClaimsRefusesAttachedMember(t *testing.T) {
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == "vm-filtered" {
				return &iface.Interface{BridgeMembers: []iface.BridgeMember{{Name: "em0"}}}, nil
			}
			t.Fatalf("attached member state must not be restored: %s", name)
			return nil, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			t.Fatalf("attached member state must not be mutated: %s %v", command, args)
			return "", nil
		},
	})

	err := restoreFilteredStandardSwitchPortClaims(
		"vm-filtered", []standardSwitchPortRuntimeSnapshot{{Name: "em0"}},
	)
	if err == nil || !strings.Contains(err.Error(), "remains attached") {
		t.Fatalf("attached member restore error = %v", err)
	}
}

func TestEditFilteredStandardSwitchRestoresOnlyNewPortClaims(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	accessVLAN := 10
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN}
	sw := networkModels.StandardSwitch{
		Name: "filtered-edit-rollback", BridgeName: "vm-filtered-edit-rb", MTU: 1500,
		VLANFiltering: true, DefaultAccessVLAN: &accessVLAN,
	}
	setTestStandardSwitchMACSource(&sw, macSource)
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed filtered switch: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{
		Name: "em0", SwitchID: sw.ID, VLANPolicy: policy,
	}).Error; err != nil {
		t.Fatalf("seed filtered switch port: %v", err)
	}
	operationErr := errors.New("runtime edit failed")
	restoreErr := errors.New("new port restore failed")
	captured := []standardSwitchPortRuntimeSnapshot{{Name: "em1", MTU: 9000}}
	editCalls := 0

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == sw.BridgeName {
				return &iface.Interface{
					Name: name, BridgeMembers: []iface.BridgeMember{{Name: "em0"}},
				}, nil
			}
			return &iface.Interface{Name: name, MTU: 9000}, nil
		},
		captureFilteredPorts: func(names []string) ([]standardSwitchPortRuntimeSnapshot, error) {
			if !slices.Equal(names, []string{"em1"}) {
				t.Fatalf("captured ports = %v, want only [em1]", names)
			}
			return captured, nil
		},
		editFilteredBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch, map[string]struct{}) error {
			editCalls++
			if editCalls == 1 {
				return operationErr
			}
			return nil
		},
		restoreFilteredPorts: func(bridge string, snapshots []standardSwitchPortRuntimeSnapshot) error {
			if bridge != sw.BridgeName || len(snapshots) != 1 || snapshots[0].Name != "em1" {
				t.Fatalf("restore request = bridge %q snapshots %#v", bridge, snapshots)
			}
			return restoreErr
		},
	})

	err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{
		ID: sw.ID,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU: 1500, Ports: []string{"em0", "em1"}, MACSource: macSource,
			VLANConfig: networkModels.StandardSwitchVLANConfig{
				Filtering:         true,
				DefaultAccessVLAN: &accessVLAN,
				PortPolicies: map[string]bridgevlan.PortPolicy{
					"em0": policy,
					"em1": policy,
				},
			},
		},
	})
	if !errors.Is(err, operationErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("joined edit/restore error = %v", err)
	}
	if editCalls != 2 {
		t.Fatalf("filtered edit calls = %d, want failed edit plus rollback", editCalls)
	}
	var ports []networkModels.NetworkPort
	if err := db.Where("switch_id = ?", sw.ID).Order("name ASC").Find(&ports).Error; err != nil {
		t.Fatalf("reload rolled-back ports: %v", err)
	}
	if len(ports) != 1 || ports[0].Name != "em0" {
		t.Fatalf("rolled-back ports = %#v, want only em0", ports)
	}
}

func TestEditStandardSwitchModeChangeSnapshotsNewLayer2PortClaims(t *testing.T) {
	accessVLAN := 20
	for _, test := range []struct {
		name              string
		beforeFiltering   bool
		beforeVLAN        int
		beforePorts       []string
		beforeMembers     []string
		afterFiltering    bool
		afterPorts        []string
		afterPortPolicies map[string]bridgevlan.PortPolicy
		wantClaims        []string
	}{
		{
			name:           "legacy VLAN parent becomes filtered member",
			beforeVLAN:     10,
			beforePorts:    []string{"em0"},
			beforeMembers:  []string{"em0.10"},
			afterFiltering: true,
			afterPorts:     []string{"em0"},
			afterPortPolicies: map[string]bridgevlan.PortPolicy{
				"em0": {Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
			},
			wantClaims: []string{"em0"},
		},
		{
			name:            "new port added while disabling filtering",
			beforeFiltering: true,
			beforePorts:     []string{"em0"},
			beforeMembers:   []string{"em0"},
			afterPorts:      []string{"em0", "em1"},
			wantClaims:      []string{"em1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, db := newNetworkServiceForTest(t,
				&networkModels.ManualSwitch{},
				&networkModels.StandardSwitch{},
				&networkModels.NetworkPort{},
			)
			macSource := createTestStandardSwitchMACSource(t, svc)
			sw := networkModels.StandardSwitch{
				Name:          "mode-claim",
				BridgeName:    "vm-mode-claim",
				MTU:           1500,
				VLAN:          test.beforeVLAN,
				DisableIPv6:   true,
				VLANFiltering: test.beforeFiltering,
			}
			setTestStandardSwitchMACSource(&sw, macSource)
			if err := db.Create(&sw).Error; err != nil {
				t.Fatalf("seed standard switch: %v", err)
			}
			for _, name := range test.beforePorts {
				if err := db.Create(&networkModels.NetworkPort{Name: name, SwitchID: sw.ID}).Error; err != nil {
					t.Fatalf("seed standard switch port %s: %v", name, err)
				}
			}

			captured := false
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					if name == sw.BridgeName {
						members := make([]iface.BridgeMember, 0, len(test.beforeMembers))
						for _, member := range test.beforeMembers {
							members = append(members, iface.BridgeMember{Name: member})
						}
						return &iface.Interface{Name: name, BridgeMembers: members}, nil
					}
					return &iface.Interface{Name: name, MTU: 1500}, nil
				},
				captureFilteredPorts: func(names []string) ([]standardSwitchPortRuntimeSnapshot, error) {
					captured = true
					if !slices.Equal(names, test.wantClaims) {
						t.Fatalf("captured ports = %v, want %v", names, test.wantClaims)
					}
					return nil, nil
				},
				deleteBridge: func(networkModels.StandardSwitch) error { return nil },
				createBridge: func(networkModels.StandardSwitch) error { return nil },
			})

			if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{
				ID: sw.ID,
				StandardSwitchConfig: StandardSwitchConfig{
					MTU:         1500,
					Ports:       test.afterPorts,
					MACSource:   macSource,
					DisableIPv6: true,
					VLANConfig: networkModels.StandardSwitchVLANConfig{
						Filtering:    test.afterFiltering,
						PortPolicies: test.afterPortPolicies,
					},
				},
			}); err != nil {
				t.Fatalf("change VLAN-filtering mode: %v", err)
			}
			if !captured {
				t.Fatal("mode change did not snapshot newly claimed Layer-2 ports")
			}
		})
	}
}
