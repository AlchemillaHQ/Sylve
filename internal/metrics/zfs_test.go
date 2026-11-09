// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestZFSMetricsTypesAndStaleness(t *testing.T) {
	now := time.Now()
	snapshot := ZFSSnapshot{
		Pools: Snapshot[ZFSPool]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []ZFSPool{{
			Name: "tank", GUID: "1", Health: "DEGRADED", Size: OptionalValue{Value: 1000, Available: true},
			IOExpiresAt: now.Add(20 * time.Second), ReadIOPS: 2, WriteIOPS: 0,
			ReadLatencySeconds: OptionalValue{Value: .00002, Available: true},
		}}},
		Datasets: Snapshot[ZFSDataset]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []ZFSDataset{{
			Name: "tank/fs", Pool: "tank", GUID: "2", Type: "filesystem", Used: OptionalValue{Value: 0, Available: true},
		}}},
		ARC: Snapshot[ZFSARC]{CollectedAt: now, LastSuccessAt: now, Success: true, Items: []ZFSARC{{Size: 500, Hits: 100, Misses: 10}}},
	}
	c := &collector{Sources{ZFS: func() ZFSSnapshot { return snapshot }}}
	families := gather(t, c)
	assertValue(t, families, "sylve_zfs_pool_health", 1, "state", "DEGRADED")
	assertValue(t, families, "sylve_zfs_pool_health", 0, "state", "ONLINE")
	if len(families["sylve_zfs_pool_health"].Metric) != len(zfsPoolStates) {
		t.Fatal("pool health did not export every state")
	}
	assertValue(t, families, "sylve_zfs_pool_read_latency_seconds", .00002)
	assertValue(t, families, "sylve_zfs_pool_write_operations_per_second", 0)
	assertValue(t, families, "sylve_zfs_dataset_used_bytes", 0)
	assertValue(t, families, "sylve_zfs_arc_hits_total", 100)
	if families["sylve_zfs_arc_hits_total"].GetType() != dto.MetricType_COUNTER || families["sylve_zfs_pool_read_operations_per_second"].GetType() != dto.MetricType_GAUGE {
		t.Fatal("ARC totals must be counters and sampled I/O rates must be gauges")
	}
	for _, name := range []string{"sylve_zfs_pool_write_latency_seconds", "sylve_zfs_pool_fragmentation_ratio", "sylve_zfs_dataset_quota_bytes", "sylve_zfs_dataset_volume_size_bytes"} {
		if families[name] != nil {
			t.Fatalf("unavailable measurement exported: %s", name)
		}
	}

	// I/O expires using its source sample age, even with a fresh pool snapshot.
	snapshot.Pools.Items[0].IOExpiresAt = now.Add(-time.Second)
	snapshot.Pools.Items[0].Health = "future-state"
	families = gather(t, c)
	assertValue(t, families, "sylve_zfs_pool_io_collection_success", 0)
	assertValue(t, families, "sylve_zfs_pool_health", 1, "state", "UNKNOWN")
	if families["sylve_zfs_pool_read_operations_per_second"] != nil || families["sylve_zfs_pool_read_latency_seconds"] != nil {
		t.Fatal("expired I/O was exported")
	}

	// A failed collection keeps exporting its last measurements until they expire.
	snapshot.Datasets.Success = false
	families = gather(t, c)
	assertValue(t, families, "sylve_collector_success", 0, "collector", "zfs_datasets")
	assertValue(t, families, "sylve_zfs_dataset_used_bytes", 0)
	snapshot.Pools.CollectedAt = now.Add(-2 * time.Minute)
	snapshot.Datasets.CollectedAt = now.Add(-2 * time.Minute)
	snapshot.ARC.CollectedAt = now.Add(-2 * time.Minute)
	families = gather(t, c)
	if len(families) != 2 {
		t.Fatalf("stale snapshots should export only collector health: %v", families)
	}
	for _, name := range []string{"zfs_pools", "zfs_datasets", "zfs_arc"} {
		assertValue(t, families, "sylve_collector_success", 0, "collector", name)
		assertValue(t, families, "sylve_collector_last_success_timestamp_seconds", float64(now.Unix()), "collector", name)
	}
}
