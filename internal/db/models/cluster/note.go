// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClusterNote struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	ContentHTML string    `gorm:"-" json:"contentHtml,omitempty"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func upsertNote(db *gorm.DB, n *ClusterNote) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if n.ID == 0 {
			var next uint
			if err := tx.
				Table("cluster_notes").
				Select("COALESCE(MAX(id), 0) + 1").
				Scan(&next).Error; err != nil {
				return err
			}
			n.ID = next
		}

		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"title", "content", "updated_at"}),
		}).Create(n).Error
	})
}
