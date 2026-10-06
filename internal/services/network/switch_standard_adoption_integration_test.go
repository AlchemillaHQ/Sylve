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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func requireStandardSwitchAdoptionIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping standard switch adoption integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("standard switch adoption integration test requires root")
	}
	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		t.Skipf("required command /sbin/ifconfig is unavailable: %v", err)
	}
}

func uniqueAdoptionName(prefix string) string {
	return fmt.Sprintf("%s-%04x%04x", prefix, os.Getpid()&0xffff, time.Now().UnixNano()&0xffff)
}

func createAdoptionTestBridge(t *testing.T, bridgeName string) {
	t.Helper()
	output, err := utils.RunCommand("/sbin/ifconfig", "bridge", "create")
	if err != nil {
		t.Fatalf("create adoption test bridge: %v", err)
	}
	rawName := strings.TrimSpace(output)
	if rawName == "" {
		t.Fatal("create adoption test bridge returned an empty name")
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", rawName, "name", bridgeName); err != nil {
		t.Fatalf("rename adoption test bridge %s: %v", rawName, err)
	}
}

func loadMACObjectForIntegration(t *testing.T, svc *Service, macSource networkModels.StandardSwitchMACSource) networkModels.Object {
	t.Helper()
	var object networkModels.Object
	if err := svc.DB.Preload("Entries").First(&object, macSource.MACObjectID).Error; err != nil {
		t.Fatalf("load integration MAC object: %v", err)
	}
	return object
}

func orphanModel(
	name, bridgeName, portName string,
	macSource networkModels.StandardSwitchMACSource,
	macObject networkModels.Object,
) networkModels.StandardSwitch {
	sw := networkModels.StandardSwitch{
		Name:          name,
		BridgeName:    bridgeName,
		MTU:           1500,
		DisableIPv6:   true,
		NetworkManual: "198.18.240.2/24",
		Ports:         []networkModels.NetworkPort{{Name: portName}},
	}
	setTestStandardSwitchMACSource(&sw, macSource)
	sw.BridgeMACObject = &macObject
	return sw
}

func TestIntegrationStandardSwitchAdoptsStaticOrphanRuntime(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-static")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	macObject := loadMACObjectForIntegration(t, svc, macSource)

	if err := createStandardBridge(orphanModel(name, bridgeName, portName, macSource, macObject)); err != nil {
		t.Fatalf("create orphan runtime bridge: %v", err)
	}

	deleteCalls := 0
	createCalls := 0
	stubSyncFunctions(t, syncStubSet{
		createBridge: func(networkModels.StandardSwitch) error {
			createCalls++
			return errors.New("adoption must not create a new bridge")
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			deleteCalls++
			return nil
		},
	})

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.240.2/24"},
		},
	})
	if err != nil {
		t.Fatalf("adopt static orphan: %v", err)
	}
	if id == 0 {
		t.Fatal("adoption returned a zero switch ID")
	}
	if createCalls != 0 || deleteCalls != 0 {
		t.Fatalf("adoption runtime calls: create=%d delete=%d, want 0/0", createCalls, deleteCalls)
	}

	if _, err := iface.Get(bridgeName); err != nil {
		t.Fatalf("adopted bridge was destroyed: %v", err)
	}
	bridge, err := iface.Get(bridgeName)
	if err != nil {
		t.Fatalf("inspect adopted bridge: %v", err)
	}
	if !interfaceHasIPv4Prefix(bridge, "198.18.240.2/24") {
		t.Fatalf("adopted bridge lost its static address: %#v", bridge.IPv4)
	}

	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, id).Error; err != nil {
		t.Fatalf("load adopted switch: %v", err)
	}
	if persisted.BridgeName != bridgeName {
		t.Fatalf("adopted switch bridge = %q, want %q", persisted.BridgeName, bridgeName)
	}
}

