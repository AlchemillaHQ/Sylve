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
	"fmt"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func testVLANPointer(value int) *int { return &value }

func TestFilteredMemberRuntimeMatchesRequiresLayer2OnlyState(t *testing.T) {
	clean := &iface.Interface{MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}}}
	if !filteredMemberRuntimeMatches(clean, 1500, false) {
		t.Fatal("clean layer-2 member did not match")
	}

	withAddress := &iface.Interface{
		MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}}, IPv4: []iface.IPv4{{}},
	}
	if filteredMemberRuntimeMatches(withAddress, 1500, false) {
		t.Fatal("addressed member unexpectedly matched")
	}

	withAutoconfiguration := &iface.Interface{
		MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}},
		ND6: iface.ND6{Flags: iface.Flags{Desc: []string{"AUTO_LINKLOCAL"}}},
	}
	if filteredMemberRuntimeMatches(withAutoconfiguration, 1500, false) {
		t.Fatal("IPv6-autoconfigured member unexpectedly matched")
	}

	down := &iface.Interface{MTU: 1500}
	if filteredMemberRuntimeMatches(down, 1500, false) {
		t.Fatal("admin-down member unexpectedly matched")
	}
}

func TestNormalizeStandardSwitchVLANConfigRequiresExactNormalizedPolicies(t *testing.T) {
	config, err := normalizeStandardSwitchVLANConfig(
		networkModels.StandardSwitchVLANConfig{
			Filtering:         true,
			DefaultAccessVLAN: testVLANPointer(99),
			PortPolicies: map[string]bridgevlan.PortPolicy{
				"igb0": {
					Mode:         " TRUNK ",
					UntaggedVLAN: testVLANPointer(10),
					TaggedVLANs:  []int{30, 20, 30},
				},
				"igb1": {Mode: bridgevlan.ModeAccess, UntaggedVLAN: testVLANPointer(40)},
			},
		},
		[]string{"igb0", "igb1"},
	)
	if err != nil {
		t.Fatalf("normalize filtered config: %v", err)
	}
	if config.PortPolicies["igb0"].Mode != bridgevlan.ModeTrunk ||
		!slices.Equal(config.PortPolicies["igb0"].TaggedVLANs, []int{20, 30}) {
		t.Fatalf("trunk policy was not canonicalized: %#v", config.PortPolicies["igb0"])
	}
	if got := config.PortPolicies["igb1"].TaggedVLANs; got == nil || len(got) != 0 {
		t.Fatalf("access policy tagged VLANs = %#v, want an empty array", got)
	}

	for _, test := range []struct {
		name     string
		config   networkModels.StandardSwitchVLANConfig
		ports    []string
		wantCode string
	}{
		{
			name: "missing selected port",
			config: networkModels.StandardSwitchVLANConfig{
				Filtering: true,
				PortPolicies: map[string]bridgevlan.PortPolicy{
					"igb0": {Mode: bridgevlan.ModeAccess, UntaggedVLAN: testVLANPointer(10)},
				},
			},
			ports:    []string{"igb0", "igb1"},
			wantCode: "standard_switch_vlan_policy_required",
		},
		{
			name: "policy for unselected port",
			config: networkModels.StandardSwitchVLANConfig{
				Filtering: true,
				PortPolicies: map[string]bridgevlan.PortPolicy{
					"igb1": {Mode: bridgevlan.ModeAccess, UntaggedVLAN: testVLANPointer(10)},
				},
			},
			ports:    []string{"igb0"},
			wantCode: "standard_switch_vlan_policy_port_not_selected",
		},
		{
			name: "default on unfiltered bridge",
			config: networkModels.StandardSwitchVLANConfig{
				DefaultAccessVLAN: testVLANPointer(10),
			},
			wantCode: "standard_switch_default_vlan_requires_filtering",
		},
		{
			name: "host VLAN on unfiltered bridge",
			config: networkModels.StandardSwitchVLANConfig{
				HostVLAN: testVLANPointer(10),
			},
			wantCode: "standard_switch_host_vlan_requires_filtering",
		},
		{
			name: "invalid filtered host VLAN",
			config: networkModels.StandardSwitchVLANConfig{
				Filtering: true,
				HostVLAN:  testVLANPointer(4095),
			},
			wantCode: "standard_switch_invalid_host_vlan",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeStandardSwitchVLANConfig(test.config, test.ports)
			if !errors.Is(err, ErrInvalidStandardSwitch) || StandardSwitchErrorCode(err) != test.wantCode {
				t.Fatalf("error = %v, code = %q; want %q", err, StandardSwitchErrorCode(err), test.wantCode)
			}
		})
	}
}

