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
	"path/filepath"

	"github.com/alchemillahq/sylve/internal"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/logger"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormLogger "gorm.io/gorm/logger"
)

const (
	sambaAuditLogsTelemetryMigrationName = "samba_audit_logs_to_telemetry_1"
	cpuStatsTelemetryMigrationName       = "cpu_stats_to_telemetry_1"
	auditRecordsTelemetryMigrationName   = "audit_records_to_telemetry_1"
)

func SetupTelemetryDatabase(cfg *internal.SylveConfig, mainDB *gorm.DB, isTest bool) *gorm.DB {
	if mainDB == nil {
		logger.L.Fatal().Msg("main database is nil while setting up telemetry database")
	}

	var logMode gormLogger.Interface
	switch cfg.Environment {
	case internal.Development:
		logMode = gormLogger.Default.LogMode(gormLogger.Warn)
	case internal.Debug:
		logMode = gormLogger.Default.LogMode(gormLogger.Info)
	case internal.Production:
		logMode = gormLogger.Default.LogMode(gormLogger.Silent)
	}

	ormConfig := &gorm.Config{
		Logger:                                   logMode,
		TranslateError:                           true,
		DisableForeignKeyConstraintWhenMigrating: true,
	}

	telemetryDBPath := ":memory:"
	mainDBPath := ""

	if !isTest {
		telemetryDBPath = filepath.Join(cfg.DataPath, "telemetry.db")
		mainDBPath = filepath.Join(cfg.DataPath, "sylve.db")
	}

	telemetryDB, err := gorm.Open(sqlite.Open(telemetryDBPath), ormConfig)
	if err != nil {
		logger.L.Fatal().Msgf("Error connecting to telemetry database: %v", err)
	}

	sqlDB, err := telemetryDB.DB()
	if err != nil {
		logger.L.Fatal().Msgf("Error getting telemetry sql database handle: %v", err)
	}

	telemetryDB.Exec("PRAGMA busy_timeout = 5000")
	telemetryDB.Exec("PRAGMA journal_mode = WAL")
	telemetryDB.Exec("PRAGMA synchronous = NORMAL")

	if err := telemetryDB.AutoMigrate(&sambaModels.SambaAuditLog{}, &infoModels.CPU{}, &infoModels.AuditRecord{}); err != nil {
		logger.L.Fatal().Msgf("Error migrating telemetry database: %v", err)
	}

	droppedSambaAuditLogTable, err := migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB, mainDBPath)
	if err != nil {
		logger.L.Fatal().Msgf("Error migrating samba audit logs to telemetry database: %v", err)
	}

	droppedCPUTable, err := migrateCPUStatsToTelemetry(mainDB, telemetryDB, mainDBPath)
	if err != nil {
		logger.L.Fatal().Msgf("Error migrating CPU stats to telemetry database: %v", err)
	}

	droppedAuditRecordTable, err := migrateAuditRecordsToTelemetry(mainDB, telemetryDB, mainDBPath)
	if err != nil {
		logger.L.Fatal().Msgf("Error migrating audit records to telemetry database: %v", err)
	}

	if (droppedSambaAuditLogTable || droppedCPUTable || droppedAuditRecordTable) && !isTest {
		if err := mainDB.Exec("VACUUM").Error; err != nil {
			logger.L.Warn().Msgf("VACUUM failed after dropping legacy telemetry tables: %v", err)
		}
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return telemetryDB
}

