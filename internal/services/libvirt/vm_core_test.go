// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"gorm.io/gorm"
)

func TestGetVMByRIDDoesNotConfuseRIDWithDatabaseID(t *testing.T) {
	db := newVMDeleteTestDB(t)
	target := vmModels.VM{ID: 44, RID: 910, Name: "rid-target"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target VM: %v", err)
	}
	if err := db.Create(&vmModels.VM{ID: 910, RID: 911, Name: "database-id-decoy"}).Error; err != nil {
		t.Fatalf("seed decoy VM: %v", err)
	}
	if err := db.Create(&vmModels.VMCPUPinning{VMID: target.ID, HostSocket: 1, HostCPU: []int{2}}).Error; err != nil {
		t.Fatalf("seed CPU pinning: %v", err)
	}

	service := &Service{DB: db}
	vm, err := service.GetVMByRID(target.RID)
	if err != nil {
		t.Fatalf("GetVMByRID: %v", err)
	}
	if vm.ID != target.ID || vm.RID != target.RID || vm.Name != target.Name {
		t.Fatalf("VM identity = ID:%d RID:%d name:%q", vm.ID, vm.RID, vm.Name)
	}
	if len(vm.CPUPinning) != 1 || vm.CPUPinning[0].HostSocket != 1 {
		t.Fatalf("CPU pinning = %+v", vm.CPUPinning)
	}
}

func TestGetVMByRIDReturnsRecordNotFoundForMissingRID(t *testing.T) {
	service := &Service{DB: newVMDeleteTestDB(t)}
	_, err := service.GetVMByRID(999)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func TestGetSimpleVMByRIDUsesStableNotFoundErrors(t *testing.T) {
	t.Run("virtualization disabled", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		if err := db.AutoMigrate(&models.BasicSettings{}); err != nil {
			t.Fatalf("migrate basic settings: %v", err)
		}
		if err := db.Create(&vmModels.VM{RID: 913, Name: "disabled-vm"}).Error; err != nil {
			t.Fatalf("seed VM: %v", err)
		}

		_, err := (&Service{DB: db}).GetSimpleVMByRID(913)
		if err == nil || !strings.Contains(err.Error(), "vm_not_found: 913") {
			t.Fatalf("error = %v, want vm_not_found", err)
		}
	})

	t.Run("RID missing", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		if err := db.AutoMigrate(&models.BasicSettings{}); err != nil {
			t.Fatalf("migrate basic settings: %v", err)
		}
		if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.Virtualization}}).Error; err != nil {
			t.Fatalf("enable virtualization: %v", err)
		}

		_, err := (&Service{DB: db}).GetSimpleVMByRID(914)
		if err == nil || !strings.Contains(err.Error(), "vm_not_found: 914") {
			t.Fatalf("error = %v, want vm_not_found", err)
		}
	})
}

func TestUpdateDescriptionAcceptsEmptyAndRejectsOversizedValue(t *testing.T) {
	requireSystemUUIDOrSkip(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := newVMDeleteTestDB(t)
	if err := db.AutoMigrate(&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{}); err != nil {
		t.Fatalf("migrate replication ownership schema: %v", err)
	}
	vm := vmModels.VM{RID: 912, Name: "description-vm", Description: "original"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	service := &Service{DB: db}

	if err := service.UpdateDescription(vm.RID, ""); err != nil {
		t.Fatalf("clear description: %v", err)
	}
	var refreshed vmModels.VM
	if err := db.Where("rid = ?", vm.RID).First(&refreshed).Error; err != nil {
		t.Fatalf("reload VM: %v", err)
	}
	if refreshed.Description != "" {
		t.Fatalf("description = %q, want empty", refreshed.Description)
	}

	oversized := strings.Repeat("x", 1025)
	if err := service.UpdateDescription(vm.RID, oversized); err == nil || !strings.Contains(err.Error(), "invalid_description") {
		t.Fatalf("oversized description error = %v", err)
	}
	if err := db.Where("rid = ?", vm.RID).First(&refreshed).Error; err != nil {
		t.Fatalf("reload VM after rejected update: %v", err)
	}
	if refreshed.Description != "" {
		t.Fatalf("description changed after rejected update: %q", refreshed.Description)
	}
}

func TestUpdateVMDescriptionRowDoesNotRestoreDeletedIdentity(t *testing.T) {
	db := newVMDeleteTestDB(t)
	service := &Service{DB: db}
	deleted := vmModels.VM{ID: 120, RID: 915, Name: "deleted", Description: "original"}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("seed deleted VM: %v", err)
	}
	if err := db.Delete(&vmModels.VM{}, deleted.ID).Error; err != nil {
		t.Fatalf("delete VM: %v", err)
	}

	err := service.updateVMDescriptionRow(deleted.ID, deleted.RID, "stale update")
	if err == nil || !strings.Contains(err.Error(), "vm_not_found") {
		t.Fatalf("stale update error = %v", err)
	}
	var count int64
	if err := db.Model(&vmModels.VM{}).Where("rid = ?", deleted.RID).Count(&count).Error; err != nil {
		t.Fatalf("count deleted VM: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleted VM was restored: count=%d", count)
	}
}

func TestUpdateVMDescriptionRowDoesNotTouchReplacementIdentity(t *testing.T) {
	db := newVMDeleteTestDB(t)
	service := &Service{DB: db}
	replacement := vmModels.VM{ID: 122, RID: 916, Name: "replacement", Description: "replacement description"}
	if err := db.Create(&replacement).Error; err != nil {
		t.Fatalf("seed replacement VM: %v", err)
	}

	err := service.updateVMDescriptionRow(121, replacement.RID, "stale update")
	if err == nil || !strings.Contains(err.Error(), "vm_not_found") {
		t.Fatalf("stale replacement update error = %v", err)
	}
	var refreshed vmModels.VM
	if err := db.First(&refreshed, replacement.ID).Error; err != nil {
		t.Fatalf("reload replacement VM: %v", err)
	}
	if refreshed.Description != replacement.Description {
		t.Fatalf("replacement description = %q, want %q", refreshed.Description, replacement.Description)
	}
}
