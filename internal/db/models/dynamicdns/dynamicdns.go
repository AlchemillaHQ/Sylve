// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdnsModels

import "time"

const (
	ProviderCloudflare = "cloudflare"
	ProviderNamecheap  = "namecheap"

	RecordTypeA    = "A"
	RecordTypeAAAA = "AAAA"
	RecordTypeBoth = "BOTH"

	SourceTypeInterface = "interface"
	SourceTypeManual    = "manual"
	SourceTypeSTUN      = "stun"
)

type Entry struct {
	ID uint `json:"id" gorm:"primaryKey;autoIncrement"`

	Enabled          bool              `json:"enabled" gorm:"not null;default:true"`
	Provider         string            `json:"provider" gorm:"not null;index"`
	ProviderSettings map[string]string `json:"providerSettings" gorm:"serializer:json;type:json"`
	ProviderSecret   string            `json:"-"`

	Hostname        string `json:"hostname" gorm:"not null;index"`
	RecordType      string `json:"recordType" gorm:"not null"`
	IntervalMinutes uint   `json:"intervalMinutes" gorm:"not null;default:10"`

	SourceType     string            `json:"sourceType" gorm:"not null"`
	SourceSettings map[string]string `json:"sourceSettings" gorm:"serializer:json;type:json"`

	LastStatus    string     `json:"lastStatus"`
	LastError     string     `json:"lastError"`
	IPv4Status    string     `json:"ipv4Status"`
	IPv4Error     string     `json:"ipv4Error"`
	IPv6Status    string     `json:"ipv6Status"`
	IPv6Error     string     `json:"ipv6Error"`
	LastIPv4      string     `json:"lastIPv4"`
	LastIPv6      string     `json:"lastIPv6"`
	LastSyncAt    *time.Time `json:"lastSyncAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (Entry) TableName() string {
	return "dynamic_dns_entries"
}
