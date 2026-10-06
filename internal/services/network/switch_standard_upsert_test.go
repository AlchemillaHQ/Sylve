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
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func useAdoptionDHCPLeaseFile(t *testing.T, contents string) string {
	t.Helper()

	original := standardSwitchDHCPLeasePath
	path := filepath.Join(t.TempDir(), "dhclient.leases")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write adoption lease file: %v", err)
		}
	}
	standardSwitchDHCPLeasePath = func(string) string { return path }
	t.Cleanup(func() { standardSwitchDHCPLeasePath = original })
	return path
}

func adoptionBridgeName(name string) string {
	return utils.ShortHash("vm-" + name)
}

func seedStandardSwitchForUpsert(t *testing.T, svc *Service, db *gorm.DB, name string, config StandardSwitchConfig) networkModels.StandardSwitch {
	t.Helper()

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: name, StandardSwitchConfig: config})
	if err != nil {
		t.Fatalf("seed standard switch %q: %v", name, err)
	}

	var stored networkModels.StandardSwitch
	if err := db.Preload("Ports").First(&stored, id).Error; err != nil {
		t.Fatalf("reload seeded standard switch %q: %v", name, err)
	}
	return stored
}

func upsertTestBaseConfig(t *testing.T, svc *Service) StandardSwitchConfig {
	t.Helper()
	return StandardSwitchConfig{
		MTU:                   1500,
		Ports:                 []string{},
		MACSource:             createTestStandardSwitchMACSource(t, svc),
		DisableBridgeOffloads: true,
		Manual: networkModels.StandardSwitchManualAddresses{
			Network4: "10.55.0.254/24",
			Gateway4: "10.55.0.1",
		},
	}
}

func TestNewStandardSwitchUpsertEquivalentIsNoOp(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	createCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("equivalent upsert must not edit the bridge")
			return nil
		},
	})

	config := upsertTestBaseConfig(t, svc)
	existing := seedStandardSwitchForUpsert(t, svc, db, "idempotent-standard", config)

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "idempotent-standard", StandardSwitchConfig: config})
	if err != nil {
		t.Fatalf("equivalent upsert failed: %v", err)
	}
	if id != existing.ID {
		t.Fatalf("equivalent upsert returned ID %d, want stable ID %d", id, existing.ID)
	}
	if createCalls != 1 {
		t.Fatalf("equivalent upsert created the bridge again: createCalls=%d", createCalls)
	}

	var stored networkModels.StandardSwitch
	if err := db.Preload("Ports").First(&stored, existing.ID).Error; err != nil {
		t.Fatalf("reload switch after no-op: %v", err)
	}
	if stored.Name != existing.Name || stored.MTU != existing.MTU {
		t.Fatalf("no-op changed persisted switch: %#v", stored)
	}
}

func TestNewStandardSwitchUpsertChangedUsesNormalEdit(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	bridgeName := adoptionBridgeName("changed-standard")
	createCalls := 0
	editCalls := 0
	bridgePresent := false
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName && bridgePresent {
				return &iface.Interface{Name: name, Groups: []string{"bridge"}, Ether: testStandardSwitchMAC}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	config := upsertTestBaseConfig(t, svc)
	existing := seedStandardSwitchForUpsert(t, svc, db, "changed-standard", config)
	bridgePresent = true

	config.MTU = 9000
	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "changed-standard", StandardSwitchConfig: config})
	if err != nil {
		t.Fatalf("changed upsert failed: %v", err)
	}
	if id != existing.ID {
		t.Fatalf("changed upsert returned ID %d, want %d", id, existing.ID)
	}
	if createCalls != 1 {
		t.Fatalf("changed upsert created a new bridge: createCalls=%d", createCalls)
	}
	if editCalls != 1 {
		t.Fatalf("changed upsert did not run the normal edit path: editCalls=%d", editCalls)
	}

	var stored networkModels.StandardSwitch
	if err := db.First(&stored, existing.ID).Error; err != nil {
		t.Fatalf("reload changed switch: %v", err)
	}
	if stored.MTU != 9000 {
		t.Fatalf("changed upsert did not persist MTU: %d", stored.MTU)
	}
}

