// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type testDownload struct {
	ID  uint `gorm:"primaryKey"`
	URL string
}

func TestGetValNil(t *testing.T) {
	got := GetVal(nil)
	if got != 0 {
		t.Fatalf("expected 0 for nil pointer, got %d", got)
	}
}

func TestGetValNonNil(t *testing.T) {
	id := uint(42)

	got := GetVal(&id)
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestExistsReturnsTrueWhenRecordExists(t *testing.T) {
	db := setupExistsTestDB(t)

	err := db.Create(&testDownload{
		URL: "https://example.com/file.raw",
	}).Error
	if err != nil {
		t.Fatalf("failed to create test record: %v", err)
	}

	exists, err := Exists[testDownload](db, "url = ?", "https://example.com/file.raw")
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}

	if !exists {
		t.Fatal("expected record to exist")
	}
}

func TestExistsReturnsFalseWhenRecordDoesNotExist(t *testing.T) {
	db := setupExistsTestDB(t)

	exists, err := Exists[testDownload](db, "url = ?", "https://example.com/missing.raw")
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}

	if exists {
		t.Fatal("expected record not to exist")
	}
}

func TestExistsReturnsErrorForInvalidQuery(t *testing.T) {
	db := setupExistsTestDB(t)

	_, err := Exists[testDownload](db, "definitely_not_a_column = ?", "value")
	if err == nil {
		t.Fatal("expected error for invalid query, got nil")
	}
}

func setupExistsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}

	err = db.AutoMigrate(&testDownload{})
	if err != nil {
		t.Fatalf("failed to migrate test schema: %v", err)
	}

	return db
}