func TestFilteredStandardSwitchHostL3DetectionAndModeChanges(t *testing.T) {
	if !standardSwitchHasHostL3(standardSwitchInput{dhcp: true}) {
		t.Fatal("DHCP must count as host layer-3 configuration")
	}
	if !standardSwitchHasHostL3(standardSwitchInput{
		manual: networkModels.StandardSwitchManualAddresses{Network6: "2001:db8::1/64"},
	}) {
		t.Fatal("manual IPv6 must count as host layer-3 configuration")
	}

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	existing := networkModels.StandardSwitch{Name: "legacy", BridgeName: "vm-legacy"}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	macSource := createTestStandardSwitchMACSource(t, svc)

	_, err := svc.validateStandardSwitchInput(0, "vm-filtered", standardSwitchInput{
		mtu:         1500,
		dhcp:        true,
		disableIPv6: true,
		macSource:   macSource,
		vlanConfig:  networkModels.StandardSwitchVLANConfig{Filtering: true},
	})
	if !errors.Is(err, ErrInvalidStandardSwitch) ||
		StandardSwitchErrorCode(err) != "standard_switch_host_vlan_required" {
		t.Fatalf("missing host VLAN error = %v, code = %q", err, StandardSwitchErrorCode(err))
	}
	normalized, err := svc.validateStandardSwitchInput(0, "vm-filtered-l2", standardSwitchInput{
		mtu:         1500,
		disableIPv6: true,
		manual: networkModels.StandardSwitchManualAddresses{
			Network6: "2001:db8::10/64",
			Gateway6: "2001:db8::1",
		},
		macSource:  macSource,
		vlanConfig: networkModels.StandardSwitchVLANConfig{Filtering: true},
	})
	if err != nil {
		t.Fatalf("stale disabled IPv6 fields required a host VLAN: %v", err)
	}
	if normalized.manual.Network6 != "" || normalized.manual.Gateway6 != "" {
		t.Fatalf("disabled IPv6 fields were not cleared: %#v", normalized.manual)
	}

	hostVLAN := 10
	if _, err := svc.validateStandardSwitchInput(0, "vm-filtered", standardSwitchInput{
		mtu:         1500,
		dhcp:        true,
		disableIPv6: true,
		macSource:   macSource,
		vlanConfig: networkModels.StandardSwitchVLANConfig{
			Filtering: true,
			HostVLAN:  &hostVLAN,
		},
	}); err != nil {
		t.Fatalf("filtered host DHCP with host VLAN was rejected: %v", err)
	}

	_, err = svc.validateStandardSwitchInput(existing.ID, existing.BridgeName, standardSwitchInput{
		mtu:         1500,
		disableIPv6: true,
		macSource:   macSource,
		vlanConfig:  networkModels.StandardSwitchVLANConfig{Filtering: true},
	})
	if err != nil {
		t.Fatalf("enabling filtering during edit was rejected: %v", err)
	}

	existing.VLANFiltering = true
	if err := db.Save(&existing).Error; err != nil {
		t.Fatalf("mark switch filtered: %v", err)
	}
	_, err = svc.validateStandardSwitchInput(existing.ID, existing.BridgeName, standardSwitchInput{
		mtu:         1500,
		disableIPv6: true,
		macSource:   macSource,
		vlanConfig:  networkModels.StandardSwitchVLANConfig{},
	})
	if err != nil {
		t.Fatalf("disabling filtering during edit was rejected: %v", err)
	}
}

func TestDefaultAccessVLANChangeRequiresEveryReferencingVMToBeStopped(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{})
	if err := db.Exec(`INSERT INTO vms (id, rid) VALUES (1, 101), (2, 102)`).Error; err != nil {
		t.Fatalf("seed VMs: %v", err)
	}
	if err := db.Exec(`INSERT INTO vm_networks (id, vm_id, switch_id, switch_type, enable) VALUES (1, 1, 7, 'standard', true), (2, 2, 7, 'standard', true)`).Error; err != nil {
		t.Fatalf("seed VM networks: %v", err)
	}

	original := standardSwitchIsDomainShutOff
	t.Cleanup(func() { standardSwitchIsDomainShutOff = original })
	var inspected []uint
	standardSwitchIsDomainShutOff = func(_ *Service, rid uint) (bool, error) {
		inspected = append(inspected, rid)
		return rid == 101, nil
	}

	err := svc.requireStoppedVMsForDefaultAccessVLANChange(7, testVLANPointer(10), testVLANPointer(20))
	if !errors.Is(err, ErrStandardSwitchInUse) ||
		StandardSwitchErrorCode(err) != "standard_switch_default_access_vlan_change_requires_stopped_vms" {
		t.Fatalf("default VLAN change error = %v, code = %q", err, StandardSwitchErrorCode(err))
	}
	if !slices.Equal(inspected, []uint{101, 102}) {
		t.Fatalf("inspected VM RIDs = %v, want [101 102]", inspected)
	}
}