func TestNewStandardSwitchRejectsCrossTypeNameConflict(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	if err := db.Create(&networkModels.ManualSwitch{Name: "shared-name", Bridge: "bridge-shared"}).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}

	createCalls := 0
	stubSyncFunctions(t, syncStubSet{
		createBridge: func(networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name:                 "shared-name",
		StandardSwitchConfig: upsertTestBaseConfig(t, svc),
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_name_conflict" {
		t.Fatalf("unexpected cross-type conflict error: %v", err)
	}
	if createCalls != 0 {
		t.Fatal("cross-type conflict still created a bridge")
	}
}

func TestNewStandardSwitchUpsertEquivalentIgnoresMissingRuntime(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	runtimePresent := true
	createCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if runtimePresent && name == "em9" {
				return &iface.Interface{Name: name, MTU: 1500, Ether: "02:00:00:00:00:09"}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			createCalls++
			return nil
		},
	})

	config := StandardSwitchConfig{
		MTU:         1500,
		Ports:       []string{"em9"},
		MACSource:   networkModels.StandardSwitchMACSource{Mode: networkModels.StandardSwitchMACModePort, Port: "em9"},
		DisableIPv6: true,
	}
	existing := seedStandardSwitchForUpsert(t, svc, db, "equivalent-offline", config)

	runtimePresent = false
	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "equivalent-offline", StandardSwitchConfig: config})
	if err != nil {
		t.Fatalf("equivalent offline upsert failed: %v", err)
	}
	if id != existing.ID {
		t.Fatalf("equivalent offline upsert ID = %d, want %d", id, existing.ID)
	}
	if createCalls != 1 {
		t.Fatalf("equivalent offline upsert touched runtime: createCalls=%d", createCalls)
	}
}

func TestNewStandardSwitchUpsertRejectsCrossTypeDoubleOwner(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	if err := db.Create(&networkModels.StandardSwitch{Name: "double", BridgeName: "vm-double", MTU: 1500}).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	if err := db.Create(&networkModels.ManualSwitch{Name: "double", Bridge: "vm-double"}).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name:                 "double",
		StandardSwitchConfig: StandardSwitchConfig{MTU: 1500, Ports: []string{}},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_name_conflict" {
		t.Fatalf("double-owner upsert error = %v, want standard_switch_name_conflict", err)
	}
}

func TestNewStandardSwitchUpsertRemovingHostVLANRequiresConfirmation(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	hostVLAN := 20
	persisted := networkModels.StandardSwitch{
		Name:          "host-l3",
		BridgeName:    "vm-host-l3",
		MTU:           1500,
		VLANFiltering: true,
		HostVLAN:      &hostVLAN,
		NetworkManual: "198.18.40.2/24",
		DisableIPv6:   true,
	}
	setTestStandardSwitchMACSource(&persisted, createTestStandardSwitchMACSource(t, svc))
	if err := db.Create(&persisted).Error; err != nil {
		t.Fatalf("seed filtered host L3 switch: %v", err)
	}

	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == "vm-host-l3" {
				return &iface.Interface{Name: name, Groups: []string{"bridge"}, Ether: testStandardSwitchMAC}, nil
			}
			return nil, errors.New("interface not found")
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "host-l3",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   networkModels.StandardSwitchMACSource{Mode: persisted.BridgeMACMode, MACObjectID: *persisted.BridgeMACObjectID},
			DisableIPv6: true,
			VLANConfig:  networkModels.StandardSwitchVLANConfig{Filtering: true},
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_host_layer3_removal_requires_confirmation" {
		t.Fatalf("host L3 removal error = %v, want confirmation required", err)
	}
	if editCalls != 0 {
		t.Fatal("host L3 removal mutated runtime before the safety check")
	}

	var stored networkModels.StandardSwitch
	if err := db.First(&stored, persisted.ID).Error; err != nil {
		t.Fatalf("reload switch: %v", err)
	}
	if stored.HostVLAN == nil || *stored.HostVLAN != hostVLAN || stored.NetworkManual != "198.18.40.2/24" {
		t.Fatalf("failed upsert modified persisted state: %#v", stored)
	}
}

