// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFindVmByMac_ResolvesFromMACObjectEntry(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	vm := vmModels.VM{Name: "vm-a", RID: 101, WoL: true}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	macObj := networkModels.Object{Name: "vm-a-mac", Type: "Mac"}
	if err := db.Create(&macObj).Error; err != nil {
		t.Fatalf("failed to seed MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{
		ObjectID: macObj.ID,
		Value:    "f6:94:a4:b3:aa:21",
	}).Error; err != nil {
		t.Fatalf("failed to seed MAC object entry: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID:  vm.ID,
		MacID: &macObj.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed VM network: %v", err)
	}

	svc := &Service{DB: db}
	got, err := svc.FindVmByMac("F6:94:A4:B3:AA:21")
	if err != nil {
		t.Fatalf("FindVmByMac returned error: %v", err)
	}

	if got.ID != vm.ID {
		t.Fatalf("FindVmByMac returned VM %d, want %d", got.ID, vm.ID)
	}
}

func TestFindVmByMac_ResolvesFromNetworkMACColumn(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	vm := vmModels.VM{Name: "vm-b", RID: 102, WoL: true}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID: vm.ID,
		MAC:  "f6:94:a4:b3:aa:21",
	}).Error; err != nil {
		t.Fatalf("failed to seed VM network: %v", err)
	}

	svc := &Service{DB: db}
	got, err := svc.FindVmByMac("F6:94:A4:B3:AA:21")
	if err != nil {
		t.Fatalf("FindVmByMac returned error: %v", err)
	}

	if got.ID != vm.ID {
		t.Fatalf("FindVmByMac returned VM %d, want %d", got.ID, vm.ID)
	}
}

func TestFindVmByMac_ReturnsWolDisabled(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	vm := vmModels.VM{Name: "vm-c", RID: 103, WoL: false}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	macObj := networkModels.Object{Name: "vm-c-mac", Type: "Mac"}
	if err := db.Create(&macObj).Error; err != nil {
		t.Fatalf("failed to seed MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{
		ObjectID: macObj.ID,
		Value:    "f6:94:a4:b3:aa:21",
	}).Error; err != nil {
		t.Fatalf("failed to seed MAC object entry: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID:  vm.ID,
		MacID: &macObj.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed VM network: %v", err)
	}

	svc := &Service{DB: db}
	_, err := svc.FindVmByMac("F6:94:A4:B3:AA:21")
	if err == nil {
		t.Fatal("FindVmByMac() expected error for WoL disabled VM, got nil")
	}

	if !strings.Contains(err.Error(), "vm_wol_disabled") {
		t.Fatalf("FindVmByMac() error = %q, want vm_wol_disabled", err.Error())
	}
}

func TestFindJailsByMac_ResolvesFromMACObjectEntry(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	jail := jailModels.Jail{Name: "jail-a", CTID: 201, WoL: true}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	macObj := networkModels.Object{Name: "jail-a-mac", Type: "Mac"}
	if err := db.Create(&macObj).Error; err != nil {
		t.Fatalf("failed to seed MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{
		ObjectID: macObj.ID,
		Value:    "f6:94:a4:b3:aa:21",
	}).Error; err != nil {
		t.Fatalf("failed to seed MAC object entry: %v", err)
	}

	if err := db.Create(&jailModels.Network{
		JailID: jail.ID,
		Name:   "net0",
		MacID:  &macObj.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail network: %v", err)
	}

	svc := &Service{DB: db}
	jails, err := svc.FindJailsByMac("F6:94:A4:B3:AA:21")
	if err != nil {
		t.Fatalf("FindJailsByMac returned error: %v", err)
	}

	if len(jails) != 1 {
		t.Fatalf("FindJailsByMac returned %d jail(s), want 1", len(jails))
	}

	if jails[0].ID != jail.ID {
		t.Fatalf("FindJailsByMac returned jail %d, want %d", jails[0].ID, jail.ID)
	}
}