func TestDefaultAccessVLANChangeIgnoresDisabledVMNetworks(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{})
	if err := db.Exec(`INSERT INTO vms (id, rid) VALUES (1, 101)`).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Exec(`INSERT INTO vm_networks (id, vm_id, switch_id, switch_type, enable) VALUES (1, 1, 7, 'standard', false)`).Error; err != nil {
		t.Fatalf("seed disabled VM network: %v", err)
	}

	original := standardSwitchIsDomainShutOff
	t.Cleanup(func() { standardSwitchIsDomainShutOff = original })
	standardSwitchIsDomainShutOff = func(_ *Service, rid uint) (bool, error) {
		t.Fatalf("disabled VM network caused VM %d state inspection", rid)
		return false, nil
	}

	if err := svc.requireStoppedVMsForDefaultAccessVLANChange(
		7, testVLANPointer(10), testVLANPointer(20),
	); err != nil {
		t.Fatalf("disabled VM network blocked default VLAN change: %v", err)
	}
}

func TestEditStandardSwitchRejectsDefaultChangeWithUnmanagedMemberBeforeTransaction(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	previousDefault, desiredDefault := 10, 20
	sw := networkModels.StandardSwitch{
		Name:              "filtered-edit",
		BridgeName:        "vm-filtered-edit",
		MTU:               1500,
		DisableIPv6:       true,
		VLANFiltering:     true,
		DefaultAccessVLAN: &previousDefault,
	}
	setTestStandardSwitchMACSource(&sw, macSource)
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed filtered switch: %v", err)
	}
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &previousDefault}
	if err := db.Create(&networkModels.NetworkPort{
		Name: "em0", SwitchID: sw.ID, VLANPolicy: policy,
	}).Error; err != nil {
		t.Fatalf("seed filtered switch port: %v", err)
	}

	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case "em0":
				return &iface.Interface{Name: name}, nil
			case sw.BridgeName:
				return &iface.Interface{
					Name: name,
					BridgeMembers: []iface.BridgeMember{
						{Name: "em0"},
						{Name: "tap9"},
					},
				}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{
		ID: sw.ID,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{"em0"},
			MACSource:   macSource,
			DisableIPv6: true,
			VLANConfig: networkModels.StandardSwitchVLANConfig{
				Filtering:         true,
				DefaultAccessVLAN: &desiredDefault,
				PortPolicies:      map[string]bridgevlan.PortPolicy{"em0": policy},
			},
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) ||
		StandardSwitchErrorCode(err) != "standard_switch_default_access_vlan_member_conflict" {
		t.Fatalf("edit default VLAN conflict = %v, code=%q", err, StandardSwitchErrorCode(err))
	}
	if editCalls != 0 {
		t.Fatalf("runtime edit called despite preflight conflict: %d", editCalls)
	}
	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, sw.ID).Error; err != nil {
		t.Fatalf("reload filtered switch: %v", err)
	}
	if persisted.DefaultAccessVLAN == nil || *persisted.DefaultAccessVLAN != previousDefault {
		t.Fatalf("database changed despite preflight conflict: %+v", persisted.DefaultAccessVLAN)
	}
}

func TestStartupRefusesUnfilteredDatabaseSwitchOnFilteredRuntimeBridge(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	sw := networkModels.StandardSwitch{Name: "legacy", BridgeName: "vm-legacy"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}

	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	err := svc.SyncStandardSwitches()
	if err == nil || !strings.Contains(err.Error(), "standard_switch_runtime_vlan_mode_mismatch") {
		t.Fatalf("expected mode mismatch, got %v", err)
	}
	if editCalls != 0 {
		t.Fatalf("runtime bridge was mutated %d times after mismatch", editCalls)
	}
}

