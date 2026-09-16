// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"errors"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"

	"gorm.io/gorm"
)

func newVMNetworkMutationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&jailModels.Network{},
	)
}

func seedVMNetworkMutationBase(
	t *testing.T,
	db *gorm.DB,
	rid uint,
	name string,
) (vmModels.VM, networkModels.StandardSwitch) {
	t.Helper()
	vm := vmModels.VM{Name: name, RID: rid}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	sw := networkModels.StandardSwitch{
		Name:       name + "-switch",
		BridgeName: name + "-bridge",
		MTU:        1500,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	return vm, sw
}

func seedVMNetworkMACObject(
	t *testing.T,
	db *gorm.DB,
	name string,
	mac string,
) networkModels.Object {
	t.Helper()
	object := networkModels.Object{Name: name, Type: "Mac"}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("failed to seed MAC object: %v", err)
	}
	entry := networkModels.ObjectEntry{ObjectID: object.ID, Value: mac}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("failed to seed MAC entry: %v", err)
	}
	object.Entries = []networkModels.ObjectEntry{entry}
	return object
}

func seedVMNetworkAttachment(
	t *testing.T,
	db *gorm.DB,
	vmID uint,
	sw networkModels.StandardSwitch,
	macObject *networkModels.Object,
	enabled bool,
) vmModels.Network {
	t.Helper()
	network := vmModels.Network{
		VMID:       vmID,
		SwitchID:   sw.ID,
		SwitchType: "standard",
		Emulation:  "virtio",
		Enable:     enabled,
	}
	if macObject != nil {
		macID := macObject.ID
		network.MacID = &macID
	}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("failed to seed VM network: %v", err)
	}
	return network
}

func networkRowCount[T any](t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(new(T)).Count(&count).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	return count
}

func noOpVMNetworkHooks() networkRuntimeHooks {
	return networkRuntimeHooks{
		syncVMNetworks: func(context.Context, *gorm.DB, uint) error { return nil },
	}
}

func TestNetworkAttachRejectsMACUsedByVMOrJail(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *gorm.DB, vmModels.VM, networkModels.StandardSwitch, networkModels.Object)
	}{
		{
			name: "another VM attachment",
			seed: func(t *testing.T, db *gorm.DB, _ vmModels.VM, sw networkModels.StandardSwitch, mac networkModels.Object) {
				otherVM := vmModels.VM{Name: "other-vm", RID: 202}
				if err := db.Create(&otherVM).Error; err != nil {
					t.Fatalf("failed to seed other VM: %v", err)
				}
				seedVMNetworkAttachment(t, db, otherVM.ID, sw, &mac, true)
			},
		},
		{
			name: "jail attachment",
			seed: func(t *testing.T, db *gorm.DB, _ vmModels.VM, sw networkModels.StandardSwitch, mac networkModels.Object) {
				macID := mac.ID
				jailNetwork := jailModels.Network{
					JailID:     77,
					Name:       "vnet0",
					SwitchID:   sw.ID,
					SwitchType: "standard",
					MacID:      &macID,
				}
				if err := db.Create(&jailNetwork).Error; err != nil {
					t.Fatalf("failed to seed jail network: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newVMNetworkMutationTestDB(t)
			vm, sw := seedVMNetworkMutationBase(t, db, 101, "target-vm")
			mac := seedVMNetworkMACObject(t, db, "selected-mac", "02:00:00:00:00:11")
			test.seed(t, db, vm, sw, mac)
			before := networkRowCount[vmModels.Network](t, db)

			service := &Service{DB: db}
			_, err := service.networkAttachApply(
				context.Background(),
				libvirtServiceInterfaces.NetworkAttachRequest{
					RID:        vm.RID,
					SwitchName: sw.Name,
					Emulation:  "virtio",
					MacID:      &mac.ID,
				},
				vm,
				noOpVMNetworkHooks(),
			)
			if err == nil || !strings.Contains(err.Error(), "mac_address_already_in_use") {
				t.Fatalf("expected MAC usage conflict, got %v", err)
			}
			if got := networkRowCount[vmModels.Network](t, db); got != before {
				t.Fatalf("attachment count changed after conflict: got %d want %d", got, before)
			}
		})
	}
}

func TestNetworkAttachRollsBackAutoMACAndNetworkWhenSyncFails(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "rollback-vm")
	service := &Service{DB: db}
	hookCalled := false

	_, err := service.networkAttachApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkAttachRequest{
			RID:        vm.RID,
			SwitchName: sw.Name,
			Emulation:  "virtio",
		},
		vm,
		networkRuntimeHooks{syncVMNetworks: func(_ context.Context, tx *gorm.DB, _ uint) error {
			hookCalled = true
			if got := networkRowCount[vmModels.Network](t, tx); got != 1 {
				t.Fatalf("sync hook saw %d networks, want 1", got)
			}
			return errors.New("define_failed")
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_networks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if !hookCalled {
		t.Fatal("expected sync hook to run")
	}
	if got := networkRowCount[vmModels.Network](t, db); got != 0 {
		t.Fatalf("network row leaked after rollback: %d", got)
	}
	if got := networkRowCount[networkModels.Object](t, db); got != 0 {
		t.Fatalf("MAC object leaked after rollback: %d", got)
	}
	if got := networkRowCount[networkModels.ObjectEntry](t, db); got != 0 {
		t.Fatalf("MAC entry leaked after rollback: %d", got)
	}
}

func TestNetworkAttachStoresAndReturnsCreatedAttachment(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "attach-vm")
	service := &Service{DB: db}

	network, err := service.networkAttachApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkAttachRequest{
			RID:        vm.RID,
			SwitchName: sw.Name,
			Emulation:  "e1000",
		},
		vm,
		noOpVMNetworkHooks(),
	)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if network == nil || network.ID == 0 || network.VMID != vm.ID || !network.Enable {
		t.Fatalf("unexpected created network: %+v", network)
	}
	if network.MacID == nil || network.AddressObj == nil || len(network.AddressObj.Entries) != 1 {
		t.Fatalf("created network did not include its MAC object: %+v", network)
	}
	if network.StandardSwitch == nil || network.StandardSwitch.ID != sw.ID {
		t.Fatalf("created network did not include its switch: %+v", network.StandardSwitch)
	}
}