func TestNewStandardSwitchAdoptsOrphanStaticBridge(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	const portName = "em9"
	bridgeName := adoptionBridgeName("adopt-static")
	editCalls := 0
	deleteCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case bridgeName:
				return &iface.Interface{
					Name:          name,
					Groups:        []string{"bridge"},
					Ether:         testStandardSwitchMAC,
					MTU:           1500,
					IPv4:          []iface.IPv4{{IP: net.ParseIP("198.18.10.2"), Netmask: "255.255.255.0"}},
					BridgeMembers: []iface.BridgeMember{{Name: portName}},
				}, nil
			case portName:
				return &iface.Interface{Name: name, MTU: 1500}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		createBridge: func(networkModels.StandardSwitch) error {
			t.Fatal("adoption must not create a new bridge")
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			deleteCalls++
			return nil
		},
	})

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-static",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.10.2/24"},
		},
	})
	if err != nil {
		t.Fatalf("adopt static orphan: %v", err)
	}
	if id == 0 {
		t.Fatal("adoption returned zero switch ID")
	}
	if editCalls != 1 {
		t.Fatalf("adoption reconcile calls = %d, want 1", editCalls)
	}
	if deleteCalls != 0 {
		t.Fatal("adoption destroyed the existing bridge")
	}

	var count int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
		t.Fatalf("count adopted switches: %v", err)
	}
	if count != 1 {
		t.Fatalf("adopted switch count = %d, want 1", count)
	}
}

func TestNewStandardSwitchAdoptionRejectsForeignMember(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	const portName = "em9"
	bridgeName := adoptionBridgeName("adopt-foreign-member")
	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case bridgeName:
				return &iface.Interface{
					Name:          name,
					Groups:        []string{"bridge"},
					Ether:         testStandardSwitchMAC,
					BridgeMembers: []iface.BridgeMember{{Name: portName}, {Name: "em8"}},
				}, nil
			case portName:
				return &iface.Interface{Name: name, MTU: 1500}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-foreign-member",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_member_conflict" {
		t.Fatalf("unexpected foreign member error: %v", err)
	}
	if editCalls != 0 {
		t.Fatal("foreign member was not rejected before any runtime mutation")
	}
	var count int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
		t.Fatalf("count switches: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected adoption persisted %d rows", count)
	}
}

func TestNewStandardSwitchAdoptionRejectsUnexpectedStaticAddress(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	bridgeName := adoptionBridgeName("adopt-foreign-address")
	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{
					Name:   name,
					Groups: []string{"bridge"},
					Ether:  testStandardSwitchMAC,
					IPv4: []iface.IPv4{
						{IP: net.ParseIP("198.18.11.2"), Netmask: "255.255.255.0"},
						{IP: net.ParseIP("203.0.113.9"), Netmask: "255.255.255.0"},
					},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-foreign-address",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.11.2/24"},
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
		t.Fatalf("unexpected foreign address error: %v", err)
	}
	if editCalls != 0 {
		t.Fatal("foreign address was not rejected before any runtime mutation")
	}
}

func TestNewStandardSwitchAdoptionAcceptsManagedDHCPLeaseOnly(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	bridgeName := adoptionBridgeName("adopt-dhcp")
	useAdoptionDHCPLeaseFile(t, "lease {\n  interface \""+bridgeName+"\";\n  fixed-address 192.0.2.50;\n}\n")

	baseIfaces := func(name string) (*iface.Interface, error) {
		if name == bridgeName {
			return &iface.Interface{
				Name:   name,
				Groups: []string{"bridge"},
				Ether:  testStandardSwitchMAC,
				IPv4:   []iface.IPv4{{IP: net.ParseIP("192.0.2.50"), Netmask: "255.255.255.0"}},
			}, nil
		}
		return nil, errors.New("interface not found")
	}

	request := CreateStandardSwitchRequest{
		Name: "adopt-dhcp",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DHCP:        true,
			DisableIPv6: true,
		},
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet:     baseIfaces,
		editBridge:   func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
		createBridge: func(networkModels.StandardSwitch) error { t.Fatal("must adopt"); return nil },
	})
	if _, err := svc.NewStandardSwitch(request); err != nil {
		t.Fatalf("adopt managed DHCP lease: %v", err)
	}

	svc2, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	useAdoptionDHCPLeaseFile(t, "lease {\n  fixed-address 192.0.2.51;\n}\n")
	request.StandardSwitchConfig.MACSource = createTestStandardSwitchMACSource(t, svc2)
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{
					Name:   name,
					Groups: []string{"bridge"},
					Ether:  testStandardSwitchMAC,
					IPv4:   []iface.IPv4{{IP: net.ParseIP("192.0.2.77"), Netmask: "255.255.255.0"}},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("unmanaged DHCP address must not be reconciled")
			return nil
		},
	})
	_, err := svc2.NewStandardSwitch(request)
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
		t.Fatalf("unmanaged DHCP address error = %v", err)
	}
}