func TestFilteredStandardSwitchStartupDoesNotFlapMatchingMembers(t *testing.T) {
	access := 10
	sw := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name:              "filtered",
		BridgeName:        "vm-filtered",
		MTU:               1500,
		VLANFiltering:     true,
		DefaultAccessVLAN: &access,
		Ports: []networkModels.NetworkPort{{
			Name: "em0",
			VLANPolicy: bridgevlan.PortPolicy{
				Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access, TaggedVLANs: []int{},
			},
		}},
	})
	bridgeInterface := &iface.Interface{
		Name: sw.BridgeName, Ether: testStandardSwitchMAC, Description: sw.Name, MTU: 1500,
		Flags: iface.Flags{Desc: []string{"UP"}},
		ND6:   iface.ND6{Flags: iface.Flags{Desc: []string{"PERFORMNUD", "IFDISABLED"}}},
		BridgeMembers: []iface.BridgeMember{
			{Name: "em0"},
			{Name: "tap0"},
		},
	}
	var commands []string
	var configured, removed, defaultChanges int
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == sw.BridgeName {
				return bridgeInterface, nil
			}
			if name == "em0" {
				return &iface.Interface{Name: name, MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}}}, nil
			}
			return nil, errors.New("interface not found")
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: access}, nil
		},
		filteredPolicyMatches: func(string, string, bridgevlan.PortPolicy) (bool, error) {
			return true, nil
		},
		configureFilteredMember: func(string, string, *int, bridgevlan.PortPolicy) error {
			configured++
			return nil
		},
		removeFilteredMember: func(string, string) error {
			removed++
			return nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			defaultChanges++
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := syncStandardSwitchRuntime(sw); err != nil {
		t.Fatalf("reconcile matching switch: %v", err)
	}
	if configured != 0 || removed != 0 || defaultChanges != 0 {
		t.Fatalf("matching runtime mutated: configure=%d remove=%d default=%d", configured, removed, defaultChanges)
	}
	if len(commands) != 0 {
		t.Fatalf("matching runtime issued commands: %v", commands)
	}
}

func TestFilteredStandardSwitchStartupRepairsPersistedBridgeDefaultsWithoutWorkloadMembers(t *testing.T) {
	access := 10
	for _, test := range []struct {
		name  string
		state bridgevlan.BridgeState
	}{
		{
			name:  "default PVID drift",
			state: bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 20},
		},
		{
			name:  "default Q-in-Q drift",
			state: bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: access, DefaultQinQ: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sw := withTestStandardSwitchMAC(networkModels.StandardSwitch{
				Name:              "filtered",
				BridgeName:        "vm-filtered",
				MTU:               1500,
				VLANFiltering:     true,
				DefaultAccessVLAN: &access,
				Ports: []networkModels.NetworkPort{{
					Name: "em0",
					VLANPolicy: bridgevlan.PortPolicy{
						Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access, TaggedVLANs: []int{},
					},
				}},
			})
			bridgeInterface := &iface.Interface{
				Name: sw.BridgeName, Ether: testStandardSwitchMAC, Description: sw.Name, MTU: 1500,
				Flags:         iface.Flags{Desc: []string{"UP"}},
				ND6:           iface.ND6{Flags: iface.Flags{Desc: []string{"IFDISABLED"}}},
				BridgeMembers: []iface.BridgeMember{{Name: "em0"}},
			}
			state := test.state
			var commands []string
			var defaultChanges, configured, removed int
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					if name == sw.BridgeName {
						return bridgeInterface, nil
					}
					if name == "em0" {
						return &iface.Interface{Name: name, MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}}}, nil
					}
					return nil, errors.New("interface not found")
				},
				inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
					return state, nil
				},
				filteredPolicyMatches: func(string, string, bridgevlan.PortPolicy) (bool, error) {
					return true, nil
				},
				configureFilteredMember: func(string, string, *int, bridgevlan.PortPolicy) error {
					configured++
					return nil
				},
				removeFilteredMember: func(string, string) error {
					removed++
					return nil
				},
				setDefaultAccessVLAN: func(bridge string, desired *int) error {
					defaultChanges++
					if bridge != sw.BridgeName || desired == nil || *desired != access {
						t.Fatalf("default repair = %s/%v, want %s/%d", bridge, desired, sw.BridgeName, access)
					}
					state.DefaultPVID = access
					state.DefaultQinQ = false
					return nil
				},
				runCommand: func(command string, args ...string) (string, error) {
					commands = append(commands, strings.Join(append([]string{command}, args...), " "))
					return "", nil
				},
			})

			if err := syncStandardSwitchRuntime(sw); err != nil {
				t.Fatalf("reconcile drifted defaults: %v", err)
			}
			if defaultChanges != 1 {
				t.Fatalf("default repairs = %d, want 1", defaultChanges)
			}
			if configured != 0 || removed != 0 || len(commands) != 0 {
				t.Fatalf("startup touched members: configure=%d remove=%d commands=%v", configured, removed, commands)
			}
		})
	}
}

