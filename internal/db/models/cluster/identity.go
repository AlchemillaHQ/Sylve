// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClusterIdentity struct {
	ID string `json:"id"`
}

func ReadClusterID(db *gorm.DB) (string, error) {
	var option ClusterOption
	err := db.Select("cluster_id").Where("id = ?", 1).Limit(1).Find(&option).Error
	return option.ClusterID, err
}

// The ID survives membership changes, and distinguishes retained user copies from another
// cluster's accounts. Initializing an existing cluster also binds its current local copies.
func initializeClusterIdentityTx(tx *gorm.DB, identity *ClusterIdentity) error {
	if identity.ID == "" {
		return fmt.Errorf("cluster_identity_required")
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ClusterOption{ID: 1}).Error; err != nil {
		return err
	}
	result := tx.Model(&ClusterOption{}).Where("id = ? AND cluster_id = ''", 1).Update("cluster_id", identity.ID)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	return tx.Model(&models.User{}).Where("source = ? AND shared AND username IN (SELECT username FROM sylve_users)", "local").
		Updates(map[string]any{
			"shared_cluster_id": identity.ID,
			"shared_user_id":    gorm.Expr("(SELECT id FROM sylve_users WHERE sylve_users.username = users.username)"),
		}).Error
}