func migrateSambaAuditLogsToTelemetry(mainDB, telemetryDB *gorm.DB, mainDBPath string) (bool, error) {
	applied, err := migrationApplied(mainDB, sambaAuditLogsTelemetryMigrationName)
	if err != nil {
		return false, err
	}

	if applied {
		return false, nil
	}

	if !mainDB.Migrator().HasTable(&sambaModels.SambaAuditLog{}) {
		if err := recordMigration(mainDB, sambaAuditLogsTelemetryMigrationName); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := copySambaAuditLogs(mainDB, telemetryDB, mainDBPath); err != nil {
		return false, err
	}

	var legacyCount int64
	if err := mainDB.Model(&sambaModels.SambaAuditLog{}).Count(&legacyCount).Error; err != nil {
		return false, fmt.Errorf("failed to count legacy samba audit logs: %w", err)
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&sambaModels.SambaAuditLog{}).Count(&telemetryCount).Error; err != nil {
		return false, fmt.Errorf("failed to count telemetry samba audit logs: %w", err)
	}

	if telemetryCount < legacyCount {
		return false, fmt.Errorf("telemetry samba audit logs count (%d) is lower than legacy count (%d)", telemetryCount, legacyCount)
	}

	if err := mainDB.Migrator().DropTable(&sambaModels.SambaAuditLog{}); err != nil {
		return false, fmt.Errorf("failed to drop legacy samba audit logs table: %w", err)
	}

	if err := recordMigration(mainDB, sambaAuditLogsTelemetryMigrationName); err != nil {
		return false, err
	}

	return true, nil
}

func copySambaAuditLogs(mainDB, telemetryDB *gorm.DB, mainDBPath string) error {
	if mainDBPath != "" {
		if err := copySambaAuditLogsUsingSQL(telemetryDB, mainDBPath); err == nil {
			return nil
		} else {
			logger.L.Warn().Err(err).Msg("SQL-level telemetry copy failed, falling back to batched copy")
		}
	}

	return copySambaAuditLogsInBatches(mainDB, telemetryDB)
}

func copySambaAuditLogsUsingSQL(telemetryDB *gorm.DB, mainDBPath string) error {
	if err := telemetryDB.Exec("ATTACH DATABASE ? AS legacy_main", mainDBPath).Error; err != nil {
		return fmt.Errorf("failed attaching legacy main database to telemetry db: %w", err)
	}

	detached := false
	defer func() {
		if detached {
			return
		}
		if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
			logger.L.Warn().Err(err).Msg("failed detaching legacy main database from telemetry db")
		}
	}()

	copySQL := `
		INSERT OR IGNORE INTO samba_audit_logs (
			"id", "share", "user", "ip", "action", "result", "path", "target", "folder", "created_at"
		)
		SELECT
			"id", "share", "user", "ip", "action", "result", "path", "target", "folder", "created_at"
		FROM legacy_main.samba_audit_logs
	`

	if err := telemetryDB.Exec(copySQL).Error; err != nil {
		return fmt.Errorf("failed to bulk-copy samba audit logs to telemetry db: %w", err)
	}

	if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
		return fmt.Errorf("failed detaching legacy main database from telemetry db: %w", err)
	}

	detached = true
	return nil
}

func copySambaAuditLogsInBatches(mainDB, telemetryDB *gorm.DB) error {
	const batchSize = 2000

	var batch []sambaModels.SambaAuditLog
	err := mainDB.Model(&sambaModels.SambaAuditLog{}).
		Order("id ASC").
		FindInBatches(&batch, batchSize, func(tx *gorm.DB, _ int) error {
			if len(batch) == 0 {
				return nil
			}

			if err := telemetryDB.
				Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
				Create(&batch).Error; err != nil {
				return fmt.Errorf("failed inserting samba audit log batch into telemetry db: %w", err)
			}

			return nil
		}).Error
	if err != nil {
		return fmt.Errorf("failed to iterate samba audit logs in batches: %w", err)
	}

	return nil
}

