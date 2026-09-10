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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func TestResolveVMJSONOutputDirectoryUsesVMRootMountpoint(t *testing.T) {
	const mountpoint = "/custom/vm-root/107"
	datasetName := "tank/sylve/virtual-machines/107"
	service := newStorageTestService(nil, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name:       datasetName,
			pool:       "tank",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: mountpoint,
		},
	})
	vm := vmModels.VM{
		RID: 107,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
		},
	}

	path, err := service.resolveVMJSONOutputDirectory(context.Background(), vm, "tank")
	if err != nil {
		t.Fatalf("resolve VM metadata directory: %v", err)
	}
	if want := filepath.Join(mountpoint, ".sylve"); path != want {
		t.Fatalf("metadata path = %q, want %q", path, want)
	}
}

func TestResolveVMJSONOutputDirectoryUsesGroupingDatasetForFilesystemOnlyPool(t *testing.T) {
	const mountpoint = "/custom/vm-group"
	datasetName := "tank/sylve/virtual-machines"
	service := newStorageTestService(nil, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name:       datasetName,
			pool:       "tank",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: mountpoint,
		},
	})
	vm := vmModels.VM{
		RID: 108,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeFilesystem, Pool: "tank"},
		},
	}

	path, err := service.resolveVMJSONOutputDirectory(context.Background(), vm, "tank")
	if err != nil {
		t.Fatalf("resolve filesystem-only VM metadata directory: %v", err)
	}
	if want := filepath.Join(mountpoint, "108", ".sylve"); path != want {
		t.Fatalf("metadata path = %q, want %q", path, want)
	}
}

func TestVMJSONOutputPoolsIgnoreLegacyISOPool(t *testing.T) {
	pools := vmJSONOutputPools([]vmModels.Storage{
		{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
		{
			Type: vmModels.VMStorageTypeDiskImage, Pool: "stale",
			DownloadUUID: "iso-1", Emulation: vmModels.AHCICDStorageEmulation,
		},
		{Type: vmModels.VMStorageTypeZVol, Dataset: vmModels.VMStorageDataset{Pool: "fast"}},
		{
			Type:    vmModels.VMStorageTypeRaw,
			Dataset: vmModels.VMStorageDataset{Name: "legacy/sylve/virtual-machines/107/raw-9"},
		},
		{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
	})
	if got := strings.Join(pools, ","); got != "tank,fast,legacy" {
		t.Fatalf("vm.json output pools = %q, want tank,fast,legacy", got)
	}
}

func TestLocalVMMetadataRetirementExplicitlyBypassesTopologyHook(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 791, "tank", false)
	if err := db.AutoMigrate(&clusterModels.ReplicationPolicy{}); err != nil {
		t.Fatalf("migrate replication policy: %v", err)
	}
	replicationguard.MarkPolicySchemaReady(db)
	if err := db.Create(&clusterModels.ReplicationPolicy{
		Name:      "protected-vm-retirement",
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   seed.VM.RID,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}

	if err := db.Where("vm_id = ?", seed.VM.ID).Delete(&vmModels.Storage{}).Error; err == nil {
		t.Fatal("normal protected storage deletion unexpectedly bypassed topology guard")
	}
	if err := deleteVMStorageRowsForLocalRetirement(db, []uint{seed.VM.ID}); err != nil {
		t.Fatalf("retire local VM storage metadata: %v", err)
	}

	var count int64
	if err := db.Model(&vmModels.Storage{}).Where("vm_id = ?", seed.VM.ID).Count(&count).Error; err != nil {
		t.Fatalf("count retired storage rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("storage rows remaining = %d, want 0", count)
	}
}

func TestWriteVMJSONKeepsDisabledManualNICWithoutRuntimeInspection(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := testutil.NewSQLiteTestDB(
		t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&vmModels.VMStorageDataset{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMSnapshot{},
		&vmModels.VM{},
	)
	vm := vmModels.VM{Name: "disabled-manual-metadata", RID: 109}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	manual := networkModels.ManualSwitch{Name: "manual-lan", Bridge: "bridge0"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create Manual Switch: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: manual.ID, SwitchType: "manual", Emulation: "virtio", Enable: false,
	}).Error; err != nil {
		t.Fatalf("create disabled VM NIC: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeRaw, Pool: "tank", Enable: true,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}

	originalInspect := inspectVMBridgeVLAN
	inspectCalls := 0
	inspectVMBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		inspectCalls++
		return bridgevlan.BridgeState{}, errors.New("bridge unavailable")
	}
	t.Cleanup(func() { inspectVMBridgeVLAN = originalInspect })

	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/109"
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name: datasetName, pool: "tank", kind: gzfs.DatasetTypeFilesystem, mountpoint: mountpoint,
		},
	})
	if err := service.WriteVMJson(vm.RID); err != nil {
		t.Fatalf("write VM metadata: %v", err)
	}
	if inspectCalls != 0 {
		t.Fatalf("metadata refresh inspected disabled Manual Switch %d times", inspectCalls)
	}

	data, err := os.ReadFile(filepath.Join(mountpoint, ".sylve", "vm.json"))
	if err != nil {
		t.Fatalf("read VM metadata: %v", err)
	}
	var written vmModels.VM
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("decode VM metadata: %v", err)
	}
	if len(written.Networks) != 1 || written.Networks[0].Attachment == nil {
		t.Fatalf("VM metadata is missing its NIC attachment: %#v", written.Networks)
	}
	attachment := written.Networks[0].Attachment
	if attachment.Version != networkAttachment.CurrentVersion ||
		attachment.Kind != networkAttachment.KindVM || attachment.SwitchName != manual.Name ||
		attachment.SwitchType != "manual" || attachment.VLANFiltering {
		t.Fatalf("unexpected disabled NIC attachment: %#v", attachment)
	}
	if written.Networks[0].StandardSwitch != nil || written.Networks[0].ManualSwitch != nil {
		t.Fatalf("metadata retained full switch models: %#v", written.Networks[0])
	}
}

