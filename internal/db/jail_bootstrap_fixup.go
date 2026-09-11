// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	legacyJailBootstrapCleanupMigration = "jail_bootstrap_pkgdb_reset_v1"
	legacyJailBootstrapCleanupStarted   = legacyJailBootstrapCleanupMigration + "_started"
	legacyJailBootstrapCleanupPhase     = "legacy_pkgdb_cleanup"
)

var legacyBootstrapPoolPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]*$`)

func cleanupLegacyJailBootstraps(db *gorm.DB) error {
	return cleanupLegacyJailBootstrapsWithZFS(db, gzfs.NewClient(gzfs.Options{}))
}

func cleanupLegacyJailBootstrapsWithZFS(db *gorm.DB, client *gzfs.Client) error {
	if !db.Migrator().HasTable(&jailModels.JailBootstrap{}) {
		return nil
	}
	var applied int64
	if err := db.Model(&models.Migrations{}).
		Where("name = ?", legacyJailBootstrapCleanupMigration).Count(&applied).Error; err != nil {
		return fmt.Errorf("check jail bootstrap cleanup migration: %w", err)
	}
	if applied > 0 {
		return nil
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		var started int64
		if err := tx.Model(&models.Migrations{}).
			Where("name = ?", legacyJailBootstrapCleanupStarted).Count(&started).Error; err != nil {
			return err
		}
		if started > 0 {
			return nil
		}
		var records []jailModels.JailBootstrap
		if err := tx.Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			if !isManagedLegacyJailBootstrap(record) {
				logger.L.Warn().Uint("bootstrapID", record.ID).
					Msg("jail bootstrap cleanup: skipping invalid managed bootstrap identity")
				continue
			}
			if err := tx.Model(&record).Updates(map[string]any{
				"status": "failed",
				"phase":  legacyJailBootstrapCleanupPhase,
				"error":  "",
			}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&models.Migrations{Name: legacyJailBootstrapCleanupStarted}).Error
	}); err != nil {
		return fmt.Errorf("prepare jail bootstrap cleanup: %w", err)
	}

	var pending []jailModels.JailBootstrap
	if err := db.Where("phase = ?", legacyJailBootstrapCleanupPhase).
		Order("id ASC").Find(&pending).Error; err != nil {
		return fmt.Errorf("load jail bootstrap cleanup targets: %w", err)
	}
	for _, record := range pending {
		ctx, cancel := context.WithTimeout(db.Statement.Context, 2*time.Minute)
		err := destroyLegacyJailBootstrap(ctx, client, record)
		cancel()
		if err != nil {
			logger.L.Warn().Err(err).Str("dataset", record.Dataset).
				Msg("jail bootstrap cleanup deferred until next startup")
			continue
		}
		if err := db.Where("id = ? AND phase = ?", record.ID, legacyJailBootstrapCleanupPhase).
			Delete(&jailModels.JailBootstrap{}).Error; err != nil {
			return fmt.Errorf("delete legacy jail bootstrap record %d: %w", record.ID, err)
		}
		logger.L.Info().Str("dataset", record.Dataset).Msg("removed legacy jail bootstrap")
	}

	var remaining int64
	if err := db.Model(&jailModels.JailBootstrap{}).
		Where("phase = ?", legacyJailBootstrapCleanupPhase).Count(&remaining).Error; err != nil {
		return fmt.Errorf("check pending jail bootstrap cleanup: %w", err)
	}
	if remaining == 0 {
		if err := db.Create(&models.Migrations{Name: legacyJailBootstrapCleanupMigration}).Error; err != nil {
			return fmt.Errorf("record jail bootstrap cleanup completion: %w", err)
		}
	}
	return nil
}

func isManagedLegacyJailBootstrap(record jailModels.JailBootstrap) bool {
	if record.ID == 0 || !legacyBootstrapPoolPattern.MatchString(record.Pool) || record.Major <= 0 || record.Minor < 0 {
		return false
	}
	var suffix string
	switch record.BootstrapType {
	case "base":
		suffix = "Base"
	case "minimal":
		suffix = "Minimal"
	default:
		return false
	}
	name := fmt.Sprintf("%d-%d-%s", record.Major, record.Minor, suffix)
	return record.Name == name && record.Dataset == record.Pool+"/sylve/bootstraps/"+name
}

func destroyLegacyJailBootstrap(ctx context.Context, client *gzfs.Client, record jailModels.JailBootstrap) error {
	if !isManagedLegacyJailBootstrap(record) {
		return fmt.Errorf("invalid managed bootstrap identity")
	}
	if client == nil || client.ZFS == nil {
		return fmt.Errorf("bootstrap ZFS client unavailable")
	}
	pool, err := client.ZFS.Get(ctx, record.Pool, false)
	if err != nil {
		return err
	}
	if pool == nil || pool.Name != record.Pool || pool.Type != gzfs.DatasetTypeFilesystem {
		return fmt.Errorf("bootstrap pool unavailable: %s", record.Pool)
	}
	dataset, err := client.ZFS.Get(ctx, record.Dataset, false)
	if err != nil {
		var commandErr *gzfs.CmdError
		if errors.As(err, &commandErr) {
			detail := strings.ToLower(commandErr.Stderr)
			if strings.Contains(detail, "dataset does not exist") || strings.Contains(detail, "no such dataset") {
				return nil
			}
		}
		return err
	}
	if dataset == nil {
		return nil
	}
	if dataset.Name != record.Dataset || dataset.Pool != record.Pool || dataset.Type != gzfs.DatasetTypeFilesystem {
		return fmt.Errorf("resolved bootstrap dataset identity mismatch: %s", record.Dataset)
	}
	return dataset.Destroy(ctx, true, false)
}
