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

type ClusterOption struct {
	ID             uint      `gorm:"primaryKey;autoIncrement:false" json:"id"`
	KeyboardLayout string    `json:"keyboardLayout"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func upsertOption(db *gorm.DB, o *ClusterOption) error {
	if o.ID == 0 {
		o.ID = 1
	}
	updatedAt := o.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = replicatedCommandTime(db)
	} else {
		updatedAt = NormalizeCommandTime(updatedAt)
	}
	o.UpdatedAt = updatedAt
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"keyboard_layout": o.KeyboardLayout,
			"updated_at":      updatedAt,
		}),
	}).Create(o).Error
}
