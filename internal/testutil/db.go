// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package testutil

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func NewSQLiteTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if len(migrateModels) > 0 {
		if err := db.AutoMigrate(migrateModels...); err != nil {
			t.Fatalf("failed to migrate test tables: %v", err)
		}
	}

	return db
}
