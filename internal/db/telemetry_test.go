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

func TestMigrateAuditRecordsToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.AuditRecord{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	userID := uint(7)
	legacyRows := []infoModels.AuditRecord{
		{
			ID:        11,
			UserID:    &userID,
			User:      "alpha",
			AuthType:  "password",
			Node:      "node-a",
			Started:   now,
			Ended:     now.Add(2 * time.Second),
			Action:    "{\"method\":\"POST\"}",
			Duration:  2 * time.Second,
			Status:    "success",
			CreatedAt: now,
			UpdatedAt: now,
			Version:   2,
		},
		{
			ID:        12,
			User:      "beta",
			AuthType:  "token",
			Node:      "node-a",
			Started:   now.Add(1 * time.Second),
			Ended:     now.Add(3 * time.Second),
			Action:    "{\"method\":\"DELETE\"}",
			Duration:  2 * time.Second,
			Status:    "server_error",
			CreatedAt: now.Add(1 * time.Second),
			UpdatedAt: now.Add(1 * time.Second),
			Version:   2,
		},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy audit rows: %v", err)
	}

	dropped, err := migrateAuditRecordsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful audit migration")
	}

	var got []infoModels.AuditRecord
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry audit rows: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&infoModels.AuditRecord{}) {
		t.Fatal("expected legacy audit_records table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", auditRecordsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking audit migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected audit migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateAuditRecordsToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.AuditRecord{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.AuditRecord{
		{ID: 1, User: "u1", AuthType: "password", Node: "n1", Started: now, Ended: now, Action: "{\"m\":\"GET\"}", Duration: 0, Status: "success", CreatedAt: now, UpdatedAt: now, Version: 2},
		{ID: 2, User: "u2", AuthType: "token", Node: "n1", Started: now.Add(time.Second), Ended: now.Add(time.Second), Action: "{\"m\":\"POST\"}", Duration: 0, Status: "success", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), Version: 2},
		{ID: 3, User: "u3", AuthType: "token", Node: "n1", Started: now.Add(2 * time.Second), Ended: now.Add(2 * time.Second), Action: "{\"m\":\"DELETE\"}", Duration: 0, Status: "client_error", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second), Version: 2},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy audit rows: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.AuditRecord{
		ID:        1,
		User:      "u1",
		AuthType:  "password",
		Node:      "n1",
		Started:   now,
		Ended:     now,
		Action:    "{\"m\":\"GET\"}",
		Duration:  0,
		Status:    "success",
		CreatedAt: now,
		UpdatedAt: now,
		Version:   2,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting audit row: %v", err)
	}

	dropped, err := migrateAuditRecordsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy audit table")
	}

	dropped, err = migrateAuditRecordsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.AuditRecord{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry audit rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry audit rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", auditRecordsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking audit migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected audit migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateAuditRecordsToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.AuditRecord{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateAuditRecordsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("audit migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install audit path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", auditRecordsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking audit migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected audit migration marker on fresh-install path, got %d", migrationCount)
	}
}

func TestMigrateRAMStatsToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.RAM{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.RAM{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.RAM{
		{ID: 11, Usage: 18.5, CreatedAt: now},
		{ID: 12, Usage: 29.7, CreatedAt: now.Add(1 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy ram rows: %v", err)
	}

	dropped, err := migrateRAMStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful ram migration")
	}

	var got []infoModels.RAM
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry ram rows: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&infoModels.RAM{}) {
		t.Fatal("expected legacy rams table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", ramStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking ram migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected ram migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateRAMStatsToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.RAM{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.RAM{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.RAM{
		{ID: 1, Usage: 5.1, CreatedAt: now},
		{ID: 2, Usage: 15.3, CreatedAt: now.Add(1 * time.Second)},
		{ID: 3, Usage: 25.4, CreatedAt: now.Add(2 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy ram rows: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.RAM{
		ID:        1,
		Usage:     5.1,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting ram row: %v", err)
	}

	dropped, err := migrateRAMStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy ram table")
	}

	dropped, err = migrateRAMStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.RAM{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry ram rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry ram rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", ramStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking ram migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected ram migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateRAMStatsToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.RAM{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateRAMStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("ram migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install ram path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", ramStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking ram migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected ram migration marker on fresh-install path, got %d", migrationCount)
	}
}

func TestMigrateSwapStatsToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.Swap{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.Swap{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.Swap{
		{ID: 11, Usage: 18.5, CreatedAt: now},
		{ID: 12, Usage: 29.7, CreatedAt: now.Add(1 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy swap rows: %v", err)
	}

	dropped, err := migrateSwapStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful swap migration")
	}

	var got []infoModels.Swap
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry swap rows: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&infoModels.Swap{}) {
		t.Fatal("expected legacy swaps table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", swapStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking swap migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected swap migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateSwapStatsToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.Swap{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.Swap{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.Swap{
		{ID: 1, Usage: 5.1, CreatedAt: now},
		{ID: 2, Usage: 15.3, CreatedAt: now.Add(1 * time.Second)},
		{ID: 3, Usage: 25.4, CreatedAt: now.Add(2 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy swap rows: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.Swap{
		ID:        1,
		Usage:     5.1,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting swap row: %v", err)
	}

	dropped, err := migrateSwapStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy swap table")
	}

	dropped, err = migrateSwapStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.Swap{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry swap rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry swap rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", swapStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking swap migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected swap migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateSwapStatsToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.Swap{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateSwapStatsToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("swap migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install swap path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", swapStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking swap migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected swap migration marker on fresh-install path, got %d", migrationCount)
	}
}

func TestMigrateNetworkInterfacesToTelemetryCopiesRowsAndDropsLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.NetworkInterface{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.NetworkInterface{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.NetworkInterface{
		{
			ID:              11,
			Name:            "igb0",
			Flags:           "UP",
			IsDelta:         false,
			Network:         "link#1",
			Address:         "aa:bb:cc:dd:ee:ff",
			ReceivedPackets: 10,
			ReceivedBytes:   1000,
			SentPackets:     5,
			SentBytes:       500,
			Collisions:      0,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              12,
			Name:            "igb0",
			Flags:           "UP",
			IsDelta:         false,
			Network:         "link#1",
			Address:         "aa:bb:cc:dd:ee:ff",
			ReceivedPackets: 15,
			ReceivedBytes:   1500,
			SentPackets:     9,
			SentBytes:       900,
			Collisions:      0,
			CreatedAt:       now.Add(1 * time.Second),
			UpdatedAt:       now.Add(1 * time.Second),
		},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy network interface rows: %v", err)
	}

	dropped, err := migrateNetworkInterfacesToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy table to be dropped after successful network interface migration")
	}

	var got []infoModels.NetworkInterface
	if err := telemetryDB.Order("id ASC").Find(&got).Error; err != nil {
		t.Fatalf("failed to read telemetry network interface rows: %v", err)
	}
	if len(got) != len(legacyRows) {
		t.Fatalf("expected %d telemetry rows, got %d", len(legacyRows), len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Fatalf("expected legacy IDs to be preserved, got ids: %d, %d", got[0].ID, got[1].ID)
	}

	if mainDB.Migrator().HasTable(&infoModels.NetworkInterface{}) {
		t.Fatal("expected legacy network_interfaces table to be dropped")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", networkStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking network interface migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected network interface migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateNetworkInterfacesToTelemetryIsIdempotentAfterPartialTargetState(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}, &infoModels.NetworkInterface{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.NetworkInterface{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	legacyRows := []infoModels.NetworkInterface{
		{ID: 1, Name: "igb0", Flags: "UP", IsDelta: false, Network: "link#1", Address: "aa:bb", ReceivedBytes: 100, SentBytes: 100, CreatedAt: now, UpdatedAt: now},
		{ID: 2, Name: "igb0", Flags: "UP", IsDelta: false, Network: "link#1", Address: "aa:bb", ReceivedBytes: 200, SentBytes: 250, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: 3, Name: "igb1", Flags: "UP", IsDelta: false, Network: "link#2", Address: "cc:dd", ReceivedBytes: 50, SentBytes: 70, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	if err := mainDB.Create(&legacyRows).Error; err != nil {
		t.Fatalf("failed to seed legacy network interface rows: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.NetworkInterface{
		ID:            1,
		Name:          "igb0",
		Flags:         "UP",
		IsDelta:       false,
		Network:       "link#1",
		Address:       "aa:bb",
		ReceivedBytes: 100,
		SentBytes:     100,
		CreatedAt:     now,
		UpdatedAt:     now,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry preexisting network interface row: %v", err)
	}

	dropped, err := migrateNetworkInterfacesToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}
	if !dropped {
		t.Fatal("expected first migration run to drop legacy network interfaces table")
	}

	dropped, err = migrateNetworkInterfacesToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second migration run to be a no-op")
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.NetworkInterface{}).Count(&telemetryCount).Error; err != nil {
		t.Fatalf("failed counting telemetry network interface rows: %v", err)
	}
	if telemetryCount != 3 {
		t.Fatalf("expected 3 telemetry network interface rows after idempotent migration, got %d", telemetryCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", networkStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking network interface migration marker count: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected network interface migration marker to be recorded once, got %d", migrationCount)
	}
}

func TestMigrateNetworkInterfacesToTelemetryHandlesFreshInstallWithoutLegacyTable(t *testing.T) {
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "sylve.db")
	telemetryPath := filepath.Join(tmp, "telemetry.db")

	mainDB := openSQLiteFileDB(t, mainPath)
	telemetryDB := openSQLiteFileDB(t, telemetryPath)

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&infoModels.NetworkInterface{}); err != nil {
		t.Fatalf("failed to migrate telemetry db table: %v", err)
	}

	dropped, err := migrateNetworkInterfacesToTelemetry(mainDB, telemetryDB, mainPath)
	if err != nil {
		t.Fatalf("network interface migration failed on fresh-install path: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy table to be dropped on fresh-install network interface path")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", networkStatsTelemetryMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking network interface migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected network interface migration marker on fresh-install path, got %d", migrationCount)
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