func TestNewStandardSwitchAdoptionAcceptsSLAACAutoconfOnly(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	bridgeName := adoptionBridgeName("adopt-slaac")
	request := CreateStandardSwitchRequest{
		Name: "adopt-slaac",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:          1500,
			Ports:        []string{},
			MACSource:    createTestStandardSwitchMACSource(t, svc),
			SLAAC:        true,
			DefaultRoute: false,
		},
	}

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{
					Name:   name,
					Groups: []string{"bridge"},
					Ether:  testStandardSwitchMAC,
					IPv6: []iface.IPv6{
						{IP: net.ParseIP("fe80::1234"), PrefixLength: 64},
						{IP: net.ParseIP("2001:db8::50"), PrefixLength: 64, AutoConf: true},
					},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		editBridge:   func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
		createBridge: func(networkModels.StandardSwitch) error { t.Fatal("must adopt"); return nil },
	})
	if _, err := svc.NewStandardSwitch(request); err != nil {
		t.Fatalf("adopt SLAAC autoconf address: %v", err)
	}

	svc2, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	request.StandardSwitchConfig.MACSource = createTestStandardSwitchMACSource(t, svc2)
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{
					Name:   name,
					Groups: []string{"bridge"},
					Ether:  testStandardSwitchMAC,
					IPv6:   []iface.IPv6{{IP: net.ParseIP("2001:db8::99"), PrefixLength: 64}},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("non-SLAAC address must not be reconciled")
			return nil
		},
	})
	_, err := svc2.NewStandardSwitch(request)
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
		t.Fatalf("non-SLAAC address error = %v", err)
	}
}

func TestNewStandardSwitchAdoptionRejectsMismatchedHostVLAN(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	const portName = "em9"
	bridgeName := adoptionBridgeName("adopt-host-vlan")
	hostVLAN := 20
	hostName := bridgeName + ".20"

	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case bridgeName:
				return &iface.Interface{
					Name:          name,
					Groups:        []string{"bridge"},
					Ether:         testStandardSwitchMAC,
					BridgeMembers: []iface.BridgeMember{{Name: portName}},
				}, nil
			case hostName:
				return &iface.Interface{Name: name, VLANParent: bridgeName, VLANTag: hostVLAN}, nil
			case portName:
				return &iface.Interface{Name: name, MTU: 1500}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			t.Fatal("mismatched host VLAN must be rejected before mutation")
			return nil
		},
	})

	accessVLAN := 20
	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-host-vlan",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.12.2/24"},
			VLANConfig: networkModels.StandardSwitchVLANConfig{
				Filtering: true,
				HostVLAN:  &hostVLAN,
				PortPolicies: map[string]bridgevlan.PortPolicy{
					portName: {Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
				},
			},
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_host_vlan_interface_conflict" {
		t.Fatalf("unexpected host VLAN mismatch error: %v", err)
	}
}

func TestNewStandardSwitchAdoptionPersistFailureDoesNotDestroyBridge(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	const portName = "em9"
	bridgeName := adoptionBridgeName("adopt-persist-failure")
	deleteCalls := 0
	editCalls := 0
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case bridgeName:
				return &iface.Interface{
					Name:          name,
					Groups:        []string{"bridge"},
					Ether:         testStandardSwitchMAC,
					BridgeMembers: []iface.BridgeMember{{Name: portName}},
				}, nil
			case portName:
				return &iface.Interface{Name: name, MTU: 1500}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
			editCalls++
			return nil
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			deleteCalls++
			return nil
		},
	})

	macSource := createTestStandardSwitchMACSource(t, svc)
	forced := errors.New("forced persistence failure")
	if err := db.Callback().Create().Before("gorm:create").Register("test_force_adopt_failure", func(tx *gorm.DB) {
		tx.AddError(forced)
	}); err != nil {
		t.Fatalf("register forced failure callback: %v", err)
	}

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-persist-failure",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "create adopted standard switch") {
		t.Fatalf("expected adoption persistence failure, got %v", err)
	}
	if editCalls != 1 {
		t.Fatalf("adoption reconcile calls = %d, want 1", editCalls)
	}
	if deleteCalls != 0 {
		t.Fatal("adopted bridge was destroyed after a failed persistence")
	}
}

