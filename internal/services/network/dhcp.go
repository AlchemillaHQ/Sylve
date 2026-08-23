// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultDHCPConfigPath = "/usr/local/etc/dnsmasq.conf"
	defaultDHCPLeasePath  = "/var/db/dnsmasq.leases"
)

var dhcpDomainRegex = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

type dhcpRuntimeOperations struct {
	configPath      string
	leasePath       string
	readFile        func(string) ([]byte, error)
	atomicWriteFile func(string, []byte, os.FileMode) error
	removeFile      func(string) error
	restart         func() error
}

type normalizedDHCPConfigRequest struct {
	standardSwitches []uint
	manualSwitches   []uint
	dnsServers       []string
	domain           string
	expandHosts      *bool
}

type dhcpConfigFileSnapshot struct {
	data   []byte
	exists bool
}

func (s *Service) GetConfig() (*networkModels.DHCPConfig, error) {
	var config networkModels.DHCPConfig
	if err := s.DB.
		Preload("StandardSwitches").
		Preload("StandardSwitches.Ports").
		Preload("ManualSwitches").
		First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("config_not_initialized: %w", err)
		}
		return nil, err
	}

	if config.StandardSwitches == nil {
		config.StandardSwitches = []networkModels.StandardSwitch{}
	}
	for i := range config.StandardSwitches {
		ensureStandardSwitchPortCollection(&config.StandardSwitches[i])
	}
	if config.ManualSwitches == nil {
		config.ManualSwitches = []networkModels.ManualSwitch{}
	}
	if config.DNSServers == nil {
		config.DNSServers = []string{}
	}

	return &config, nil
}

func ensureStandardSwitchPortCollection(sw *networkModels.StandardSwitch) {
	if sw != nil && sw.Ports == nil {
		sw.Ports = []networkModels.NetworkPort{}
	}
}

func (s *Service) SaveConfig(req *networkServiceInterfaces.ModifyDHCPConfigRequest) error {
	normalized, err := normalizeDHCPConfigRequest(req)
	if err != nil {
		return err
	}

	// DHCP config references switches, so serialize this mutation with switch
	// creation, editing, and deletion as well as dnsmasq file application.
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	s.dhcpRuntimeMutex.Lock()
	defer s.dhcpRuntimeMutex.Unlock()

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin_dhcp_config_transaction: %w", tx.Error)
	}
	transactionOpen := true
	rollback := func() {
		if !transactionOpen {
			return
		}
		transactionOpen = false
		if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
			logger.L.Error().Err(rollbackErr).Msg("dhcp_config_transaction_rollback_failed")
		}
	}
	defer rollback()

	var current networkModels.DHCPConfig
	if err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("StandardSwitches").
		Preload("ManualSwitches").
		First(&current).Error; err != nil {
		return fmt.Errorf("config_not_initialized: %w", err)
	}

	standardSwitches, err := loadDHCPStandardSwitches(tx, normalized.standardSwitches)
	if err != nil {
		return err
	}
	manualSwitches, err := loadDHCPManualSwitches(tx, normalized.manualSwitches)
	if err != nil {
		return err
	}
	if err := validateDHCPRangeSwitchRetention(tx, normalized.standardSwitches, normalized.manualSwitches); err != nil {
		return err
	}

	expandHosts := current.ExpandHosts
	if normalized.expandHosts != nil {
		expandHosts = *normalized.expandHosts
	}

	if sameDHCPConfig(
		&current,
		normalized.standardSwitches,
		normalized.manualSwitches,
		normalized.dnsServers,
		normalized.domain,
		expandHosts,
	) {
		return nil
	}

	if err := tx.Model(&current).Association("StandardSwitches").Replace(standardSwitches); err != nil {
		return fmt.Errorf("replace_dhcp_standard_switches: %w", err)
	}
	if err := tx.Model(&current).Association("ManualSwitches").Replace(manualSwitches); err != nil {
		return fmt.Errorf("replace_dhcp_manual_switches: %w", err)
	}

	current.DNSServers = append([]string{}, normalized.dnsServers...)
	current.Domain = normalized.domain
	current.ExpandHosts = expandHosts
	if err := tx.Model(&current).
		Select("DNSServers", "Domain", "ExpandHosts").
		Updates(&current).Error; err != nil {
		return fmt.Errorf("save_dhcp_config: %w", err)
	}

	candidate, err := renderDHCPConfig(tx)
	if err != nil {
		return err
	}
	snapshot, err := s.snapshotDHCPConfigFile()
	if err != nil {
		return fmt.Errorf("snapshot_dhcp_config: %w", err)
	}
	if err := s.writeDHCPConfigFile(candidate); err != nil {
		return fmt.Errorf("write_dhcp_config: %w", err)
	}

	if err := s.restartDNSMasq(); err != nil {
		rollback()
		s.restoreDHCPRuntimeAfterFailure(snapshot, "restart_failed")
		return fmt.Errorf("restart_dnsmasq: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		transactionOpen = false
		_ = tx.Rollback().Error
		s.restoreDHCPRuntimeAfterFailure(snapshot, "commit_failed")
		return fmt.Errorf("commit_dhcp_config: %w", err)
	}
	transactionOpen = false

	return nil
}

