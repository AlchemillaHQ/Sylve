// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

func SetupDatabase(cfg *internal.SylveConfig, isTest bool) *gorm.DB {
	ormConfig := &gorm.Config{
		Logger:         gormLogger.Default.LogMode(gormLogger.Warn),
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

	sqlDB, err := db.DB()
	if err != nil {
		logger.L.Fatal().Msgf("Error getting sql database handle: %v", err)
	}

	db.Exec("PRAGMA foreign_keys = OFF")
	db.Exec("PRAGMA busy_timeout = 5000")
	db.Exec("PRAGMA journal_mode = WAL")
	db.Exec("PRAGMA synchronous = NORMAL")

	PreMigrationFixups(db)

	err = db.AutoMigrate(
		&models.BasicSettings{},

		&models.System{},
		&models.User{},
		&models.Group{},
		&models.Token{},
		&models.SystemSecrets{},

		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VM{},

		&jailModels.Network{},
		&jailModels.Storage{},
		&jailModels.JailStats{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Jail{},

		&models.PassedThroughIDs{},
		&models.Triggers{},
		&models.DevdEvent{},

		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},

		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
		// &networkModels.DHCPOption{},

		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.Swap{},
		&infoModels.NetworkInterface{},
		&infoModels.Note{},
		&infoModels.AuditRecord{},

		&infoModels.ZPoolHistorical{},

		&zfsModels.PeriodicSnapshot{},

		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},

		&utilitiesModels.CloudInitTemplate{},
		&utilitiesModels.DownloadedFile{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.WoL{},

		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
		&sambaModels.SambaAuditLog{},

		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.ClusterOption{},
		&clusterModels.ClusterNote{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupEvent{},

		&models.Migrations{},
	)

	if err != nil {
		logger.L.Fatal().Msgf("Error migrating database: %v", err)
	}

	db.Exec("PRAGMA foreign_keys = ON")

	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)

	err = setupInitUsers(db, cfg)
	if err != nil {
		logger.L.Fatal().Msgf("Error setting up initial users: %v", err)
	}

	err = initClusterRecord(db)
	if err != nil {
		logger.L.Fatal().Msgf("Error initializing cluster record: %v", err)
	}

	err = initDHCPConfig(db)
	if err != nil {
		logger.L.Fatal().Msgf("Error initializing DHCP config: %v", err)
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

	db.Model(&models.BasicSettings{}).
		Where("id = ? AND (SELECT COUNT(*) FROM basic_settings) = 1", 1).
		Update("restarted", true)

	return db
}

func setupInitUsers(db *gorm.DB, cfg *internal.SylveConfig) error {
	const username = "admin"
	adminCfg := cfg.Admin

	var user models.User
	result := db.Where("username = ?", username).First(&user)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			hashed, err := utils.HashPassword(adminCfg.Password)
			if err != nil {
				logger.L.Error().Msgf("Failed to hash password for admin user: %v", err)
				return err
			}

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
			logger.L.Info().Msg("Admin user created")
		} else {
			logger.L.Error().Msgf("Error querying admin user: %v", result.Error)
			return result.Error
		}
	} else {
		updates := map[string]any{}
		needsUpdate := false

		if user.Email != adminCfg.Email {
			updates["email"] = adminCfg.Email
			needsUpdate = true
		}

		if !user.Admin {
			updates["admin"] = true
			needsUpdate = true
		}

		if adminCfg.Password != "" {
			if !utils.CheckPasswordHash(adminCfg.Password, user.Password) {
				hashed, err := utils.HashPassword(adminCfg.Password)
				if err != nil {
					logger.L.Error().Msgf("Failed to hash password for admin update: %v", err)
					return err
				}
				updates["password"] = hashed
				needsUpdate = true
			}
		}

		if !needsUpdate {
			logger.L.Debug().Msg("Admin user up to date, no changes needed")
			return nil
		}

		if err := db.Model(&user).Updates(updates).Error; err != nil {
			logger.L.Error().Msgf("Failed to update admin user: %v", err)
			return err
		}

		logger.L.Info().Msg("Admin user updated")
	}

	exists, err := system.UnixUserExists(username)
	if err != nil {
		logger.L.Error().Msgf("Error checking Unix user 'admin': %v", err)
	}
	if !exists {
		err := system.CreateUnixUser(username, "/usr/sbin/nologin", "/nonexistent", "")
		if err != nil {
			logger.L.Error().Msgf("Failed to create Unix user 'admin': %v", err)
			return err
		}
		logger.L.Info().Msg("Unix user 'admin' created")
	}

	return nil
}

func initClusterRecord(db *gorm.DB) error {
	cluster := &clusterModels.Cluster{
		Enabled:       false,
		Key:           "",
		RaftBootstrap: nil,
		RaftIP:        "",
		RaftPort:      0,
	}

	if err := db.Create(cluster).Error; err != nil {
		logger.L.Error().Msgf("Failed to create initial data center record: %v", err)
		return err
	}

	return nil
}

func initDHCPConfig(db *gorm.DB) error {
	var count int64
	if err := db.Model(&networkModels.DHCPConfig{}).Count(&count).Error; err != nil {
		logger.L.Error().Msgf("Failed to query DHCP config count: %v", err)
		return err
	}

	if count > 0 {
		return nil
	}

	dhcpConfig := &networkModels.DHCPConfig{
		StandardSwitches: []networkModels.StandardSwitch{},
		ManualSwitches:   []networkModels.ManualSwitch{},
		DNSServers:       []string{"1.1.1.1", "1.0.0.1", "8.8.8.8"},
		Domain:           "lan",
		ExpandHosts:      true,
	}

	if err := db.Create(dhcpConfig).Error; err != nil {
		logger.L.Error().Msgf("Failed to create initial DHCP config record: %v", err)
		return err
	}

	return nil
}