func TestNewStandardSwitchAdoptionHydratesReferencedObjects(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	macSource := createTestStandardSwitchMACSource(t, svc)
	networkObj := networkModels.Object{
		Name:    "adopt-network",
		Type:    "Network",
		Entries: []networkModels.ObjectEntry{{Value: "198.18.20.2/24"}},
	}
	if err := db.Create(&networkObj).Error; err != nil {
		t.Fatalf("seed network object: %v", err)
	}
	gatewayObj := networkModels.Object{
		Name:    "adopt-gateway",
		Type:    "Host",
		Entries: []networkModels.ObjectEntry{{Value: "198.18.20.1"}},
	}
	if err := db.Create(&gatewayObj).Error; err != nil {
		t.Fatalf("seed gateway object: %v", err)
	}

	const portName = "em9"
	bridgeName := adoptionBridgeName("adopt-objects")
	var captured networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			switch name {
			case bridgeName:
				return &iface.Interface{
					Name:          name,
					Groups:        []string{"bridge"},
					Ether:         testStandardSwitchMAC,
					BridgeMembers: []iface.BridgeMember{{Name: portName}},
					IPv4:          []iface.IPv4{{IP: net.ParseIP("198.18.20.2"), Netmask: "255.255.255.0"}},
				}, nil
			case portName:
				return &iface.Interface{Name: name, MTU: 1500}, nil
			default:
				return nil, errors.New("interface not found")
			}
		},
		editBridge: func(_, newSw networkModels.StandardSwitch) error {
			captured = newSw
			return nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-objects",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			Network4ID:  networkObj.ID,
			Gateway4ID:  gatewayObj.ID,
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if err != nil {
		t.Fatalf("adopt with referenced objects: %v", err)
	}
	if captured.Network(4) != "198.18.20.2/24" {
		t.Fatalf("adopted switch network resolved to %q, want object value", captured.Network(4))
	}
	if captured.Gateway(4) != "198.18.20.1" {
		t.Fatalf("adopted switch gateway resolved to %q, want object value", captured.Gateway(4))
	}
	if _, err := desiredStandardSwitchMAC(captured); err != nil {
		t.Fatalf("adopted switch MAC object not hydrated: %v", err)
	}
}

