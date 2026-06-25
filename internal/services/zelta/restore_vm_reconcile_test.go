// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

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

func TestNormalizeRestoredVMNetworksSkipsUnresolved(t *testing.T) {
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

	resolved, requiresSync, err := svc.normalizeRestoredVMNetworks(tx, 10, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requiresSync {
		t.Fatal("expected requiresSync=false")
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved network (lan), dmz should be skipped, got %d", len(resolved))
	}
	if resolved[0].SwitchID != lan.ID {
		t.Fatalf("expected switch ID %d, got %d", lan.ID, resolved[0].SwitchID)
	}
	if resolved[0].Emulation != "e1000" {
		t.Fatalf("expected emulation e1000, got %q", resolved[0].Emulation)
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

	resolved, _, err := svc.normalizeRestoredVMNetworks(tx, 10, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Emulation != "virtio" {
		t.Fatalf("expected default emulation virtio, got %q", resolved[0].Emulation)
	}
}
