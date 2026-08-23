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
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestForceRemoveVMDBRecords_RemovesRegistrationOnly(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMStorageDataset{},
	)
	svc := &Service{DB: db}

	vm := vmModels.VM{RID: 700, Name: "orphan"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to create vm: %v", err)
	}

	ds := vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/sylve/virtual-machines/700/zvol-1"}
	if err := db.Create(&ds).Error; err != nil {
		t.Fatalf("failed to create storage dataset: %v", err)
	}

	if err := db.Create(&vmModels.Storage{
		VMID:      vm.ID,
		Type:      vmModels.VMStorageTypeZVol,
		Pool:      "zroot",
		DatasetID: &ds.ID,
	}).Error; err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	if err := db.Create(&vmModels.Network{
		VMID:       vm.ID,
		SwitchID:   1,
		SwitchType: "standard",
	}).Error; err != nil {
		t.Fatalf("failed to create network: %v", err)
	}

	warnings := make([]string, 0)
	svc.forceRemoveVMDBRecords(700, false, &warnings)

	var vmCount int64
	if err := db.Model(&vmModels.VM{}).Where("rid = ?", 700).Count(&vmCount).Error; err != nil {
		t.Fatalf("failed to count vms: %v", err)
	}
	if vmCount != 0 {
		t.Fatalf("expected VM record removed, found %d", vmCount)
	}

	var storageCount int64
	if err := db.Model(&vmModels.Storage{}).Where("vm_id = ?", vm.ID).Count(&storageCount).Error; err != nil {
		t.Fatalf("failed to count storages: %v", err)
	}
	if storageCount != 0 {
		t.Fatalf("expected storages removed, found %d", storageCount)
	}

	var networkCount int64
	if err := db.Model(&vmModels.Network{}).Where("vm_id = ?", vm.ID).Count(&networkCount).Error; err != nil {
		t.Fatalf("failed to count networks: %v", err)
	}
	if networkCount != 0 {
		t.Fatalf("expected networks removed, found %d", networkCount)
	}
}

func TestPurgeVMRegistrationReturnsNotFoundBeforeOrphanCheck(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	service := &Service{DB: db, uri: "://"}

	_, err := service.PurgeVMRegistration(701, true)
	if err == nil || !strings.Contains(err.Error(), "vm_not_found") {
		t.Fatalf("purge error = %v, want vm_not_found", err)
	}
}

func TestForceAndPurgeRefuseCleanupWhenOrphanStateCannotBeVerified(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Service, uint) error
	}{
		{
			name: "force delete",
			run: func(service *Service, rid uint) error {
				_, err := service.ForceRemoveVM(rid, true, context.Background())
				return err
			},
		},
		{
			name: "registration purge",
			run: func(service *Service, rid uint) error {
				_, err := service.PurgeVMRegistration(rid, true)
				return err
			},
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
			rid := uint(702 + index)
			if err := db.Create(&vmModels.VM{RID: rid, Name: tt.name}).Error; err != nil {
				t.Fatalf("seed VM: %v", err)
			}
			service := &Service{DB: db, uri: "://"}

			err := tt.run(service, rid)
			if err == nil || !strings.Contains(err.Error(), "vm_orphan_check_unavailable") {
				t.Fatalf("operation error = %v, want vm_orphan_check_unavailable", err)
			}

			var count int64
			if err := db.Model(&vmModels.VM{}).Where("rid = ?", rid).Count(&count).Error; err != nil {
				t.Fatalf("count VM rows: %v", err)
			}
			if count != 1 {
				t.Fatalf("VM registration count = %d, want 1", count)
			}
		})
	}
}
