// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

var (
	standardSwitchNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	standardSwitchPortPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
)

type standardSwitchInput struct {
	mtu                   int
	vlan                  int
	network4ID            uint
	network6ID            uint
	gateway4ID            uint
	gateway6ID            uint
	ports                 []string
	private               bool
	dhcp                  bool
	disableIPv6           bool
	slaac                 bool
	defaultRoute          bool
	disableBridgeOffloads bool
	manual                networkModels.StandardSwitchManualAddresses
	macSource             networkModels.StandardSwitchMACSource
}

func normalizeStandardSwitchMAC(value string) (string, error) {
	hardwareAddress, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(hardwareAddress) != 6 {
		return "", invalidStandardSwitch("invalid_standard_switch_mac_address", err)
	}
	if hardwareAddress[0]&1 != 0 {
		return "", invalidStandardSwitch("invalid_standard_switch_mac_address", nil)
	}

	allZero := true
	for _, octet := range hardwareAddress {
		if octet != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "", invalidStandardSwitch("invalid_standard_switch_mac_address", nil)
	}

	return hardwareAddress.String(), nil
}

func normalizeStandardSwitchName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxStandardSwitchNameBytes || !standardSwitchNamePattern.MatchString(name) {
		return "", invalidStandardSwitch("invalid_standard_switch_name", nil)
	}
	return name, nil
}

func normalizeStandardSwitchPorts(ports []string) ([]string, error) {
	if len(ports) > MaxStandardSwitchPorts {
		return nil, invalidStandardSwitch("standard_switch_too_many_ports", nil)
	}

	normalized := make([]string, 0, len(ports))
	seen := make(map[string]struct{}, len(ports))
	for _, raw := range ports {
		name := strings.TrimSpace(raw)
		if name == "" || len(name) > MaxStandardSwitchPortNameBytes || !standardSwitchPortPattern.MatchString(name) {
			return nil, invalidStandardSwitch("invalid_standard_switch_port", nil)
		}
		if _, exists := seen[name]; exists {
			return nil, invalidStandardSwitch("duplicate_standard_switch_port", nil)
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}

	return normalized, nil
}

func (s *Service) validateStandardSwitchMACSource(
	source networkModels.StandardSwitchMACSource,
	ports []string,
) (networkModels.StandardSwitchMACSource, error) {
	source.Mode = strings.ToLower(strings.TrimSpace(source.Mode))
	source.Port = strings.TrimSpace(source.Port)

	switch source.Mode {
	case networkModels.StandardSwitchMACModePort:
		if source.Port == "" {
			return source, invalidStandardSwitch("standard_switch_mac_port_required", nil)
		}
		if source.MACObjectID != 0 {
			return source, invalidStandardSwitch("standard_switch_mac_source_conflict", nil)
		}

		selected := false
		for _, port := range ports {
			if port == source.Port {
				selected = true
				break
			}
		}
		if !selected {
			return source, invalidStandardSwitch("standard_switch_mac_port_not_selected", nil)
		}

		interfaceObj, err := syncIfaceGet(source.Port)
		if err != nil {
			if isInterfaceMissingError(err) {
				return source, invalidStandardSwitch("standard_switch_mac_port_not_found", err)
			}
			return source, fmt.Errorf("inspect standard switch MAC source port %q: %w", source.Port, err)
		}
		if interfaceObj == nil {
			return source, invalidStandardSwitch("standard_switch_mac_port_not_found", nil)
		}
		mac := interfaceObj.Ether
		if strings.TrimSpace(mac) == "" {
			mac = interfaceObj.HWAddr
		}
		if _, err := normalizeStandardSwitchMAC(mac); err != nil {
			return source, err
		}
		return source, nil

	case networkModels.StandardSwitchMACModeObject:
		if source.Port != "" {
			return source, invalidStandardSwitch("standard_switch_mac_source_conflict", nil)
		}
		if source.MACObjectID == 0 {
			return source, invalidStandardSwitch("standard_switch_mac_object_required", nil)
		}

		var object networkModels.Object
		if err := s.DB.Preload("Entries").First(&object, source.MACObjectID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return source, invalidStandardSwitch("invalid_standard_switch_mac_object", err)
			}
			return source, fmt.Errorf("load standard switch MAC object: %w", err)
		}
		if object.Type != "Mac" || len(object.Entries) != 1 {
			return source, invalidStandardSwitch("invalid_standard_switch_mac_object", nil)
		}
		if _, err := normalizeStandardSwitchMAC(object.Entries[0].Value); err != nil {
			return source, err
		}
		return source, nil

	case "":
		return source, invalidStandardSwitch("standard_switch_mac_source_required", nil)
	default:
		return source, invalidStandardSwitch("invalid_standard_switch_mac_mode", nil)
	}
}

