// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestInvalidateZFSCacheCoalescesByKind(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{})
	first := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	last := first.Add(10 * time.Second)

	if err := invalidateZFSCacheAt(conn, ZFSCacheKindSnapshot, first); err != nil {
		t.Fatalf("failed to create initial invalidation: %v", err)
	}
	if err := invalidateZFSCacheAt(conn, ZFSCacheKindSnapshot, last); err != nil {
		t.Fatalf("failed to coalesce invalidation: %v", err)
	}

	var invalidations []models.ZFSCacheInvalidation
	if err := conn.Find(&invalidations).Error; err != nil {
		t.Fatalf("failed to load invalidations: %v", err)
	}
	if len(invalidations) != 1 {
		t.Fatalf("expected one invalidation row, got %d", len(invalidations))
	}
	if invalidations[0].Generation != 2 {
		t.Fatalf("expected generation 2, got %d", invalidations[0].Generation)
	}
	if !invalidations[0].FirstDirtyAt.Equal(first) {
		t.Fatalf("first dirty time changed: got %v, want %v", invalidations[0].FirstDirtyAt, first)
	}
	if !invalidations[0].LastDirtyAt.Equal(last) {
		t.Fatalf("last dirty time not updated: got %v, want %v", invalidations[0].LastDirtyAt, last)
	}
}

func TestReplaceLegacyNetlinkEventsSeedsInvalidationsAndDropsTable(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.ZFSCacheInvalidation{})
	if err := conn.Exec(`CREATE TABLE netlink_events (id INTEGER PRIMARY KEY, raw TEXT)`).Error; err != nil {
		t.Fatalf("failed creating legacy netlink table: %v", err)
	}
	if err := conn.Exec(`INSERT INTO netlink_events (raw) VALUES ('event')`).Error; err != nil {
		t.Fatalf("failed inserting legacy event: %v", err)
	}

	if err := replaceLegacyNetlinkEvents(conn); err != nil {
		t.Fatalf("failed replacing legacy netlink events: %v", err)
	}
	if conn.Migrator().HasTable("netlink_events") {
		t.Fatal("expected legacy netlink_events table to be dropped")
	}

	var invalidations []models.ZFSCacheInvalidation
	if err := conn.Order("kind ASC").Find(&invalidations).Error; err != nil {
		t.Fatalf("failed loading seeded invalidations: %v", err)
	}
	if len(invalidations) != 2 {
		t.Fatalf("expected two seeded invalidations, got %d", len(invalidations))
	}
	for _, invalidation := range invalidations {
		if !IsZFSCacheInvalidationKind(invalidation.Kind) || invalidation.Generation != 1 {
			t.Fatalf("unexpected seeded invalidation: %+v", invalidation)
		}
	}

	if err := replaceLegacyNetlinkEvents(conn); err != nil {
		t.Fatalf("expected migration to be idempotent: %v", err)
	}
	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting invalidations: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected idempotent migration to retain two rows, got %d", count)
	}
}

func TestReplaceLegacyNetlinkEventsRollsBackDropOnMigrationRecordFailure(t *testing.T) {
	conn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.ZFSCacheInvalidation{})
	if err := conn.Exec(`CREATE TABLE netlink_events (id INTEGER PRIMARY KEY, raw TEXT)`).Error; err != nil {
		t.Fatalf("failed creating legacy netlink table: %v", err)
	}
	if err := conn.Exec(`
		CREATE TRIGGER fail_netlink_replacement_migration
		BEFORE INSERT ON migrations
		WHEN NEW.name = 'replace_netlink_events_with_zfs_cache_invalidations_1'
		BEGIN
			SELECT RAISE(ABORT, 'forced migration record failure');
		END
	`).Error; err != nil {
		t.Fatalf("failed creating migration failure trigger: %v", err)
	}

	if err := replaceLegacyNetlinkEvents(conn); err == nil {
		t.Fatal("expected migration record failure")
	}
	if !conn.Migrator().HasTable("netlink_events") {
		t.Fatal("legacy table drop was not rolled back")
	}

	var count int64
	if err := conn.Model(&models.ZFSCacheInvalidation{}).Count(&count).Error; err != nil {
		t.Fatalf("failed checking rolled-back invalidations: %v", err)
	}
	if count != 0 {
		t.Fatalf("seeded invalidations were not rolled back: %d", count)
	}

	if err := conn.Model(&models.Migrations{}).
		Where("name = ?", "replace_netlink_events_with_zfs_cache_invalidations_1").
		Count(&count).Error; err != nil {
		t.Fatalf("failed checking migration record: %v", err)
	}
	if count != 0 {
		t.Fatalf("migration was recorded despite failure: %d", count)
	}
}
