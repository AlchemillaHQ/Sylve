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
	"slices"
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

	if err := mainDB.AutoMigrate(&models.Migrations{}, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate legacy db tables: %v", err)
	}
	if err := telemetryDB.AutoMigrate(&sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("failed to migrate telemetry db tables: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	shares := []sambaModels.SambaShare{
		{Name: "alpha", Dataset: "alpha-guid", AuditRetentionDays: sambaModels.AuditRetentionDaysPointer(14)},
		{Name: "beta", Dataset: "beta-guid", AuditRetentionDays: sambaModels.AuditRetentionDaysPointer(0)},
	}
	if err := mainDB.Create(&shares).Error; err != nil {
		t.Fatalf("failed to seed Samba shares: %v", err)
	}
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
	if got[0].ShareID != uint(shares[0].ID) || sambaModels.AuditRetentionDaysValue(got[0].RetentionDays) != 14 {
		t.Fatalf("alpha audit retention metadata was not migrated: %+v", got[0])
	}
	if got[1].ShareID != uint(shares[1].ID) || sambaModels.AuditRetentionDaysValue(got[1].RetentionDays) != 0 {
		t.Fatalf("beta unlimited audit retention was not migrated: %+v", got[1])
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

// telemetryMigrationCase is one legacy table that moves from the main DB to the telemetry DB.
type telemetryMigrationCase struct {
	name    string
	model   any
	marker  string
	migrate func(mainDB, telemetryDB *gorm.DB, mainPath string) (bool, error)
	rows    func(now time.Time, ids ...uint) any // pointer to a slice of rows with these IDs
}

func telemetryMigrationCases() []telemetryMigrationCase {
	at := func(now time.Time, i int) time.Time { return now.Add(time.Duration(i) * time.Second) }
	userID := uint(7)
	return []telemetryMigrationCase{
		{"cpu", &infoModels.CPU{}, cpuStatsTelemetryMigrationName, migrateCPUStatsToTelemetry, func(now time.Time, ids ...uint) any {
			rows := []infoModels.CPU{}
			for i, id := range ids {
				rows = append(rows, infoModels.CPU{ID: id, Usage: 10 + float64(i), CreatedAt: at(now, i)})
			}
			return &rows
		}},
		{"ram", &infoModels.RAM{}, ramStatsTelemetryMigrationName, migrateRAMStatsToTelemetry, func(now time.Time, ids ...uint) any {
			rows := []infoModels.RAM{}
			for i, id := range ids {
				rows = append(rows, infoModels.RAM{ID: id, Usage: 10 + float64(i), CreatedAt: at(now, i)})
			}
			return &rows
		}},
		{"swap", &infoModels.Swap{}, swapStatsTelemetryMigrationName, migrateSwapStatsToTelemetry, func(now time.Time, ids ...uint) any {
			rows := []infoModels.Swap{}
			for i, id := range ids {
				rows = append(rows, infoModels.Swap{ID: id, Usage: 10 + float64(i), CreatedAt: at(now, i)})
			}
			return &rows
		}},
		{"audit records", &infoModels.AuditRecord{}, auditRecordsTelemetryMigrationName, migrateAuditRecordsToTelemetry, func(now time.Time, ids ...uint) any {
			rows := []infoModels.AuditRecord{}
			for i, id := range ids {
				rows = append(rows, infoModels.AuditRecord{
					ID: id, UserID: &userID, User: "alpha", AuthType: "password", Node: "node-a",
					Started: at(now, i), Ended: at(now, i+2), Action: `{"method":"POST"}`, Duration: 2 * time.Second,
					Status: "success", CreatedAt: at(now, i), UpdatedAt: at(now, i), Version: 2,
				})
			}
			return &rows
		}},
		{"network interfaces", &infoModels.NetworkInterface{}, networkStatsTelemetryMigrationName, migrateNetworkInterfacesToTelemetry, func(now time.Time, ids ...uint) any {
			rows := []infoModels.NetworkInterface{}
			for i, id := range ids {
				rows = append(rows, infoModels.NetworkInterface{
					ID: id, ReceivedBytes: 1000 + int64(i)*500, SentBytes: 500 + int64(i)*400, CreatedAt: at(now, i), UpdatedAt: at(now, i),
				})
			}
			return &rows
		}},
	}
}

func TestMigrateTelemetryTables(t *testing.T) {
	open := func(t *testing.T) (*gorm.DB, *gorm.DB, string) {
		tmp := t.TempDir()
		mainPath := filepath.Join(tmp, "sylve.db")
		return openSQLiteFileDB(t, mainPath), openSQLiteFileDB(t, filepath.Join(tmp, "telemetry.db")), mainPath
	}
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	requireMarkerOnce := func(t *testing.T, mainDB *gorm.DB, marker string) {
		t.Helper()
		var count int64
		must(t, mainDB.Table("migrations").Where("name = ?", marker).Count(&count).Error)
		if count != 1 {
			t.Fatalf("expected migration marker %s once, got %d", marker, count)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)

	for _, tc := range telemetryMigrationCases() {
		t.Run(tc.name+"/copies rows and drops legacy table", func(t *testing.T) {
			mainDB, telemetryDB, mainPath := open(t)
			must(t, mainDB.AutoMigrate(&models.Migrations{}, tc.model))
			must(t, telemetryDB.AutoMigrate(tc.model))
			must(t, mainDB.Create(tc.rows(now, 11, 12)).Error)

			dropped, err := tc.migrate(mainDB, telemetryDB, mainPath)
			if err != nil || !dropped {
				t.Fatalf("expected migration to drop the legacy table, dropped=%v err=%v", dropped, err)
			}
			var ids []uint
			must(t, telemetryDB.Model(tc.model).Order("id ASC").Pluck("id", &ids).Error)
			if !slices.Equal(ids, []uint{11, 12}) {
				t.Fatalf("expected legacy IDs [11 12] to be preserved, got %v", ids)
			}
			if mainDB.Migrator().HasTable(tc.model) {
				t.Fatal("expected legacy table to be dropped")
			}
			requireMarkerOnce(t, mainDB, tc.marker)
		})

		t.Run(tc.name+"/idempotent after partial target state", func(t *testing.T) {
			mainDB, telemetryDB, mainPath := open(t)
			must(t, mainDB.AutoMigrate(&models.Migrations{}, tc.model))
			must(t, telemetryDB.AutoMigrate(tc.model))
			must(t, mainDB.Create(tc.rows(now, 1, 2, 3)).Error)
			must(t, telemetryDB.Create(tc.rows(now, 1)).Error)

			if dropped, err := tc.migrate(mainDB, telemetryDB, mainPath); err != nil || !dropped {
				t.Fatalf("expected first run to drop the legacy table, dropped=%v err=%v", dropped, err)
			}
			if dropped, err := tc.migrate(mainDB, telemetryDB, mainPath); err != nil || dropped {
				t.Fatalf("expected second run to be a no-op, dropped=%v err=%v", dropped, err)
			}
			var count int64
			must(t, telemetryDB.Model(tc.model).Count(&count).Error)
			if count != 3 {
				t.Fatalf("expected 3 telemetry rows after idempotent migration, got %d", count)
			}
			requireMarkerOnce(t, mainDB, tc.marker)
		})

		t.Run(tc.name+"/fresh install without legacy table", func(t *testing.T) {
			mainDB, telemetryDB, mainPath := open(t)
			must(t, mainDB.AutoMigrate(&models.Migrations{}))
			must(t, telemetryDB.AutoMigrate(tc.model))

			if dropped, err := tc.migrate(mainDB, telemetryDB, mainPath); err != nil || dropped {
				t.Fatalf("expected no legacy table to drop on fresh install, dropped=%v err=%v", dropped, err)
			}
			requireMarkerOnce(t, mainDB, tc.marker)
		})
	}
}

func TestPrepareZFSHistoryTablesResetsLegacyDataOnce(t *testing.T) {
	tmp := t.TempDir()
	mainDB := openSQLiteFileDB(t, filepath.Join(tmp, "sylve.db"))
	telemetryDB := openSQLiteFileDB(t, filepath.Join(tmp, "telemetry.db"))

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}
	if err := mainDB.Exec(`CREATE TABLE z_pool_historicals (id integer primary key, name text)`).Error; err != nil {
		t.Fatalf("failed creating legacy main zpool history table: %v", err)
	}
	if err := mainDB.Exec(`INSERT INTO z_pool_historicals (id, name) VALUES (1, 'legacy-main')`).Error; err != nil {
		t.Fatalf("failed seeding legacy main zpool history: %v", err)
	}
	if err := telemetryDB.Exec(`CREATE TABLE z_pool_historicals (id integer primary key, name text)`).Error; err != nil {
		t.Fatalf("failed creating legacy telemetry zpool history table: %v", err)
	}
	if err := telemetryDB.Exec(`INSERT INTO z_pool_historicals (id, name) VALUES (1, 'legacy-telemetry')`).Error; err != nil {
		t.Fatalf("failed seeding legacy telemetry zpool history: %v", err)
	}
	if err := telemetryDB.Exec(`CREATE TABLE zfsarc_historicals (id integer primary key, size integer)`).Error; err != nil {
		t.Fatalf("failed creating legacy ARC history table: %v", err)
	}
	if err := telemetryDB.Exec(`INSERT INTO zfsarc_historicals (id, size) VALUES (1, 1024)`).Error; err != nil {
		t.Fatalf("failed seeding legacy ARC history: %v", err)
	}

	dropped, err := prepareZFSHistoryTables(mainDB, telemetryDB)
	if err != nil {
		t.Fatalf("failed preparing ZFS history tables: %v", err)
	}
	if !dropped {
		t.Fatal("expected legacy main zpool history table to be dropped")
	}
	if mainDB.Migrator().HasTable(&infoModels.ZPoolHistorical{}) {
		t.Fatal("legacy main zpool history table still exists")
	}
	if !telemetryDB.Migrator().HasColumn(&infoModels.ZPoolHistorical{}, "SampleCount") {
		t.Fatal("current zpool history schema was not created")
	}
	if !telemetryDB.Migrator().HasColumn(&infoModels.ZFSARCHistorical{}, "TargetSize") {
		t.Fatal("current ARC history schema was not created")
	}

	var poolCount int64
	if err := telemetryDB.Model(&infoModels.ZPoolHistorical{}).Count(&poolCount).Error; err != nil {
		t.Fatalf("failed counting reset zpool history: %v", err)
	}
	var arcCount int64
	if err := telemetryDB.Model(&infoModels.ZFSARCHistorical{}).Count(&arcCount).Error; err != nil {
		t.Fatalf("failed counting reset ARC history: %v", err)
	}
	if poolCount != 0 || arcCount != 0 {
		t.Fatalf("expected reset history tables to be empty, got pool=%d ARC=%d", poolCount, arcCount)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := telemetryDB.Create(&infoModels.ZPoolHistorical{GUID: "pool-1", Name: "tank", CreatedAt: now}).Error; err != nil {
		t.Fatalf("failed seeding current zpool history: %v", err)
	}
	if err := telemetryDB.Create(&infoModels.ZFSARCHistorical{Size: 2048, CreatedAt: now}).Error; err != nil {
		t.Fatalf("failed seeding current ARC history: %v", err)
	}

	dropped, err = prepareZFSHistoryTables(mainDB, telemetryDB)
	if err != nil {
		t.Fatalf("second ZFS history preparation failed: %v", err)
	}
	if dropped {
		t.Fatal("expected second ZFS history preparation to preserve current tables")
	}
	if err := telemetryDB.Model(&infoModels.ZPoolHistorical{}).Count(&poolCount).Error; err != nil {
		t.Fatalf("failed counting preserved zpool history: %v", err)
	}
	if err := telemetryDB.Model(&infoModels.ZFSARCHistorical{}).Count(&arcCount).Error; err != nil {
		t.Fatalf("failed counting preserved ARC history: %v", err)
	}
	if poolCount != 1 || arcCount != 1 {
		t.Fatalf("expected current history to survive restart, got pool=%d ARC=%d", poolCount, arcCount)
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", zfsTelemetryFreshStartMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking ZFS fresh-start marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected one ZFS fresh-start marker, got %d", migrationCount)
	}
}

func TestPrepareZFSHistoryTablesCreatesFreshSchema(t *testing.T) {
	tmp := t.TempDir()
	mainDB := openSQLiteFileDB(t, filepath.Join(tmp, "sylve.db"))
	telemetryDB := openSQLiteFileDB(t, filepath.Join(tmp, "telemetry.db"))

	if err := mainDB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatalf("failed to migrate main db migrations table: %v", err)
	}

	dropped, err := prepareZFSHistoryTables(mainDB, telemetryDB)
	if err != nil {
		t.Fatalf("failed preparing fresh ZFS history tables: %v", err)
	}
	if dropped {
		t.Fatal("expected no legacy main table to be dropped on fresh install")
	}
	if !telemetryDB.Migrator().HasTable(&infoModels.ZPoolHistorical{}) {
		t.Fatal("fresh zpool history table was not created")
	}
	if !telemetryDB.Migrator().HasTable(&infoModels.ZFSARCHistorical{}) {
		t.Fatal("fresh ARC history table was not created")
	}

	var migrationCount int64
	if err := mainDB.Table("migrations").Where("name = ?", zfsTelemetryFreshStartMigrationName).Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking fresh-install ZFS marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected one fresh-install ZFS marker, got %d", migrationCount)
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