func TestIntegrationStandardSwitchAdoptionRejectsForeignMemberWithoutMutation(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-foreign")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	createAdoptionTestBridge(t, bridgeName)
	foreignOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create foreign epair: %v", err)
	}
	foreignPort := strings.TrimSpace(foreignOutput)
	if foreignPort == "" {
		t.Fatal("create foreign epair returned an empty name")
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", bridgeName, "addm", foreignPort, "up"); err != nil {
		t.Fatalf("attach foreign member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", foreignPort, "destroy")
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)

	_, err = svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{},
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_member_conflict" {
		t.Fatalf("foreign member adoption error = %v", err)
	}

	bridge, inspectErr := iface.Get(bridgeName)
	if inspectErr != nil {
		t.Fatalf("rejected adoption destroyed the bridge: %v", inspectErr)
	}
	memberAttached := false
	for _, member := range bridge.BridgeMembers {
		if member.Name == foreignPort {
			memberAttached = true
		}
	}
	if !memberAttached {
		t.Fatal("rejected adoption detached the foreign member")
	}
	var count int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
		t.Fatalf("count switches: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected adoption persisted %d rows", count)
	}
}

func TestIntegrationStandardSwitchAdoptsLegacyVLANOrphan(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-vlan")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	vlanName := fmt.Sprintf("%s.10", portName)
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", vlanName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	macObject := loadMACObjectForIntegration(t, svc, macSource)

	runtimeModel := orphanModel(name, bridgeName, portName, macSource, macObject)
	runtimeModel.VLAN = 10
	runtimeModel.NetworkManual = ""
	if err := createStandardBridge(runtimeModel); err != nil {
		t.Fatalf("create legacy VLAN orphan runtime: %v", err)
	}
	if _, err := iface.Get(vlanName); err != nil {
		t.Fatalf("expected orphan VLAN interface: %v", err)
	}

	stubSyncFunctions(t, syncStubSet{
		createBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not create a new bridge")
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not delete the bridge")
		},
	})

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			VLAN:        10,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if err != nil {
		t.Fatalf("adopt legacy VLAN orphan: %v", err)
	}
	if _, err := iface.Get(vlanName); err != nil {
		t.Fatalf("adopted VLAN member disappeared: %v", err)
	}
	var persisted networkModels.StandardSwitch
	if err := db.Preload("Ports").First(&persisted, id).Error; err != nil {
		t.Fatalf("load adopted VLAN switch: %v", err)
	}
	if persisted.VLAN != 10 || len(persisted.Ports) != 1 {
		t.Fatalf("adopted VLAN switch = %#v", persisted)
	}
}

