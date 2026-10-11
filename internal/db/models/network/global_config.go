// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

type HostAlias struct {
	Address  string   `json:"address"`
	Hostname string   `json:"hostname"`
	Aliases  []string `json:"aliases"`
}

// GlobalConfig controls this node's resolver and Sylve-owned host aliases only.
// A missing record means OS-managed DNS and no managed aliases.
type GlobalConfig struct {
	ID                    uint        `json:"-" gorm:"primaryKey"`
	Revision              uint64      `json:"revision"`
	DNSMode               string      `json:"dnsMode" gorm:"not null;default:os"`
	DNSServers            []string    `json:"dnsServers" gorm:"serializer:json"`
	SearchDomainsOverride bool        `json:"searchDomainsOverride"`
	SearchDomains         []string    `json:"searchDomains" gorm:"serializer:json"`
	HostAliases           []HostAlias `json:"hostAliases" gorm:"serializer:json"`
}
