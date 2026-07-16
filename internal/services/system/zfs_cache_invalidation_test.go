// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"testing"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFlushZFSCacheInvalidationsCoalescesPendingKinds(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{})
	if err := conn.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed creating basic settings: %v", err)
	}
	service := &Service{DB: conn}
	pending := map[string]map[string]struct{}{
		db.ZFSCacheKindGenericDataset: {"zroot": {}},
		db.ZFSCacheKindSnapshot:       {"zroot": {}},
	}

	service.flushZFSCacheInvalidations(context.Background(), pending)
	if len(pending) != 0 {
		t.Fatalf("expected all pending kinds to be flushed, got %v", pending)
	}

	var invalidations []models.ZFSCacheInvalidation
	if err := conn.Find(&invalidations).Error; err != nil {
		t.Fatalf("failed loading invalidations: %v", err)
	}
	if len(invalidations) != 2 {
		t.Fatalf("expected two invalidation rows, got %d", len(invalidations))
	}
}

func TestFlushZFSCacheInvalidationsRetainsFailedWrites(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := conn.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed creating basic settings: %v", err)
	}
	service := &Service{DB: conn}
	pending := map[string]map[string]struct{}{db.ZFSCacheKindSnapshot: {"zroot": {}}}

	service.flushZFSCacheInvalidations(context.Background(), pending)
	if _, ok := pending[db.ZFSCacheKindSnapshot]; !ok {
		t.Fatal("expected failed invalidation write to remain pending")
	}
}

func TestFlushZFSCacheInvalidationsIgnoresUnmanagedPools(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{})
	if err := conn.Create(&models.BasicSettings{Pools: []string{"zroot"}}).Error; err != nil {
		t.Fatalf("failed creating basic settings: %v", err)
	}
	service := &Service{DB: conn}
	pending := map[string]map[string]struct{}{db.ZFSCacheKindSnapshot: {"backup": {}}}

	service.flushZFSCacheInvalidations(context.Background(), pending)
	if len(pending) != 0 {
		t.Fatalf("expected unmanaged pool events to be discarded, got %v", pending)
	}

	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting invalidations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no invalidation for unmanaged pool, got %d", count)
	}
}