func TestNewStandardSwitchAdoptionAcceptsLegacyVLANMembers(t *testing.T) {
	type legacyCase struct {
		name         string
		memberName   string
		rawIPv4      bool
		wantDetach   bool
		wantConflict bool
	}
	cases := []legacyCase{
		{name: "derived vlan subinterface", memberName: "em9.10"},
		{name: "raw parent port", memberName: "em9", wantDetach: true},
		{name: "raw parent with foreign address", memberName: "em9", rawIPv4: true, wantConflict: true},
	}

	for index, test := range cases {
		switchName := fmt.Sprintf("adopt-vlan-%d", index)
		t.Run(test.name, func(t *testing.T) {
			svc, db := newNetworkServiceForTest(t,
				&networkModels.ManualSwitch{},
				&networkModels.StandardSwitch{},
				&networkModels.NetworkPort{},
			)
			bridgeName := adoptionBridgeName(switchName)
			var commands []string
			editCalls := 0
			deleteCalls := 0
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					switch name {
					case bridgeName:
						return &iface.Interface{
							Name:          name,
							Groups:        []string{"bridge"},
							Ether:         testStandardSwitchMAC,
							BridgeMembers: []iface.BridgeMember{{Name: test.memberName}},
						}, nil
					case "em9":
						member := &iface.Interface{Name: name, MTU: 1500}
						if test.rawIPv4 && test.memberName == "em9" {
							member.IPv4 = []iface.IPv4{{IP: net.ParseIP("203.0.113.9"), Netmask: "255.255.255.0"}}
						}
						return member, nil
					case "em9.10":
						if test.memberName != "em9.10" {
							return nil, errors.New("interface not found")
						}
						return &iface.Interface{
							Name:       name,
							MTU:        1500,
							Groups:     []string{"svm-vlan"},
							VLANParent: "em9",
							VLANTag:    10,
						}, nil
					default:
						return nil, errors.New("interface not found")
					}
				},
				runCommand: func(command string, args ...string) (string, error) {
					commands = append(commands, strings.Join(append([]string{command}, args...), " "))
					return "", nil
				},
				editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
					editCalls++
					return nil
				},
				deleteBridge: func(networkModels.StandardSwitch) error {
					deleteCalls++
					return nil
				},
			})

			id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
				Name: switchName,
				StandardSwitchConfig: StandardSwitchConfig{
					MTU:         1500,
					VLAN:        10,
					Ports:       []string{"em9"},
					MACSource:   createTestStandardSwitchMACSource(t, svc),
					DisableIPv6: true,
				},
			})

			if test.wantConflict {
				if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
					t.Fatalf("foreign raw legacy member error = %v", err)
				}
				if editCalls != 0 {
					t.Fatal("foreign raw legacy member was mutated before rejection")
				}
				for _, command := range commands {
					if strings.Contains(command, "deletem") {
						t.Fatalf("foreign raw legacy member was detached: %v", commands)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("adopt legacy VLAN member %s: %v", test.memberName, err)
			}
			if id == 0 {
				t.Fatal("adoption returned a zero ID")
			}
			if editCalls != 1 {
				t.Fatalf("adoption reconcile calls = %d, want 1", editCalls)
			}
			if deleteCalls != 0 {
				t.Fatal("adoption destroyed the bridge")
			}
			detached := false
			for _, command := range commands {
				if command == "/sbin/ifconfig "+bridgeName+" deletem em9" {
					detached = true
				}
			}
			if detached != test.wantDetach {
				t.Fatalf("raw member detach = %t, want %t; commands=%v", detached, test.wantDetach, commands)
			}
			if test.wantDetach {
				if commandIndex(commands, "/sbin/ifconfig em9 up") == -1 {
					t.Fatalf("detached raw member was not brought back up: %v", commands)
				}
			}

			var count int64
			if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
				t.Fatalf("count switches: %v", err)
			}
			if count != 1 {
				t.Fatalf("adopted switch count = %d, want 1", count)
			}
		})
	}
}

func TestParseStandardSwitchDHCPLeasesFiltersInterfacesAndComments(t *testing.T) {
	data := []byte(`
# commented fixed-address 203.0.113.1;
lease {
  interface "vm-test";
  fixed-address 192.0.2.50;
  option subnet-mask 255.255.255.0;
}
lease {
  # fixed-address 192.0.2.99;
  interface "other0";
  fixed-address 198.51.100.7;
}
lease {
  interface "vm-test";
  fixed-address 192.0.2.60;
}
lease {
  interface "vm-test";
  fixed-address 192.0.2.70;
  option subnet-mask 255.0.0.300;
}
`)
	leases := parseStandardSwitchDHCPLeases(data, "vm-test")
	if len(leases) != 2 {
		t.Fatalf("parsed leases = %#v, want 2", leases)
	}
	if got := leases[0].address.String(); got != "192.0.2.50" {
		t.Fatalf("first lease address = %s, want 192.0.2.50", got)
	}
	if leases[0].prefix != 24 {
		t.Fatalf("first lease prefix = %d, want 24", leases[0].prefix)
	}
	if got := leases[1].address.String(); got != "192.0.2.60" {
		t.Fatalf("second lease address = %s, want 192.0.2.60", got)
	}
	if leases[1].prefix != -1 {
		t.Fatalf("second lease prefix = %d, want -1 (no mask)", leases[1].prefix)
	}
	for _, lease := range leases {
		if lease.address.String() == "198.51.100.7" || lease.address.String() == "192.0.2.70" ||
			lease.address.String() == "192.0.2.99" {
			t.Fatalf("lease for a foreign interface or invalid mask was accepted: %#v", lease)
		}
	}
}

