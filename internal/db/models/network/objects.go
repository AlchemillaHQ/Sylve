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
	ID                     uint       `json:"id" gorm:"primaryKey"`
	Name                   string     `json:"name" gorm:"uniqueIndex;not null"`
	Type                   string     `json:"type" gorm:"not null"` // "Host", "Mac", "Network", "Port", "Country", "List", "FQDN", "DUID"
	Comment                string     `json:"description"`
	AutoUpdate             bool       `json:"autoUpdate" gorm:"default:true"`
	RefreshIntervalSeconds uint       `json:"refreshIntervalSeconds" gorm:"default:300"`
	SourceChecksum         string     `json:"sourceChecksum"`
	ResolutionChecksum     string     `json:"resolutionChecksum"`
	LastRefreshAt          *time.Time `json:"lastRefreshAt"`
	LastRefreshError       string     `json:"lastRefreshError"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
	IsUsed                 bool       `json:"isUsed" gorm:"-"`
	IsUsedBy               string     `json:"isUsedBy" gorm:"-"` // "", "dhcp" for now

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
	ID            uint      `json:"id" gorm:"primaryKey"`
	ObjectID      uint      `json:"objectId" gorm:"index"`
	ResolvedIP    string    `json:"resolvedIp"` // deprecated mirror for resolved IP entries
	ResolvedValue string    `json:"resolvedValue"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ObjectListSnapshot stores resolved values for dynamic List objects in a single row.
// This avoids row explosions in object_resolutions for very large external lists.
type ObjectListSnapshot struct {
	ObjectID   uint      `json:"objectId" gorm:"primaryKey"`
	Checksum   string    `json:"checksum" gorm:"index"`
	ValueCount uint      `json:"valueCount"`
	Encoding   string    `json:"encoding"`
	Payload    []byte    `json:"-" gorm:"type:blob"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