func (s *Service) validateStandardSwitchObject(id uint, objectType string, family int, field string) (string, error) {
	if id == 0 {
		return "", nil
	}

	var object networkModels.Object
	if err := s.DB.Preload("Entries").First(&object, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", invalidStandardSwitch("invalid_standard_switch_"+field+"_object", err)
		}
		return "", fmt.Errorf("load standard switch %s object: %w", field, err)
	}
	if object.Type != objectType || len(object.Entries) != 1 {
		return "", invalidStandardSwitch("invalid_standard_switch_"+field+"_object", nil)
	}

	value := strings.TrimSpace(object.Entries[0].Value)
	valid := false
	switch {
	case objectType == "Network" && family == 4:
		valid = utils.IsAssignableIPv4CIDR(value)
	case objectType == "Network" && family == 6:
		valid = utils.IsAssignableIPv6CIDR(value)
	case objectType == "Host" && family == 4:
		valid = utils.IsValidIPv4(value)
	case objectType == "Host" && family == 6:
		valid = utils.IsValidIPv6(value)
	}
	if !valid {
		return "", invalidStandardSwitch("invalid_standard_switch_"+field+"_object", nil)
	}

	return value, nil
}

func (s *Service) validateStandardSwitchInput(
	excludeID uint,
	bridgeName string,
	input standardSwitchInput,
) (standardSwitchInput, error) {
	if input.mtu == 0 {
		input.mtu = 1500
	}
	if !utils.IsValidMTU(input.mtu) {
		return input, invalidStandardSwitch("invalid_standard_switch_mtu", nil)
	}
	if !utils.IsValidVLAN(input.vlan) {
		return input, invalidStandardSwitch("invalid_standard_switch_vlan", nil)
	}

	modes := normalizeStandardSwitchAddressModes(standardSwitchAddressModes{
		network4ID:  input.network4ID,
		network6ID:  input.network6ID,
		gateway4ID:  input.gateway4ID,
		gateway6ID:  input.gateway6ID,
		dhcp:        input.dhcp,
		disableIPv6: input.disableIPv6,
		slaac:       input.slaac,
		manual:      input.manual,
	})
	input.network4ID = modes.network4ID
	input.network6ID = modes.network6ID
	input.gateway4ID = modes.gateway4ID
	input.gateway6ID = modes.gateway6ID
	input.dhcp = modes.dhcp
	input.disableIPv6 = modes.disableIPv6
	input.slaac = modes.slaac
	input.manual = modes.manual
	if input.dhcp {
		input.defaultRoute = false
	}

	manual, err := validateStandardSwitchManual(
		input.network4ID,
		input.gateway4ID,
		input.network6ID,
		input.gateway6ID,
		input.manual,
	)
	if err != nil {
		return input, err
	}
	input.manual = manual

	network4, err := s.validateStandardSwitchObject(input.network4ID, "Network", 4, "network4")
	if err != nil {
		return input, err
	}
	gateway4, err := s.validateStandardSwitchObject(input.gateway4ID, "Host", 4, "gateway4")
	if err != nil {
		return input, err
	}
	network6, err := s.validateStandardSwitchObject(input.network6ID, "Network", 6, "network6")
	if err != nil {
		return input, err
	}
	gateway6, err := s.validateStandardSwitchObject(input.gateway6ID, "Host", 6, "gateway6")
	if err != nil {
		return input, err
	}

	if network4 == "" {
		network4 = input.manual.Network4
	}
	if gateway4 == "" {
		gateway4 = input.manual.Gateway4
	}
	if network6 == "" {
		network6 = input.manual.Network6
	}
	if gateway6 == "" {
		gateway6 = input.manual.Gateway6
	}

	if gateway4 != "" && network4 == "" {
		return input, invalidStandardSwitch("standard_switch_ipv4_gateway_requires_network", nil)
	}
	if gateway6 != "" && network6 == "" {
		return input, invalidStandardSwitch("standard_switch_ipv6_gateway_requires_network", nil)
	}
	if input.defaultRoute && (network4 == "" || gateway4 == "") {
		return input, invalidStandardSwitch("standard_switch_default_route_requires_ipv4_gateway", nil)
	}

	input.ports, err = normalizeStandardSwitchPorts(input.ports)
	if err != nil {
		return input, err
	}
	var excludeIDPointer *uint
	if excludeID != 0 {
		excludeIDPointer = &excludeID
	}
	if conflicts, conflictErr := s.conflictingPortsForVLAN(input.ports, input.vlan, excludeIDPointer); conflictErr != nil {
		return input, conflictErr
	} else if len(conflicts) > 0 {
		return input, standardSwitchConflict("standard_switch_port_conflict", nil)
	}

	input.macSource, err = s.validateStandardSwitchMACSource(input.macSource, input.ports)
	if err != nil {
		return input, err
	}

	for _, port := range input.ports {
		if port == bridgeName {
			return input, invalidStandardSwitch("standard_switch_cannot_use_own_bridge", nil)
		}
		interfaceObj, inspectErr := syncIfaceGet(port)
		if inspectErr != nil {
			if isInterfaceMissingError(inspectErr) {
				return input, invalidStandardSwitch("standard_switch_port_not_found", inspectErr)
			}
			return input, fmt.Errorf("inspect standard switch port %q: %w", port, inspectErr)
		}
		if interfaceObj == nil {
			return input, invalidStandardSwitch("standard_switch_port_not_found", nil)
		}

		if input.vlan > 0 {
			vlanName := fmt.Sprintf("%s.%d", port, input.vlan)
			vlanObj, vlanErr := syncIfaceGet(vlanName)
			if vlanErr != nil && !isInterfaceMissingError(vlanErr) {
				return input, fmt.Errorf("inspect standard switch VLAN interface %q: %w", vlanName, vlanErr)
			}
			if vlanErr == nil && vlanObj != nil && !utils.Contains(vlanObj.Groups, "svm-vlan") {
				return input, standardSwitchConflict("standard_switch_vlan_interface_conflict", nil)
			}
		}
	}

	if input.defaultRoute {
		query := s.DB.Model(&networkModels.StandardSwitch{}).Where("default_route = ?", true)
		if excludeID != 0 {
			query = query.Where("id <> ?", excludeID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return input, fmt.Errorf("check standard switch default route conflict: %w", err)
		}
		if count > 0 {
			return input, standardSwitchConflict("standard_switch_default_route_conflict", nil)
		}
	}

	return input, nil
}

func (s *Service) checkStandardSwitchCreateConflicts(name, bridgeName string) error {
	checks := []struct {
		query *gorm.DB
		code  string
		label string
	}{
		{
			query: s.DB.Model(&networkModels.StandardSwitch{}).Where("name = ?", name),
			code:  "standard_switch_name_conflict",
			label: "standard switch name",
		},
		{
			query: s.DB.Model(&networkModels.ManualSwitch{}).Where("name = ?", name),
			code:  "standard_switch_name_conflict",
			label: "manual switch name",
		},
		{
			query: s.DB.Model(&networkModels.StandardSwitch{}).Where("bridge_name = ?", bridgeName),
			code:  "standard_switch_bridge_conflict",
			label: "standard switch bridge",
		},
		{
			query: s.DB.Model(&networkModels.ManualSwitch{}).Where("bridge = ?", bridgeName),
			code:  "standard_switch_bridge_conflict",
			label: "manual switch bridge",
		},
	}

	for _, check := range checks {
		var count int64
		if err := check.query.Count(&count).Error; err != nil {
			return fmt.Errorf("check %s conflict: %w", check.label, err)
		}
		if count > 0 {
			return standardSwitchConflict(check.code, nil)
		}
	}

	interfaceObj, err := syncIfaceGet(bridgeName)
	if err == nil && interfaceObj != nil {
		return standardSwitchConflict("standard_switch_bridge_conflict", nil)
	}
	if err != nil && !isInterfaceMissingError(err) {
		return fmt.Errorf("inspect generated standard switch bridge %q: %w", bridgeName, err)
	}

	return nil
}

func isStandardSwitchDuplicateError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}