func TestIntegrationStandardSwitchAdoptsFilteredHostVLANOrphan(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-host-vlan")
	bridgeName := adoptionBridgeName(name)
	hostVLAN := 123
	hostName := fmt.Sprintf("%s.%d", bridgeName, hostVLAN)
	for _, candidate := range []string{bridgeName, hostName} {
		if _, err := iface.Get(candidate); err == nil || !isInterfaceMissingError(err) {
			t.Fatalf("integration interface name %s is unavailable: %v", candidate, err)
		}
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", hostName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	macObject := loadMACObjectForIntegration(t, svc, macSource)

	accessVLAN := hostVLAN
	runtimeModel := networkModels.StandardSwitch{
		Name:          name,
		BridgeName:    bridgeName,
		MTU:           1500,
		NetworkManual: "198.18.241.2/24",
		VLANFiltering: true,
		HostVLAN:      &hostVLAN,
		Ports: []networkModels.NetworkPort{{
			Name: portName,
			VLANPolicy: bridgevlan.PortPolicy{
				Mode:         bridgevlan.ModeAccess,
				UntaggedVLAN: &accessVLAN,
			},
		}},
	}
	setTestStandardSwitchMACSource(&runtimeModel, macSource)
	runtimeModel.BridgeMACObject = &macObject
	if err := createStandardBridge(runtimeModel); err != nil {
		t.Fatalf("create filtered host VLAN orphan: %v", err)
	}
	hostInterface, err := iface.Get(hostName)
	if err != nil {
		t.Fatalf("expected orphan host VLAN interface: %v", err)
	}
	if !managedStandardSwitchHostVLAN(hostInterface) {
		t.Fatalf("orphan host VLAN %s is not managed: %#v", hostName, hostInterface.Groups)
	}

	stubSyncFunctions(t, syncStubSet{
		inspectBridgeVLAN: func(name string) (bridgevlan.BridgeState, error) {
			return bridgevlan.InspectBridge(name)
		},
		createBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not create a new bridge")
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not delete the bridge")
		},
	})

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:       1500,
			Ports:     []string{portName},
			MACSource: macSource,
			Manual:    networkModels.StandardSwitchManualAddresses{Network4: "198.18.241.2/24"},
			VLANConfig: networkModels.StandardSwitchVLANConfig{
				Filtering: true,
				HostVLAN:  &hostVLAN,
				PortPolicies: map[string]bridgevlan.PortPolicy{
					portName: {Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("adopt filtered host VLAN orphan: %v", err)
	}
	if _, err := iface.Get(hostName); err != nil {
		t.Fatalf("adopted host VLAN interface disappeared: %v", err)
	}
	var persisted networkModels.StandardSwitch
	if err := db.First(&persisted, id).Error; err != nil {
		t.Fatalf("load adopted filtered switch: %v", err)
	}
	if !persisted.VLANFiltering || persisted.HostVLAN == nil || *persisted.HostVLAN != hostVLAN {
		t.Fatalf("adopted filtered switch = %#v", persisted)
	}
}

func TestIntegrationStandardSwitchAdoptionPersistenceFailureKeepsBridge(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-keep")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)
	macObject := loadMACObjectForIntegration(t, svc, macSource)
	if err := createStandardBridge(orphanModel(name, bridgeName, portName, macSource, macObject)); err != nil {
		t.Fatalf("create orphan runtime bridge: %v", err)
	}

	forced := errors.New("forced adoption persistence failure")
	if err := db.Callback().Create().Before("gorm:create").Register("integration_force_adopt_failure", func(tx *gorm.DB) {
		tx.AddError(forced)
	}); err != nil {
		t.Fatalf("register forced failure callback: %v", err)
	}

	_, err = svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
			Manual:      networkModels.StandardSwitchManualAddresses{Network4: "198.18.240.2/24"},
		},
	})
	if err == nil {
		t.Fatal("expected adoption persistence failure")
	}
	if _, inspectErr := iface.Get(bridgeName); inspectErr != nil {
		t.Fatalf("adopted bridge was destroyed after a failed persistence: %v", inspectErr)
	}
}