func TestDHCPLeaseMatchesRequiresPrefixEvidence(t *testing.T) {
	lease := standardSwitchDHCPLease{address: netip.MustParseAddr("192.0.2.50"), prefix: 24}
	if !dhcpLeaseMatches([]standardSwitchDHCPLease{lease}, netip.MustParseAddr("192.0.2.50"), 24) {
		t.Fatal("matching prefix was rejected")
	}
	if dhcpLeaseMatches([]standardSwitchDHCPLease{lease}, netip.MustParseAddr("192.0.2.50"), 16) {
		t.Fatal("mismatched prefix was accepted")
	}
	noMask := standardSwitchDHCPLease{address: netip.MustParseAddr("192.0.2.60"), prefix: -1}
	if !dhcpLeaseMatches([]standardSwitchDHCPLease{noMask}, netip.MustParseAddr("192.0.2.60"), 30) {
		t.Fatal("maskless lease did not match by address")
	}
}

func TestNewStandardSwitchAdoptionDoesNotReadLeasesForStatic(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	original := standardSwitchDHCPLeasePath
	standardSwitchDHCPLeasePath = func(string) string {
		t.Fatal("static adoption must not read the DHCP lease file")
		return ""
	}
	t.Cleanup(func() { standardSwitchDHCPLeasePath = original })

	bridgeName := adoptionBridgeName("adopt-static-no-lease")
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{
					Name:   name,
					Groups: []string{"bridge"},
					Ether:  testStandardSwitchMAC,
					IPv4:   []iface.IPv4{{IP: net.ParseIP("198.18.30.2"), Netmask: "255.255.255.0"}},
				}, nil
			}
			return nil, errors.New("interface not found")
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
	})

	if _, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-static-no-lease",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.30.2/24"},
		},
	}); err != nil {
		t.Fatalf("static adoption read leases or failed: %v", err)
	}
}

func TestNewStandardSwitchAdoptionRejectsForeignLegacyVLAN(t *testing.T) {
	const portName = "em9"

	tests := []struct {
		name     string
		build    func() *iface.Interface
		wantCode string
	}{
		{
			name: "wrong tag",
			build: func() *iface.Interface {
				return &iface.Interface{
					Name:       portName + ".10",
					Groups:     []string{"svm-vlan"},
					VLANParent: portName,
					VLANTag:    20,
				}
			},
			wantCode: "standard_switch_vlan_interface_conflict",
		},
		{
			name: "foreign address",
			build: func() *iface.Interface {
				return &iface.Interface{
					Name:       portName + ".10",
					Groups:     []string{"svm-vlan"},
					VLANParent: portName,
					VLANTag:    10,
					IPv4:       []iface.IPv4{{IP: net.ParseIP("203.0.113.9"), Netmask: "255.255.255.0"}},
				}
			},
			wantCode: "standard_switch_runtime_address_conflict",
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newNetworkServiceForTest(t,
				&networkModels.ManualSwitch{},
				&networkModels.StandardSwitch{},
				&networkModels.NetworkPort{},
			)
			switchName := fmt.Sprintf("adopt-legacy-foreign-%d", index)
			bridgeName := adoptionBridgeName(switchName)
			editCalls := 0
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					switch name {
					case bridgeName:
						return &iface.Interface{
							Name:          name,
							Groups:        []string{"bridge"},
							Ether:         testStandardSwitchMAC,
							BridgeMembers: []iface.BridgeMember{{Name: portName + ".10"}},
						}, nil
					case portName:
						return &iface.Interface{Name: name, MTU: 1500}, nil
					case portName + ".10":
						return test.build(), nil
					default:
						return nil, errors.New("interface not found")
					}
				},
				editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
					editCalls++
					return nil
				},
			})

			_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
				Name: switchName,
				StandardSwitchConfig: StandardSwitchConfig{
					MTU:         1500,
					VLAN:        10,
					Ports:       []string{portName},
					MACSource:   createTestStandardSwitchMACSource(t, svc),
					DisableIPv6: true,
				},
			})
			if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != test.wantCode {
				t.Fatalf("legacy VLAN adoption error = %v, want %s", err, test.wantCode)
			}
			if editCalls != 0 {
				t.Fatal("foreign legacy VLAN was mutated before rejection")
			}
		})
	}
}

