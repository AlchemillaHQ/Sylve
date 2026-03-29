// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func wolSeedMACObject(t *testing.T, db *gorm.DB, name string, mac string) uint {
	t.Helper()

	obj := networkModels.Object{Name: name, Type: "Mac"}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed MAC object: %v", err)
	}

	entry := networkModels.ObjectEntry{ObjectID: obj.ID, Value: mac}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("failed to seed MAC object entry: %v", err)
	}

	return obj.ID
}

func wolSeedVMWithMACObject(t *testing.T, db *gorm.DB, rid uint, name string, wol bool, macObjID uint) vmModels.VM {
	t.Helper()

	vm := vmModels.VM{Name: name, RID: rid, WoL: wol}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID:  vm.ID,
		MacID: &macObjID,
	}).Error; err != nil {
		t.Fatalf("failed to seed VM network: %v", err)
	}

	return vm
}

func wolSeedVMWithDirectMAC(t *testing.T, db *gorm.DB, rid uint, name string, wol bool, mac string) vmModels.VM {
	t.Helper()

	vm := vmModels.VM{Name: name, RID: rid, WoL: wol}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID: vm.ID,
		MAC:  mac,
	}).Error; err != nil {
		t.Fatalf("failed to seed VM network with direct MAC: %v", err)
	}

	return vm
}

func wolSeedJailWithMAC(t *testing.T, db *gorm.DB, ctid uint, name string, wol bool, macObjID uint) jailModels.Jail {
	t.Helper()

	startAtBoot := false
	resourceLimits := true
	jail := jailModels.Jail{
		Name:           name,
		CTID:           ctid,
		Type:           jailModels.JailTypeFreeBSD,
		StartAtBoot:    &startAtBoot,
		ResourceLimits: &resourceLimits,
		WoL:            wol,
	}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	if err := db.Create(&jailModels.Network{
		JailID: jail.ID,
		Name:   "net0",
		MacID:  &macObjID,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail network: %v", err)
	}

	return jail
}

func newWoLTaskTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.WoL{},
		&vmModels.VM{},
		&vmModels.Network{},
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)

	return &Service{DB: db}, db
}

func TestFindVMsByMac_ResolvesFromDirectNetworkMAC(t *testing.T) {
	svc, db := newWoLTaskTestService(t)

	vm := wolSeedVMWithDirectMAC(t, db, 801, "vm-direct", true, "f6:94:a4:b3:aa:77")

	got, err := svc.findVMsByMac("F6:94:A4:B3:AA:77")
	if err != nil {
		t.Fatalf("findVMsByMac returned error: %v", err)
	}

	if len(got) != 1 || got[0].ID != vm.ID {
		t.Fatalf("findVMsByMac returned %+v, want VM ID %d", got, vm.ID)
	}
}

func TestFindJailsByMac_ResolvesFromMACObjectEntry(t *testing.T) {
	svc, db := newWoLTaskTestService(t)

	macID := wolSeedMACObject(t, db, "jail-mac", "f6:94:a4:b3:aa:78")
	jail := wolSeedJailWithMAC(t, db, 901, "jail-a", true, macID)

	got, err := svc.findJailsByMac("F6:94:A4:B3:AA:78")
	if err != nil {
		t.Fatalf("findJailsByMac returned error: %v", err)
	}

	if len(got) != 1 || got[0].ID != jail.ID {
		t.Fatalf("findJailsByMac returned %+v, want jail ID %d", got, jail.ID)
	}
}

