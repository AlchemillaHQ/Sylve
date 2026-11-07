// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"maps"
	"slices"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestNormalizeTargetPoolsDropsSameNameAndBlankEntries(t *testing.T) {
	got := normalizeTargetPools(map[string]string{" tank ": " fast ", "zroot": "zroot", "": "data", "old": " "})
	if !maps.Equal(got, map[string]string{"tank": "fast"}) {
		t.Fatalf("normalized pools = %v", got)
	}
	if got := normalizeTargetPools(map[string]string{"zroot": "zroot"}); got != nil {
		t.Fatalf("identity mapping = %v, want nil", got)
	}
}

func TestTargetDatasetRootsStagesPoolsMergedIntoOne(t *testing.T) {
	const tank, fast, data = "tank/sylve/virtual-machines/40", "fast/sylve/virtual-machines/40", "data/sylve/virtual-machines/40"
	got := TargetDatasetRoots([]string{tank, fast}, map[string]string{"tank": "data"})
	if !slices.Equal(got, []TargetRoot{
		{Source: fast, Root: fast, Receive: fast},
		{Source: tank, Root: data, Receive: data},
	}) {
		t.Fatalf("distinct pools = %+v", got)
	}

	got = TargetDatasetRoots([]string{tank, data, fast}, map[string]string{"tank": "data", "fast": "data"})
	if !slices.Equal(got, []TargetRoot{
		{Source: data, Root: data, Receive: data},
		{Source: fast, Root: data, Receive: data + ".merge-1"},
		{Source: tank, Root: data, Receive: data + ".merge-2"},
	}) {
		t.Fatalf("merged pools = %+v", got)
	}
}

func TestTargetPoolPreflightChecksMappingAgainstUsableTargetPools(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&clusterModels.BackupJob{}, &clusterModels.ClusterSSHIdentity{},
	)
	seed := db.Session(&gorm.Session{SkipHooks: true})
	vm := vmModels.VM{RID: 40, Name: "vm-40"}
	if err := seed.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}
	for _, pool := range []string{"tank", "fast"} {
		if err := seed.Create(&vmModels.Storage{VMID: vm.ID, Type: vmModels.VMStorageTypeZVol, Pool: pool}).Error; err != nil {
			t.Fatal(err)
		}
	}

	target := clusterService.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*clusterService.Service)
	target.NodeID = "target-node"
	clusterService.OnNode(target, "migration-target-pools", func(context.Context, string, struct{}) ([]string, error) {
		return []string{"data", "zroot"}, nil
	})
	// A dataset left on data by an earlier attempt shows up as a warning.
	const stale = "data/sylve/virtual-machines/40"
	clusterService.OnNode(target, "migration-target-dataset",
		func(_ context.Context, _ string, req TargetDatasetRequest) (TargetDatasetInfo, error) {
			return TargetDatasetInfo{Exists: req.Dataset == stale}, nil
		})
	svc := &Service{DB: db, Cluster: target}
	node := clusterModels.ClusterNode{NodeUUID: "target-node"}

	reasons, guestPools, nodePools := svc.targetPoolPreflight(t.Context(), "vm", 40, node, nil)
	if !slices.Equal(guestPools, []string{"fast", "tank"}) || !slices.Equal(nodePools, []string{"data", "zroot"}) {
		t.Fatalf("guest pools = %v, node pools = %v", guestPools, nodePools)
	}
	if !slices.Equal(reasons, []string{"target_missing_pool: fast", "target_missing_pool: tank"}) {
		t.Fatalf("unmapped reasons = %v", reasons)
	}

	empty := clusterService.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*clusterService.Service)
	empty.NodeID = "target-node"
	clusterService.OnNode(empty, "migration-target-pools", func(context.Context, string, struct{}) ([]string, error) {
		return []string{}, nil
	})
	reasons, _, _ = (&Service{DB: db, Cluster: empty}).targetPoolPreflight(t.Context(), "vm", 40, node, nil)
	if !slices.Equal(reasons, []string{"target_has_no_usable_pools"}) {
		t.Fatalf("no-pool target reasons = %v", reasons)
	}

	// Two pools may merge into one target pool.
	reasons, _, _ = svc.targetPoolPreflight(t.Context(), "vm", 40, node, map[string]string{"fast": "data", "tank": "data", "old": "zroot"})
	if !slices.Equal(reasons, []string{"target_pool_mapping_unknown: old", "warning_stale_dataset_on_target: " + stale}) {
		t.Fatalf("merging mapping reasons = %v", reasons)
	}
	reasons, _, _ = svc.targetPoolPreflight(t.Context(), "vm", 40, node, map[string]string{"fast": "gone", "tank": "gone"})
	if !slices.Equal(reasons, []string{"target_missing_pool: gone"}) {
		t.Fatalf("shared missing pool reasons = %v", reasons)
	}

	if err := db.Create(&clusterModels.BackupJob{
		Name: "vm-40-nightly", Mode: clusterModels.BackupJobModeVM, TargetID: 1,
		SourceDataset: "tank/sylve/virtual-machines/40", CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatal(err)
	}
	reasons, _, _ = svc.targetPoolPreflight(t.Context(), "vm", 40, node, map[string]string{"fast": "data", "tank": "zroot"})
	if !slices.Equal(reasons, []string{"target_pool_change_with_backup_jobs", "warning_stale_dataset_on_target: " + stale}) {
		t.Fatalf("backup job reasons = %v", reasons)
	}
}
