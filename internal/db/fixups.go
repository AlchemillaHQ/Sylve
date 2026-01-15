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