func interfaceListContains(values []string, interfaceName string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == interfaceName {
			return true
		}
	}
	return false
}

func commaSeparatedInterfacesContain(value, interfaceName string) bool {
	return interfaceListContains(strings.Split(value, ","), interfaceName)
}

func (s *Service) checkStandardSwitchExternalUsage(bridgeName string) error {
	if s.DB.Migrator().HasTable(&networkModels.StaticRoute{}) {
		var routes []networkModels.StaticRoute
		if err := s.DB.Select("interface", "gateway_zone").Find(&routes).Error; err != nil {
			return fmt.Errorf("check standard switch static route usage: %w", err)
		}
		for _, route := range routes {
			if strings.TrimSpace(route.Interface) == bridgeName || strings.TrimSpace(route.GatewayZone) == bridgeName {
				return standardSwitchInUse("standard_switch_in_use_by_static_route")
			}
		}
	}

	if s.DB.Migrator().HasTable(&networkModels.FirewallTrafficRule{}) {
		var rules []networkModels.FirewallTrafficRule
		if err := s.DB.Select("ingress_interfaces", "egress_interfaces").Find(&rules).Error; err != nil {
			return fmt.Errorf("check standard switch firewall traffic usage: %w", err)
		}
		for _, rule := range rules {
			if interfaceListContains(rule.IngressInterfaces, bridgeName) || interfaceListContains(rule.EgressInterfaces, bridgeName) {
				return standardSwitchInUse("standard_switch_in_use_by_firewall")
			}
		}
	}

	if s.DB.Migrator().HasTable(&networkModels.FirewallNATRule{}) {
		var rules []networkModels.FirewallNATRule
		if err := s.DB.Select("ingress_interfaces", "egress_interfaces").Find(&rules).Error; err != nil {
			return fmt.Errorf("check standard switch firewall NAT usage: %w", err)
		}
		for _, rule := range rules {
			if interfaceListContains(rule.IngressInterfaces, bridgeName) || interfaceListContains(rule.EgressInterfaces, bridgeName) {
				return standardSwitchInUse("standard_switch_in_use_by_firewall")
			}
		}
	}

	if s.DB.Migrator().HasTable(&dynamicDNSModels.Entry{}) {
		var entries []dynamicDNSModels.Entry
		if err := s.DB.Select("source_type", "source_settings").Find(&entries).Error; err != nil {
			return fmt.Errorf("check standard switch dynamic DNS usage: %w", err)
		}
		for _, entry := range entries {
			if entry.SourceType == dynamicDNSModels.SourceTypeInterface && strings.TrimSpace(entry.SourceSettings["interface"]) == bridgeName {
				return standardSwitchInUse("standard_switch_in_use_by_dynamic_dns")
			}
		}
	}

	if s.DB.Migrator().HasTable(&mdnsModels.MdnsSettings{}) {
		var settings []mdnsModels.MdnsSettings
		if err := s.DB.Select("interfaces").Find(&settings).Error; err != nil {
			return fmt.Errorf("check standard switch mDNS settings usage: %w", err)
		}
		for _, setting := range settings {
			if commaSeparatedInterfacesContain(setting.Interfaces, bridgeName) {
				return standardSwitchInUse("standard_switch_in_use_by_mdns")
			}
		}
	}
	if s.DB.Migrator().HasTable(&mdnsModels.MdnsRecord{}) {
		var records []mdnsModels.MdnsRecord
		if err := s.DB.Select("interfaces").Find(&records).Error; err != nil {
			return fmt.Errorf("check standard switch mDNS record usage: %w", err)
		}
		for _, record := range records {
			if commaSeparatedInterfacesContain(record.Interfaces, bridgeName) {
				return standardSwitchInUse("standard_switch_in_use_by_mdns")
			}
		}
	}
	if s.DB.Migrator().HasTable(&sambaModels.SambaSettings{}) {
		var settings []sambaModels.SambaSettings
		if err := s.DB.Select("interfaces").Find(&settings).Error; err != nil {
			return fmt.Errorf("check standard switch Samba usage: %w", err)
		}
		for _, setting := range settings {
			if commaSeparatedInterfacesContain(setting.Interfaces, bridgeName) {
				return standardSwitchInUse("standard_switch_in_use_by_samba")
			}
		}
	}

	if s.DB.Migrator().HasTable(&networkModels.WireGuardServer{}) {
		var servers []networkModels.WireGuardServer
		if err := s.DB.Select("masquerade_ipv4_interface", "masquerade_ipv6_interface").Find(&servers).Error; err != nil {
			return fmt.Errorf("check standard switch WireGuard usage: %w", err)
		}
		for _, server := range servers {
			if strings.TrimSpace(server.MasqueradeIPv4Interface) == bridgeName ||
				strings.TrimSpace(server.MasqueradeIPv6Interface) == bridgeName {
				return standardSwitchInUse("standard_switch_in_use_by_wireguard")
			}
		}
	}

	return nil
}