func TestNetworkUpdateSynchronizesRequestedEnableState(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "state-vm")
	mac := seedVMNetworkMACObject(t, db, "state-mac", "02:00:00:00:00:21")
	network := seedVMNetworkAttachment(t, db, vm.ID, sw, &mac, true)
	service := &Service{DB: db}

	for _, enabled := range []bool{false, true} {
		requested := enabled
		hookSawState := !enabled
		updated, err := service.networkUpdateApply(
			context.Background(),
			libvirtServiceInterfaces.NetworkUpdateRequest{
				RID:       vm.RID,
				NetworkID: network.ID,
				Enable:    &requested,
			},
			vm,
			networkRuntimeHooks{syncVMNetworks: func(_ context.Context, tx *gorm.DB, _ uint) error {
				var current vmModels.Network
				if err := tx.Session(&gorm.Session{SkipHooks: true}).First(&current, network.ID).Error; err != nil {
					return err
				}
				hookSawState = current.Enable
				return nil
			}},
		)
		if err != nil {
			t.Fatalf("update enable=%t failed: %v", enabled, err)
		}
		if hookSawState != enabled {
			t.Fatalf("sync hook saw enable=%t, want %t", hookSawState, enabled)
		}
		if updated == nil || updated.Enable != enabled {
			t.Fatalf("response enable=%v, want %t", updated, enabled)
		}
	}
}

