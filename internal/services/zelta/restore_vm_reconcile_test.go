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
