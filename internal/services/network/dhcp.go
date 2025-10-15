// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"os"
	"regexp"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) GetConfig() (*networkModels.DHCPConfig, error) {
	var config networkModels.DHCPConfig
	if err := s.DB.
		Preload("StandardSwitches").
		Preload("ManualSwitches").First(&config).Error; err != nil {
		if err.Error() == "record not found" {
			return &networkModels.DHCPConfig{
				StandardSwitches: []networkModels.StandardSwitch{},
				ManualSwitches:   []networkModels.ManualSwitch{},
				DNSServers:       []string{},
				Domain:           "",
				ExpandHosts:      true,
			}, nil
		}

		return nil, err
	}

	return &config, nil
}

func (s *Service) SaveConfig(req *networkServiceInterfaces.ModifyDHCPConfigRequest) error {
	current := &networkModels.DHCPConfig{}
	if err := s.DB.
		Preload("StandardSwitches").
		Preload("ManualSwitches").
		First(current).Error; err != nil {
		return fmt.Errorf("config_not_initialized: %w", err)
	}

	if len(req.StandardSwitches) > 0 {
		var count int64
		if err := s.DB.Model(&networkModels.StandardSwitch{}).
			Where("id IN ?", req.StandardSwitches).
			Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(req.StandardSwitches)) {
			return fmt.Errorf("one or more standard switch IDs are invalid")
		}
	}

	if len(req.ManualSwitches) > 0 {
		var count int64
		if err := s.DB.Model(&networkModels.ManualSwitch{}).
			Where("id IN ?", req.ManualSwitches).
			Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(req.ManualSwitches)) {
			return fmt.Errorf("one or more manual switch IDs are invalid")
		}
	}

	if len(req.DNSServers) > 0 {
		for _, dns := range req.DNSServers {
			if !utils.IsValidIPv4(dns) && !utils.IsValidIPv6(dns) {
				return fmt.Errorf("invalid_dns_server_ip: %s", dns)
			}
		}
	}

	var domainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	if !domainRegex.MatchString(req.Domain) {
		return fmt.Errorf("invalid_domain: %s", req.Domain)
	}

	sameCount := 0

	if current.StandardSwitches != nil && len(current.StandardSwitches) == len(req.StandardSwitches) {
		switchMap := make(map[uint]bool, len(req.StandardSwitches))
		for _, id := range req.StandardSwitches {
			switchMap[id] = true
		}
		same := true
		for _, sw := range current.StandardSwitches {
			if !switchMap[sw.ID] {
				same = false
				break
			}
		}
		if same {
			sameCount++
		}
	}

	if current.ManualSwitches != nil && len(current.ManualSwitches) == len(req.ManualSwitches) {
		switchMap := make(map[uint]bool, len(req.ManualSwitches))
		for _, id := range req.ManualSwitches {
			switchMap[id] = true
		}
		same := true
		for _, sw := range current.ManualSwitches {
			if !switchMap[sw.ID] {
				same = false
				break
			}
		}
		if same {
			sameCount++
		}
	}

	if current.DNSServers != nil && len(current.DNSServers) == len(req.DNSServers) {
		dnsMap := make(map[string]bool, len(req.DNSServers))
		for _, d := range req.DNSServers {
			dnsMap[d] = true
		}
		same := true
		for _, d := range current.DNSServers {
			if !dnsMap[d] {
				same = false
				break
			}
		}
		if same {
			sameCount++
		}
	}

	if current.Domain == req.Domain {
		sameCount++
	}

	if req.ExpandHosts != nil && current.ExpandHosts == *req.ExpandHosts {
		sameCount++
	}

	if sameCount == 5 {
		return fmt.Errorf("no_changes_detected")
	}

	var stdSwitches []networkModels.StandardSwitch
	if len(req.StandardSwitches) > 0 {
		if err := s.DB.Where("id IN ?", req.StandardSwitches).Find(&stdSwitches).Error; err != nil {
			return err
		}
	}

	var manSwitches []networkModels.ManualSwitch
	if len(req.ManualSwitches) > 0 {
		if err := s.DB.Where("id IN ?", req.ManualSwitches).Find(&manSwitches).Error; err != nil {
			return err
		}
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(current).Association("StandardSwitches").Replace(stdSwitches); err != nil {
			return err
		}
		if err := tx.Model(current).Association("ManualSwitches").Replace(manSwitches); err != nil {
			return err
		}

		current.DNSServers = append([]string(nil), req.DNSServers...)
		current.Domain = req.Domain
		if req.ExpandHosts != nil {
			current.ExpandHosts = *req.ExpandHosts
		} else {
			current.ExpandHosts = true
		}

		return tx.Save(current).Error
	}); err != nil {
		return err
	}

	return s.WriteDHCPConfig()
}