func TestNetworkUpdateDisabledNICUsesPersistedManualSwitchIdentity(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, standard := seedVMNetworkMutationBase(t, db, 101, "disabled-manual-vm")
	manual := networkModels.ManualSwitch{
		Name: "unavailable-manual", Bridge: "bridge-unavailable",
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}
	mac := seedVMNetworkMACObject(t, db, "disabled-manual-mac", "02:00:00:00:00:22")
	network := seedVMNetworkAttachment(t, db, vm.ID, standard, &mac, true)

	originalInspect := inspectVMBridgeVLAN
	inspectCalls := 0
	inspectVMBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		inspectCalls++
		return bridgevlan.BridgeState{}, errors.New("bridge is unavailable")
	}
	t.Cleanup(func() { inspectVMBridgeVLAN = originalInspect })

	enabled := false
	updated, err := (&Service{DB: db}).networkUpdateApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkUpdateRequest{
			RID: vm.RID, NetworkID: network.ID, SwitchName: &manual.Name, Enable: &enabled,
		},
		vm,
		noOpVMNetworkHooks(),
	)
	if err != nil {
		t.Fatalf("update disabled NIC to unavailable Manual Switch: %v", err)
	}
	if inspectCalls != 0 {
		t.Fatalf("disabled NIC update inspected Manual Switch runtime %d times", inspectCalls)
	}
	if updated.Enable || updated.SwitchType != "manual" || updated.SwitchID != manual.ID ||
		updated.ManualSwitch == nil || updated.ManualSwitch.ID != manual.ID {
		t.Fatalf("unexpected disabled NIC response: %#v", updated)
	}
}

func TestNetworkUpdateRejectsAttachmentFromAnotherVM(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "owner-vm")
	otherVM := vmModels.VM{Name: "other-vm", RID: 202}
	if err := db.Create(&otherVM).Error; err != nil {
		t.Fatalf("failed to seed other VM: %v", err)
	}
	mac := seedVMNetworkMACObject(t, db, "other-mac", "02:00:00:00:00:31")
	network := seedVMNetworkAttachment(t, db, otherVM.ID, sw, &mac, true)
	service := &Service{DB: db}
	enabled := false

	_, err := service.networkUpdateApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkUpdateRequest{
			RID:       vm.RID,
			NetworkID: network.ID,
			Enable:    &enabled,
		},
		vm,
		noOpVMNetworkHooks(),
	)
	if err == nil || !strings.Contains(err.Error(), "network_not_found") {
		t.Fatalf("expected membership failure, got %v", err)
	}

	var stored vmModels.Network
	if err := db.Session(&gorm.Session{SkipHooks: true}).First(&stored, network.ID).Error; err != nil {
		t.Fatalf("network was removed after rejected update: %v", err)
	}
	if !stored.Enable {
		t.Fatal("network changed after rejected update")
	}
}

func TestNetworkUpdateRollsBackGeneratedMACWhenSyncFails(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "update-rollback-vm")
	oldMAC := seedVMNetworkMACObject(t, db, "old-mac", "02:00:00:00:00:41")
	network := seedVMNetworkAttachment(t, db, vm.ID, sw, &oldMAC, true)
	service := &Service{DB: db}
	generateMAC := uint(0)

	_, err := service.networkUpdateApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkUpdateRequest{
			RID:       vm.RID,
			NetworkID: network.ID,
			MacID:     &generateMAC,
		},
		vm,
		networkRuntimeHooks{syncVMNetworks: func(context.Context, *gorm.DB, uint) error {
			return errors.New("define_failed")
		}},
	)
	if err == nil {
		t.Fatal("expected update sync failure")
	}
	if got := networkRowCount[networkModels.Object](t, db); got != 1 {
		t.Fatalf("generated MAC object leaked after rollback: object count=%d", got)
	}

	var stored vmModels.Network
	if err := db.Session(&gorm.Session{SkipHooks: true}).First(&stored, network.ID).Error; err != nil {
		t.Fatalf("failed to reload network: %v", err)
	}
	if stored.MacID == nil || *stored.MacID != oldMAC.ID {
		t.Fatalf("network MAC changed despite rollback: %+v", stored.MacID)
	}
}

func TestNetworkUpdatePreservesLegacyRawMAC(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "legacy-vm")
	network := seedVMNetworkAttachment(t, db, vm.ID, sw, nil, true)
	if err := db.Model(&vmModels.Network{}).Where("id = ?", network.ID).
		Update("mac", "02:00:00:00:00:51").Error; err != nil {
		t.Fatalf("failed to seed raw MAC: %v", err)
	}
	service := &Service{DB: db}
	enabled := false

	updated, err := service.networkUpdateApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkUpdateRequest{
			RID:       vm.RID,
			NetworkID: network.ID,
			Enable:    &enabled,
		},
		vm,
		noOpVMNetworkHooks(),
	)
	if err != nil {
		t.Fatalf("legacy raw-MAC update failed: %v", err)
	}
	if updated.MacID != nil || updated.MAC != "02:00:00:00:00:51" || updated.Enable {
		t.Fatalf("legacy raw MAC was not preserved: %+v", updated)
	}
}

