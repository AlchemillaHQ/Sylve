// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestCollectVMISOUUIDs(t *testing.T) {
	storages := []vmModels.Storage{
		{Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "iso-a", Name: "Ubuntu ISO", Enable: true},
		{Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "iso-a", Name: "Duplicate", Enable: true},
		{Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "  ", Name: "blank-uuid", Enable: true},
		{Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "iso-disabled", Name: "Disabled", Enable: false},
		{Type: vmModels.VMStorageTypeZVol, DownloadUUID: "zvol-uuid", Name: "Disk", Enable: true},
		{Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "iso-b", Name: "", Enable: true},
	}

	uuids, nameByUUID := collectVMISOUUIDs(storages)

	if len(uuids) != 2 {
		t.Fatalf("expected 2 unique enabled ISO uuids, got %d (%v)", len(uuids), uuids)
	}

	want := map[string]string{
		"iso-a": "Ubuntu ISO",
		"iso-b": "iso-b", // empty name falls back to the uuid
	}
	for _, u := range uuids {
		if _, ok := want[u]; !ok {
			t.Fatalf("unexpected uuid in result: %q", u)
		}
	}
	for u, name := range want {
		if nameByUUID[u] != name {
			t.Fatalf("expected friendly name %q for %q, got %q", name, u, nameByUUID[u])
		}
	}
}

func TestCollectVMISOUUIDs_Empty(t *testing.T) {
	uuids, nameByUUID := collectVMISOUUIDs(nil)
	if len(uuids) != 0 {
		t.Fatalf("expected no uuids, got %v", uuids)
	}
	if len(nameByUUID) != 0 {
		t.Fatalf("expected empty name map, got %v", nameByUUID)
	}
}

func hasReasonPrefix(reasons []string, prefix string) bool {
	for _, r := range reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestVMConfigPreflightReasons(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		RAM:        8 << 30, // 8 GiB
		PCIDevices: []int{1, 2},
		CPUPinning: []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: []int{0}}},
	}
	target := clusterModels.ClusterNode{Memory: 4 << 30, MemoryUsage: 50}

	reasons := svc.vmConfigPreflightReasons(vm, target)

	for _, want := range []string{
		"warning_pci_passthrough_not_migrated",
		"warning_cpu_pinning_reset",
		"warning_target_insufficient_memory",
	} {
		if !hasReasonPrefix(reasons, want) {
			t.Fatalf("expected a reason with prefix %q, got %v", want, reasons)
		}
	}

	for _, r := range reasons {
		if !strings.HasPrefix(r, "warning_") {
			t.Fatalf("expected only warnings (non-blocking), got hard reason %q", r)
		}
	}
}

func TestVMConfigPreflightReasons_None(t *testing.T) {
	svc := &Service{}
	vm := vmModels.VM{RAM: 1 << 30}
	target := clusterModels.ClusterNode{Memory: 16 << 30, MemoryUsage: 10}

	reasons := svc.vmConfigPreflightReasons(vm, target)
	if len(reasons) != 0 {
		t.Fatalf("expected no reasons for a plain VM on a roomy target, got %v", reasons)
	}
}
