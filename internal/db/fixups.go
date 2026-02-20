// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"gorm.io/gorm"
)

func Fixups(db *gorm.DB) error {
	runNetworkDeltaMigration(db)
	createSylveUnixGroup(db)

	return nil
}

func PreMigrationFixups(db *gorm.DB) {
	deduplicateJailHooks(db)
}

func runNetworkDeltaMigration(db *gorm.DB) {
	const name = "network_interface_delta_migration_2"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed")
		return
	}

	if count > 0 {
		return
	}

	if err := db.Exec(`DELETE FROM network_interfaces`).Error; err != nil {
		logger.L.Err(err).Msg("failed deleting network interfaces")
		return
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func createSylveUnixGroup(db *gorm.DB) {
	var count int64
	if err := db.
		Table("groups").
		Where("name = ?", "sylve_g").
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("failed checking for sylve unix group")
		return
	}

	if count > 0 {
		exists := system.UnixGroupExists("sylve_g")
		if !exists {
			if err := system.CreateUnixGroup("sylve_g"); err != nil {
				logger.L.Err(err).Msg("failed creating sylve unix group")
			}
		}

		return
	}

	if err := db.Exec(`INSERT INTO groups (name, notes, created_at, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "sylve_g", "Default sylve unix group").Error; err != nil {
		logger.L.Err(err).Msg("failed creating sylve unix group")
		return
	}

	exists := system.UnixGroupExists("sylve_g")
	if !exists {
		if err := system.CreateUnixGroup("sylve_g"); err != nil {
			logger.L.Err(err).Msg("failed creating sylve unix group")
		}
	}
}

/**
 * A previous migration omitted a primary key on the jail_hooks table,
 * which caused runaway duplicate entries during association updates and
 * made it impossible to target specific hooks.
 * * This fixup deduplicates the existing entries so GORM can safely apply
 * an auto-incrementing primary key during the subsequent AutoMigrate pass.
 */
func deduplicateJailHooks(db *gorm.DB) {
	const name = "deduplicate_jail_hooks_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for deduplicate_jail_hooks")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("jail_hooks") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			CREATE TABLE jail_hooks_temp (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				jid INTEGER,
				phase TEXT,
				enabled BOOLEAN,
				script TEXT
			)
		`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO jail_hooks_temp (jid, phase, enabled, script)
			SELECT DISTINCT jid, phase, enabled, script FROM jail_hooks
		`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`DROP TABLE jail_hooks`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`ALTER TABLE jail_hooks_temp RENAME TO jail_hooks`).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		logger.L.Err(err).Msg("failed deduplicating jail hooks")
		return
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})

	logger.L.Info().Msg("Deduplicated jail hooks and migrated schema")
}