func TestNetworkDetachRollsBackOnSyncFailureAndRetainsMACObject(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "detach-rollback-vm")
	mac := seedVMNetworkMACObject(t, db, "detach-rollback-mac", "02:00:00:00:00:61")
	network := seedVMNetworkAttachment(t, db, vm.ID, sw, &mac, true)
	service := &Service{DB: db}
	req := libvirtServiceInterfaces.NetworkDetachRequest{RID: vm.RID, NetworkID: network.ID}

	err := service.networkDetachApply(
		context.Background(),
		req,
		vm.ID,
		networkRuntimeHooks{syncVMNetworks: func(context.Context, *gorm.DB, uint) error {
			return errors.New("define_failed")
		}},
	)
	if err == nil {
		t.Fatal("expected detach sync failure")
	}
	if got := networkRowCount[vmModels.Network](t, db); got != 1 {
		t.Fatalf("network detach was not rolled back: %d rows", got)
	}
	if got := networkRowCount[networkModels.Object](t, db); got != 1 {
		t.Fatalf("MAC object changed during failed detach: %d rows", got)
	}
}

func TestNetworkDetachDeletesOnlyAttachment(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "detach-vm")
	mac := seedVMNetworkMACObject(t, db, "detach-mac", "02:00:00:00:00:71")
	network := seedVMNetworkAttachment(t, db, vm.ID, sw, &mac, true)
	service := &Service{DB: db}

	err := service.networkDetachApply(
		context.Background(),
		libvirtServiceInterfaces.NetworkDetachRequest{RID: vm.RID, NetworkID: network.ID},
		vm.ID,
		noOpVMNetworkHooks(),
	)
	if err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	if got := networkRowCount[vmModels.Network](t, db); got != 0 {
		t.Fatalf("attachment was not deleted: %d rows", got)
	}
	if got := networkRowCount[networkModels.Object](t, db); got != 1 {
		t.Fatalf("detach deleted MAC object: %d rows", got)
	}
	if got := networkRowCount[networkModels.ObjectEntry](t, db); got != 1 {
		t.Fatalf("detach deleted MAC entry: %d rows", got)
	}
}
func TestFilteredSwitchVMCompatibility(t *testing.T) {
	defaultVLAN := 42
	filtered := networkModels.StandardSwitch{
		Name:              "filtered",
		BridgeName:        "bridge-filtered",
		VLANFiltering:     true,
		DefaultAccessVLAN: &defaultVLAN,
	}
	sw := networkAttachment.ResolvedSwitch{
		Name: filtered.Name, Type: "standard", Bridge: filtered.BridgeName,
		VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN, Standard: &filtered,
	}

	originalValidate := validateVMFilteredBridge
	t.Cleanup(func() { validateVMFilteredBridge = originalValidate })
	called := false
	validateVMFilteredBridge = func(bridge string, expected *int) (bridgevlan.BridgeState, error) {
		called = true
		if bridge != filtered.BridgeName || expected == nil || *expected != defaultVLAN {
			t.Fatalf("unexpected live validation: bridge=%q default=%v", bridge, expected)
		}
		return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
	}

	if err := validateDesiredVMNetworkSwitchCompatibility(sw); err != nil {
		t.Fatalf("stored filtered switch rejected: %v", err)
	}
	if called {
		t.Fatal("database-only validation inspected live bridge state")
	}
	if err := validateEffectiveVMNetworkSwitchCompatibility(sw); err != nil {
		t.Fatalf("live filtered switch rejected: %v", err)
	}
	if !called {
		t.Fatal("live validation did not inspect bridge state")
	}

	filtered.DefaultAccessVLAN = nil
	sw.DefaultAccessVLAN = nil
	if err := validateDesiredVMNetworkSwitchCompatibility(sw); err == nil ||
		!strings.Contains(err.Error(), "filtered_switch_vm_default_access_vlan_required") {
		t.Fatalf("expected missing-default rejection, got %v", err)
	}
}

