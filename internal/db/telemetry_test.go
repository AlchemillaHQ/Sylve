// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateSambaAuditLogsToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []sambaModels.SambaAuditLog{
		{
			ID:        11,
			Share:     "alpha",
			User:      "user-a",
			IP:        "10.0.0.1",
			Action:    "mkdirat",
			Result:    "ok",
			Path:      "/alpha",
			Target:    "",
			Folder:    "alpha",
			CreatedAt: now,
		},
		{
			ID:        12,
			Share:     "beta",
			User:      "user-b",
			IP:        "10.0.0.2",
			Action:    "create_file",
			Result:    "ok",
			Path:      "/beta/file.txt",
			Target:    "",
			Folder:    "beta",
			CreatedAt: now.Add(1 * time.Second),
		},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy audit logs: %v", err)
	}

	dropped, err := migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful migration")
	}

	var got []sambaModels.SambaAuditLog
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry logs: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&sambaModels.SambaAuditLog{}) {
		t.Fatal("expected legacy samba_audit_logs table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", sambaAuditLogsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateSambaAuditLogsToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []sambaModels.SambaAuditLog{
		{ID: 1, Share: "s1", User: "u1", IP: "1.1.1.1", Action: "mkdirat", Result: "ok", Path: "/a", Folder: "a", CreatedAt: now},
		{ID: 2, Share: "s2", User: "u2", IP: "2.2.2.2", Action: "create_file", Result: "ok", Path: "/b", Folder: "b", CreatedAt: now.Add(time.Second)},
		{ID: 3, Share: "s3", User: "u3", IP: "3.3.3.3", Action: "renameat", Result: "ok", Path: "/c", Folder: "c", CreatedAt: now.Add(2 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy audit logs: %v", err)
	}

	if err := telemetryDB.Create(&sambaModels.SambaAuditLog{
		ID:        1,
		Share:     "s1",
		User:      "u1",
		IP:        "1.1.1.1",
		Action:    "mkdirat",
		Result:    "ok",
		Path:      "/a",
		Folder:    "a",
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting row: %v", err)
	}

	dropped, err := migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy table")
	}

	dropped, err = migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&sambaModels.SambaAuditLog{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", sambaAuditLogsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateSambaAuditLogsToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", sambaAuditLogsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration marker on fresh-install path, got %d", migrationCount)
	}
}

func TestMigrateCPUStatsToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.CPU{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.CPU{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.CPU{
		{ID: 11, Usage: 18.5, CreatedAt: now},
		{ID: 12, Usage: 29.7, CreatedAt: now.Add(1 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy cpu rows: %v", err)
	}

	dropped, err := migrateCPUStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful cpu migration")
	}

	var got []infoModels.CPU
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry cpu rows: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&infoModels.CPU{}) {
		t.Fatal("expected legacy cpus table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", cpuStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking cpu migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected cpu migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateCPUStatsToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.CPU{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.CPU{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.CPU{
		{ID: 1, Usage: 5.1, CreatedAt: now},
		{ID: 2, Usage: 15.3, CreatedAt: now.Add(1 * time.Second)},
		{ID: 3, Usage: 25.4, CreatedAt: now.Add(2 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy cpu rows: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.CPU{
		ID:        1,
		Usage:     5.1,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting cpu row: %v", err)
	}

	dropped, err := migrateCPUStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy cpu table")
	}

	dropped, err = migrateCPUStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.CPU{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry cpu rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry cpu rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", cpuStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking cpu migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected cpu migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateCPUStatsToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.CPU{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateCPUStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("cpu migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install cpu path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", cpuStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking cpu migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected cpu migration marker on fresh-install path, got %d", migrationCount)
	}
}

func openSQLiteFileDB(t *testing.T, path string) *gorm.DB {
	t.Helper()

	dbConn, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed opening sqlite file db at %s: %v", path, err)
	}

	sqlDB, err := dbConn.DB()
	if err != nil {
		t.Fatalf("failed getting sql handle for %s: %v", path, err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return dbConn
}