func TestFilteredStandardSwitchStartupRefusesDefaultRepairWithWorkloadMember(t *testing.T) {
	access := 10
	sw := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name: "filtered", BridgeName: "vm-filtered", MTU: 1500,
		VLANFiltering: true, DefaultAccessVLAN: &access,
		Ports: []networkModels.NetworkPort{{
			Name: "em0",
			VLANPolicy: bridgevlan.PortPolicy{
				Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access, TaggedVLANs: []int{},
			},
		}},
	})
	var defaultChanges int
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == sw.BridgeName {
				return &iface.Interface{
					Name:          name,
					BridgeMembers: []iface.BridgeMember{{Name: "em0"}, {Name: "tap0"}},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 20}, nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			defaultChanges++
			return nil
		},
	})

	err := syncStandardSwitchRuntime(sw)
	if err == nil || !strings.Contains(err.Error(), "unmanaged member tap0 is attached") {
		t.Fatalf("expected attached-workload refusal, got %v", err)
	}
	if defaultChanges != 0 {
		t.Fatalf("default VLAN changed %d times despite attached workload", defaultChanges)
	}
}

func TestFilteredStandardSwitchStartupRefusesToEnableFiltering(t *testing.T) {
	sw := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name:          "filtered",
		BridgeName:    "vm-filtered",
		MTU:           1500,
		VLANFiltering: true,
	})
	var defaultChanges, configured int
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: false}, nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			defaultChanges++
			return nil
		},
		configureFilteredMember: func(string, string, *int, bridgevlan.PortPolicy) error {
			configured++
			return nil
		},
	})

	err := syncStandardSwitchRuntime(sw)
	if err == nil || !strings.Contains(err.Error(), "VLAN filtering is disabled") {
		t.Fatalf("expected filtering mismatch, got %v", err)
	}
	if defaultChanges != 0 || configured != 0 {
		t.Fatalf("runtime mutated after filtering mismatch: default=%d configure=%d", defaultChanges, configured)
	}
}

func TestFilteredStandardSwitchPolicyDriftTouchesOnlyDriftedMember(t *testing.T) {
	access10, access20 := 10, 20
	sw := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name:          "filtered",
		BridgeName:    "vm-filtered",
		MTU:           1500,
		VLANFiltering: true,
		Ports: []networkModels.NetworkPort{
			{Name: "em0", VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access10}},
			{Name: "em1", VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access20}},
		},
	})
	bridgeInterface := &iface.Interface{
		Name: sw.BridgeName, Ether: testStandardSwitchMAC, Description: sw.Name, MTU: 1500,
		Flags: iface.Flags{Desc: []string{"UP"}},
		ND6:   iface.ND6{Flags: iface.Flags{Desc: []string{"IFDISABLED"}}},
		BridgeMembers: []iface.BridgeMember{
			{Name: "em0"}, {Name: "em1"}, {Name: "tap0"},
		},
	}
	var commands, configured []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case sw.BridgeName:
				return bridgeInterface, nil
			case "em0", "em1":
				return &iface.Interface{Name: name, MTU: 1500, Flags: iface.Flags{Desc: []string{"UP"}}}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
		filteredPolicyMatches: func(_ string, member string, _ bridgevlan.PortPolicy) (bool, error) {
			return member == "em0", nil
		},
		configureFilteredMember: func(_ string, member string, _ *int, _ bridgevlan.PortPolicy) error {
			configured = append(configured, member)
			return nil
		},
		removeFilteredMember: func(_ string, member string) error {
			t.Fatalf("unexpected member removal: %s", member)
			return nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			t.Fatal("unexpected default VLAN change")
			return nil
		},
		stopDhclient: func(string) error { return nil },
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})

	if err := reconcileFilteredStandardBridge(sw, sw); err != nil {
		t.Fatalf("reconcile drifted switch: %v", err)
	}
	if !slices.Equal(configured, []string{"em1"}) {
		t.Fatalf("configured members = %v, want [em1]", configured)
	}
	for _, command := range commands {
		padded := " " + command + " "
		if strings.Contains(padded, " em0 ") || strings.Contains(padded, " tap0 ") {
			t.Fatalf("unrelated member was touched: %s", command)
		}
	}
}

