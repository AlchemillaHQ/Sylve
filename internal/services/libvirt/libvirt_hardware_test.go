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
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"

	"gorm.io/gorm"
)

func TestValidateCPUHardwareRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     libvirtServiceInterfaces.ModifyCPURequest
		wantErr string
	}{
		{
			name:    "negative topology",
			req:     libvirtServiceInterfaces.ModifyCPURequest{CPUSockets: -1, CPUCores: 1, CPUThreads: 1},
			wantErr: "cpu_topology_must_be_positive",
		},
		{
			name: "too many pins",
			req: libvirtServiceInterfaces.ModifyCPURequest{
				CPUSockets: 1,
				CPUCores:   1,
				CPUThreads: 1,
				CPUPinning: []libvirtServiceInterfaces.CPUPinning{{Socket: 0, Cores: []int{0, 1}}},
			},
			wantErr: "cpu_pinning_exceeds_vcpu_count",
		},
		{
			name: "valid",
			req: libvirtServiceInterfaces.ModifyCPURequest{
				CPUSockets: 1,
				CPUCores:   2,
				CPUThreads: 1,
				CPUPinning: []libvirtServiceInterfaces.CPUPinning{{Socket: 0, Cores: []int{0, 1}}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCPUHardwareRequest(test.req)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid request, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestValidateVNCResolutionBounds(t *testing.T) {
	t.Parallel()

	for _, resolution := range []string{"640x480", "1920x1080", "3840x2160"} {
		if err := validateVNCResolution(resolution); err != nil {
			t.Fatalf("expected %s to be valid, got %v", resolution, err)
		}
	}
	for _, resolution := range []string{"", "640", "639x480", "640x479", "3841x2160", "3840x2161"} {
		if err := validateVNCResolution(resolution); err == nil {
			t.Fatalf("expected %s to be invalid", resolution)
		}
	}
}

func TestNormalizePassthroughDeviceIDs(t *testing.T) {
	t.Parallel()

	empty, err := normalizePassthroughDeviceIDs([]int{})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("expected explicit empty list, got %#v err=%v", empty, err)
	}
	if _, err := normalizePassthroughDeviceIDs([]int{0}); err == nil {
		t.Fatal("expected zero mapping ID to fail")
	}
	if _, err := normalizePassthroughDeviceIDs([]int{2, 2}); err == nil {
		t.Fatal("expected duplicate mapping ID to fail")
	}
}

func TestUpdatePassthroughRejectsMissingMapping(t *testing.T) {
	t.Parallel()

	_, err := updatePassthrough(`<domain type="bhyve"/>`, []int{9}, nil)
	if err == nil || !strings.Contains(err.Error(), "passthrough_device_not_found") {
		t.Fatalf("expected missing mapping error, got %v", err)
	}
}

func TestUpdatePassthroughClearPreservesUnrelatedXML(t *testing.T) {
	t.Parallel()

	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><memoryBacking><hugepages/><locked/></memoryBacking><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-s 10:0,passthru,2/0/0"/></bhyve:commandline></domain>`
	updated, err := updatePassthrough(xml, []int{}, nil)
	if err != nil {
		t.Fatalf("clear passthrough: %v", err)
	}
	if !strings.Contains(updated, "<hugepages") {
		t.Fatalf("unrelated memory backing was removed: %s", updated)
	}
	if strings.Contains(updated, "<locked") || strings.Contains(updated, "passthru") {
		t.Fatalf("passthrough-specific XML remained: %s", updated)
	}
	if !strings.Contains(updated, `value="-S"`) {
		t.Fatalf("unrelated bhyve argument was removed: %s", updated)
	}
}

func TestUpdatePassthroughAddsValidatedMapping(t *testing.T) {
	t.Parallel()

	updated, err := updatePassthrough(
		`<domain type="bhyve"/>`,
		[]int{7},
		[]models.PassedThroughIDs{{ID: 7, DeviceID: "2/0/0"}},
	)
	if err != nil {
		t.Fatalf("add passthrough: %v", err)
	}
	if !strings.Contains(updated, "<locked") || !strings.Contains(updated, "passthru,2/0/0") {
		t.Fatalf("expected passthrough XML, got %s", updated)
	}
}

func TestApplyVMHardwareMutationRollsBackDBAndRestoresRuntime(t *testing.T) {
	t.Parallel()

	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	vm := vmModels.VM{RID: 101, Name: "hardware-test", RAM: minimumVMHardwareRAMBytes}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	restoredXML := ""
	restoredJSON := false
	service := &Service{DB: db}
	err := service.applyVMHardwareMutation(
		vm.RID,
		"<domain>old</domain>",
		"<domain>new</domain>",
		func(tx *gorm.DB) error {
			return tx.Model(&vm).Update("ram", minimumVMHardwareRAMBytes*2).Error
		},
		vmHardwareMutationHooks{
			defineXML: func(string) error { return errors.New("define failed") },
			writeVMJSON: func(*gorm.DB, uint) error {
				t.Fatal("vm.json write must not run after define failure")
				return nil
			},
			restoreXML: func(xml string) error {
				restoredXML = xml
				return nil
			},
			restoreVMJSON: func(uint) error {
				restoredJSON = true
				return nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "failed_to_define_domain_with_modified_xml") {
		t.Fatalf("expected define failure, got %v", err)
	}

	var stored vmModels.VM
	if err := db.Where("rid = ?", vm.RID).First(&stored).Error; err != nil {
		t.Fatalf("reload VM: %v", err)
	}
	if stored.RAM != minimumVMHardwareRAMBytes {
		t.Fatalf("database mutation was not rolled back: ram=%d", stored.RAM)
	}
	if restoredXML != "<domain>old</domain>" || !restoredJSON {
		t.Fatalf("runtime was not reconciled: xml=%q json=%t", restoredXML, restoredJSON)
	}
}

func TestApplyVMHardwareMutationCommitsOnlyAfterStrictJSONWrite(t *testing.T) {
	t.Parallel()

	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	vm := vmModels.VM{RID: 102, Name: "hardware-success", RAM: minimumVMHardwareRAMBytes}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	definedXML := ""
	service := &Service{DB: db}
	err := service.applyVMHardwareMutation(
		vm.RID,
		"<domain>old</domain>",
		"<domain>new</domain>",
		func(tx *gorm.DB) error {
			return tx.Model(&vm).Update("ram", minimumVMHardwareRAMBytes*2).Error
		},
		vmHardwareMutationHooks{
			defineXML: func(xml string) error {
				definedXML = xml
				return nil
			},
			writeVMJSON: func(tx *gorm.DB, rid uint) error {
				var pending vmModels.VM
				if err := tx.Where("rid = ?", rid).First(&pending).Error; err != nil {
					return err
				}
				if pending.RAM != minimumVMHardwareRAMBytes*2 {
					return errors.New("vm.json did not observe pending database state")
				}
				return nil
			},
			restoreXML:    func(string) error { return errors.New("restore should not run") },
			restoreVMJSON: func(uint) error { return errors.New("restore should not run") },
		},
	)
	if err != nil {
		t.Fatalf("apply hardware mutation: %v", err)
	}
	if definedXML != "<domain>new</domain>" {
		t.Fatalf("unexpected defined XML: %q", definedXML)
	}

	var stored vmModels.VM
	if err := db.Where("rid = ?", vm.RID).First(&stored).Error; err != nil {
		t.Fatalf("reload VM: %v", err)
	}
	if stored.RAM != minimumVMHardwareRAMBytes*2 {
		t.Fatalf("database mutation was not committed: ram=%d", stored.RAM)
	}
}
