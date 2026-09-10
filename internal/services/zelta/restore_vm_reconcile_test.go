// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

type restoreVMRuntimeLockProbe struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
	check func()
	err   error
}

func (p *restoreVMRuntimeLockProbe) RemoveLvVm(uint) error {
	p.check()
	return nil
}

func (p *restoreVMRuntimeLockProbe) CreateLvVm(int, context.Context) error {
	p.check()
	return p.err
}

func TestIntegrationRestoreVMReleasesSwitchLockBeforeRuntimeRebuild(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	svc, db := newTestZeltaServiceWithDB(t,
		&vmModels.VM{}, &vmModels.VMStorageDataset{}, &vmModels.VMSnapshot{},
		&vmModels.Storage{}, &vmModels.Network{}, &vmModels.VMCPUPinning{},
		&networkModels.StandardSwitch{}, &networkModels.ManualSwitch{},
		&networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{},
	)
	svc.GZFS = client
	vid := 10
	sw := networkModels.StandardSwitch{
		Name: "restore-lock-test", BridgeName: "bridge-restore-lock-test",
		VLANFiltering: true, DefaultAccessVLAN: &vid,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	dataset := pool + "/sylve/virtual-machines/731"
	zfstest.EnsureDataset(t, client, dataset)
	mountpoint, err := svc.runLocalZFSGet(context.Background(), "mountpoint", dataset)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(vmModels.VM{
		RID: 731, Name: "restored-vm",
		Networks: []vmModels.Network{{
			Enable: true, Emulation: "virtio",
			Attachment: &networkAttachment.Contract{
				Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
				SwitchType: "standard", SwitchName: sw.Name,
				VLANFiltering: true, DefaultAccessVLAN: &vid,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mountpoint, ".sylve"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, ".sylve/vm.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}

	stopAfterRebuild := errors.New("stop after runtime lock probe")
	calls := 0
	svc.VM = &restoreVMRuntimeLockProbe{err: stopAfterRebuild, check: func() {
		calls++
		var count int64
		if err := db.Model(&vmModels.Network{}).Where("switch_id = ?", sw.ID).Count(&count).Error; err != nil || count != 1 {
			t.Errorf("attachment must be committed before runtime rebuild: count=%d err=%v", count, err)
		}
		acquired := make(chan struct{})
		go func() {
			unlock := bridgevlan.LockStandardSwitchLifecycle(sw.BridgeName)
			unlock()
			close(acquired)
		}()
		select {
		case <-acquired:
		case <-time.After(time.Second):
			t.Error("runtime rebuild still holds the switch lifecycle lock; VM CRUD lock ordering would deadlock")
		}
	}}
	err = svc.reconcileRestoredVMFromDataset(context.Background(), dataset, "", true, false, false)
	if !errors.Is(err, stopAfterRebuild) {
		t.Fatalf("expected runtime lock probe error, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected RemoveLvVm and CreateLvVm probes, got %d", calls)
	}
}

func TestNormalizeRestoredVMBootROMPreservesExplicitFirmware(t *testing.T) {
	defaultBootROM := vmModels.VMBootROMUEFI
	if runtime.GOARCH == "arm64" {
		defaultBootROM = vmModels.VMBootROMUBoot
	}
	tests := []struct {
		name  string
		input vmModels.VMBootROM
		want  vmModels.VMBootROM
	}{
		{name: "uboot", input: vmModels.VMBootROMUBoot, want: vmModels.VMBootROMUBoot},
		{name: "uboot normalized", input: vmModels.VMBootROM(" UBOOT "), want: vmModels.VMBootROMUBoot},
		{name: "uefi", input: vmModels.VMBootROMUEFI, want: vmModels.VMBootROMUEFI},
		{name: "none", input: vmModels.VMBootROMNone, want: vmModels.VMBootROMNone},
		{name: "empty legacy value", input: "", want: defaultBootROM},
		{name: "unknown legacy value", input: "unknown", want: vmModels.VMBootROMUEFI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRestoredVMBootROM(tt.input); got != tt.want {
				t.Fatalf("normalize restored boot ROM %q = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeRestoredVMStoragesClearsLegacyISOTopology(t *testing.T) {
	service := &Service{}
	datasetID := uint(99)
	storages, err := service.normalizeRestoredVMStorages(context.Background(), nil, 107, []vmModels.Storage{
		{
			ID: 12, Type: vmModels.VMStorageTypeDiskImage, Name: "installer",
			DownloadUUID: "iso-107", Pool: "stale", DatasetID: &datasetID,
			Dataset:   vmModels.VMStorageDataset{ID: datasetID, Pool: "stale", Name: "stale/iso"},
			Emulation: vmModels.AHCICDStorageEmulation, Enable: true,
		},
	})
	if err != nil {
		t.Fatalf("normalize restored ISO: %v", err)
	}
	if len(storages) != 1 || storages[0].Pool != "" || storages[0].DatasetID != nil || storages[0].Dataset.ID != 0 {
		t.Fatalf("restored ISO retained ZFS topology: %+v", storages)
	}
}

func TestInferRestoredVMRootDatasets(t *testing.T) {
	roots := inferRestoredVMRootDatasets(7, []vmModels.Storage{
		{Pool: "zroot", Dataset: vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/vms/7"}},
	}, "")
	if len(roots) != 1 {
		t.Fatalf("expected 1 root from pool, got %d: %v", len(roots), roots)
	}
	if roots[0] != "zroot/sylve/virtual-machines/7" {
		t.Fatalf("unexpected root: %q", roots[0])
	}

	roots = inferRestoredVMRootDatasets(7, []vmModels.Storage{
		{Dataset: vmModels.VMStorageDataset{Pool: "tank", Name: "tank/vms/7"}},
		{Dataset: vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/vms/7"}},
	}, "")
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots from multiple pools, got %d: %v", len(roots), roots)
	}

	roots = inferRestoredVMRootDatasets(7, []vmModels.Storage{
		{Pool: "zroot", Dataset: vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/vms/7"}},
	}, "tank/virtual-machines/7")
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots (pool + destination), got %d: %v", len(roots), roots)
	}

	roots = inferRestoredVMRootDatasets(7, nil, "")
	if len(roots) != 0 {
		t.Fatalf("empty storages should return empty, got %d", len(roots))
	}

	roots = inferRestoredVMRootDatasets(0, []vmModels.Storage{
		{Pool: "zroot"},
	}, "")
	if len(roots) == 1 {
		if roots[0] != "zroot/sylve/virtual-machines/0" {
			t.Fatalf("rid=0 path: %q", roots[0])
		}
	}

	roots = inferRestoredVMRootDatasets(7, []vmModels.Storage{
		{Pool: "zroot", Dataset: vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/vms/7"}},
		{Pool: "zroot", Dataset: vmModels.VMStorageDataset{Pool: "zroot", Name: "zroot/vms/7"}},
	}, "")
	if len(roots) != 1 {
		t.Fatalf("duplicate pool should be deduplicated, got %d: %v", len(roots), roots)
	}
}

func TestNormalizeRestoredVMNetworksRejectsUnresolved(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed lan switch: %v", err)
	}
	collisionSwitch := networkModels.ManualSwitch{Name: "collision", Bridge: "bridge-collision"}
	if err := db.Create(&collisionSwitch).Error; err != nil {
		t.Fatalf("failed to seed collision switch: %v", err)
	}
	if err := db.Create(&jailModels.Network{
		JailID: 99, Name: "restored-vm-10-network-1",
		SwitchID: collisionSwitch.ID, SwitchType: "manual",
	}).Error; err != nil {
		t.Fatalf("failed to seed transient-name collision: %v", err)
	}

	networks := []vmModels.Network{
		{
			Enable:     true,
			SwitchType: "standard",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "lan",
				BridgeName: "bridge-lan",
			},
			Emulation: "e1000",
		},
		{
			SwitchType: "standard",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "dmz",
				BridgeName: "bridge-dmz",
			},
			Emulation: "virtio",
		},
	}

	tx := db.Begin()
	defer tx.Rollback()

	resolved, err := svc.normalizeRestoredVMNetworks(tx, 10, networks)
	if !errors.Is(err, ErrSwitchNotFound) {
		t.Fatalf("expected ErrSwitchNotFound, got resolved=%v err=%v", resolved, err)
	}
}

func TestNormalizeRestoredVMNetworksDefaultsEmulation(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed lan switch: %v", err)
	}

	networks := []vmModels.Network{
		{
			SwitchType: "standard",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "lan",
				BridgeName: "bridge-lan",
			},
			Emulation: "",
		},
	}

	tx := db.Begin()
	defer tx.Rollback()

	resolved, err := svc.normalizeRestoredVMNetworks(tx, 10, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Emulation != "virtio" {
		t.Fatalf("expected default emulation virtio, got %q", resolved[0].Emulation)
	}
	if resolved[0].Enable {
		t.Fatal("explicitly disabled network became enabled during restore normalization")
	}
}

func TestNormalizeRestoredVMNetworksPreservesEffectiveAccessVLAN(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)
	targetVLAN := 10
	target := networkModels.StandardSwitch{
		Name:              "vm-filtered",
		BridgeName:        "bridge-vm-filtered",
		VLANFiltering:     true,
		DefaultAccessVLAN: &targetVLAN,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target switch: %v", err)
	}

	attachment := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
		SwitchName: target.Name, SwitchType: "standard", VLANFiltering: true,
		DefaultAccessVLAN: &targetVLAN,
	}
	network := vmModels.Network{
		Enable: true, SwitchType: "standard", Attachment: &attachment, Emulation: "virtio",
	}
	tx := db.Begin()
	defer tx.Rollback()
	resolved, err := svc.normalizeRestoredVMNetworks(tx, 10, []vmModels.Network{network})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("matching target rejected: resolved=%v err=%v", resolved, err)
	}

	wrongVLAN := 20
	network.Attachment.DefaultAccessVLAN = &wrongVLAN
	_, err = svc.normalizeRestoredVMNetworks(tx, 10, []vmModels.Network{network})
	if err == nil || !strings.Contains(err.Error(), "vm_network_default_access_vlan_mismatch") {
		t.Fatalf("different effective VLAN accepted: %v", err)
	}
}
