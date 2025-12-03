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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) DisinheritNetwork(ctId uint) error {
	var jail jailModels.Jail

	if err := s.DB.Preload("Networks").
		Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		return err
	}

	jail.InheritIPv4 = false
	jail.InheritIPv6 = false

	return s.SyncNetwork(ctId, jail, true)
}

func (s *Service) InheritNetwork(ctId uint, ipv4 bool, ipv6 bool) error {
	var jail jailModels.Jail

	if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		return err
	}

	jail.InheritIPv4 = ipv4
	jail.InheritIPv6 = ipv6

	return s.SyncNetwork(ctId, jail, true)
}

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
				if strings.HasPrefix(rcLines[i], "ifconfig") {
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

	cleanedCfg := s.RemoveSylveAdditionsFromHook(string(preStartCfg))
	if err != nil {
		return err
	}

	if err := os.WriteFile(preStartPath, []byte(cleanedCfg), 0755); err != nil {
		return err
	}

	if inheriting {
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
	}

	jail.InheritIPv4 = ipv4
	jail.InheritIPv6 = ipv6

	if err := s.DB.Save(&jail).Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) AddNetwork(ctId uint,
	switchName string,
	macId uint,
	ip4 uint,
	ip4gw uint,
	ip6 uint,
	ip6gw uint,
	dhcp bool,
	slaac bool) error {
	var jail jailModels.Jail
	var network jailModels.Network

	if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		return err
	}

	if jail.InheritIPv4 || jail.InheritIPv6 {
		return fmt.Errorf("cannot_add_network_when_inheriting_network")
	}

	existing := false

	for _, network := range jail.Networks {
		if err := network.AfterFind(s.DB); err != nil {
			return err
		}

		if network.StandardSwitch != nil {
			if network.StandardSwitch.Name == switchName {
				existing = true
			}
		}

		if network.ManualSwitch != nil {
			if network.ManualSwitch.Name == switchName {
				existing = true
			}
		}
	}

	if existing {
		return fmt.Errorf("network_with_same_switch_already_exists")
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
		if ip4 == 0 || ip4gw == 0 {
			return fmt.Errorf("ip4_and_ip4gw_must_be_specified_when_dhcp_is_disabled")
		}

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

	network.JailID = jail.ID
	err := s.DB.Create(&network).Error
	if err != nil {
		return fmt.Errorf("failed_to_create_network: %w", err)
	}

	jail.Networks = append(jail.Networks, network)

	err = s.NetworkService.SyncEpairs()

	if err != nil {
		return fmt.Errorf("failed_to_sync_epairs: %w", err)
	}

	return s.SyncNetwork(ctId, jail, true)
}

func (s *Service) DeleteNetwork(ctId uint, networkId uint) error {
	var network jailModels.Network
	err := s.DB.Find(&network, networkId).Error
	if err != nil {
		return fmt.Errorf("failed_to_find_network: %w", err)
	}

	epair := fmt.Sprintf("%s_%d", utils.HashIntToNLetters(int(ctId), 5), network.SwitchID)
	err = s.NetworkService.DeleteEpair(epair)
	if err != nil {
		return err
	}

	var jailNetwork jailModels.Network
	err = s.DB.Where("id = ?", networkId).Delete(&jailNetwork).Error

	if err != nil {
		return err
	}

	var jail jailModels.Jail

	err = s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error
	if err != nil {
		return err
	}

	return s.SyncNetwork(ctId, jail, true)
}

func (s *Service) GetNetworkCleanedConfig(ctId uint) (string, error) {
	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return "", err
	}

	lines := strings.Split(cfg, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.Contains(lines[i], "ip4=") ||
			strings.Contains(lines[i], "ip6=") ||
			strings.Contains(lines[i], "vnet;") ||
			strings.Contains(lines[i], "vnet.interface") ||
			strings.Contains(lines[i], "ifconfig") ||
			strings.Contains(lines[i], "route add default") ||
			strings.Contains(lines[i], "sysrc ipv6") ||
			strings.Contains(lines[i], "dhclient") {
			lines = append(lines[:i], lines[i+1:]...)
			i--
		}
	}

	cfg = strings.Join(lines, "\n")

	return cfg, nil
}

