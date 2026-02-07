// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) SetInheritance(ctId uint, ipv4 bool, ipv6 bool) error {
	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return err
	}

	mountPoint, err := s.GetJailBaseMountPoint(ctId)
	if err != nil {
		return err
	}

	if jail.InheritIPv4 == ipv4 && jail.InheritIPv6 == ipv6 {
		return nil
	}

	preStartPath, err := s.GetHookScriptPath(ctId, "pre-start")
	if err != nil {
		return err
	}

	startPath, err := s.GetHookScriptPath(ctId, "start")
	if err != nil {
		return err
	}

	var inheriting bool

	if ipv4 || ipv6 {
		inheriting = true
	} else {
		inheriting = false
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return err
	}

	// This will clean up jail config from any existing vnet settings
	lines := strings.Split(cfg, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], "vnet;") ||
			strings.Contains(lines[i], "vnet.interface") ||
			strings.Contains(lines[i], "ip4=") ||
			strings.Contains(lines[i], "ip6=") {
			lines = append(lines[:i], lines[i+1:]...)
			i--
		}
	}

	// We need to clean up rc.conf if it's a FreeBSD jail
	if jail.Type == jailModels.JailTypeFreeBSD {
		rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")
		if _, statErr := os.Stat(rcConfPath); statErr == nil {
			rcConf, err := os.ReadFile(rcConfPath)
			if err != nil {
				return err
			}

			rcLines := strings.Split(string(rcConf), "\n")
			for i := 0; i < len(rcLines); i++ {
				if strings.HasPrefix(rcLines[i], "ifconfig") ||
					strings.HasPrefix(rcLines[i], "ipv6") ||
					strings.HasPrefix(rcLines[i], "defaultrouter") {
					rcLines = append(rcLines[:i], rcLines[i+1:]...)
					i--
				}
			}

			if err := os.WriteFile(rcConfPath, []byte(strings.Join(rcLines, "\n")), 0644); err != nil {
				return err
			}
		}
	}

	preStartCfg, err := os.ReadFile(preStartPath)
	if err != nil {
		return err
	}

	startCfg, err := os.ReadFile(startPath)
	if err != nil {
		return err
	}

	cleanedPrestartCfg := s.RemoveSylveAdditionsFromHook(string(preStartCfg))
	if err != nil {
		return err
	}

	cleanedStartCfg := s.RemoveSylveAdditionsFromHook(string(startCfg))
	if err != nil {
		return err
	}

	if err := os.WriteFile(preStartPath, []byte(cleanedPrestartCfg), 0755); err != nil {
		return err
	}

	if err := os.WriteFile(startPath, []byte(cleanedStartCfg), 0755); err != nil {
		return err
	}

	if inheriting {
		// Clean up existing epair interfaces if any networks exist
		if len(jail.Networks) > 0 {
			ctidHash := utils.HashIntToNLetters(int(ctId), 5)

			jail.InheritIPv4 = ipv4
			jail.InheritIPv6 = ipv6
			if err := s.DB.Save(&jail).Error; err != nil {
				return err
			}

			for _, network := range jail.Networks {
				if network.SwitchID > 0 {
					epairName := fmt.Sprintf("%s_%s", ctidHash, fmt.Sprintf("net%d", network.ID))
					logger.L.Debug().Msgf("SetInheritance: deleting epair %s", epairName)
					if err := s.NetworkService.DeleteEpair(epairName); err != nil {
						logger.L.Warn().Msgf("Warning: failed to delete epair %s: %v", epairName, err)
					}
				}
			}

			result := s.DB.Where("jid = ?", jail.ID).Delete(&jailModels.Network{})
			if result.Error != nil {
				return fmt.Errorf("failed to delete network entries: %w", result.Error)
			}
		} else {
			// No networks to clean up, just update jail flags
			jail.InheritIPv4 = ipv4
			jail.InheritIPv6 = ipv6
			if err := s.DB.Save(&jail).Error; err != nil {
				return err
			}
		}

		var toAppend strings.Builder
		if ipv4 {
			toAppend.WriteString("\tip4=inherit;\n")
		}
		if ipv6 {
			toAppend.WriteString("\tip6=inherit;\n")
		}

		newCfg, err := s.AppendToConfig(ctId, strings.Join(lines, "\n"), toAppend.String())
		if err != nil {
			return err
		}

		if err := s.SaveJailConfig(ctId, newCfg); err != nil {
			return err
		}
	} else {
		// Disinheriting - no networks should exist since they were deleted when inheriting
		jail.InheritIPv4 = ipv4
		jail.InheritIPv6 = ipv6
		if err := s.DB.Save(&jail).Error; err != nil {
			return err
		}

		// Since we deleted all networks during inherit, set to disable mode
		toAppend := "\tip4=disable;\n\tip6=disable;\n"
		newCfg, err := s.AppendToConfig(ctId, strings.Join(lines, "\n"), toAppend)
		if err != nil {
			return err
		}

		if err := s.SaveJailConfig(ctId, newCfg); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) AddNetwork(req jailServiceInterfaces.AddJailNetworkRequest) error {
	macId := uint(0)
	ip4 := uint(0)
	ip4gw := uint(0)
	ip6 := uint(0)
	ip6gw := uint(0)
	dhcp := false
	slaac := false
	defaultGateway := false

	if req.IP4 != nil {
		ip4 = *req.IP4
	}

	if req.IP4GW != nil {
		ip4gw = *req.IP4GW
	}

	if req.IP6 != nil {
		ip6 = *req.IP6
	}

	if req.IP6GW != nil {
		ip6gw = *req.IP6GW
	}

	if req.DHCP != nil {
		dhcp = *req.DHCP
	}

	if req.SLAAC != nil {
		slaac = *req.SLAAC
	}

	if req.MacID != nil {
		macId = *req.MacID
	}

	if req.DefaultGateway != nil {
		defaultGateway = *req.DefaultGateway
	}

	if dhcp && slaac && defaultGateway {
		return fmt.Errorf("cannot_set_dhcp_slaac_and_default_gateway_together")
	}

	ctId := req.CTID
	switchName := req.SwitchName

	var jail jailModels.Jail
	var network jailModels.Network

	if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		return err
	}

	if jail.Type == jailModels.JailTypeLinux {
		if ip4 != 0 || ip4gw != 0 || ip6 != 0 || ip6gw != 0 {
			return fmt.Errorf("cannot_set_ip_when_linux_jail")
		}

		if dhcp || slaac {
			return fmt.Errorf("cannot_set_dhcp_or_slaac_when_linux_jail")
		}
	}

	if jail.InheritIPv4 || jail.InheritIPv6 {
		return fmt.Errorf("cannot_add_network_when_inheriting_network")
	}

	switchId := uint(0)
	switchType := ""
	dbSwName := ""

	var stdSwitch networkModels.StandardSwitch
	if err := s.DB.Where("name = ?", switchName).First(&stdSwitch).Error; err == nil {
		switchId = stdSwitch.ID
		switchType = "standard"
		dbSwName = stdSwitch.Name
	} else {
		var manualSwitch networkModels.ManualSwitch
		if err := s.DB.Where("name = ?", switchName).First(&manualSwitch).Error; err == nil {
			switchId = manualSwitch.ID
			switchType = "manual"
			dbSwName = manualSwitch.Name
		}
	}

	if switchType == "" || switchId == 0 {
		return fmt.Errorf("switch_not_found")
	}

	network.SwitchID = switchId
	network.SwitchType = switchType

	if !dhcp {
		if ip4 != 0 && ip4gw != 0 {
			_, err := s.NetworkService.GetObjectEntryByID(ip4)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip4_object: %w", err)
			}

			_, err = s.NetworkService.GetObjectEntryByID(ip4gw)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip4gw_object: %w", err)
			}

			network.IPv4ID = &ip4
			network.IPv4GwID = &ip4gw
		}
	} else {
		network.DHCP = true
	}

	if !slaac {
		if ip6 != 0 && ip6gw != 0 {
			_, err := s.NetworkService.GetObjectEntryByID(ip6)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip6_object: %w", err)
			}

			_, err = s.NetworkService.GetObjectEntryByID(ip6gw)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip6gw_object: %w", err)
			}

			network.IPv6ID = &ip6
			network.IPv6GwID = &ip6gw
		}
	} else {
		network.SLAAC = true
	}

	if macId == 0 {
		macAddress := utils.GenerateRandomMAC()
		base := fmt.Sprintf("%s-%s", jail.Name, dbSwName)
		name := base

		for i := 0; ; i++ {
			if i > 0 {
				name = fmt.Sprintf("%s-%d", base, i)
			}

			var exists int64

			if err := s.DB.
				Model(&networkModels.Object{}).
				Where("name = ?", name).
				Limit(1).
				Count(&exists).Error; err != nil {
				return fmt.Errorf("failed_to_check_mac_object_exists: %w", err)
			}

			if exists == 0 {
				break
			}
		}

		macObj := networkModels.Object{
			Name: name,
			Type: "Mac",
		}

		if err := s.DB.Create(&macObj).Error; err != nil {
			return fmt.Errorf("failed_to_create_mac_object: %w", err)
		}

		macEntry := networkModels.ObjectEntry{
			ObjectID: macObj.ID,
			Value:    macAddress,
		}

		if err := s.DB.Create(&macEntry).Error; err != nil {
			return fmt.Errorf("failed_to_create_mac_entry: %w", err)
		}

		network.MacID = &macObj.ID
	} else {
		_, err := s.NetworkService.GetObjectEntryByID(macId)
		if err != nil {
			return fmt.Errorf("failed_to_get_mac_object: %w", err)
		}

		network.MacID = &macId
	}

	network.Name = req.Name
	network.JailID = jail.ID
	err := s.DB.Create(&network).Error
	if err != nil {
		return fmt.Errorf("failed_to_create_network: %w", err)
	}

	err = s.NetworkService.SyncEpairs(false)
	if err != nil {
		return fmt.Errorf("failed_to_sync_epairs: %w", err)
	}

	// Reload jail with new network
	if err := s.DB.Preload("Networks").
		Where("ct_id = ?", ctId).
		First(&jail).Error; err != nil {
		return err
	}

	return s.SyncNetwork(ctId, jail)
}

