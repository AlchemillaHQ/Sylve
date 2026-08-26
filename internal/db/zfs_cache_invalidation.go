// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	ZFSCacheKindGenericDataset = "generic-dataset"
	ZFSCacheKindSnapshot       = "snapshot"
)

func IsZFSCacheInvalidationKind(kind string) bool {
	return kind == ZFSCacheKindGenericDataset || kind == ZFSCacheKindSnapshot
}

func InvalidateZFSCache(conn *gorm.DB, kind string) error {
	return invalidateZFSCacheAt(conn, kind, time.Now().UTC())
}

func InvalidateZFSCaches(conn *gorm.DB) error {
	now := time.Now().UTC()
	for _, kind := range []string{ZFSCacheKindGenericDataset, ZFSCacheKindSnapshot} {
		if err := invalidateZFSCacheAt(conn, kind, now); err != nil {
			return fmt.Errorf("invalidate %s zfs cache: %w", kind, err)
		}
	}
	return nil
}

func invalidateZFSCacheAt(conn *gorm.DB, kind string, now time.Time) error {
	if conn == nil {
		return fmt.Errorf("zfs cache invalidation database is nil")
	}
	if !IsZFSCacheInvalidationKind(kind) {
		return fmt.Errorf("unsupported zfs cache invalidation kind %q", kind)
	}

	return conn.Exec(`
		INSERT INTO zfs_cache_invalidations
			(kind, generation, first_dirty_at, last_dirty_at)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(kind) DO UPDATE SET
			generation = generation + 1,
			last_dirty_at = excluded.last_dirty_at
	`, kind, now, now).Error
}