func TestAddFilteredBridgeMemberStaysDownUntilPolicyIsVerified(t *testing.T) {
	originalIfaceGet := syncIfaceGet
	originalRun := syncRunCommand
	originalStop := syncStopDhclient
	originalConfigure := syncConfigureFilteredMember
	originalSetPrivate := syncSetBridgeMemberPrivate
	t.Cleanup(func() {
		syncIfaceGet = originalIfaceGet
		syncRunCommand = originalRun
		syncStopDhclient = originalStop
		syncConfigureFilteredMember = originalConfigure
		syncSetBridgeMemberPrivate = originalSetPrivate
	})

	var operations []string
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name}, nil
	}
	syncStopDhclient = func(string) error { return nil }
	syncRunCommand = func(command string, args ...string) (string, error) {
		operations = append(operations, strings.Join(append([]string{command}, args...), " "))
		return "", nil
	}
	syncConfigureFilteredMember = func(
		bridge, member string,
		expected *int,
		policy bridgevlan.PortPolicy,
	) error {
		operations = append(operations, "configure "+bridge+" "+member)
		return nil
	}
	syncSetBridgeMemberPrivate = func(bridge, member string, private bool) error {
		operations = append(operations, fmt.Sprintf("private %s %s %t", bridge, member, private))
		return nil
	}

	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: testVLANPointer(10)}
	if err := addFilteredBridgeMember("bridge0", "igb0", 1500, false, nil, policy); err != nil {
		t.Fatalf("add filtered member: %v", err)
	}
	down := slices.Index(operations, "/sbin/ifconfig igb0 down")
	mtu := slices.Index(operations, "/sbin/ifconfig igb0 mtu 1500")
	configured := slices.Index(operations, "configure bridge0 igb0")
	private := slices.Index(operations, "private bridge0 igb0 false")
	up := slices.Index(operations, "/sbin/ifconfig igb0 up")
	if down < 0 || mtu < 0 || configured < 0 || private < 0 || up < 0 ||
		!(down < mtu && mtu < configured && configured < private && private < up) {
		t.Fatalf("operation order = %v; expected down before mutation, policy and isolation, then up", operations)
	}
}

func TestAddFilteredBridgeMemberFailureLeavesSafeRuntimeState(t *testing.T) {
	for _, test := range []struct {
		name                string
		wasAttached         bool
		attachedAfterPolicy bool
		mtuFails            bool
		removeFails         bool
		wantAttached        bool
		wantUp              bool
		wantRemoval         bool
		wantError           string
	}{
		{
			name: "new member is removed", attachedAfterPolicy: true,
			wantUp: true, wantRemoval: true, wantError: "policy apply failed",
		},
		{
			name:   "new member already detached",
			wantUp: true, wantError: "policy apply failed",
		},
		{
			name: "failure before attach", mtuFails: true,
			wantUp: true, wantError: "mtu failed",
		},
		{
			name:                "failed removal keeps new member isolated",
			attachedAfterPolicy: true, removeFails: true,
			wantAttached: true, wantRemoval: true, wantError: "member still attached",
		},
		{
			name:        "failed repair keeps existing member isolated",
			wasAttached: true, wantAttached: true, wantError: "policy apply failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attached := test.wasAttached
			up := true
			removals := 0
			var operations []string
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					state := &iface.Interface{Name: name}
					if name == "bridge0" && attached {
						state.BridgeMembers = []iface.BridgeMember{{Name: "igb0"}}
					}
					return state, nil
				},
				stopDhclient: func(string) error { return nil },
				runCommand: func(command string, args ...string) (string, error) {
					full := strings.Join(append([]string{command}, args...), " ")
					operations = append(operations, full)
					switch full {
					case "/sbin/ifconfig igb0 down":
						up = false
					case "/sbin/ifconfig igb0 up":
						up = true
					case "/sbin/ifconfig igb0 mtu 1500":
						if test.mtuFails {
							return "", errors.New("mtu failed")
						}
					}
					return "", nil
				},
				configureFilteredMember: func(string, string, *int, bridgevlan.PortPolicy) error {
					if test.mtuFails {
						t.Fatal("policy configured after pre-attachment failure")
					}
					operations = append(operations, "configure")
					attached = test.wasAttached || test.attachedAfterPolicy
					return errors.New("policy apply failed")
				},
				removeFilteredMember: func(string, string) error {
					removals++
					operations = append(operations, "remove")
					if test.removeFails {
						return errors.New("member still attached")
					}
					attached = false
					return nil
				},
			})

			policy := bridgevlan.PortPolicy{
				Mode: bridgevlan.ModeAccess, UntaggedVLAN: testVLANPointer(10),
			}
			err := addFilteredBridgeMember("bridge0", "igb0", 1500, false, nil, policy)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("add filtered member error = %v, want %q", err, test.wantError)
			}
			if attached != test.wantAttached || up != test.wantUp ||
				(removals != 0) != test.wantRemoval {
				t.Fatalf(
					"final state: attached=%t up=%t removals=%d operations=%v",
					attached, up, removals, operations,
				)
			}
			if removals != 0 && up {
				configured := slices.Index(operations, "configure")
				removed := slices.Index(operations, "remove")
				restored := slices.Index(operations, "/sbin/ifconfig igb0 up")
				if configured < 0 || removed < configured || restored < removed {
					t.Fatalf("unsafe cleanup order: %v", operations)
				}
			}
		})
	}
}