func TestNewStandardSwitchAdoptionRejectsForeignFilteredPolicy(t *testing.T) {
	const portName = "em9"

	t.Run("foreign bridge default PVID", func(t *testing.T) {
		svc, _ := newNetworkServiceForTest(t,
			&networkModels.ManualSwitch{},
			&networkModels.StandardSwitch{},
			&networkModels.NetworkPort{},
		)
		bridgeName := adoptionBridgeName("adopt-filtered-pvid")
		hostVLAN := 20
		stubSyncFunctions(t, syncStubSet{
			ifaceGet: func(name string) (*iface.Interface, error) {
				switch name {
				case bridgeName:
					return &iface.Interface{
						Name:          name,
						Groups:        []string{"bridge"},
						Ether:         testStandardSwitchMAC,
						BridgeMembers: []iface.BridgeMember{{Name: portName}},
					}, nil
				case portName:
					return &iface.Interface{Name: name, MTU: 1500}, nil
				default:
					return nil, errors.New("interface not found")
				}
			},
			inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
				return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 99}, nil
			},
			editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
				t.Fatal("foreign default PVID was mutated before rejection")
				return nil
			},
		})

		accessVLAN := 20
		_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
			Name: "adopt-filtered-pvid",
			StandardSwitchConfig: StandardSwitchConfig{
				MTU:         1500,
				Ports:       []string{portName},
				MACSource:   createTestStandardSwitchMACSource(t, svc),
				DisableIPv6: true,
				VLANConfig: networkModels.StandardSwitchVLANConfig{
					Filtering: true,
					HostVLAN:  &hostVLAN,
					PortPolicies: map[string]bridgevlan.PortPolicy{
						portName: {Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
					},
				},
			},
		})
		if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_vlan_conflict" {
			t.Fatalf("foreign default PVID error = %v", err)
		}
	})

	t.Run("foreign member policy", func(t *testing.T) {
		svc, _ := newNetworkServiceForTest(t,
			&networkModels.ManualSwitch{},
			&networkModels.StandardSwitch{},
			&networkModels.NetworkPort{},
		)
		bridgeName := adoptionBridgeName("adopt-filtered-member")
		hostVLAN := 20
		stubSyncFunctions(t, syncStubSet{
			ifaceGet: func(name string) (*iface.Interface, error) {
				switch name {
				case bridgeName:
					return &iface.Interface{
						Name:          name,
						Groups:        []string{"bridge"},
						Ether:         testStandardSwitchMAC,
						BridgeMembers: []iface.BridgeMember{{Name: portName}},
					}, nil
				case portName:
					return &iface.Interface{Name: name, MTU: 1500}, nil
				default:
					return nil, errors.New("interface not found")
				}
			},
			inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
				return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 0}, nil
			},
			filteredPolicyContains: func(string, string, bridgevlan.PortPolicy) (bool, error) {
				return false, nil
			},
			editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error {
				t.Fatal("foreign member policy was mutated before rejection")
				return nil
			},
		})

		accessVLAN := 20
		_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
			Name: "adopt-filtered-member",
			StandardSwitchConfig: StandardSwitchConfig{
				MTU:         1500,
				Ports:       []string{portName},
				MACSource:   createTestStandardSwitchMACSource(t, svc),
				DisableIPv6: true,
				VLANConfig: networkModels.StandardSwitchVLANConfig{
					Filtering: true,
					HostVLAN:  &hostVLAN,
					PortPolicies: map[string]bridgevlan.PortPolicy{
						portName: {Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
					},
				},
			},
		})
		if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_member_vlan_conflict" {
			t.Fatalf("foreign member policy error = %v", err)
		}
	})
}

func TestNewStandardSwitchAdoptionRejectsVLANModeMismatch(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)

	bridgeName := adoptionBridgeName("adopt-mode-mismatch")
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == bridgeName {
				return &iface.Interface{Name: name, Groups: []string{"bridge"}, Ether: testStandardSwitchMAC}, nil
			}
			return nil, errors.New("interface not found")
		},
		inspectBridgeVLAN: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
	})

	_, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: "adopt-mode-mismatch",
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   createTestStandardSwitchMACSource(t, svc),
			DisableIPv6: true,
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_vlan_mode_conflict" {
		t.Fatalf("unexpected VLAN mode mismatch error: %v", err)
	}
}
