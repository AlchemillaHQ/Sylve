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
	"fmt"
	"os"
	"path/filepath"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

func SetupBackupDatabase(dataPath, clusterKey string) (*gorm.DB, error) {
	if dataPath == "" {
		return nil, fmt.Errorf("backup_data_path_required")
	}

	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return nil, fmt.Errorf("create_backup_data_path: %w", err)
	}

	dbPath := filepath.Join(dataPath, "sylve-backup.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger:         gormLogger.Default.LogMode(gormLogger.Warn),
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open_backup_db: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("backup_sql_handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	db.Exec("PRAGMA foreign_keys = OFF")
	db.Exec("PRAGMA busy_timeout = 5000")
	db.Exec("PRAGMA journal_mode = WAL")
	db.Exec("PRAGMA synchronous = NORMAL")

	if err := db.AutoMigrate(
		&models.SystemSecrets{},
		&clusterModels.Cluster{},
		&clusterModels.BackupReplicationEvent{},
	); err != nil {
		return nil, fmt.Errorf("migrate_backup_db: %w", err)
	}

	var c clusterModels.Cluster
	if err := db.Order("id ASC").First(&c).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load_backup_cluster_record: %w", err)
		}

		c = clusterModels.Cluster{
			Enabled:       false,
			Key:           clusterKey,
			RaftBootstrap: nil,
			RaftIP:        "",
			RaftPort:      0,
		}
		if err := db.Create(&c).Error; err != nil {
			return nil, fmt.Errorf("init_backup_cluster_record: %w", err)
		}
	} else if clusterKey != "" && c.Key != clusterKey {
		if err := db.Model(&c).Update("key", clusterKey).Error; err != nil {
			return nil, fmt.Errorf("update_backup_cluster_key: %w", err)
		}
	}

	db.Exec("PRAGMA foreign_keys = ON")
	return db, nil
}