func migrateCPUStatsToTelemetry(mainDB, telemetryDB *gorm.DB, mainDBPath string) (bool, error) {
	applied, err := migrationApplied(mainDB, cpuStatsTelemetryMigrationName)
	if err != nil {
		return false, err
	}

	if applied {
		return false, nil
	}

	if !mainDB.Migrator().HasTable(&infoModels.CPU{}) {
		if err := recordMigration(mainDB, cpuStatsTelemetryMigrationName); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := copyCPUStats(mainDB, telemetryDB, mainDBPath); err != nil {
		return false, err
	}

	var legacyCount int64
	if err := mainDB.Model(&infoModels.CPU{}).Count(&legacyCount).Error; err != nil {
		return false, fmt.Errorf("failed to count legacy cpu rows: %w", err)
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.CPU{}).Count(&telemetryCount).Error; err != nil {
		return false, fmt.Errorf("failed to count telemetry cpu rows: %w", err)
	}

	if telemetryCount < legacyCount {
		return false, fmt.Errorf("telemetry cpu row count (%d) is lower than legacy count (%d)", telemetryCount, legacyCount)
	}

	if err := mainDB.Migrator().DropTable(&infoModels.CPU{}); err != nil {
		return false, fmt.Errorf("failed to drop legacy cpu table: %w", err)
	}

	if err := recordMigration(mainDB, cpuStatsTelemetryMigrationName); err != nil {
		return false, err
	}

	return true, nil
}

func copyCPUStats(mainDB, telemetryDB *gorm.DB, mainDBPath string) error {
	if mainDBPath != "" {
		if err := copyCPUStatsUsingSQL(telemetryDB, mainDBPath); err == nil {
			return nil
		} else {
			logger.L.Warn().Err(err).Msg("SQL-level CPU telemetry copy failed, falling back to batched copy")
		}
	}

	return copyCPUStatsInBatches(mainDB, telemetryDB)
}

func copyCPUStatsUsingSQL(telemetryDB *gorm.DB, mainDBPath string) error {
	if err := telemetryDB.Exec("ATTACH DATABASE ? AS legacy_main", mainDBPath).Error; err != nil {
		return fmt.Errorf("failed attaching legacy main database to telemetry db for cpu copy: %w", err)
	}

	detached := false
	defer func() {
		if detached {
			return
		}
		if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
			logger.L.Warn().Err(err).Msg("failed detaching legacy main database from telemetry db after cpu copy")
		}
	}()

	copySQL := `
		INSERT OR IGNORE INTO cpus (
			"id", "usage", "created_at"
		)
		SELECT
			"id", "usage", "created_at"
		FROM legacy_main.cpus
	`

	if err := telemetryDB.Exec(copySQL).Error; err != nil {
		return fmt.Errorf("failed to bulk-copy cpu rows to telemetry db: %w", err)
	}

	if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
		return fmt.Errorf("failed detaching legacy main database from telemetry db after cpu copy: %w", err)
	}

	detached = true
	return nil
}

func copyCPUStatsInBatches(mainDB, telemetryDB *gorm.DB) error {
	const batchSize = 2000

	var batch []infoModels.CPU
	err := mainDB.Model(&infoModels.CPU{}).
		Order("id ASC").
		FindInBatches(&batch, batchSize, func(tx *gorm.DB, _ int) error {
			if len(batch) == 0 {
				return nil
			}

			if err := telemetryDB.
				Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
				Create(&batch).Error; err != nil {
				return fmt.Errorf("failed inserting cpu batch into telemetry db: %w", err)
			}

			return nil
		}).Error
	if err != nil {
		return fmt.Errorf("failed to iterate cpu rows in batches: %w", err)
	}

	return nil
}