func (s *Service) DeleteNetwork(ctId uint, networkId uint) error {
	var network jailModels.Network
	err := s.DB.Find(&network, networkId).Error
	if err != nil {
		return fmt.Errorf("failed_to_find_network: %w", err)
	}

	epair := fmt.Sprintf("%s_%s", utils.HashIntToNLetters(int(ctId), 5), fmt.Sprintf("net%d", network.ID))
	err = s.NetworkService.DeleteEpair(epair)
	if err != nil {
		return err
	}

	err = s.DB.Where("id = ?", networkId).Delete(&network).Error
	if err != nil {
		return err
	}

	// Reload jail after deletion and sync
	var jail jailModels.Jail
	if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		return err
	}

	return s.SyncNetwork(ctId, jail)
}

func (s *Service) SyncNetwork(ctId uint, jail jailModels.Jail) error {
	mountPoint, err := s.GetJailBaseMountPoint(ctId)
	if err != nil {
		return err
	}

	// Clean up jail config from any existing network settings
	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return err
	}

	lines := strings.Split(cfg, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], "vnet;") ||
			strings.Contains(lines[i], "vnet.interface") ||
			strings.Contains(lines[i], "ip4=") ||
			strings.Contains(lines[i], "ip6=") {
			lines = append(lines[:i], lines[i+1:]...)
			i--
		}
	}

	// Clean up rc.conf if it's a FreeBSD jail
	if jail.Type == jailModels.JailTypeFreeBSD {
		rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")
		if _, statErr := os.Stat(rcConfPath); statErr == nil {
			rcConf, err := os.ReadFile(rcConfPath)
			if err != nil {
				return err
			}

			rcLines := strings.Split(string(rcConf), "\n")
			for i := 0; i < len(rcLines); i++ {
				if strings.HasPrefix(rcLines[i], "ifconfig") ||
					strings.HasPrefix(rcLines[i], "ipv6") ||
					strings.HasPrefix(rcLines[i], "defaultrouter") ||
					strings.HasPrefix(rcLines[i], "# Sylve Network Configuration") {
					rcLines = append(rcLines[:i], rcLines[i+1:]...)
					i--
				}
			}

			if err := os.WriteFile(rcConfPath, []byte(strings.Join(rcLines, "\n")), 0644); err != nil {
				return err
			}
		}
	}

	// Clean up hook scripts
	hooks := []string{"pre-start", "start", "post-start"}
	for _, hookName := range hooks {
		hookPath, err := s.GetHookScriptPath(ctId, hookName)
		if err != nil {
			continue // Hook file might not exist, that's ok
		}

		hookContent, err := os.ReadFile(hookPath)
		if err != nil {
			continue
		}

		cleanedContent := s.RemoveSylveAdditionsFromHook(string(hookContent))
		if err := os.WriteFile(hookPath, []byte(cleanedContent), 0755); err != nil {
			return err
		}
	}

	var newCfg string

	// Handle inheritance mode
	if jail.InheritIPv4 || jail.InheritIPv6 {
		var toAppend strings.Builder
		if jail.InheritIPv4 {
			toAppend.WriteString("\tip4=inherit;\n")
		}
		if jail.InheritIPv6 {
			toAppend.WriteString("\tip6=inherit;\n")
		}

		newCfg, err = s.AppendToConfig(ctId, strings.Join(lines, "\n"), toAppend.String())
		if err != nil {
			return err
		}
	} else {
		// VNET mode
		if jail.Networks != nil && len(jail.Networks) > 0 {
			ctidHash := utils.HashIntToNLetters(int(ctId), 5)

			// Ensure epairs exist
			if err := s.NetworkService.SyncEpairs(false); err != nil {
				return err
			}

			// Build jail config additions
			var jailCfgBuilder strings.Builder
			jailCfgBuilder.WriteString("\tvnet;\n")

			hasPreStartPointer := false
			for _, line := range lines {
				if strings.Contains(line, "exec.prestart") {
					hasPreStartPointer = true
					break
				}
			}

			if !hasPreStartPointer {
				preStartPath, err := s.GetHookScriptPath(ctId, "pre-start")
				if err == nil {
					jailCfgBuilder.WriteString(fmt.Sprintf("\texec.prestart += \"%s\";\n", preStartPath))
				}
			}

			// Add vnet.interface entries
			for _, n := range jail.Networks {
				if n.SwitchID == 0 {
					continue
				}
				jailCfgBuilder.WriteString(fmt.Sprintf("\tvnet.interface += \"%s_%sb\";\n", ctidHash, fmt.Sprintf("net%d", n.ID)))
			}

			// Build pre-start hook script content
			var preStartBuilder strings.Builder

			// Build rc.conf content for network configuration
			var rcConfLines []string

			for _, n := range jail.Networks {
				if n.SwitchID == 0 {
					continue
				}
				networkId := fmt.Sprintf("net%d", n.ID)

				// MAC and bridge setup in pre-start
				if n.MacID != nil && *n.MacID > 0 {
					mac, err := s.NetworkService.GetObjectEntryByID(*n.MacID)
					if err != nil {
						return fmt.Errorf("failed to get mac address: %w", err)
					}
					prevMAC, err := utils.PreviousMAC(mac)
					if err != nil {
						return fmt.Errorf("failed to get previous mac: %w", err)
					}

					epairA := fmt.Sprintf("%s_%sa", ctidHash, networkId)
					epairB := fmt.Sprintf("%s_%sb", ctidHash, networkId)

					preStartBuilder.WriteString(fmt.Sprintf("# Setup Network Interface %s\n", epairB))
					preStartBuilder.WriteString(fmt.Sprintf("ifconfig %s ether %s up\n", epairA, prevMAC))
					preStartBuilder.WriteString(fmt.Sprintf(
						"ifconfig %s descr \"(%s) (%d)\"\n",
						epairA,
						jail.Name,
						jail.CTID,
					))

					preStartBuilder.WriteString(fmt.Sprintf("ifconfig %s ether %s up\n", epairB, mac))
					preStartBuilder.WriteString("\n")

					bridgeName, err := s.NetworkService.GetBridgeNameByIDType(n.SwitchID, n.SwitchType)
					if err != nil {
						return fmt.Errorf("failed to get bridge name: %w", err)
					}
					preStartBuilder.WriteString(fmt.Sprintf("if ! ifconfig %s | grep -qw %s; then\n", bridgeName, epairA))
					preStartBuilder.WriteString(fmt.Sprintf("\tifconfig %s addm %s 2>&1 || true\n", bridgeName, epairA))
					preStartBuilder.WriteString("fi\n")
					preStartBuilder.WriteString(fmt.Sprintf("# End Setup Network Interface %s\n\n", epairB))
				}

				// IPv4 configuration
				if n.DHCP {
					rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_%sb=\"SYNCDHCP\"", ctidHash, networkId))
				} else if n.IPv4ID != nil && *n.IPv4ID > 0 && n.IPv4GwID != nil && *n.IPv4GwID > 0 {
					ipv4, err := s.NetworkService.GetObjectEntryByID(*n.IPv4ID)
					if err != nil {
						return fmt.Errorf("failed to get ipv4 address: %w", err)
					}
					ipv4Gw, err := s.NetworkService.GetObjectEntryByID(*n.IPv4GwID)
					if err != nil {
						return fmt.Errorf("failed to get ipv4 gateway: %w", err)
					}
					ip, mask, err := utils.SplitIPv4AndMask(ipv4)
					if err != nil {
						return fmt.Errorf("failed to split ipv4 address and mask: %w", err)
					}

					rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_%sb=\"inet %s netmask %s\"", ctidHash, networkId, ip, mask))

					if n.DefaultGateway {
						rcConfLines = append(rcConfLines, fmt.Sprintf("defaultrouter=\"%s\"", ipv4Gw))
					}
				}

				// IPv6 configuration
				if n.SLAAC {
					rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_%sb_ipv6=\"inet6 accept_rtadv\"", ctidHash, networkId))
				} else if n.IPv6ID != nil && *n.IPv6ID > 0 && n.IPv6GwID != nil && *n.IPv6GwID > 0 {
					ipv6, err := s.NetworkService.GetObjectEntryByID(*n.IPv6ID)
					if err != nil {
						return fmt.Errorf("failed to get ipv6 address: %w", err)
					}
					ipv6Gw, err := s.NetworkService.GetObjectEntryByID(*n.IPv6GwID)
					if err != nil {
						return fmt.Errorf("failed to get ipv6 gateway: %w", err)
					}

					rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_%sb_ipv6=\"inet6 %s\"", ctidHash, networkId, ipv6))
					if n.DefaultGateway {
						rcConfLines = append(rcConfLines, fmt.Sprintf("ipv6_defaultrouter=\"%s\"", ipv6Gw))
					}
				}
			}

			// Write network configuration to rc.conf
			if len(rcConfLines) > 0 && jail.Type == jailModels.JailTypeFreeBSD {
				rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")

				// Read current rc.conf content (already cleaned above)
				currentRcConf, err := os.ReadFile(rcConfPath)
				if err != nil {
					return err
				}

				// Append new network configuration
				rcConfContent := string(currentRcConf)
				if !strings.HasSuffix(rcConfContent, "\n") {
					rcConfContent += "\n"
				}
				rcConfContent += "# Sylve Network Configuration\n"
				rcConfContent += strings.Join(rcConfLines, "\n") + "\n"

				if err := os.WriteFile(rcConfPath, []byte(rcConfContent), 0644); err != nil {
					return err
				}
			}

			// Update hook files with network configuration
			preStartPath, err := s.GetHookScriptPath(ctId, "pre-start")
			if err != nil {
				return err
			}

			currentPreStartContent, err := os.ReadFile(preStartPath)
			if err != nil {
				return err
			}

			newPreStartContent := s.AddSylveNetworkToHook(string(currentPreStartContent), preStartBuilder.String())
			if err := os.WriteFile(preStartPath, []byte(newPreStartContent), 0755); err != nil {
				return err
			}

			// Add jail config
			newCfg, err = s.AppendToConfig(ctId, strings.Join(lines, "\n"), jailCfgBuilder.String())
			if err != nil {
				return err
			}
		} else {
			// No networks configured: disable both stacks
			toAppend := "\tip4=disable;\n\tip6=disable;\n"
			newCfg, err = s.AppendToConfig(ctId, strings.Join(lines, "\n"), toAppend)
			if err != nil {
				return err
			}
		}
	}

	if err := s.SaveJailConfig(ctId, newCfg); err != nil {
		return err
	}

	return nil
}