func TestRemoveFilteredBridgeMemberLeavesSafeRuntimeState(t *testing.T) {
	for _, test := range []struct {
		name               string
		removeError        string
		detachedAfterError bool
		wantAttached       bool
		wantUp             bool
	}{
		{name: "successful removal", wantUp: true},
		{name: "failed while attached", removeError: "member still attached", wantAttached: true},
		{
			name: "failed after detachment", removeError: "isolation verification failed",
			detachedAfterError: true, wantUp: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attached := true
			up := true
			var operations []string
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					state := &iface.Interface{Name: name}
					if attached {
						state.BridgeMembers = []iface.BridgeMember{{Name: "igb0"}}
					}
					return state, nil
				},
				runCommand: func(command string, args ...string) (string, error) {
					full := strings.Join(append([]string{command}, args...), " ")
					operations = append(operations, full)
					if full == "/sbin/ifconfig igb0 down" {
						up = false
					} else if full == "/sbin/ifconfig igb0 up" {
						up = true
					}
					return "", nil
				},
				removeFilteredMember: func(string, string) error {
					operations = append(operations, "remove")
					if test.removeError != "" {
						if test.detachedAfterError {
							attached = false
						}
						return errors.New(test.removeError)
					}
					attached = false
					return nil
				},
			})

			err := removeFilteredStandardBridgeMember("bridge0", "igb0")
			if test.removeError == "" {
				if err != nil {
					t.Fatalf("remove filtered member: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.removeError) {
				t.Fatalf("remove filtered member error = %v, want %q", err, test.removeError)
			}
			if attached != test.wantAttached || up != test.wantUp {
				t.Fatalf(
					"final state: attached=%t up=%t operations=%v",
					attached, up, operations,
				)
			}
			if !slices.Equal(
				operations[:2],
				[]string{"/sbin/ifconfig igb0 down", "remove"},
			) {
				t.Fatalf("unsafe removal order: %v", operations)
			}
		})
	}
}

func TestFilteredStandardSwitchRollbackNeverRebuildsBridge(t *testing.T) {
	previous := networkModels.StandardSwitch{
		Name:          "filtered",
		BridgeName:    "vm-filtered",
		VLANFiltering: true,
	}
	current := previous
	current.MTU = 9000

	var deleted, created int
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case previous.BridgeName:
				return &iface.Interface{Name: name}, nil
			case "epair0a", "tap0":
				return &iface.Interface{Name: name}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			return errors.New("reverse edit failed")
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			deleted++
			return nil
		},
		createBridge: func(networkModels.StandardSwitch) error {
			created++
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			t.Fatalf("detached filtered members must not be reattached generically: %s %s", command, strings.Join(args, " "))
			return "", nil
		},
	})

	err := restoreStandardSwitchEditRuntime(previous, current, []string{"epair0a", "tap0"})
	if err == nil || !strings.Contains(err.Error(), "restore filtered standard switch in place") {
		t.Fatalf("rollback error = %v, want in-place restore error", err)
	}
	if deleted != 0 || created != 0 {
		t.Fatalf("destructive rollback calls: delete=%d create=%d, want none", deleted, created)
	}
}

func TestFilteredStandardSwitchInterfaceGuardAllowsOrdinaryInterfaces(t *testing.T) {
	svc := seedFilteredStandardSwitchInterface(t)
	if err := svc.rejectFilteredStandardBridgeInterfaces("vm-ordinary", "em0"); err != nil {
		t.Fatalf("ordinary interfaces were rejected: %v", err)
	}
}

func TestKnownStandardSwitchJailMembersUsesPersistedJailIdentity(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StandardSwitch{})
	if err := db.Exec(`INSERT INTO jails (id, ct_id) VALUES (?, ?), (?, ?)`, 1, 7401, 2, 7402).Error; err != nil {
		t.Fatalf("seed jails: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO jail_networks (id, jid, name, switch_id, switch_type) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		17, 1, "vnet0", 9, "standard",
		18, 1, "vnet1", 10, "standard",
		19, 2, "vnet0", 9, "manual",
	).Error; err != nil {
		t.Fatalf("seed jail networks: %v", err)
	}

	members, err := svc.knownStandardSwitchJailMembers(9)
	if err != nil {
		t.Fatalf("load known jail members: %v", err)
	}
	want := fmt.Sprintf("%s_net17a", utils.HashIntToNLetters(7401, 5))
	if len(members) != 1 {
		t.Fatalf("known members = %v, want only %s", members, want)
	}
	if _, ok := members[want]; !ok {
		t.Fatalf("known members = %v, missing %s", members, want)
	}
}

