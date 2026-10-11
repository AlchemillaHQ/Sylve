// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import networkModels "github.com/alchemillahq/sylve/internal/db/models/network"

type UpdateGlobalConfigRequest struct {
	DNSMode               string                    `json:"dnsMode"`
	DNSServers            []string                  `json:"dnsServers"`
	SearchDomainsOverride bool                      `json:"searchDomainsOverride"`
	SearchDomains         []string                  `json:"searchDomains"`
	HostAliases           []networkModels.HostAlias `json:"hostAliases"`
	ExpectedRevision      *uint64                   `json:"expectedRevision"`
}

type GlobalConfigState struct {
	networkModels.GlobalConfig
	Hostname               string   `json:"hostname"`
	EffectiveDNSServers    []string `json:"effectiveDnsServers"`
	EffectiveSearchDomains []string `json:"effectiveSearchDomains"`
	IPv4Gateway            *string  `json:"ipv4Gateway"`
	IPv6Gateway            *string  `json:"ipv6Gateway"`
	Warnings               []string `json:"warnings"`
}
