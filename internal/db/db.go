// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"errors"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
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

	db.Exec("PRAGMA busy_timeout = 5000")
	db.Exec("PRAGMA journal_mode = WAL")
	db.Exec("PRAGMA synchronous = NORMAL")

	// Pre-migration fixups use the migrations tracking table, so ensure it
	// exists before running any pre-migration logic.
	if err := db.AutoMigrate(&models.Migrations{}); err != nil {
		logger.L.Fatal().Msgf("Error bootstrapping migrations table: %v", err)
	}

	PreMigrationFixups(db)

	err = db.AutoMigrate(
		&models.BasicSettings{},
		&models.Notification{},
		&models.NotificationSuppression{},
		&models.NotificationKindRule{},
		&models.NotificationTransportConfig{},

		&models.System{},
		&models.User{},
		&models.PAMIdentity{},
		&models.Group{},
		&models.Token{},
		&models.WebAuthnCredential{},
		&models.WebAuthnChallenge{},
		&models.SystemSecrets{},

		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMSnapshot{},
		&vmModels.VMTemplate{},
		&vmModels.VM{},

		&jailModels.Network{},
		&jailModels.Storage{},
		&jailModels.JailStats{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.JailTemplate{},
		&jailModels.Jail{},
		&jailModels.JailBootstrap{},

		&models.PassedThroughIDs{},
		&models.Triggers{},
		&models.NetlinkEvent{},
		&models.SystemTunable{},

		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ObjectListSnapshot{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
		&networkModels.FirewallAdvancedSettings{},
		&networkModels.StaticRoute{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{},

		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
		// &networkModels.DHCPOption{},

		&infoModels.Note{},

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

		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},

		&iscsiModels.ISCSIInitiator{},
		&iscsiModels.ISCSITarget{},
		&iscsiModels.ISCSITargetPortal{},
		&iscsiModels.ISCSITargetLUN{},

		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
		&clusterModels.ClusterOption{},
		&clusterModels.ClusterNote{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.BackupEvent{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationReceipt{},
		&clusterModels.ClusterSSHIdentity{},
		&clusterModels.EncryptionKey{},
		&taskModels.GuestLifecycleTask{},

		&models.Migrations{},
	)

	if err != nil {
		logger.L.Fatal().Msgf("Error migrating database: %v", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	err = setupInitUsers(db, cfg)
	if err != nil {
		logger.L.Fatal().Msgf("Error setting up initial users: %v", err)
	}

	if cfg.Admin.ForcePasswordReset {
		logger.L.Warn().Msg("Admin force password reset detected; clearing config flag")
		if err := config.ResetForcePasswordReset(); err != nil {
			logger.L.Error().Msgf("Failed to clear forcePasswordReset flag: %v", err)
		}
	}

	err = initClusterRecord(db)
	if err != nil {
		logger.L.Fatal().Msgf("Error initializing cluster record: %v", err)
	}

	err = initDHCPConfig(db)
	if err != nil {
		logger.L.Fatal().Msgf("Error initializing DHCP config: %v", err)
	}

	err = initFirewallConfig(db)
	if err != nil {
		logger.L.Fatal().Msgf("Error initializing firewall config: %v", err)
	}

	err = Fixups(db)

	if err != nil {
		logger.L.Fatal().Msgf("Error applying database fixups: %v", err)
	}

	err = PruneJobs(db)

	if err != nil {
		logger.L.Error().Err(err).Msgf("Error pruning database of unnecessary records: %v", err)
	}

	if !isTest {
		if err := db.Exec("VACUUM").Error; err != nil {
			logger.L.Warn().Msgf("VACUUM failed: %v", err)
		}
	}

	db.Model(&models.BasicSettings{}).
		Where("id = ? AND (SELECT COUNT(*) FROM basic_settings) = 1", 1).
		Update("restarted", true)

	return db
}

func setupInitUsers(db *gorm.DB, cfg *internal.SylveConfig) error {
	const username = "admin"
	adminCfg := cfg.Admin

	// Import root user if it exists as a Unix user but not in the DB.
	setupRootUser(db)

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
				Password: hashed,
				Admin:    true,
				Source:   "local",
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

		if adminCfg.ForcePasswordReset && adminCfg.Password != "" {
			if !utils.CheckPasswordHash(adminCfg.Password, user.Password) {
				hashed, err := utils.HashPassword(adminCfg.Password)
				if err != nil {
					logger.L.Error().Msgf("Failed to hash password for admin update: %v", err)
					return err
				}
				updates["password"] = hashed
				needsUpdate = true
				logger.L.Warn().Msg("Admin password forcefully reset from config")
			}
		}

		if !needsUpdate {
			logger.L.Debug().Msg("Admin user up to date, no changes needed")
		} else if err := db.Model(&user).Updates(updates).Error; err != nil {
			logger.L.Error().Msgf("Failed to update admin user: %v", err)
			return err
		} else {
			logger.L.Info().Msg("Admin user updated")
		}
	}

	return nil
}

func setupRootUser(db *gorm.DB) {
	const username = "root"

	var existing models.User
	if err := db.Where("username = ?", username).First(&existing).Error; err == nil {
		if !existing.Admin {
			if err := db.Model(&existing).Update("admin", true).Error; err != nil {
				logger.L.Warn().Msgf("Failed to grant admin to root user: %v", err)
			} else {
				logger.L.Info().Msg("Granted admin to root user")
			}
		}
		setupWheelGroup(db, &existing)
		return
	}

	exists, err := system.UnixUserExists(username)
	if err != nil {
		logger.L.Warn().Msgf("Error checking Unix user 'root': %v", err)
		return
	}
	if !exists {
		return
	}

	info, err := system.GetUnixUserInfoFull(username)
	if err != nil {
		logger.L.Warn().Msgf("Failed to get Unix info for root: %v", err)
		return
	}

	rootUser := models.User{
		Username:      username,
		FullName:      info.FullName,
		UID:           info.UID,
		Shell:         info.Shell,
		HomeDirectory: info.HomeDir,
		HomeDirPerms:  493,
		Admin:         true,
		Source:        "pam",
	}
	if err := db.Create(&rootUser).Error; err != nil {
		logger.L.Warn().Msgf("Failed to import root user into DB: %v", err)
		return
	}
	logger.L.Info().Msg("Root user imported into Sylve")

	setupWheelGroup(db, &rootUser)
}

func setupWheelGroup(db *gorm.DB, rootUser *models.User) {
	const groupName = "wheel"

	if !system.UnixGroupExists(groupName) {
		return
	}

	var grp models.Group
	if err := db.Where("name = ?", groupName).First(&grp).Error; err != nil {
		grp = models.Group{Name: groupName}
		if err := db.Create(&grp).Error; err != nil {
			logger.L.Warn().Msgf("Failed to create wheel group record: %v", err)
			return
		}
		logger.L.Info().Msg("Wheel group imported into Sylve")
	}

	inGroup, err := system.IsUserInGroup(rootUser.Username, groupName)
	if err != nil {
		logger.L.Warn().Msgf("Failed to check wheel membership: %v", err)
		return
	}
	if !inGroup {
		return
	}

	if err := db.Model(&grp).Association("Users").Append(rootUser); err != nil {
		logger.L.Warn().Msgf("Failed to associate root with wheel: %v", err)
	}
}

func ensureUserInSylveG(db *gorm.DB, username string) {
	var grp models.Group
	if err := db.Where("name = ?", "sylve_g").First(&grp).Error; err != nil {
		return
	}

	var dbUser models.User
	if err := db.Where("username = ?", username).First(&dbUser).Error; err != nil {
		return
	}

	if err := system.AddUserToGroup(username, "sylve_g"); err != nil {
		logger.L.Warn().Msgf("Failed to add %s to sylve_g unix group: %v", username, err)
	}

	var cnt int64
	if err := db.Table("user_groups").
		Where("user_id = ? AND group_id = ?", dbUser.ID, grp.ID).
		Count(&cnt).Error; err != nil {
		logger.L.Warn().Msgf("Failed to check sylve_g membership for %s: %v", username, err)
		return
	}
	if cnt > 0 {
		return
	}

	if err := db.Model(&grp).Association("Users").Append(&dbUser); err != nil {
		logger.L.Warn().Msgf("Failed to associate %s with sylve_g: %v", username, err)
	}
}

func initClusterRecord(db *gorm.DB) error {
	var keepID uint

	err := db.Model(&clusterModels.Cluster{}).
		Order("key DESC, id ASC").
		Select("id").
		First(&keepID).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			defaultCluster := &clusterModels.Cluster{
				Enabled:       false,
				Key:           "",
				RaftBootstrap: nil,
				RaftIP:        "",
				RaftPort:      8180,
			}

			if err := db.Create(defaultCluster).Error; err != nil {
				logger.L.Error().Msgf("Failed to create initial cluster record: %v", err)
				return err
			}
			return nil
		}

		logger.L.Error().Msgf("Failed to query best cluster record: %v", err)
		return err
	}

	res := db.Where("id != ?", keepID).Delete(&clusterModels.Cluster{})
	if res.Error != nil {
		logger.L.Error().Msgf("Failed to clean up cluster records: %v", res.Error)
		return res.Error
	}

	if res.RowsAffected > 0 {
		logger.L.Info().Msgf("Purged %d duplicate cluster records!", res.RowsAffected)
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

func initFirewallConfig(db *gorm.DB) error {
	var count int64
	if err := db.Model(&networkModels.FirewallAdvancedSettings{}).Count(&count).Error; err != nil {
		logger.L.Error().Msgf("Failed to query firewall config count: %v", err)
		return err
	}

	if count > 0 {
		return nil
	}

	firewallConfig := &networkModels.FirewallAdvancedSettings{
		PreRules:  "",
		PostRules: "",
	}

	if err := db.Create(firewallConfig).Error; err != nil {
		logger.L.Error().Msgf("Failed to create initial firewall config record: %v", err)
		return err
	}

	return nil
}