func (s *Service) EditNetwork(req jailServiceInterfaces.EditJailNetworkRequest) error {
	macId := uint(0)
	ip4 := uint(0)
	ip4gw := uint(0)
	ip6 := uint(0)
	ip6gw := uint(0)
	dhcp := false
	slaac := false
	defaultGateway := false

	if req.IP4 != nil {
		ip4 = *req.IP4
	}

	if req.IP4GW != nil {
		ip4gw = *req.IP4GW
	}

	if req.IP6 != nil {
		ip6 = *req.IP6
	}

	if req.IP6GW != nil {
		ip6gw = *req.IP6GW
	}

	if req.DHCP != nil {
		dhcp = *req.DHCP
	}

	if req.SLAAC != nil {
		slaac = *req.SLAAC
	}

	if req.MacID != nil {
		macId = *req.MacID
	}

	if req.DefaultGateway != nil {
		defaultGateway = *req.DefaultGateway
	}

	if dhcp && slaac && defaultGateway {
		return fmt.Errorf("cannot_set_dhcp_slaac_and_default_gateway_together")
	}

	switchName := req.SwitchName

	// Find the network to edit
	var network jailModels.Network
	if err := s.DB.First(&network, "id = ?", req.NetworkID).Error; err != nil {
		return fmt.Errorf("failed_to_find_network: %w", err)
	}

	// Find the jail this network belongs to
	var jail jailModels.Jail
	if err := s.DB.Preload("Networks").Where("id = ?", network.JailID).First(&jail).Error; err != nil {
		return fmt.Errorf("failed_to_find_jail: %w", err)
	}

	if jail.Type == jailModels.JailTypeLinux {
		if ip4 != 0 || ip4gw != 0 || ip6 != 0 || ip6gw != 0 {
			return fmt.Errorf("cannot_set_ip_when_linux_jail")
		}

		if dhcp || slaac {
			return fmt.Errorf("cannot_set_dhcp_or_slaac_when_linux_jail")
		}
	}

	if jail.InheritIPv4 || jail.InheritIPv6 {
		return fmt.Errorf("cannot_edit_network_when_inheriting_network")
	}

	// Find switch information
	switchId := uint(0)
	switchType := ""
	dbSwName := ""

	var stdSwitch networkModels.StandardSwitch
	if err := s.DB.Where("name = ?", switchName).First(&stdSwitch).Error; err == nil {
		switchId = stdSwitch.ID
		switchType = "standard"
		dbSwName = stdSwitch.Name
	} else {
		var manualSwitch networkModels.ManualSwitch
		if err := s.DB.Where("name = ?", switchName).First(&manualSwitch).Error; err == nil {
			switchId = manualSwitch.ID
			switchType = "manual"
			dbSwName = manualSwitch.Name
		}
	}

	if switchType == "" || switchId == 0 {
		return fmt.Errorf("switch_not_found")
	}

	// Check if switching to a different switch - need to handle epair cleanup/recreation
	switchChanged := network.SwitchID != switchId || network.SwitchType != switchType

	// Update network properties
	network.Name = req.Name
	network.SwitchID = switchId
	network.SwitchType = switchType

	// Reset IP configurations
	network.IPv4ID = nil
	network.IPv4GwID = nil
	network.IPv6ID = nil
	network.IPv6GwID = nil
	network.DHCP = false
	network.SLAAC = false
	network.DefaultGateway = defaultGateway

	// Set IPv4 configuration
	if !dhcp {
		if ip4 != 0 && ip4gw != 0 {
			_, err := s.NetworkService.GetObjectEntryByID(ip4)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip4_object: %w", err)
			}

			_, err = s.NetworkService.GetObjectEntryByID(ip4gw)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip4gw_object: %w", err)
			}

			network.IPv4ID = &ip4
			network.IPv4GwID = &ip4gw
		}
	} else {
		network.DHCP = true
	}

	// Set IPv6 configuration
	if !slaac {
		if ip6 != 0 && ip6gw != 0 {
			_, err := s.NetworkService.GetObjectEntryByID(ip6)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip6_object: %w", err)
			}

			_, err = s.NetworkService.GetObjectEntryByID(ip6gw)
			if err != nil {
				return fmt.Errorf("failed_to_get_ip6gw_object: %w", err)
			}

			network.IPv6ID = &ip6
			network.IPv6GwID = &ip6gw
		}
	} else {
		network.SLAAC = true
	}

	// Handle MAC address
	if macId == 0 {
		// Generate new MAC if not provided
		macAddress := utils.GenerateRandomMAC()
		base := fmt.Sprintf("%s-%s", jail.Name, dbSwName)
		name := base

		for i := 0; ; i++ {
			if i > 0 {
				name = fmt.Sprintf("%s-%d", base, i)
			}

			var exists int64
			if err := s.DB.
				Model(&networkModels.Object{}).
				Where("name = ?", name).
				Limit(1).
				Count(&exists).Error; err != nil {
				return fmt.Errorf("failed_to_check_mac_object_exists: %w", err)
			}

			if exists == 0 {
				break
			}
		}

		macObj := networkModels.Object{
			Name: name,
			Type: "Mac",
		}

		if err := s.DB.Create(&macObj).Error; err != nil {
			return fmt.Errorf("failed_to_create_mac_object: %w", err)
		}

		macEntry := networkModels.ObjectEntry{
			ObjectID: macObj.ID,
			Value:    macAddress,
		}

		if err := s.DB.Create(&macEntry).Error; err != nil {
			return fmt.Errorf("failed_to_create_mac_entry: %w", err)
		}

		network.MacID = &macObj.ID
	} else {
		_, err := s.NetworkService.GetObjectEntryByID(macId)
		if err != nil {
			return fmt.Errorf("failed_to_get_mac_object: %w", err)
		}

		network.MacID = &macId
	}

	// Save the updated network
	if err := s.DB.Save(&network).Error; err != nil {
		return fmt.Errorf("failed_to_update_network: %w", err)
	}

	// If switch changed, sync epairs to handle interface changes
	if switchChanged {
		if err := s.NetworkService.SyncEpairs(false); err != nil {
			return fmt.Errorf("failed_to_sync_epairs: %w", err)
		}
	}

	// Reload jail with updated network and sync configuration
	if err := s.DB.Preload("Networks").Where("ct_id = ?", jail.CTID).First(&jail).Error; err != nil {
		return fmt.Errorf("failed_to_reload_jail: %w", err)
	}

	return s.SyncNetwork(jail.CTID, jail)
}