func TestIntegrationStandardSwitchAdoptsRawLegacyMemberAndConverges(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-legacy-raw")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	vlanName := fmt.Sprintf("%s.10", portName)
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", vlanName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	createAdoptionTestBridge(t, bridgeName)
	if _, err := utils.RunCommand("/sbin/ifconfig", bridgeName, "addm", portName, "up"); err != nil {
		t.Fatalf("attach raw legacy member: %v", err)
	}

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	for _, statement := range []string{
		`CREATE TABLE dhcp_standard_switches (standard_switch_id integer)`,
		`CREATE TABLE dhcp_ranges (standard_switch_id integer)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create switch usage table: %v", err)
		}
	}
	macSource := createTestStandardSwitchMACSource(t, svc)

	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			VLAN:        10,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if err != nil {
		t.Fatalf("adopt raw legacy member: %v", err)
	}

	bridge, err := iface.Get(bridgeName)
	if err != nil {
		t.Fatalf("inspect adopted bridge: %v", err)
	}
	memberNames := make([]string, 0, len(bridge.BridgeMembers))
	for _, member := range bridge.BridgeMembers {
		memberNames = append(memberNames, member.Name)
	}
	if len(memberNames) != 1 || memberNames[0] != vlanName {
		t.Fatalf("adopted bridge members = %v, want [%s]", memberNames, vlanName)
	}
	if _, err := iface.Get(vlanName); err != nil {
		t.Fatalf("derived VLAN member missing after adoption: %v", err)
	}

	if err := svc.DeleteStandardSwitch(id); err != nil {
		t.Fatalf("delete adopted legacy switch: %v", err)
	}
	for _, candidate := range []string{bridgeName, vlanName} {
		if _, err := iface.Get(candidate); err == nil || !isInterfaceMissingError(err) {
			t.Fatalf("interface %s survived delete: %v", candidate, err)
		}
	}
}

func TestIntegrationStandardSwitchAdoptionRejectsForeignLegacyVLANDerivedInterface(t *testing.T) {
	requireStandardSwitchAdoptionIntegration(t)

	name := uniqueAdoptionName("adopt-legacy-negative")
	bridgeName := adoptionBridgeName(name)
	if _, err := iface.Get(bridgeName); err == nil || !isInterfaceMissingError(err) {
		t.Fatalf("integration bridge name %s is unavailable: %v", bridgeName, err)
	}

	portOutput, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Fatalf("create integration epair: %v", err)
	}
	portName := strings.TrimSpace(portOutput)
	if portName == "" {
		t.Fatal("create integration epair returned an empty name")
	}
	vlanName := fmt.Sprintf("%s.10", portName)
	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", vlanName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", bridgeName, "destroy")
		_, _ = utils.RunCommand("/sbin/ifconfig", portName, "destroy")
	})

	if _, err := utils.RunCommand(
		"/sbin/ifconfig", "vlan", "create",
		"vlandev", portName,
		"vlan", "10",
		"descr", "sylve-adoption-negative",
		"name", vlanName,
		"group", "svm-vlan",
		"up",
	); err != nil {
		t.Fatalf("create derived VLAN interface: %v", err)
	}
	if _, err := utils.RunCommand("/sbin/ifconfig", vlanName, "inet", "203.0.113.9/24", "up"); err != nil {
		t.Fatalf("assign foreign address to derived VLAN: %v", err)
	}
	createAdoptionTestBridge(t, bridgeName)

	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
	)
	macSource := createTestStandardSwitchMACSource(t, svc)

	stubSyncFunctions(t, syncStubSet{
		inspectBridgeVLAN: func(name string) (bridgevlan.BridgeState, error) {
			return bridgevlan.InspectBridge(name)
		},
		createBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not create a new bridge")
		},
		deleteBridge: func(networkModels.StandardSwitch) error {
			return errors.New("adoption must not delete the bridge")
		},
	})

	_, err = svc.NewStandardSwitch(CreateStandardSwitchRequest{
		Name: name,
		StandardSwitchConfig: StandardSwitchConfig{
			MTU:         1500,
			VLAN:        10,
			Ports:       []string{portName},
			MACSource:   macSource,
			DisableIPv6: true,
		},
	})
	if !errors.Is(err, ErrStandardSwitchConflict) || StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
		t.Fatalf("foreign legacy VLAN adoption error = %v", err)
	}

	if _, err := iface.Get(bridgeName); err != nil {
		t.Fatalf("rejected adoption destroyed the bridge: %v", err)
	}
	vlanInterface, err := iface.Get(vlanName)
	if err != nil {
		t.Fatalf("rejected adoption destroyed the derived VLAN: %v", err)
	}
	if len(vlanInterface.IPv4) != 1 || vlanInterface.IPv4[0].IP.String() != "203.0.113.9" {
		t.Fatalf("rejected adoption mutated the foreign derived VLAN: %#v", vlanInterface.IPv4)
	}
	var count int64
	if err := db.Model(&networkModels.StandardSwitch{}).Count(&count).Error; err != nil {
		t.Fatalf("count switches: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected adoption persisted %d rows", count)
	}
}
