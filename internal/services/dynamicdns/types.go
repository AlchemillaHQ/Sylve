// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"context"
	"net/netip"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

const (
	DefaultIntervalMinutes uint = 10
	MinimumIntervalMinutes uint = 1
	MaximumIntervalMinutes uint = 24 * 60
	DefaultSTUNServer           = "stun.l.google.com:19302"

	SourceSettingInterface  = "interface"
	SourceSettingIPv4       = "ipv4"
	SourceSettingIPv6       = "ipv6"
	SourceSettingSTUNServer = "server"

	NamecheapSettingDomain = "domain"
	NamecheapSettingHost   = "host"
)

type AddressSet struct {
	IPv4 netip.Addr
	IPv6 netip.Addr
}

type DNSProvider interface {
	ID() string
	Validate(context.Context, string, string, string, map[string]string) (map[string]string, error)
	Upsert(context.Context, string, map[string]string, string, string, netip.Addr) error
}

type IPSourceResolver interface {
	Type() string
	Resolve(context.Context, map[string]string) (AddressSet, error)
}

type EntryInput struct {
	Enabled          bool              `json:"enabled"`
	Provider         string            `json:"provider"`
	ProviderSettings map[string]string `json:"providerSettings"`
	Token            string            `json:"token"`
	Hostname         string            `json:"hostname"`
	RecordType       string            `json:"recordType"`
	IntervalMinutes  uint              `json:"intervalMinutes"`
	SourceType       string            `json:"sourceType"`
	SourceSettings   map[string]string `json:"sourceSettings"`
}

type EntryView struct {
	dynamicDNSModels.Entry
	CredentialConfigured bool `json:"credentialConfigured"`
}

func entryView(entry dynamicDNSModels.Entry) EntryView {
	return EntryView{
		Entry:                entry,
		CredentialConfigured: entry.ProviderSecret != "",
	}
}

func cloneSettings(settings map[string]string) map[string]string {
	if len(settings) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(settings))
	for key, value := range settings {
		cloned[key] = value
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	copy := *value
	return &copy
}
