// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClusterOption struct {
	SSHHostTrustInitialized bool      `gorm:"not null;default:false" json:"sshHostTrustInitialized,omitempty"`
	ID                      uint      `gorm:"primaryKey;autoIncrement:false" json:"id"`
	KeyboardLayout          string    `json:"keyboardLayout"`
	JoinKeyHash             string    `gorm:"not null;default:''" json:"-"`
	CreatedAt               time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt               time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type ClusterJoinKey struct {
	Hash string `json:"hash"`
}

func setClusterJoinKeyTxn(tx *gorm.DB, key *ClusterJoinKey) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"join_key_hash", "updated_at"}),
	}).Create(&ClusterOption{ID: 1, JoinKeyHash: key.Hash}).Error
}

func JoinKeyHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func JoinKeyMatches(db *gorm.DB, secret string) bool {
	var option ClusterOption
	if secret == "" || db.Select("join_key_hash").Where("id = ?", 1).Limit(1).Find(&option).Error != nil ||
		option.JoinKeyHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(JoinKeyHash(secret)), []byte(option.JoinKeyHash)) == 1
}