func TestValidateVMNetworksForStartRejectsFilteredBridgeDrift(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "start-vm")
	defaultVLAN := 77
	if err := db.Model(&networkModels.StandardSwitch{}).Where("id = ?", sw.ID).Updates(map[string]any{
		"vlan_filtering":      true,
		"default_access_vlan": defaultVLAN,
	}).Error; err != nil {
		t.Fatalf("failed to enable filtering: %v", err)
	}
	seedVMNetworkAttachment(t, db, vm.ID, sw, nil, true)

	originalValidate := validateVMFilteredBridge
	t.Cleanup(func() { validateVMFilteredBridge = originalValidate })
	validateVMFilteredBridge = func(string, *int) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{}, errors.New("default PVID drift")
	}

	err := (&Service{DB: db}).validateVMNetworksForStart(vm.ID)
	if err == nil || !strings.Contains(err.Error(), "filtered_switch_runtime_mismatch") {
		t.Fatalf("expected runtime drift rejection, got %v", err)
	}
}

func TestVMStandardSwitchLifecycleBridgesIncludesEnabledStandardSwitches(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, enabledSwitch := seedVMNetworkMutationBase(t, db, 101, "lifecycle-vm")
	defaultVLAN := 10
	if err := db.Model(&enabledSwitch).Updates(map[string]any{
		"vlan_filtering":      true,
		"default_access_vlan": defaultVLAN,
	}).Error; err != nil {
		t.Fatalf("enable filtering on lifecycle switch: %v", err)
	}
	enabledSwitch.VLANFiltering = true
	enabledSwitch.DefaultAccessVLAN = &defaultVLAN
	unfilteredSwitch := networkModels.StandardSwitch{
		Name: "unfiltered-switch", BridgeName: "unfiltered-bridge", MTU: 1500,
	}
	if err := db.Create(&unfilteredSwitch).Error; err != nil {
		t.Fatalf("seed unfiltered switch: %v", err)
	}
	disabledSwitch := networkModels.StandardSwitch{
		Name: "disabled-switch", BridgeName: "disabled-bridge", MTU: 1500,
		VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&disabledSwitch).Error; err != nil {
		t.Fatalf("seed disabled switch: %v", err)
	}
	manualSwitch := networkModels.ManualSwitch{Name: "manual", Bridge: "manual-bridge"}
	if err := db.Create(&manualSwitch).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}

	seedVMNetworkAttachment(t, db, vm.ID, enabledSwitch, nil, true)
	seedVMNetworkAttachment(t, db, vm.ID, enabledSwitch, nil, true)
	seedVMNetworkAttachment(t, db, vm.ID, unfilteredSwitch, nil, true)
	seedVMNetworkAttachment(t, db, vm.ID, disabledSwitch, nil, false)
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: manualSwitch.ID, SwitchType: "manual", Emulation: "virtio", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed manual network: %v", err)
	}

	bridges, err := (&Service{DB: db}).vmStandardSwitchLifecycleBridges(vm.ID, true)
	if err != nil {
		t.Fatalf("resolve lifecycle bridges: %v", err)
	}
	want := []string{enabledSwitch.BridgeName, unfilteredSwitch.BridgeName}
	if len(bridges) != len(want) || bridges[0] != want[0] || bridges[1] != want[1] {
		t.Fatalf("lifecycle bridges = %v, want %v", bridges, want)
	}
}

func TestVMStartValidationRejectsUnfilteredSwitchRuntimeModeDrift(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 102, "unfiltered-drift-vm")
	seedVMNetworkAttachment(t, db, vm.ID, sw, nil, true)

	originalInspect := inspectVMBridgeVLAN
	inspectVMBridgeVLAN = func(bridge string) (bridgevlan.BridgeState, error) {
		if bridge != sw.BridgeName {
			t.Fatalf("inspected bridge = %q, want %q", bridge, sw.BridgeName)
		}
		return bridgevlan.BridgeState{VLANFiltering: true}, nil
	}
	t.Cleanup(func() { inspectVMBridgeVLAN = originalInspect })

	err := (&Service{DB: db}).validateVMNetworksForStart(vm.ID)
	if err == nil || !strings.Contains(err.Error(), "unfiltered_switch_runtime_vlan_mode_mismatch") {
		t.Fatalf("expected unfiltered runtime mode rejection, got %v", err)
	}
}