func normalizeDHCPConfigRequest(req *networkServiceInterfaces.ModifyDHCPConfigRequest) (*normalizedDHCPConfigRequest, error) {
	if req == nil {
		return nil, invalidDHCPConfig("invalid_dhcp_config_request", nil)
	}

	standardSwitches, err := normalizeDHCPSwitchIDs(req.StandardSwitches, "invalid_dhcp_standard_switch_id")
	if err != nil {
		return nil, err
	}
	manualSwitches, err := normalizeDHCPSwitchIDs(req.ManualSwitches, "invalid_dhcp_manual_switch_id")
	if err != nil {
		return nil, err
	}
	if len(standardSwitches)+len(manualSwitches) > MaxDHCPConfigSwitches {
		return nil, invalidDHCPConfig("too_many_dhcp_config_switches", nil)
	}

	if len(req.DNSServers) > MaxDHCPConfigDNSServers {
		return nil, invalidDHCPConfig("too_many_dhcp_dns_servers", nil)
	}
	dnsServers := make([]string, 0, len(req.DNSServers))
	seenDNS := make(map[string]struct{}, len(req.DNSServers))
	for _, rawDNS := range req.DNSServers {
		address, err := netip.ParseAddr(strings.TrimSpace(rawDNS))
		if err != nil {
			return nil, invalidDHCPConfig("invalid_dhcp_dns_server", err)
		}
		dns := address.String()
		if _, seen := seenDNS[dns]; seen {
			continue
		}
		seenDNS[dns] = struct{}{}
		dnsServers = append(dnsServers, dns)
	}

	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if len(domain) > MaxDHCPConfigDomainBytes {
		return nil, invalidDHCPConfig("dhcp_domain_too_long", nil)
	}
	if domain != "" && !dhcpDomainRegex.MatchString(domain) {
		return nil, invalidDHCPConfig("invalid_dhcp_domain", nil)
	}

	return &normalizedDHCPConfigRequest{
		standardSwitches: standardSwitches,
		manualSwitches:   manualSwitches,
		dnsServers:       dnsServers,
		domain:           domain,
		expandHosts:      req.ExpandHosts,
	}, nil
}

