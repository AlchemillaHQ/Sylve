// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

const (
	MaxGlobalDNSServers    = 3
	MaxGlobalSearchDomains = 6
	MaxGlobalHostAliases   = 128
	MaxGlobalAliases       = 16
)

var (
	ErrInvalidGlobalConfig  = errors.New("invalid_network_global_config")
	ErrGlobalConfigConflict = errors.New("network_global_config_conflict")
	globalDNSZonePattern    = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,32}$`)
)

type GlobalConfigError struct {
	Code  string
	Cause error
}

func (e *GlobalConfigError) Error() string { return e.Code }
func (e *GlobalConfigError) Unwrap() error { return e.Cause }

func invalidGlobalConfig(code string) error {
	return &GlobalConfigError{Code: code, Cause: ErrInvalidGlobalConfig}
}

func conflictingGlobalConfig(code string) error {
	return &GlobalConfigError{Code: code, Cause: ErrGlobalConfigConflict}
}

func defaultGlobalConfig() networkModels.GlobalConfig {
	return networkModels.GlobalConfig{
		ID: 1, DNSMode: "os", DNSServers: []string{}, SearchDomains: []string{}, HostAliases: []networkModels.HostAlias{},
	}
}

func loadGlobalConfig(db *gorm.DB) (networkModels.GlobalConfig, error) {
	var config networkModels.GlobalConfig
	result := db.Limit(1).Find(&config, 1)
	if result.Error != nil {
		return config, result.Error
	}
	if result.RowsAffected == 0 {
		return defaultGlobalConfig(), nil
	}
	if config.DNSServers == nil {
		config.DNSServers = []string{}
	}
	if config.SearchDomains == nil {
		config.SearchDomains = []string{}
	}
	if config.HostAliases == nil {
		config.HostAliases = []networkModels.HostAlias{}
	}
	return config, nil
}

func (s *Service) GetGlobalConfig(ctx context.Context) (*networkServiceInterfaces.GlobalConfigState, error) {
	s.globalConfigMutex.Lock()
	defer s.globalConfigMutex.Unlock()
	config, err := loadGlobalConfig(s.DB.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	state := &networkServiceInterfaces.GlobalConfigState{
		GlobalConfig: config, Hostname: hostname, EffectiveDNSServers: []string{}, EffectiveSearchDomains: []string{}, Warnings: []string{},
	}
	runtime := s.globalRuntime()
	resolver, err := os.ReadFile(runtime.resolverPath)
	if err == nil {
		state.EffectiveDNSServers, state.EffectiveSearchDomains = parseGlobalResolver(resolver)
	} else if !errors.Is(err, os.ErrNotExist) {
		state.Warnings = append(state.Warnings, "Unable to read the effective resolver configuration.")
	}
	for _, family := range []string{"inet", "inet6"} {
		commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		output, routeErr := runtime.runCommand(commandCtx, "/sbin/route", "-n", "get", "-"+family, "default")
		cancel()
		var gateway *string
		if routeErr == nil {
			value := ""
			for _, line := range strings.Split(output, "\n") {
				if key, val, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && key == "gateway" {
					value = strings.TrimSpace(val)
					break
				}
			}
			gateway = &value
		} else if strings.Contains(output, "not in table") || strings.Contains(output, "No such process") {
			value := ""
			gateway = &value
		} else {
			state.Warnings = append(state.Warnings, "Unable to read the "+family+" default gateway.")
		}
		if family == "inet" {
			state.IPv4Gateway = gateway
		} else {
			state.IPv6Gateway = gateway
		}
	}
	return state, nil
}

func normalizeGlobalConfig(req networkServiceInterfaces.UpdateGlobalConfigRequest) (networkModels.GlobalConfig, error) {
	config := defaultGlobalConfig()
	config.DNSMode = req.DNSMode
	config.SearchDomainsOverride = req.SearchDomainsOverride
	if config.DNSMode != "os" && config.DNSMode != "manual" {
		return config, invalidGlobalConfig("invalid_global_dns_mode")
	}
	if len(req.DNSServers) > MaxGlobalDNSServers || (config.DNSMode == "manual" && len(req.DNSServers) == 0) {
		return config, invalidGlobalConfig("invalid_global_dns_server_count")
	}
	if config.DNSMode == "os" && len(req.DNSServers) > 0 {
		return config, invalidGlobalConfig("global_os_dns_has_manual_servers")
	}
	for _, raw := range req.DNSServers {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		unmapped := address.Unmap()
		if err != nil || unmapped.IsUnspecified() || unmapped.IsMulticast() || unmapped.String() == "255.255.255.255" ||
			(address.Zone() != "" && (!address.IsLinkLocalUnicast() || !globalDNSZonePattern.MatchString(address.Zone()))) ||
			(address.Is6() && address.IsLinkLocalUnicast() && address.Zone() == "") {
			return config, invalidGlobalConfig("invalid_global_dns_server")
		}
		value := unmapped.String()
		if slices.Contains(config.DNSServers, value) {
			return config, invalidGlobalConfig("duplicate_global_dns_server")
		}
		config.DNSServers = append(config.DNSServers, value)
	}
	if len(req.SearchDomains) > MaxGlobalSearchDomains || (!req.SearchDomainsOverride && len(req.SearchDomains) > 0) {
		return config, invalidGlobalConfig("invalid_global_search_domains")
	}
	for _, raw := range req.SearchDomains {
		domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if !validGlobalHostname(domain) || slices.Contains(config.SearchDomains, domain) {
			return config, invalidGlobalConfig("invalid_global_search_domain")
		}
		config.SearchDomains = append(config.SearchDomains, domain)
	}
	if len(strings.Join(config.SearchDomains, " ")) > 255 {
		return config, invalidGlobalConfig("global_search_domains_too_long")
	}
	if len(req.HostAliases) > MaxGlobalHostAliases {
		return config, invalidGlobalConfig("too_many_global_host_aliases")
	}
	seen := map[string]bool{}
	for _, entry := range req.HostAliases {
		address, err := netip.ParseAddr(strings.TrimSpace(entry.Address))
		unmapped := address.Unmap()
		if err != nil || address.Zone() != "" || unmapped.IsUnspecified() || unmapped.IsMulticast() || unmapped.String() == "255.255.255.255" {
			return config, invalidGlobalConfig("invalid_global_host_address")
		}
		if len(entry.Aliases) > MaxGlobalAliases {
			return config, invalidGlobalConfig("too_many_global_host_names")
		}
		entry.Address = unmapped.String()
		names := append([]string{entry.Hostname}, entry.Aliases...)
		for i, raw := range names {
			name := strings.ToLower(strings.TrimSpace(raw))
			if !validGlobalHostname(name) {
				return config, invalidGlobalConfig("invalid_global_host_name")
			}
			key := fmt.Sprintf("%t/%s", unmapped.Is6(), name)
			if seen[key] {
				return config, invalidGlobalConfig("duplicate_global_host_name")
			}
			seen[key] = true
			names[i] = name
		}
		entry.Hostname, entry.Aliases = names[0], names[1:]
		config.HostAliases = append(config.HostAliases, entry)
	}
	return config, nil
}

func validGlobalHostname(name string) bool {
	if len(name) == 0 || len(name) > 253 || !dhcpDomainRegex.MatchString(name) {
		return false
	}
	_, err := netip.ParseAddr(name)
	return err != nil
}

func (s *Service) UpdateGlobalConfig(ctx context.Context, req networkServiceInterfaces.UpdateGlobalConfigRequest) error {
	config, err := normalizeGlobalConfig(req)
	if err != nil {
		return err
	}
	if req.ExpectedRevision == nil {
		return invalidGlobalConfig("global_config_revision_required")
	}
	s.globalConfigMutex.Lock()
	defer s.globalConfigMutex.Unlock()
	return s.saveGlobalConfig(ctx, config, req.ExpectedRevision)
}

// ApplyBootstrapDNS uses the same persistent resolver owner as the settings page.
func (s *Service) ApplyBootstrapDNS(ctx context.Context, servers []string) error {
	s.globalConfigMutex.Lock()
	defer s.globalConfigMutex.Unlock()
	current, err := loadGlobalConfig(s.DB.WithContext(ctx))
	if err != nil {
		return err
	}
	config, err := normalizeGlobalConfig(networkServiceInterfaces.UpdateGlobalConfigRequest{
		DNSMode: "manual", DNSServers: servers, SearchDomainsOverride: current.SearchDomainsOverride,
		SearchDomains: current.SearchDomains, HostAliases: current.HostAliases,
	})
	if err != nil {
		return err
	}
	return s.saveGlobalConfig(ctx, config, nil)
}

func (s *Service) saveGlobalConfig(ctx context.Context, candidate networkModels.GlobalConfig, expectedRevision *uint64) error {
	var application *globalConfigApplication
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadGlobalConfig(tx)
		if err != nil {
			return err
		}
		if expectedRevision != nil && *expectedRevision != current.Revision {
			return conflictingGlobalConfig("global_config_revision_conflict")
		}
		candidate.Revision = current.Revision
		if reflect.DeepEqual(current, candidate) {
			return nil
		}
		application, err = s.prepareGlobalConfig(current, candidate, false)
		if err != nil {
			return err
		}
		candidate.Revision++
		if err := tx.Save(&candidate).Error; err != nil {
			return err
		}
		return application.apply(ctx)
	})
	if err != nil && application != nil {
		return errors.Join(err, application.restore())
	}
	return err
}

func (s *Service) ReconcileGlobalConfig(ctx context.Context) error {
	s.globalConfigMutex.Lock()
	defer s.globalConfigMutex.Unlock()
	config, err := loadGlobalConfig(s.DB.WithContext(ctx))
	if err != nil {
		return err
	}
	// In particular, upgrades and startup must not take ownership of existing files.
	if config.DNSMode == "os" && !config.SearchDomainsOverride && len(config.HostAliases) == 0 {
		return nil
	}
	application, err := s.prepareGlobalConfig(config, config, true)
	if err != nil {
		return err
	}
	if err := application.apply(ctx); err != nil {
		return errors.Join(err, application.restore())
	}
	return nil
}
