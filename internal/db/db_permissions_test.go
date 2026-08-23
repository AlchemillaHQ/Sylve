// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHardenDatabaseFilesRestrictsExistingDatabaseFiles(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "sylve.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.WriteFile(databasePath+suffix, []byte("test"), 0664); err != nil {
			t.Fatalf("create database file %q: %v", suffix, err)
		}
		if err := os.Chmod(databasePath+suffix, 0664); err != nil {
			t.Fatalf("set database file mode %q: %v", suffix, err)
		}
	}

	if err := hardenDatabaseFiles(databasePath); err != nil {
		t.Fatalf("harden database files: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := os.Stat(databasePath + suffix)
		if err != nil {
			t.Fatalf("stat database file %q: %v", suffix, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("database file %q mode = %o, want 600", suffix, info.Mode().Perm())
		}
	}
}

func TestHardenDatabaseFilesAllowsMissingFiles(t *testing.T) {
	if err := hardenDatabaseFiles(filepath.Join(t.TempDir(), "missing.db")); err != nil {
		t.Fatalf("harden missing database files: %v", err)
	}
}

func TestConfigureSQLite(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sylve.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := configureSQLite(database); err != nil {
		t.Fatalf("configure database: %v", err)
	}

	var synchronous int
	if err := database.Raw("PRAGMA synchronous").Scan(&synchronous).Error; err != nil {
		t.Fatalf("read synchronous mode: %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous=%d want=1", synchronous)
	}
	var busyTimeout int
	if err := database.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout=%d want=5000", busyTimeout)
	}
	var journalMode string
	if err := database.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode=%q want=wal", journalMode)
	}

	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close SQL database: %v", err)
	}
	if err := configureSQLite(database); err == nil {
		t.Fatal("expected closed database configuration to fail")
	}
}