func migrateAuditRecordsToTelemetry(mainDB, telemetryDB *gorm.DB, mainDBPath string) (bool, error) {
	applied, err := migrationApplied(mainDB, auditRecordsTelemetryMigrationName)
	if err != nil {
		return false, err
	}

	if applied {
		return false, nil
	}

	if !mainDB.Migrator().HasTable(&infoModels.AuditRecord{}) {
		if err := recordMigration(mainDB, auditRecordsTelemetryMigrationName); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := copyAuditRecords(mainDB, telemetryDB, mainDBPath); err != nil {
		return false, err
	}

	var legacyCount int64
	if err := mainDB.Model(&infoModels.AuditRecord{}).Count(&legacyCount).Error; err != nil {
		return false, fmt.Errorf("failed to count legacy audit records: %w", err)
	}

	var telemetryCount int64
	if err := telemetryDB.Model(&infoModels.AuditRecord{}).Count(&telemetryCount).Error; err != nil {
		return false, fmt.Errorf("failed to count telemetry audit records: %w", err)
	}

	if telemetryCount < legacyCount {
		return false, fmt.Errorf("telemetry audit record count (%d) is lower than legacy count (%d)", telemetryCount, legacyCount)
	}

	if err := mainDB.Migrator().DropTable(&infoModels.AuditRecord{}); err != nil {
		return false, fmt.Errorf("failed to drop legacy audit records table: %w", err)
	}

	if err := recordMigration(mainDB, auditRecordsTelemetryMigrationName); err != nil {
		return false, err
	}

	return true, nil
}

func copyAuditRecords(mainDB, telemetryDB *gorm.DB, mainDBPath string) error {
	if mainDBPath != "" {
		if err := copyAuditRecordsUsingSQL(telemetryDB, mainDBPath); err == nil {
			return nil
		} else {
			logger.L.Warn().Err(err).Msg("SQL-level audit record telemetry copy failed, falling back to batched copy")
		}
	}

	return copyAuditRecordsInBatches(mainDB, telemetryDB)
}

func copyAuditRecordsUsingSQL(telemetryDB *gorm.DB, mainDBPath string) error {
	if err := telemetryDB.Exec("ATTACH DATABASE ? AS legacy_main", mainDBPath).Error; err != nil {
		return fmt.Errorf("failed attaching legacy main database to telemetry db for audit record copy: %w", err)
	}

	detached := false
	defer func() {
		if detached {
			return
		}
		if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
			logger.L.Warn().Err(err).Msg("failed detaching legacy main database from telemetry db after audit record copy")
		}
	}()

	copySQL := `
		INSERT OR IGNORE INTO audit_records (
			"id", "user_id", "user", "auth_type", "node", "started", "ended", "action", "duration", "status", "created_at", "updated_at", "version"
		)
		SELECT
			"id", "user_id", "user", "auth_type", "node", "started", "ended", "action", "duration", "status", "created_at", "updated_at", "version"
		FROM legacy_main.audit_records
	`

	if err := telemetryDB.Exec(copySQL).Error; err != nil {
		return fmt.Errorf("failed to bulk-copy audit records to telemetry db: %w", err)
	}

	if err := telemetryDB.Exec("DETACH DATABASE legacy_main").Error; err != nil {
		return fmt.Errorf("failed detaching legacy main database from telemetry db after audit record copy: %w", err)
	}

	detached = true
	return nil
}

func copyAuditRecordsInBatches(mainDB, telemetryDB *gorm.DB) error {
	const batchSize = 1000

	var batch []infoModels.AuditRecord
	err := mainDB.Model(&infoModels.AuditRecord{}).
		Order("id ASC").
		FindInBatches(&batch, batchSize, func(tx *gorm.DB, _ int) error {
			if len(batch) == 0 {
				return nil
			}

			if err := telemetryDB.
				Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
				Create(&batch).Error; err != nil {
				return fmt.Errorf("failed inserting audit record batch into telemetry db: %w", err)
			}

			return nil
		}).Error
	if err != nil {
		return fmt.Errorf("failed to iterate audit records in batches: %w", err)
	}

	return nil
}

func migrationApplied(mainDB *gorm.DB, name string) (bool, error) {
	var count int64
	if err := mainDB.Table("migrations").Where("name = ?", name).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed checking migration '%s': %w", name, err)
	}

	return count > 0, nil
}

func recordMigration(mainDB *gorm.DB, name string) error {
	if err := mainDB.Table("migrations").Create(map[string]any{"name": name}).Error; err != nil {
		return fmt.Errorf("failed recording migration '%s': %w", name, err)
	}

	return nil
}
