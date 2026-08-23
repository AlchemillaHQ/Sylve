// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRequireNoManagedGuestsWithinRestore(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
	)

	jail := jailModels.Jail{CTID: 100, Name: "restore-guard-jail"}
	if err := database.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	if err := database.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   "zroot",
		GUID:   "restore-guard-jail-guid",
		Name:   "zroot/sylve/jails/100",
	}).Error; err != nil {
		t.Fatalf("create jail storage: %v", err)
	}

	vm := vmModels.VM{RID: 200, Name: "restore-guard-vm"}
	if err := database.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	dataset := vmModels.VMStorageDataset{
		Pool: "fast",
		Name: "fast/sylve/virtual-machines/200/zvol-1",
		GUID: "restore-guard-vm-guid",
	}
	if err := database.Create(&dataset).Error; err != nil {
		t.Fatalf("create VM dataset: %v", err)
	}
	if err := database.Create(&vmModels.Storage{
		VMID:      vm.ID,
		Type:      vmModels.VMStorageTypeZVol,
		Pool:      "fast",
		Enable:    true,
		DatasetID: &dataset.ID,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}

	service := &Service{DB: database}
	for _, tc := range []struct {
		destination string
		wantGuest   string
	}{
		{destination: "zroot/sylve", wantGuest: "jail:100"},
		{destination: "zroot/sylve/jails/100", wantGuest: "jail:100"},
		{destination: "fast/sylve", wantGuest: "vm:200"},
	} {
		err := service.requireNoManagedGuestsWithinRestore(t.Context(), tc.destination)
		if err == nil || !strings.Contains(err.Error(), tc.wantGuest) {
			t.Fatalf("guard %s error = %v, want %s", tc.destination, err, tc.wantGuest)
		}
	}

	if err := service.requireNoManagedGuestsWithinRestore(t.Context(), "tank/unrelated"); err != nil {
		t.Fatalf("unrelated destination rejected: %v", err)
	}
}

func TestRequireNoManagedGuestsAllowsFreshInventory(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
	)
	service := &Service{DB: database}
	if err := service.requireNoManagedGuestsWithinRestore(t.Context(), "zroot/sylve"); err != nil {
		t.Fatalf("fresh inventory rejected: %v", err)
	}
}

func TestRequireNoManagedGuestsFailsClosedForPartialInventory(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Storage{})
	if err := database.Migrator().DropTable(&jailModels.Storage{}); err != nil {
		t.Fatalf("drop jail storage inventory: %v", err)
	}

	service := &Service{DB: database}
	err := service.requireNoManagedGuestsWithinRestore(t.Context(), "zroot/sylve")
	if err == nil || !strings.Contains(err.Error(), "restore_jail_inventory_unavailable") {
		t.Fatalf("partial inventory did not fail closed: %v", err)
	}
}