func (s *Service) WatchNetworkObjectChanges() error {
	var triggers []models.Triggers
	if err := s.DB.
		Where("action = ? AND completed = 0", "edit_network_object_used_by_jails").
		Find(&triggers).Error; err != nil {
		return fmt.Errorf("failed to find triggers: %w", err)
	}

	if len(triggers) == 0 {
		return nil
	}

	jailToTriggerIDs := map[int64][]int64{}
	for _, t := range triggers {
		var jailIDs []int64
		if err := json.Unmarshal([]byte(t.Data), &jailIDs); err != nil {
			logger.L.Warn().Msgf("Bad trigger data id=%d data=%q err=%v\n", t.ID, t.Data, err)
			continue
		}
		for _, jailID := range jailIDs {
			jailToTriggerIDs[jailID] = append(jailToTriggerIDs[jailID], int64(t.ID))
		}
	}

	for jailID := range jailToTriggerIDs {
		var jail jailModels.Jail
		if err := s.DB.Preload("Networks").First(&jail, "id = ?", jailID).Error; err != nil {
			logger.L.Warn().Msgf("Failed to find jail id=%d err=%v\n", jailID, err)
			continue
		}

		err := s.SyncNetwork(uint(jail.CTID), jail)

		if err != nil {
			logger.L.Warn().Msgf("Failed to sync network for jail id=%d err=%v\n", jailID, err)
		}
	}

	var allTriggerIDs []int64
	for _, ids := range jailToTriggerIDs {
		allTriggerIDs = append(allTriggerIDs, ids...)
	}

	if err := s.DB.Model(&models.Triggers{}).
		Where("id IN ?", allTriggerIDs).
		Updates(map[string]any{
			"completed": true,
		}).Error; err != nil {
		return fmt.Errorf("failed to mark triggers complete: %w", err)
	}

	return nil
}