func normalizeDHCPSwitchIDs(ids []uint, invalidCode string) ([]uint, error) {
	normalized := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, invalidDHCPConfig(invalidCode, nil)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

func loadDHCPStandardSwitches(tx *gorm.DB, ids []uint) ([]networkModels.StandardSwitch, error) {
	if len(ids) == 0 {
		return []networkModels.StandardSwitch{}, nil
	}

	var switches []networkModels.StandardSwitch
	if err := tx.Where("id IN ?", ids).Find(&switches).Error; err != nil {
		return nil, fmt.Errorf("load_dhcp_standard_switches: %w", err)
	}
	if len(switches) != len(ids) {
		return nil, invalidDHCPConfig("dhcp_standard_switch_not_found", nil)
	}
	return switches, nil
}

func loadDHCPManualSwitches(tx *gorm.DB, ids []uint) ([]networkModels.ManualSwitch, error) {
	if len(ids) == 0 {
		return []networkModels.ManualSwitch{}, nil
	}

	var switches []networkModels.ManualSwitch
	if err := tx.Where("id IN ?", ids).Find(&switches).Error; err != nil {
		return nil, fmt.Errorf("load_dhcp_manual_switches: %w", err)
	}
	if len(switches) != len(ids) {
		return nil, invalidDHCPConfig("dhcp_manual_switch_not_found", nil)
	}
	return switches, nil
}

func sameDHCPConfig(
	current *networkModels.DHCPConfig,
	standardSwitches []uint,
	manualSwitches []uint,
	dnsServers []string,
	domain string,
	expandHosts bool,
) bool {
	currentStandard := make([]uint, 0, len(current.StandardSwitches))
	for _, sw := range current.StandardSwitches {
		currentStandard = append(currentStandard, sw.ID)
	}
	currentManual := make([]uint, 0, len(current.ManualSwitches))
	for _, sw := range current.ManualSwitches {
		currentManual = append(currentManual, sw.ID)
	}

	return sameUintSet(currentStandard, standardSwitches) &&
		sameUintSet(currentManual, manualSwitches) &&
		slices.Equal(current.DNSServers, dnsServers) &&
		current.Domain == domain &&
		current.ExpandHosts == expandHosts
}

func sameUintSet(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]uint{}, left...)
	rightCopy := append([]uint{}, right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func validateDHCPRangeSwitchRetention(tx *gorm.DB, standardSwitches []uint, manualSwitches []uint) error {
	standardSet := make(map[uint]struct{}, len(standardSwitches))
	for _, id := range standardSwitches {
		standardSet[id] = struct{}{}
	}
	manualSet := make(map[uint]struct{}, len(manualSwitches))
	for _, id := range manualSwitches {
		manualSet[id] = struct{}{}
	}

	var ranges []networkModels.DHCPRange
	if err := tx.Select("standard_switch_id", "manual_switch_id").Find(&ranges).Error; err != nil {
		return fmt.Errorf("load_dhcp_ranges_for_config_validation: %w", err)
	}
	for _, dhcpRange := range ranges {
		if dhcpRange.StandardSwitchID != nil {
			if _, retained := standardSet[*dhcpRange.StandardSwitchID]; !retained {
				return conflictingDHCPConfig("dhcp_switch_has_ranges", nil)
			}
		}
		if dhcpRange.ManualSwitchID != nil {
			if _, retained := manualSet[*dhcpRange.ManualSwitchID]; !retained {
				return conflictingDHCPConfig("dhcp_switch_has_ranges", nil)
			}
		}
	}
	return nil
}

// WriteDHCPConfig atomically renders and applies the committed DHCP state.
func (s *Service) WriteDHCPConfig() error {
	s.dhcpRuntimeMutex.Lock()
	defer s.dhcpRuntimeMutex.Unlock()

	candidate, err := renderDHCPConfig(s.DB)
	if err != nil {
		return err
	}
	snapshot, err := s.snapshotDHCPConfigFile()
	if err != nil {
		return fmt.Errorf("snapshot_dhcp_config: %w", err)
	}
	if err := s.writeDHCPConfigFile(candidate); err != nil {
		return fmt.Errorf("write_dhcp_config: %w", err)
	}
	if err := s.restartDNSMasq(); err != nil {
		s.restoreDHCPRuntimeAfterFailure(snapshot, "restart_failed")
		return fmt.Errorf("restart_dnsmasq: %w", err)
	}
	return nil
}

func renderDHCPConfig(db *gorm.DB) ([]byte, error) {
	var current networkModels.DHCPConfig
	if err := db.
		Preload("StandardSwitches").
		Preload("ManualSwitches").
		First(&current).Error; err != nil {
		return nil, fmt.Errorf("config_not_initialized: %w", err)
	}

	var config strings.Builder
	config.WriteString("# This file is managed by Sylve. Manual changes will be overwritten.\n\n")

	interfaces := make([]string, 0, len(current.StandardSwitches)+len(current.ManualSwitches))
	for _, sw := range current.StandardSwitches {
		interfaces = append(interfaces, sw.BridgeName)
	}
	for _, sw := range current.ManualSwitches {
		interfaces = append(interfaces, sw.Bridge)
	}
	for _, iface := range interfaces {
		fmt.Fprintf(&config, "interface=%s\n", iface)
	}
	if len(interfaces) > 0 {
		config.WriteString("bind-interfaces\n\n")
	}

	for _, dns := range current.DNSServers {
		fmt.Fprintf(&config, "server=%s\n", dns)
	}
	if len(current.DNSServers) > 0 {
		config.WriteString("\n")
	}
	if current.Domain != "" {
		fmt.Fprintf(&config, "domain=%s\n\n", current.Domain)
		fmt.Fprintf(&config, "domain-needed\n")
		fmt.Fprintf(&config, "local=/%s/\n\n", current.Domain)
	}
	if current.ExpandHosts {
		config.WriteString("expand-hosts\n\n")
	}

	var ranges []networkModels.DHCPRange
	if err := db.Preload("StandardSwitch").Preload("ManualSwitch").Find(&ranges).Error; err != nil {
		return nil, fmt.Errorf("failed_to_fetch_dhcp_ranges: %w", err)
	}
	for _, r := range ranges {
		var rangeInterface string
		if r.StandardSwitch != nil {
			rangeInterface = r.StandardSwitch.BridgeName
		} else if r.ManualSwitch != nil {
			rangeInterface = r.ManualSwitch.Bridge
		} else {
			continue
		}

		expiry := "infinite"
		if r.Expiry != 0 {
			expiry = fmt.Sprintf("%d", r.Expiry)
		}
		switch r.Type {
		case "ipv4":
			fmt.Fprintf(&config, "dhcp-range=%s,%s,%s,%s\n", rangeInterface, r.StartIP, r.EndIP, expiry)
		case "ipv6":
			if r.StartIP != "" && r.EndIP != "" {
				fmt.Fprintf(&config, "dhcp-range=%s,%s,%s", rangeInterface, r.StartIP, r.EndIP)
			} else {
				fmt.Fprintf(&config, "dhcp-range=::,constructor:%s", rangeInterface)
			}
			if r.RAOnly {
				config.WriteString(",ra-only")
			}
			if r.SLAAC {
				config.WriteString(",slaac")
			}
			fmt.Fprintf(&config, ",%s\n", expiry)
		}
	}
	config.WriteString("\n")

	var leases []networkModels.DHCPStaticLease
	if err := db.
		Preload("IPObject.Entries").
		Preload("MACObject.Entries").
		Preload("DUIDObject.Entries").
		Find(&leases).Error; err != nil {
		return nil, fmt.Errorf("failed_to_fetch_static_leases: %w", err)
	}
	for _, lease := range leases {
		ipType := "ipv4"
		var ip, mac, duid string
		if lease.IPObject != nil && len(lease.IPObject.Entries) > 0 {
			ip = lease.IPObject.Entries[0].Value
			if utils.IsValidIPv6(ip) {
				ipType = "ipv6"
			}
		}
		if lease.MACObject != nil && len(lease.MACObject.Entries) > 0 {
			mac = lease.MACObject.Entries[0].Value
		}
		if lease.DUIDObject != nil && len(lease.DUIDObject.Entries) > 0 {
			duid = lease.DUIDObject.Entries[0].Value
		}

		switch ipType {
		case "ipv4":
			if ip == "" || mac == "" {
				continue
			}
			fmt.Fprintf(&config, "dhcp-host=%s,%s", mac, ip)
		case "ipv6":
			if ip == "" || duid == "" {
				continue
			}
			fmt.Fprintf(&config, "dhcp-host=id:%s,[%s]", duid, ip)
		}
		if lease.Hostname != "" {
			fmt.Fprintf(&config, ",%s", lease.Hostname)
		}
		config.WriteString(",infinite\n")
	}
	config.WriteString("\n")

	// quick hack: add dhcp-options for add default-route
	config.WriteString("dhcp-option=option:router,10.0.1.1\n")
	config.WriteString("dhcp-option=6,10.0.1.9,10.0.1.16\n")
	config.WriteString("port=53\n")
	config.WriteString("bogus-priv\n\n")

	return []byte(config.String()), nil
}

func (s *Service) snapshotDHCPConfigFile() (dhcpConfigFileSnapshot, error) {
	data, err := s.readDHCPConfigFile()
	if err == nil {
		return dhcpConfigFileSnapshot{data: data, exists: true}, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return dhcpConfigFileSnapshot{}, nil
	}
	return dhcpConfigFileSnapshot{}, err
}

func (s *Service) restoreDHCPRuntimeAfterFailure(snapshot dhcpConfigFileSnapshot, operation string) {
	if err := s.restoreDHCPConfigFile(snapshot); err != nil {
		logger.L.Error().Err(err).Str("operation", operation).Msg("dhcp_config_file_restore_failed")
		return
	}
	if err := s.restartDNSMasq(); err != nil {
		logger.L.Error().Err(err).Str("operation", operation).Msg("dhcp_runtime_restore_failed")
	}
}

func (s *Service) restoreDHCPConfigFile(snapshot dhcpConfigFileSnapshot) error {
	if snapshot.exists {
		return s.writeDHCPConfigFile(snapshot.data)
	}
	err := s.removeDHCPConfigFile()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Service) dhcpConfigPath() string {
	if s.dhcpRuntime.configPath != "" {
		return s.dhcpRuntime.configPath
	}
	return defaultDHCPConfigPath
}

func (s *Service) dhcpLeasePath() string {
	if s.dhcpRuntime.leasePath != "" {
		return s.dhcpRuntime.leasePath
	}
	return defaultDHCPLeasePath
}

func (s *Service) readDHCPConfigFile() ([]byte, error) {
	if s.dhcpRuntime.readFile != nil {
		return s.dhcpRuntime.readFile(s.dhcpConfigPath())
	}
	return os.ReadFile(s.dhcpConfigPath())
}

func (s *Service) readDHCPLeaseFile() ([]byte, error) {
	if s.dhcpRuntime.readFile != nil {
		return s.dhcpRuntime.readFile(s.dhcpLeasePath())
	}
	return os.ReadFile(s.dhcpLeasePath())
}

func (s *Service) writeDHCPConfigFile(data []byte) error {
	if s.dhcpRuntime.atomicWriteFile != nil {
		return s.dhcpRuntime.atomicWriteFile(s.dhcpConfigPath(), data, 0o644)
	}
	return utils.AtomicWriteFile(s.dhcpConfigPath(), data, 0o644)
}

func (s *Service) writeDHCPLeaseFile(data []byte) error {
	if s.dhcpRuntime.atomicWriteFile != nil {
		return s.dhcpRuntime.atomicWriteFile(s.dhcpLeasePath(), data, 0o644)
	}
	return utils.AtomicWriteFile(s.dhcpLeasePath(), data, 0o644)
}

func (s *Service) removeDHCPConfigFile() error {
	if s.dhcpRuntime.removeFile != nil {
		return s.dhcpRuntime.removeFile(s.dhcpConfigPath())
	}
	return os.Remove(s.dhcpConfigPath())
}

func (s *Service) restartDNSMasq() error {
	if s.dhcpRuntime.restart != nil {
		return s.dhcpRuntime.restart()
	}
	return system.ServiceAction("dnsmasq", "onerestart")
}

func (s *Service) RestartDNSMasq() error {
	s.dhcpRuntimeMutex.Lock()
	defer s.dhcpRuntimeMutex.Unlock()
	return s.restartDNSMasq()
}