func (s *Service) WriteDHCPConfig() error {
	var current networkModels.DHCPConfig
	var config string

	if err := s.DB.
		Preload("StandardSwitches").
		Preload("ManualSwitches").
		First(&current).Error; err != nil {
		return fmt.Errorf("config_not_initialized: %w", err)
	}

	var interfaces []string
	for _, sw := range current.StandardSwitches {
		interfaces = append(interfaces, sw.BridgeName)
	}

	for _, sw := range current.ManualSwitches {
		interfaces = append(interfaces, sw.Bridge)
	}

	config += "# This file is managed by Sylve. Manual changes will be overwritten.\n\n"

	for _, iface := range interfaces {
		config += fmt.Sprintf("interface=%s\n", iface)
	}

	if len(interfaces) > 0 {
		config += "bind-interfaces\n\n"
	}

	if len(current.DNSServers) > 0 {
		for _, dns := range current.DNSServers {
			config += fmt.Sprintf("server=%s\n", dns)
		}
		config += "\n"
	}

	if current.Domain != "" {
		config += fmt.Sprintf("domain=%s\n", current.Domain)
		config += "\n"
	}

	if current.ExpandHosts {
		config += "expand-hosts\n\n"
	}

	var ranges []networkModels.DHCPRange
	if err := s.DB.Preload("StandardSwitch").Preload("ManualSwitch").Find(&ranges).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_dhcp_ranges: %w", err)
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

		var expiry string
		if r.Expiry == 0 {
			expiry = "infinite"
		} else {
			expiry = fmt.Sprintf("%d", r.Expiry)
		}

		if r.Type == "ipv4" {
			config += fmt.Sprintf("dhcp-range=%s,%s,%s,%s\n", rangeInterface, r.StartIP, r.EndIP, expiry)
		}

		if r.Type == "ipv6" {
			if r.StartIP != "" && r.EndIP != "" {
				config += fmt.Sprintf("dhcp-range=%s,%s,%s", rangeInterface, r.StartIP, r.EndIP)
				if r.RAOnly {
					config += ",ra-only"
				}
				if r.SLAAC {
					config += ",slaac"
				}
				config += fmt.Sprintf(",%s\n", expiry)
			} else {
				config += fmt.Sprintf("dhcp-range=::,constructor:%s", rangeInterface)
				if r.RAOnly {
					config += ",ra-only"
				}
				if r.SLAAC {
					config += ",slaac"
				}
				config += fmt.Sprintf(",%s\n", expiry)
			}
		}
	}

	config += "\n"

	var leases []networkModels.DHCPStaticLease
	if err := s.DB.
		Preload("IPObject.Entries").
		Preload("MACObject.Entries").
		Preload("DUIDObject.Entries").Find(&leases).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_static_leases: %w", err)
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

		if ip == "" && mac == "" && duid == "" {
			continue
		}

		ipCfg := ""

		if ipType == "ipv4" {
			if ip == "" || mac == "" {
				continue
			}

			ipCfg = fmt.Sprintf("dhcp-host=%s,%s", mac, ip)

			if lease.Hostname != "" {
				ipCfg += fmt.Sprintf(",%s", lease.Hostname)
			}

			ipCfg += ",infinite"

			config += ipCfg + "\n"
			continue
		}

		if ipType == "ipv6" {
			if ip == "" || duid == "" {
				continue
			}

			ipCfg = fmt.Sprintf("dhcp-host=id:%s,[%s]", duid, ip)

			if lease.Hostname != "" {
				ipCfg += fmt.Sprintf(",%s", lease.Hostname)
			}

			ipCfg += ",infinite"

			config += ipCfg + "\n"
			continue
		}
	}

	config += "\n"

	filePath := "/usr/local/etc/dnsmasq.conf"

	if err := os.WriteFile(filePath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write dnsmasq configuration to %s: %w", filePath, err)
	}

	return system.ServiceAction("dnsmasq", "restart")
}
