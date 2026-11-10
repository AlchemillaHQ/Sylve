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
	"encoding/hex"
	"time"
)

type EncryptionKey struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UUID      string    `gorm:"uniqueIndex;not null" json:"uuid"`
	KeyData   string    `gorm:"type:text;not null" json:"keyData"`
	KeyFormat string    `gorm:"default:passphrase" json:"keyFormat"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

type EncryptionKeyRemoval struct {
	UUID    string `json:"uuid"`
	KeyHash string `json:"keyHash"`
}

type EncryptionKeyTombstone struct {
	UUID    string `gorm:"primaryKey"`
	KeyHash string `gorm:"not null"`
}

func EncryptionKeyHash(keyData string) string {
	sum := sha256.Sum256([]byte(keyData))
	return hex.EncodeToString(sum[:])
}
