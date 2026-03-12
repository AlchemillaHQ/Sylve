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

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newCPUPinValidationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&vmModels.VM{}, &vmModels.VMCPUPinning{}); err != nil {
		t.Fatalf("failed to migrate vm tables: %v", err)
	}

	return db
}

func seedPinnedVM(t *testing.T, db *gorm.DB, rid uint, socket int, cores []int) {
	t.Helper()

	vm := vmModels.VM{
		RID:  rid,
		Name: "seed-vm",
	}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	pin := vmModels.VMCPUPinning{
		VMID:       vm.ID,
		HostSocket: socket,
		HostCPU:    cores,
	}
	if err := db.Create(&pin).Error; err != nil {
		t.Fatalf("failed to seed vm cpu pinning: %v", err)
	}
}

func TestValidateCPUPins_AllowsSameLocalIndexAcrossDifferentSockets(t *testing.T) {
	db := newCPUPinValidationTestDB(t)

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 2,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{10}},
			{Socket: 1, Cores: []int{10}},
		},
	}

	if err := validateCPUPins(db, req, 32, 2, 16); err != nil {
		t.Fatalf("expected pinning to be valid, got error: %v", err)
	}
}

func TestValidateCPUPins_DoesNotConflictAcrossDifferentSocketsForSameLocalCore(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{10})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 1, Cores: []int{10}},
		},
	}

	if err := validateCPUPins(db, req, 32, 2, 16); err != nil {
		t.Fatalf("expected no conflict for different socket/global core, got error: %v", err)
	}
}

func TestValidateCPUPins_ConflictsOnSameGlobalCore(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{10})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{10}},
		},
	}

	err := validateCPUPins(db, req, 32, 2, 16)
	if err == nil {
		t.Fatalf("expected core conflict, got nil")
	}

	if !strings.Contains(err.Error(), "core_conflict: core=10 already_pinned_by_rid=116") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCPUPins_SingleSocketUsesLocalCoreIndices(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{2, 5})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   4,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{0, 1, 3, 4}},
		},
	}

	if err := validateCPUPins(db, req, 8, 1, 8); err != nil {
		t.Fatalf("expected valid single-socket pinning, got error: %v", err)
	}
}

func TestValidateCPUPins_DualSocketScenarioFromProductionPayload(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 121, 0, []int{32, 33, 34, 35, 36, 37, 38, 39})

	rid := uint(107)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   32,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 1, Cores: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39}},
		},
	}

	if err := validateCPUPins(db, req, 80, 2, 40); err != nil {
		t.Fatalf("expected production dual-socket payload to be valid, got error: %v", err)
	}
}
