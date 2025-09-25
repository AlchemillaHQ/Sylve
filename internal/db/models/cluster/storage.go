// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClusterS3Config struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"uniqueIndex" json:"name"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

type ClusterDirectoryConfig struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex" json:"name"`
	Path string `json:"path"`
}

func upsertS3Cfg(db *gorm.DB, n *ClusterS3Config) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if n.ID == 0 {
			var next uint
			if err := tx.
				Table("cluster_s3_configs").
				Select("COALESCE(MAX(id), 0) + 1").
				Scan(&next).Error; err != nil {
				return err
			}
			n.ID = next
		}

		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "endpoint", "region", "bucket", "access_key", "secret_key"}),
		}).Create(n).Error
	})
}

func upsertDirCfg(db *gorm.DB, n *ClusterDirectoryConfig) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if n.ID == 0 {
			var next uint
			if err := tx.
				Table("cluster_directory_configs").
				Select("COALESCE(MAX(id), 0) + 1").
				Scan(&next).Error; err != nil {
				return err
			}
			n.ID = next
		}

		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "path"}),
		}).Create(n).Error
	})
}
