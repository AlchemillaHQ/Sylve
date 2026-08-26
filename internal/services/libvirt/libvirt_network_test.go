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
	"github.com/alchemillahq/sylve/internal/testutil"

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
