// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

type Object struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"uniqueIndex;not null"`
	Type      string    `json:"type" gorm:"not null"` // "Host", "Mac", "Network", "Port", "Country", "List"
	Comment   string    `json:"description"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	IsUsed    bool      `json:"isUsed" gorm:"-"`
	IsUsedBy  string    `json:"isUsedBy" gorm:"-"` // "", "dhcp" for now

	Entries     []ObjectEntry      `json:"entries" gorm:"foreignKey:ObjectID"`
	Resolutions []ObjectResolution `json:"resolutions" gorm:"foreignKey:ObjectID"`
}

type ObjectEntry struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	ObjectID  uint      `json:"objectId" gorm:"index"`
	Value     string    `json:"value"` // IP, CIDR, port, country code, FQDN, etc.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ObjectResolution struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	ObjectID   uint      `json:"objectId" gorm:"index"`
	ResolvedIP string    `json:"resolvedIp"` // actual IP resolved only in the case of FQDN
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