func TestProcessWolTask_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(t *testing.T, db *gorm.DB) (string, *uint, *uint)
		vmStartErr     error
		jailStartErr   error
		wantStatus     string
		wantVMStarts   int
		wantJailStarts int
	}{
		{
			name: "vm-only MAC object match enabled -> completed",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:21"
				macID := wolSeedMACObject(t, db, "vm-a-mac", mac)
				vm := wolSeedVMWithMACObject(t, db, 301, "vm-a", true, macID)
				return mac, &vm.RID, nil
			},
			wantStatus:     "completed",
			wantVMStarts:   1,
			wantJailStarts: 0,
		},
		{
			name: "vm-only direct network MAC match enabled -> completed",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:2a"
				vm := wolSeedVMWithDirectMAC(t, db, 302, "vm-direct", true, mac)
				return mac, &vm.RID, nil
			},
			wantStatus:     "completed",
			wantVMStarts:   1,
			wantJailStarts: 0,
		},
		{
			name: "jail-only match enabled -> completed",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:22"
				macID := wolSeedMACObject(t, db, "jail-a-mac", mac)
				jail := wolSeedJailWithMAC(t, db, 401, "jail-a", true, macID)
				return mac, nil, &jail.CTID
			},
			wantStatus:     "completed",
			wantVMStarts:   0,
			wantJailStarts: 1,
		},
		{
			name: "matched guests but all wol disabled -> wol_disabled",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:23"
				vmMacID := wolSeedMACObject(t, db, "vm-disabled-mac", mac)
				jailMacID := wolSeedMACObject(t, db, "jail-disabled-mac", mac)
				vm := wolSeedVMWithMACObject(t, db, 303, "vm-disabled", false, vmMacID)
				jail := wolSeedJailWithMAC(t, db, 402, "jail-disabled", false, jailMacID)
				return mac, &vm.RID, &jail.CTID
			},
			wantStatus:     "wol_disabled",
			wantVMStarts:   0,
			wantJailStarts: 0,
		},
		{
			name: "no match -> guest_not_found",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				return "f6:94:a4:b3:aa:24", nil, nil
			},
			wantStatus:     "guest_not_found",
			wantVMStarts:   0,
			wantJailStarts: 0,
		},
		{
			name: "multiple eligible guests -> ambiguous_mac",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:25"
				vmMacID := wolSeedMACObject(t, db, "vm-ambiguous-mac", mac)
				jailMacID := wolSeedMACObject(t, db, "jail-ambiguous-mac", mac)
				vm := wolSeedVMWithMACObject(t, db, 304, "vm-ambiguous", true, vmMacID)
				jail := wolSeedJailWithMAC(t, db, 403, "jail-ambiguous", true, jailMacID)
				return mac, &vm.RID, &jail.CTID
			},
			wantStatus:     "ambiguous_mac",
			wantVMStarts:   0,
			wantJailStarts: 0,
		},
		{
			name: "vm start failure -> failed_to_start_vm:*",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:26"
				macID := wolSeedMACObject(t, db, "vm-fail-mac", mac)
				vm := wolSeedVMWithMACObject(t, db, 305, "vm-fail", true, macID)
				return mac, &vm.RID, nil
			},
			vmStartErr:     errors.New("boom-vm"),
			wantStatus:     "failed_to_start_vm:",
			wantVMStarts:   1,
			wantJailStarts: 0,
		},
		{
			name: "jail start failure -> failed_to_start_jail:*",
			setup: func(t *testing.T, db *gorm.DB) (string, *uint, *uint) {
				mac := "f6:94:a4:b3:aa:27"
				macID := wolSeedMACObject(t, db, "jail-fail-mac", mac)
				jail := wolSeedJailWithMAC(t, db, 404, "jail-fail", true, macID)
				return mac, nil, &jail.CTID
			},
			jailStartErr:   errors.New("boom-jail"),
			wantStatus:     "failed_to_start_jail:",
			wantVMStarts:   0,
			wantJailStarts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, db := newWoLTaskTestService(t)
			mac, wantVMRID, wantJailCTID := tt.setup(t, db)

			vmStartCalls := 0
			jailStartCalls := 0

			svc.wolStartVMFn = func(vm vmModels.VM) error {
				vmStartCalls++
				if wantVMRID != nil && vm.RID != *wantVMRID {
					t.Fatalf("wolStartVMFn got VM RID %d, want %d", vm.RID, *wantVMRID)
				}
				return tt.vmStartErr
			}
			svc.wolStartJailFn = func(ctid int) error {
				jailStartCalls++
				if wantJailCTID != nil && uint(ctid) != *wantJailCTID {
					t.Fatalf("wolStartJailFn got jail CTID %d, want %d", ctid, *wantJailCTID)
				}
				return tt.jailStartErr
			}

			status := svc.processWolTask(utilitiesModels.WoL{Mac: mac, Status: "pending"})

			if strings.HasSuffix(tt.wantStatus, ":") {
				if !strings.HasPrefix(status, tt.wantStatus) {
					t.Fatalf("processWolTask() status = %q, want prefix %q", status, tt.wantStatus)
				}
			} else if status != tt.wantStatus {
				t.Fatalf("processWolTask() status = %q, want %q", status, tt.wantStatus)
			}

			if vmStartCalls != tt.wantVMStarts {
				t.Fatalf("VM start calls = %d, want %d", vmStartCalls, tt.wantVMStarts)
			}
			if jailStartCalls != tt.wantJailStarts {
				t.Fatalf("jail start calls = %d, want %d", jailStartCalls, tt.wantJailStarts)
			}
		})
	}
}

func TestProcessQueuedWolTask_UpdatesStatus(t *testing.T) {
	svc, db := newWoLTaskTestService(t)

	mac := "f6:94:a4:b3:aa:88"
	macID := wolSeedMACObject(t, db, "vm-queue-mac", mac)
	wolSeedVMWithMACObject(t, db, 901, "vm-queue", true, macID)

	svc.wolStartVMFn = func(vm vmModels.VM) error { return nil }
	svc.wolStartJailFn = func(ctid int) error { return nil }

	wol := utilitiesModels.WoL{Mac: mac, Status: "pending"}
	if err := db.Create(&wol).Error; err != nil {
		t.Fatalf("failed to seed wol row: %v", err)
	}

	if err := svc.processQueuedWolTask(context.Background(), wolProcessPayload{WoLID: wol.ID}); err != nil {
		t.Fatalf("processQueuedWolTask returned error: %v", err)
	}

	var refreshed utilitiesModels.WoL
	if err := db.First(&refreshed, wol.ID).Error; err != nil {
		t.Fatalf("failed to reload wol row: %v", err)
	}

	if refreshed.Status != "completed" {
		t.Fatalf("wol status = %q, want completed", refreshed.Status)
	}
}
