// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newManagedPoolDashboardTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{})
	if err := database.Create(&models.BasicSettings{
		ID:          1,
		Pools:       []string{"managed"},
		Initialized: true,
	}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.ZPoolHistorical{}, &infoModels.ZFSARCHistorical{})
	service := &Service{
		DB:                        database,
		TelemetryDB:               telemetryDB,
		poolIOStats:               make(map[string]poolIOStat),
		managedPoolIONames:        make(map[string]struct{}),
		pendingCacheInvalidations: make(map[string]uint64),
		listHostPools: func(context.Context) ([]*gzfs.ZPool, error) {
			return []*gzfs.ZPool{
				{Name: "managed", PoolGUID: "managed-guid"},
				{Name: "unmanaged", PoolGUID: "unmanaged-guid"},
			}, nil
		},
	}
	return service, telemetryDB
}

func TestDashboardHistoryOnlyReturnsManagedPools(t *testing.T) {
	service, telemetryDB := newManagedPoolDashboardTestService(t)
	base := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	rows := []infoModels.ZPoolHistorical{
		{GUID: "managed-guid", Name: "managed", Health: "ONLINE", Size: 100, CreatedAt: base},
		{GUID: "unmanaged-guid", Name: "unmanaged", Health: "ONLINE", Size: 200, CreatedAt: base},
	}
	if err := telemetryDB.Create(&rows).Error; err != nil {
		t.Fatalf("seed pool telemetry: %v", err)
	}

	history, err := service.GetDashboardHistory(context.Background(), zfsServiceInterfaces.DashboardHistoryQuery{
		From:      base.Add(-time.Minute),
		To:        base.Add(time.Minute),
		MaxPoints: 120,
	})
	if err != nil {
		t.Fatalf("get dashboard history: %v", err)
	}
	if len(history.Pools) != 1 || history.Pools[0].GUID != "managed-guid" {
		t.Fatalf("dashboard history exposed unmanaged pools: %+v", history.Pools)
	}
	if history.Cursors.Pool != rows[0].ID {
		t.Fatalf("pool cursor = %d, want managed row %d", history.Cursors.Pool, rows[0].ID)
	}

	delta, err := service.GetDashboardHistoryDelta(context.Background(), zfsServiceInterfaces.DashboardDeltaQuery{})
	if err != nil {
		t.Fatalf("get dashboard delta: %v", err)
	}
	if len(delta.Pools) != 1 || delta.Pools[0].GUID != "managed-guid" {
		t.Fatalf("dashboard delta exposed unmanaged pools: %+v", delta.Pools)
	}

	_, err = service.GetDashboardHistory(context.Background(), zfsServiceInterfaces.DashboardHistoryQuery{
		From:      base.Add(-time.Minute),
		To:        base.Add(time.Minute),
		PoolGUID:  "unmanaged-guid",
		MaxPoints: 120,
	})
	if !errors.Is(err, ErrPoolNotFound) {
		t.Fatalf("unmanaged pool query error = %v, want ErrPoolNotFound", err)
	}
}

func TestReconcileManagedPoolTelemetryPurgesUnmanagedPools(t *testing.T) {
	service, telemetryDB := newManagedPoolDashboardTestService(t)
	base := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	rows := []infoModels.ZPoolHistorical{
		{GUID: "managed-guid", Name: "managed", CreatedAt: base},
		{GUID: "old-managed-guid", Name: "managed", CreatedAt: base},
		{GUID: "unmanaged-guid", Name: "unmanaged", CreatedAt: base},
		{GUID: "", Name: "managed", CreatedAt: base},
		{GUID: "", Name: "unmanaged", CreatedAt: base},
	}
	if err := telemetryDB.Create(&rows).Error; err != nil {
		t.Fatalf("seed pool telemetry: %v", err)
	}
	service.poolIOStats["managed"] = poolIOStat{SampledAt: base}
	service.poolIOStats["unmanaged"] = poolIOStat{SampledAt: base}

	if err := service.ReconcileManagedPoolTelemetry(context.Background()); err != nil {
		t.Fatalf("reconcile managed telemetry: %v", err)
	}

	var remaining []infoModels.ZPoolHistorical
	if err := telemetryDB.Order("id ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining telemetry: %v", err)
	}
	if len(remaining) != 2 || remaining[0].GUID != "managed-guid" || remaining[1].GUID != "" || remaining[1].Name != "managed" {
		t.Fatalf("unexpected remaining telemetry: %+v", remaining)
	}
	if _, exists := service.poolIOStats["unmanaged"]; exists {
		t.Fatal("unmanaged pool I/O remained cached")
	}
	if _, exists := service.poolIOStats["managed"]; !exists {
		t.Fatal("managed pool I/O was removed")
	}

	service.setPoolIOStat("unmanaged", poolIOStat{SampledAt: base.Add(time.Second)})
	if _, exists := service.poolIOStats["unmanaged"]; exists {
		t.Fatal("unmanaged pool I/O was cached after reconciliation")
	}
}