func (s *Service) checkStandardSwitchUsage(id uint, bridgeName string) error {
	checks := []struct {
		query *gorm.DB
		code  string
		label string
	}{
		{
			query: s.DB.Model(&vmModels.Network{}).Where("switch_id = ? AND switch_type = ?", id, "standard"),
			code:  "standard_switch_in_use_by_vm",
			label: "VM",
		},
		{
			query: s.DB.Model(&jailModels.Network{}).Where("switch_id = ? AND switch_type = ?", id, "standard"),
			code:  "standard_switch_in_use_by_jail",
			label: "jail",
		},
		{
			query: s.DB.Table("dhcp_standard_switches").Where("standard_switch_id = ?", id),
			code:  "standard_switch_in_use_by_dhcp_config",
			label: "DHCP config",
		},
		{
			query: s.DB.Model(&networkModels.DHCPRange{}).Where("standard_switch_id = ?", id),
			code:  "standard_switch_in_use_by_dhcp_range",
			label: "DHCP range",
		},
	}

	for _, check := range checks {
		var count int64
		if err := check.query.Count(&count).Error; err != nil {
			return fmt.Errorf("check standard switch %s usage: %w", check.label, err)
		}
		if count > 0 {
			return standardSwitchInUse(check.code)
		}
	}

	return s.checkStandardSwitchExternalUsage(bridgeName)
}
