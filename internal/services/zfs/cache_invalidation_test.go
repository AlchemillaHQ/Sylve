// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
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

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestProcessCacheInvalidationsRespectsQuietAndMaximumDelays(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{})
	now := time.Now().UTC().Truncate(time.Millisecond)
	queryNow := now.In(time.FixedZone("UTC+9", 9*60*60))
	invalidation := models.ZFSCacheInvalidation{
		Kind:         db.ZFSCacheKindSnapshot,
		Generation:   1,
		FirstDirtyAt: now.Add(-5 * time.Second),
		LastDirtyAt:  now.Add(-time.Second),
	}
	if err := conn.Create(&invalidation).Error; err != nil {
		t.Fatalf("failed creating invalidation: %v", err)
	}

	refreshes := 0
	refresh := func(context.Context, string) error {
		refreshes++
		return nil
	}
	if err := processCacheInvalidations(context.Background(), conn, queryNow, refresh); err != nil {
		t.Fatalf("failed processing quiet invalidation: %v", err)
	}
	if refreshes != 0 {
		t.Fatalf("expected invalidation to remain debounced, got %d refreshes", refreshes)
	}

	if err := conn.Model(&invalidation).Update("first_dirty_at", now.Add(-31*time.Second)).Error; err != nil {
		t.Fatalf("failed aging invalidation: %v", err)
	}
	if err := processCacheInvalidations(context.Background(), conn, queryNow, refresh); err != nil {
		t.Fatalf("failed processing maximum-delay invalidation: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("expected one maximum-delay refresh, got %d", refreshes)
	}

	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting invalidations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected successful refresh to clear invalidation, got %d rows", count)
	}
}

func TestInvalidateCacheRetriesFailedPersistence(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t)
	service := &Service{DB: conn}

	if err := service.invalidateCache(context.Background(), db.ZFSCacheKindSnapshot); err == nil {
		t.Fatal("expected invalidation write to fail before migration")
	}
	if _, ok := service.pendingCacheInvalidationSnapshot()[db.ZFSCacheKindSnapshot]; !ok {
		t.Fatal("expected failed invalidation to remain pending")
	}

	if err := conn.AutoMigrate(&models.ZFSCacheInvalidation{}); err != nil {
		t.Fatalf("failed creating invalidation table: %v", err)
	}
	service.retryPendingCacheInvalidations(context.Background())
	if len(service.pendingCacheInvalidationSnapshot()) != 0 {
		t.Fatal("expected successful retry to clear in-memory invalidation")
	}

	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting persisted invalidations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one persisted invalidation after retry, got %d", count)
	}
}

func TestPendingCacheInvalidationRejectsStaleClearAfterRecreation(t *testing.T) {
	service := &Service{}
	first := service.rememberCacheInvalidation(db.ZFSCacheKindSnapshot)
	service.clearPendingCacheInvalidation(db.ZFSCacheKindSnapshot, first)

	second := service.rememberCacheInvalidation(db.ZFSCacheKindSnapshot)
	if second == first {
		t.Fatalf("expected monotonic retry token, got %d twice", second)
	}
	service.clearPendingCacheInvalidation(db.ZFSCacheKindSnapshot, first)
	if got := service.pendingCacheInvalidationSnapshot()[db.ZFSCacheKindSnapshot]; got != second {
		t.Fatalf("stale clear removed new pending invalidation: got %d, want %d", got, second)
	}

	service.clearPendingCacheInvalidation(db.ZFSCacheKindSnapshot, second)
	if len(service.pendingCacheInvalidationSnapshot()) != 0 {
		t.Fatal("expected current retry token to clear pending invalidation")
	}
}

func TestProcessCacheInvalidationsRetainsRefreshFailures(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{})
	now := time.Now().UTC().Truncate(time.Millisecond)
	invalidation := models.ZFSCacheInvalidation{
		Kind:         db.ZFSCacheKindGenericDataset,
		Generation:   1,
		FirstDirtyAt: now.Add(-time.Minute),
		LastDirtyAt:  now.Add(-time.Minute),
	}
	if err := conn.Create(&invalidation).Error; err != nil {
		t.Fatalf("failed creating invalidation: %v", err)
	}

	expectedErr := errors.New("cache write failed")
	err := processCacheInvalidations(context.Background(), conn, now, func(context.Context, string) error {
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected refresh failure, got %v", err)
	}

	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting invalidations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected failed refresh to remain pending, got %d rows", count)
	}
}

func TestProcessCacheInvalidationsRetainsNewGenerationDuringRefresh(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{})
	now := time.Now().UTC().Truncate(time.Millisecond)
	invalidation := models.ZFSCacheInvalidation{
		Kind:         db.ZFSCacheKindSnapshot,
		Generation:   1,
		FirstDirtyAt: now.Add(-time.Minute),
		LastDirtyAt:  now.Add(-time.Minute),
	}
	if err := conn.Create(&invalidation).Error; err != nil {
		t.Fatalf("failed creating invalidation: %v", err)
	}

	refreshes := 0
	err := processCacheInvalidations(context.Background(), conn, now, func(context.Context, string) error {
		refreshes++
		return conn.Model(&models.ZFSCacheInvalidation{}).
			Where("id = ?", invalidation.ID).
			Updates(map[string]any{
				"generation":    gorm.Expr("generation + 1"),
				"last_dirty_at": now,
			}).Error
	})
	if err != nil {
		t.Fatalf("failed processing invalidation: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("expected one refresh, got %d", refreshes)
	}

	var remaining models.ZFSCacheInvalidation
	if err := conn.First(&remaining, invalidation.ID).Error; err != nil {
		t.Fatalf("new generation was lost: %v", err)
	}
	if remaining.Generation != 2 {
		t.Fatalf("expected generation 2 to remain pending, got %d", remaining.Generation)
	}
	if !remaining.FirstDirtyAt.Equal(remaining.LastDirtyAt) {
		t.Fatalf("expected maximum-delay window to restart, got first=%v last=%v", remaining.FirstDirtyAt, remaining.LastDirtyAt)
	}

	if err := processCacheInvalidations(context.Background(), conn, now.Add(time.Second), func(context.Context, string) error {
		refreshes++
		return nil
	}); err != nil {
		t.Fatalf("failed rechecking debounce: %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("expected new generation to be debounced, got %d refreshes", refreshes)
	}

	if err := processCacheInvalidations(context.Background(), conn, now.Add(3*time.Second), func(context.Context, string) error {
		refreshes++
		return nil
	}); err != nil {
		t.Fatalf("failed processing new generation: %v", err)
	}
	if refreshes != 2 {
		t.Fatalf("expected new generation to refresh after quiet period, got %d refreshes", refreshes)
	}
}
