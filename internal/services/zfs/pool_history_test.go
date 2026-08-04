// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetZpoolHistoricalStatsBucketsRatesAndPreservesPoolIdentity(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &infoModels.ZPoolHistorical{})
	service := &Service{TelemetryDB: database}
	base := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)

	records := []infoModels.ZPoolHistorical{
		{
			GUID: "destroyed-pool-guid", Name: "new-name", Health: "ONLINE", Allocated: 999, Free: 1, Size: 1000,
			CreatedAt: base.Add(-10 * time.Minute),
		},
		{
			GUID: "pool-guid", Name: "old-name", Health: "ONLINE", Allocated: 100, Free: 900, Size: 1000,
			Fragmentation: 4, DedupRatio: 1.1, ReadIOPS: 10, WriteIOPS: 4,
			ReadBytesPerSecond: 100, WriteBytesPerSecond: 40,
			ReadLatencyNanos: 100, WriteLatencyNanos: 400, CreatedAt: base.Add(10 * time.Second),
		},
		{
			GUID: "pool-guid", Name: "old-name", Health: "DEGRADED", Allocated: 120, Free: 880, Size: 1000,
			Fragmentation: 5, DedupRatio: 1.2, ReadIOPS: 20, WriteIOPS: 6,
			ReadBytesPerSecond: 300, WriteBytesPerSecond: 60,
			ReadLatencyNanos: 200, WriteLatencyNanos: 600, CreatedAt: base.Add(40 * time.Second),
		},
		{
			GUID: "pool-guid", Name: "new-name", Health: "ONLINE", Allocated: 140, Free: 860, Size: 1000,
			Fragmentation: 6, DedupRatio: 1.3, ReadIOPS: 30, WriteIOPS: 8,
			ReadBytesPerSecond: 500, WriteBytesPerSecond: 80,
			ReadLatencyNanos: 300, WriteLatencyNanos: 800, CreatedAt: base.Add(70 * time.Second),
		},
	}
	if err := database.Create(&records).Error; err != nil {
		t.Fatalf("seed zpool history: %v", err)
	}

	result, count, err := service.GetZpoolHistoricalStats(1, 0)
	if err != nil {
		t.Fatalf("get zpool history: %v", err)
	}
	if count != 4 {
		t.Fatalf("record count: got %d, want 4", count)
	}
	if _, exists := result["old-name"]; exists {
		t.Fatal("pool rename split one GUID into multiple series")
	}
	points := result["new-name"]
	if len(points) != 2 {
		t.Fatalf("point count: got %d, want 2", len(points))
	}

	first := points[0]
	if first.Time != base.UnixMilli() {
		t.Fatalf("bucket time: got %d, want %d", first.Time, base.UnixMilli())
	}
	if first.Health != "DEGRADED" || first.Allocated != 120 || first.Fragmentation != 5 {
		t.Fatalf("bucket did not retain latest gauge values: %+v", first)
	}
	if first.ReadIOPS != 15 || first.WriteIOPS != 5 || first.ReadBytesPerSecond != 200 || first.WriteBytesPerSecond != 50 {
		t.Fatalf("bucket did not average rates: %+v", first)
	}
	if first.ReadLatencyNanos != 166 || first.WriteLatencyNanos != 520 {
		t.Fatalf("bucket did not operation-weight latency: %+v", first)
	}

	limited, _, err := service.GetZpoolHistoricalStats(1, 1)
	if err != nil {
		t.Fatalf("get limited zpool history: %v", err)
	}
	if got := limited["new-name"]; len(got) != 1 || got[0].Allocated != 140 {
		t.Fatalf("limit did not retain latest point: %+v", got)
	}
}

func TestGetZpoolHistoricalStatsValidatesRange(t *testing.T) {
	service := &Service{}
	if _, _, err := service.GetZpoolHistoricalStats(0, 1); err == nil {
		t.Fatal("expected zero interval to fail")
	}
	if _, _, err := service.GetZpoolHistoricalStats(1, 10001); err == nil {
		t.Fatal("expected excessive limit to fail")
	}
}