func TestWriteVMJSONUsesDesiredManagedSwitchState(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := testutil.NewSQLiteTestDB(
		t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&vmModels.VMStorageDataset{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMSnapshot{},
		&vmModels.VM{},
	)
	vm := vmModels.VM{Name: "managed-metadata", RID: 110}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	defaultVLAN := 42
	managed := networkModels.StandardSwitch{
		Name: "tenant", BridgeName: "vm-tenant", VLANFiltering: true,
		DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&managed).Error; err != nil {
		t.Fatalf("create Standard Switch: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: managed.ID, SwitchType: "standard",
		Emulation: "virtio", Enable: true,
	}).Error; err != nil {
		t.Fatalf("create enabled VM NIC: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeRaw, Pool: "tank", Enable: true,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}

	originalValidate := validateVMFilteredBridge
	validateCalls := 0
	validateVMFilteredBridge = func(string, *int) (bridgevlan.BridgeState, error) {
		validateCalls++
		return bridgevlan.BridgeState{}, errors.New("managed bridge unavailable")
	}
	t.Cleanup(func() { validateVMFilteredBridge = originalValidate })

	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/110"
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name: datasetName, pool: "tank", kind: gzfs.DatasetTypeFilesystem, mountpoint: mountpoint,
		},
	})
	if err := service.WriteVMJson(vm.RID); err != nil {
		t.Fatalf("write VM metadata without managed bridge runtime: %v", err)
	}
	if validateCalls != 0 {
		t.Fatalf("metadata inspected managed bridge %d times", validateCalls)
	}

	data, err := os.ReadFile(filepath.Join(mountpoint, ".sylve", "vm.json"))
	if err != nil {
		t.Fatalf("read VM metadata: %v", err)
	}
	var written vmModels.VM
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("decode VM metadata: %v", err)
	}
	if len(written.Networks) != 1 || written.Networks[0].Attachment == nil {
		t.Fatalf("VM metadata is missing its NIC attachment: %#v", written.Networks)
	}
	attachment := written.Networks[0].Attachment
	if !attachment.VLANFiltering || attachment.DefaultAccessVLAN == nil ||
		*attachment.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("unexpected managed NIC attachment: %#v", attachment)
	}
}
