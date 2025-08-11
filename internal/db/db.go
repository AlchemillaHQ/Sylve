// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"sylve/internal"
	"sylve/internal/db/models"
	infoModels "sylve/internal/db/models/info"
	jailModels "sylve/internal/db/models/jail"
	networkModels "sylve/internal/db/models/network"
	sambaModels "sylve/internal/db/models/samba"
	utilitiesModels "sylve/internal/db/models/utilities"
	vmModels "sylve/internal/db/models/vm"
	zfsModels "sylve/internal/db/models/zfs"
	"sylve/internal/logger"
	"sylve/pkg/system"
	"sylve/pkg/utils"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

func SetupDatabase(cfg *internal.SylveConfig, isTest bool) *gorm.DB {
	ormConfig := &gorm.Config{
		Logger:         gormLogger.Default.LogMode(gormLogger.Silent),
		TranslateError: true,
	}

	var db *gorm.DB
	var err error

	if isTest {
		db, err = gorm.Open(sqlite.Open(":memory:"), ormConfig)
	} else {
		db, err = gorm.Open(sqlite.Open(cfg.DataPath+"/sylve.db"), ormConfig)
	}

	if err != nil {
		logger.L.Fatal().Msgf("Error connecting to database: %v", err)
	}

	db.Exec("PRAGMA foreign_keys = OFF")
	db.Exec("PRAGMA journal_mode = WAL")
	db.Exec("PRAGMA synchronous = NORMAL")

	err = db.AutoMigrate(
		&models.System{},
		&models.User{},
		&models.Group{},
		&models.Token{},
		&models.SystemSecrets{},

		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VM{},

		&jailModels.Network{},
		&jailModels.JailStats{},
		&jailModels.Jail{},

		&models.PassedThroughIDs{},

		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},

		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.Swap{},
		&infoModels.IODelay{},
		&infoModels.NetworkInterface{},
		&infoModels.Note{},
		&infoModels.AuditRecord{},

		&infoModels.ZPoolHistorical{},

		&zfsModels.PeriodicSnapshot{},

		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},

		&utilitiesModels.DownloadedFile{},
		&utilitiesModels.Downloads{},

		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
		&sambaModels.SambaAuditLog{},
	)

	if err != nil {
		logger.L.Fatal().Msgf("Error migrating database: %v", err)
	}

	db.Exec("PRAGMA foreign_keys = ON")

	err = setupInitUsers(db, cfg)

	if err != nil {
		logger.L.Fatal().Msgf("Error setting up initial users: %v", err)
	}

	if !isTest {
		if err := db.Exec("VACUUM").Error; err != nil {
			logger.L.Warn().Msgf("VACUUM failed: %v", err)
		}
	}

	err = Fixups(db)

	if err != nil {
		logger.L.Fatal().Msgf("Error applying database fixups: %v", err)
	}

	return db
}

func setupInitUsers(db *gorm.DB, cfg *internal.SylveConfig) error {
	const username = "admin"
	adminCfg := cfg.Admin

	var user models.User
	result := db.Where("username = ?", username).First(&user)

	hashed, err := utils.HashPassword(adminCfg.Password)
	if err != nil {
		logger.L.Error().Msgf("Failed to hash password for admin user: %v", err)
		return err
	}

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			newUser := models.User{
				Username: username,
				Email:    adminCfg.Email,
				Password: hashed,
				Admin:    true,
			}
			if err := db.Create(&newUser).Error; err != nil {
				logger.L.Error().Msgf("Failed to create admin user: %v", err)
				return err
			}
			logger.L.Info().Msg("Admin user created successfully")
		} else {
			logger.L.Error().Msgf("Error querying admin user: %v", result.Error)
			return result.Error
		}
	} else {
		if user.Email == adminCfg.Email && utils.CheckPasswordHash(adminCfg.Password, user.Password) && user.Admin {
			logger.L.Debug().Msg("Admin user upto date, no changes needed")
			return nil
		}

		updates := map[string]interface{}{
			"email":    adminCfg.Email,
			"password": hashed,
			"admin":    true,
		}

		if err := db.Model(&user).Updates(updates).Error; err != nil {
			logger.L.Error().Msgf("Failed to update admin user: %v", err)
			return err
		}

		logger.L.Info().Msg("Admin user updated successfully")
	}

	exists, err := system.UnixUserExists(username)
	if err != nil {
		logger.L.Error().Msgf("Error checking Unix user 'admin': %v", err)
	}
	if !exists {
		err := system.CreateUnixUser(username, "/usr/sbin/nologin", "/nonexistent")
		if err != nil {
			logger.L.Error().Msgf("Failed to create Unix user 'admin': %v", err)
			return err
		}
		logger.L.Info().Msg("Unix user 'admin' created successfully")
	}

	return nil
}