func TestCreateVMXMLFilteredPrivateSwitchKeepsPlainBridgeInterface(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm, sw := seedVMNetworkMutationBase(t, db, 101, "xml-vm")
	defaultVLAN := 88
	sw.VLANFiltering = true
	sw.DefaultAccessVLAN = &defaultVLAN
	sw.Private = true
	if err := db.Save(&sw).Error; err != nil {
		t.Fatalf("failed to update filtered switch: %v", err)
	}
	vm.Networks = []vmModels.Network{{
		VMID: vm.ID, SwitchID: sw.ID, SwitchType: "standard", Emulation: "virtio", Enable: true,
	}}

	xmlText, err := (&Service{DB: db}).CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("failed to create VM XML: %v", err)
	}
	if !strings.Contains(xmlText, `source bridge="`+sw.BridgeName+`"`) {
		t.Fatalf("generated XML did not reference filtered bridge: %s", xmlText)
	}
	if strings.Contains(xmlText, "<vlan") {
		t.Fatalf("generated unsupported Libvirt VLAN XML: %s", xmlText)
	}
	if strings.Contains(xmlText, "isolated") || strings.Contains(xmlText, "<port") {
		t.Fatalf("generated unsupported bridge port isolation XML: %s", xmlText)
	}
}

func TestCreateVMXMLValidatesLiveManualSwitchVLANState(t *testing.T) {
	db := newVMNetworkMutationTestDB(t)
	vm := vmModels.VM{Name: "manual-xml-vm", RID: 102}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	manual := networkModels.ManualSwitch{Name: "manual-xml", Bridge: "bridge-manual-xml"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}
	vm.Networks = []vmModels.Network{{
		VMID: vm.ID, SwitchID: manual.ID, SwitchType: "manual", Emulation: "virtio", Enable: true,
	}}

	originalInspect := inspectVMBridgeVLAN
	inspectVMBridgeVLAN = func(bridge string) (bridgevlan.BridgeState, error) {
		if bridge != manual.Bridge {
			t.Fatalf("inspected bridge = %q, want %q", bridge, manual.Bridge)
		}
		return bridgevlan.BridgeState{VLANFiltering: true}, nil
	}
	t.Cleanup(func() { inspectVMBridgeVLAN = originalInspect })

	_, err := (&Service{DB: db}).CreateVmXML(vm, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "filtered_switch_vm_default_access_vlan_required") {
		t.Fatalf("manual filtered switch without a default PVID was accepted: %v", err)
	}
}

func TestVMNetworkAttachmentFromMetadataHasNarrowLegacyFallback(t *testing.T) {
	legacy := vmModels.Network{
		SwitchType: "standard",
		StandardSwitch: &networkModels.StandardSwitch{
			Name: "legacy-lan", BridgeName: "vm-legacy-lan",
		},
	}
	contract, err := VMNetworkAttachmentFromMetadata(legacy)
	if err != nil {
		t.Fatalf("read legacy unfiltered metadata: %v", err)
	}
	if contract.SwitchName != "legacy-lan" || contract.SwitchType != "standard" || contract.VLANFiltering {
		t.Fatalf("unexpected legacy contract: %#v", contract)
	}

	defaultVLAN := 10
	legacy.StandardSwitch.VLANFiltering = true
	legacy.StandardSwitch.DefaultAccessVLAN = &defaultVLAN
	if _, err := VMNetworkAttachmentFromMetadata(legacy); err == nil ||
		!strings.Contains(err.Error(), "legacy_filtered_vm_network_attachment_unsupported") {
		t.Fatalf("legacy metadata inferred filtered semantics: %v", err)
	}

	filtered := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion,
		Kind:    networkAttachment.KindVM, SwitchName: "tenant", SwitchType: "standard",
		VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN,
	}
	legacy.Attachment = &filtered
	contract, err = VMNetworkAttachmentFromMetadata(legacy)
	if err != nil {
		t.Fatalf("read versioned filtered metadata: %v", err)
	}
	if contract.DefaultAccessVLAN == nil || *contract.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("versioned contract was not authoritative: %#v", contract)
	}
}