func TestFilteredDefaultVLANChangeAllowsKnownJailMember(t *testing.T) {
	previousDefault, desiredDefault := 10, 20
	previous := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name: "filtered", BridgeName: "vm-filtered", MTU: 1500,
		VLANFiltering: true, DefaultAccessVLAN: &previousDefault,
	})
	desired := previous
	desired.DefaultAccessVLAN = &desiredDefault
	known := map[string]struct{}{"jail_net1a": {}}

	defaultChanges := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name != previous.BridgeName {
				t.Fatalf("unexpected interface lookup: %s", name)
			}
			return &iface.Interface{
				Name: name, Description: desired.Name, MTU: desired.MTU, Ether: testStandardSwitchMAC,
				Flags:         iface.Flags{Desc: []string{"UP"}},
				ND6:           iface.ND6{Flags: iface.Flags{Desc: []string{"IFDISABLED"}}},
				BridgeMembers: []iface.BridgeMember{{Name: "jail_net1a"}},
			}, nil
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: previousDefault}, nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			defaultChanges++
			return nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			t.Fatalf("already-correct bridge required unexpected command: %s %v", command, args)
			return "", nil
		},
	})

	if err := reconcileFilteredStandardBridge(previous, desired, known); err != nil {
		t.Fatalf("change default VLAN with known jail member: %v", err)
	}
	if defaultChanges != 1 {
		t.Fatalf("default VLAN changes = %d, want 1", defaultChanges)
	}
}

func TestRepairFilteredBridgeDefaultsAllowsKnownJailMember(t *testing.T) {
	desiredDefault := 20
	sw := networkModels.StandardSwitch{
		BridgeName: "vm-filtered", VLANFiltering: true, DefaultAccessVLAN: &desiredDefault,
	}
	bridge := &iface.Interface{BridgeMembers: []iface.BridgeMember{{Name: "jail_net1a"}}}
	known := map[string]struct{}{"jail_net1a": {}}

	repaired := 0
	stubSyncFunctions(t, syncStubSet{
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 10}, nil
		},
		setDefaultAccessVLAN: func(string, *int) error {
			repaired++
			return nil
		},
	})

	if err := repairFilteredStandardBridgeDefaults(sw, bridge, known); err != nil {
		t.Fatalf("repair defaults with known jail member: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("default repairs = %d, want 1", repaired)
	}
}

func TestReconcileStandardSwitchPrivateMembersScopesIsolationToGuests(t *testing.T) {
	var calls []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name,
				BridgeMembers: []iface.BridgeMember{
					{Name: "em0"},
					{Name: "jail0a"},
					{Name: "unknown0"},
				},
			}, nil
		},
		setMemberPrivate: func(bridge, member string, private bool) error {
			calls = append(calls, fmt.Sprintf("%s/%s=%t", bridge, member, private))
			return nil
		},
	})

	sw := networkModels.StandardSwitch{
		BridgeName: "vm-private", Private: true,
		Ports: []networkModels.NetworkPort{{Name: "em0"}},
	}
	if err := reconcileStandardSwitchPrivateMembers(
		sw,
		map[string]struct{}{"jail0a": {}},
	); err != nil {
		t.Fatalf("reconcile private members: %v", err)
	}
	if !slices.Equal(calls, []string{"vm-private/em0=false", "vm-private/jail0a=true"}) {
		t.Fatalf("private member calls = %v", calls)
	}
}

func TestReconcileStandardSwitchPrivateMembersJoinsAllFailures(t *testing.T) {
	physicalErr := errors.New("physical isolation failure")
	guestErr := errors.New("guest isolation failure")
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name:          name,
				BridgeMembers: []iface.BridgeMember{{Name: "em0"}, {Name: "jail0a"}},
			}, nil
		},
		setMemberPrivate: func(_ string, member string, _ bool) error {
			switch member {
			case "em0":
				return physicalErr
			case "jail0a":
				return guestErr
			default:
				return nil
			}
		},
	})

	err := reconcileStandardSwitchPrivateMembers(
		networkModels.StandardSwitch{
			BridgeName: "vm-private", Private: true,
			Ports: []networkModels.NetworkPort{{Name: "em0"}},
		},
		map[string]struct{}{"jail0a": {}},
	)
	if !errors.Is(err, physicalErr) || !errors.Is(err, guestErr) {
		t.Fatalf("joined private reconciliation error = %v", err)
	}
}