func (s *Service) SyncNetwork(ctId uint, jail jailModels.Jail, save bool) error {
	if save {
		if err := s.DB.Save(&jail).Error; err != nil {
			return err
		}
	}

	cfg, err := s.GetNetworkCleanedConfig(ctId)
	if err != nil {
		return err
	}

	var newCfg string

	// Moving from VNET to Inherited
	if jail.InheritIPv4 || jail.InheritIPv6 {
		if jail.Networks != nil && len(jail.Networks) > 0 {
			for _, network := range jail.Networks {
				if err := s.DeleteNetwork(ctId, network.ID); err != nil {
					return err
				}
			}
		}

		var toAppend strings.Builder
		if jail.InheritIPv4 {
			toAppend.WriteString("\tip4=inherit;\n")
		}
		if jail.InheritIPv6 {
			toAppend.WriteString("\tip6=inherit;\n")
		}

		newCfg, err = s.AppendToConfig(ctId, cfg, toAppend.String())
		if err != nil {
			return err
		}
	} else {
		// VNET mode
		if jail.Networks != nil && len(jail.Networks) > 0 {
			ctidHash := utils.HashIntToNLetters(int(ctId), 5)

			// Ensure epairs exist
			if err := s.NetworkService.SyncEpairs(); err != nil {
				return err
			}

			var b strings.Builder

			// vnet declaration once
			b.WriteString("\tvnet;\n")

			// vnet.interface per NIC
			for _, n := range jail.Networks {
				if n.SwitchID == 0 {
					continue
				}
				b.WriteString(fmt.Sprintf("\tvnet.interface += \"%s_%db\";\n", ctidHash, n.SwitchID))
			}

			// Defaults only once
			setV4Default := false
			setV6Default := false

			// Track if *any* NIC configured IPv6 (SLAAC or static)
			for _, n := range jail.Networks {
				if n.SwitchID == 0 {
					continue
				}
				networkId := n.SwitchID

				// --- MAC + Bridge membership ---
				if n.MacID != nil && *n.MacID > 0 {
					mac, err := s.NetworkService.GetObjectEntryByID(*n.MacID)
					if err != nil {
						return fmt.Errorf("failed to get mac address: %w", err)
					}
					prevMAC, err := utils.PreviousMAC(mac)
					if err != nil {
						return fmt.Errorf("failed to get previous mac: %w", err)
					}

					b.WriteString(fmt.Sprintf("\texec.prestart += \"ifconfig %s_%da ether %s up\";\n", ctidHash, networkId, prevMAC))
					b.WriteString(fmt.Sprintf("\texec.prestart += \"ifconfig %s_%db ether %s up\";\n", ctidHash, networkId, mac))

					bridgeName, err := s.NetworkService.GetBridgeNameByIDType(n.SwitchID, n.SwitchType)
					if err != nil {
						return fmt.Errorf("failed to get bridge name: %w", err)
					}
					b.WriteString(fmt.Sprintf(
						"\texec.prestart += \"if ! ifconfig %s | grep -qw %s_%da; then ifconfig %s addm %s_%da; fi\";\n",
						bridgeName, ctidHash, networkId, bridgeName, ctidHash, networkId,
					))
				}

				// --- IPv4 (independent of IPv6) ---
				if n.DHCP {
					b.WriteString(fmt.Sprintf("\texec.start += \"dhclient %s_%db\";\n", ctidHash, networkId))
					b.WriteString(fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%db=\\\"DHCP\\\"\";\n", ctidHash, networkId))
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

					b.WriteString(fmt.Sprintf("\texec.start += \"ifconfig %s_%db inet %s netmask %s\";\n", ctidHash, networkId, ip, mask))
					if !setV4Default {
						b.WriteString(fmt.Sprintf("\texec.start += \"route add default %s\";\n", ipv4Gw))
						setV4Default = true
					}
					b.WriteString(fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%db=\\\"inet %s netmask %s\\\"\";\n", ctidHash, networkId, ip, mask))
				}

				// --- IPv6 (independent of IPv4) ---
				if n.SLAAC {
					b.WriteString(fmt.Sprintf("\texec.start += \"ifconfig %s_%db inet6 accept_rtadv up\";\n", ctidHash, networkId))
					b.WriteString(fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%db_ipv6=\\\"inet6 accept_rtadv\\\"\";\n", ctidHash, networkId))
				} else if n.IPv6ID != nil && *n.IPv6ID > 0 && n.IPv6GwID != nil && *n.IPv6GwID > 0 {
					ipv6, err := s.NetworkService.GetObjectEntryByID(*n.IPv6ID)
					if err != nil {
						return fmt.Errorf("failed to get ipv6 address: %w", err)
					}
					ipv6Gw, err := s.NetworkService.GetObjectEntryByID(*n.IPv6GwID)
					if err != nil {
						return fmt.Errorf("failed to get ipv6 gateway: %w", err)
					}

					b.WriteString(fmt.Sprintf("\texec.start += \"ifconfig %s_%db inet6 %s\";\n", ctidHash, networkId, ipv6))
					if !setV6Default {
						b.WriteString(fmt.Sprintf("\texec.start += \"sysrc ipv6_defaultrouter=\\\"%s\\\"\";\n", ipv6Gw))
						setV6Default = true
					}
					b.WriteString(fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%db_ipv6=\\\"inet6 %s\\\"\";\n", ctidHash, networkId, ipv6))
				}
			}

			newCfg, err = s.AppendToConfig(ctId, cfg, b.String())
			if err != nil {
				return err
			}
		} else {
			// No networks configured: disable both stacks explicitly
			toAppend := "\tip4=disable;\n\tip6=disable;\n"
			var err error
			newCfg, err = s.AppendToConfig(ctId, cfg, toAppend)
			if err != nil {
				return err
			}
		}
	}

	if err := s.SaveJailConfig(ctId, newCfg); err != nil {
		return err
	}

	// If inherited, scrub rc.conf of per-if ifconfig/ipv6* lines
	mountPoint, err := s.GetJailBaseMountPoint(ctId)
	if err != nil {
		return err
	}

	rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")
	if _, statErr := os.Stat(rcConfPath); statErr == nil {
		if jail.InheritIPv4 || jail.InheritIPv6 {
			rcConf, err := os.ReadFile(rcConfPath)
			if err != nil {
				return err
			}
			lines := strings.Split(string(rcConf), "\n")
			for i := 0; i < len(lines); i++ {
				if strings.HasPrefix(lines[i], "ifconfig") || strings.HasPrefix(lines[i], "ipv6") {
					lines = append(lines[:i], lines[i+1:]...)
					i--
				}
			}
			if err := os.WriteFile(rcConfPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
				return err
			}
		}
	}

	return nil
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

		err := s.SyncNetwork(uint(jail.CTID), jail, false)

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
