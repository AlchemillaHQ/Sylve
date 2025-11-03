// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"

	"gorm.io/gorm"

	sdb "github.com/alchemillahq/sylve/internal/db"

	cpuid "github.com/klauspost/cpuid/v2"
)

var _ jailServiceInterfaces.JailServiceInterface = (*Service)(nil)

type Service struct {
	DB             *gorm.DB
	NetworkService networkServiceInterfaces.NetworkServiceInterface

	crudMutex sync.Mutex
}

func NewJailService(db *gorm.DB, networkService networkServiceInterfaces.NetworkServiceInterface) jailServiceInterfaces.JailServiceInterface {
	return &Service{
		DB:             db,
		NetworkService: networkService,
	}
}

func (s *Service) GetJails() ([]jailModels.Jail, error) {
	var jails []jailModels.Jail
	if err := s.DB.
		Preload("Networks").
		Preload("Networks.MacAddressObj").
		Preload("Networks.MacAddressObj.Entries").
		Preload("Networks.MacAddressObj.Resolutions").
		Find(&jails).Error; err != nil {
		logger.L.Error().Err(err).Msg("get_jails: failed to fetch jails")
		return nil, fmt.Errorf("failed_to_fetch_jails: %w", err)
	}
	return jails, nil
}

func (s *Service) GetJailsSimple() ([]jailServiceInterfaces.SimpleList, error) {
	var jails []jailModels.Jail

	if err := s.DB.Model(&jailModels.Jail{}).Select("id, name, ct_id").Find(&jails).Error; err != nil {
		logger.L.Error().Err(err).Msg("get_jails_simple: failed to fetch jails")
		return nil, fmt.Errorf("failed_to_fetch_jails_simple: %w", err)
	}

	var list []jailServiceInterfaces.SimpleList

	var states []jailServiceInterfaces.State
	states, err := s.GetStates()
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_states: %w", err)
	}

	for _, jail := range jails {
		var state string

		for _, s := range states {
			if s.CTID == jail.CTID {
				state = s.State
				break
			}
		}

		list = append(list, jailServiceInterfaces.SimpleList{
			ID:    jail.ID,
			Name:  jail.Name,
			CTID:  jail.CTID,
			State: state,
		})
	}

	return list, nil
}

func (s *Service) ValidateCreate(data jailServiceInterfaces.CreateJailRequest) error {
	if data.Name == "" || !utils.IsValidVMName(data.Name) {
		return fmt.Errorf("invalid_vm_name")
	}

	if data.CTID == nil || *data.CTID <= 0 || *data.CTID > 9999 {
		return fmt.Errorf("invalid_ct_id")
	}

	if data.Description != "" && (len(data.Description) < 1 || len(data.Description) > 1024) {
		return fmt.Errorf("invalid_description")
	}

	datasets, err := zfs.Datasets("")
	if err != nil {
		return fmt.Errorf("failed_to_get_dataset: %w", err)
	}

	if datasets == nil {
		return fmt.Errorf("dataset_not_found")
	}

	var dataset *zfs.Dataset

	for _, d := range datasets {
		if d.GUID == data.Dataset {
			dataset = d
			break
		}
	}

	if dataset == nil {
		return fmt.Errorf("dataset_not_found")
	}

	mountPoint, err := dataset.GetProperty("mountpoint")
	if err != nil {
		return fmt.Errorf("failed_to_get_dataset_mountpoint: %w", err)
	}

	if mountPoint == "" {
		return fmt.Errorf("dataset_mountpoint_not_found")
	}

	emptyDir, err := utils.IsEmptyDir(mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_check_if_empty_mountpoint: %w", err)
	}

	hasBase, err := utils.DoesPathHaveBase(mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_check_if_mountpoint_has_base: %w", err)
	}

	if !emptyDir && !hasBase {
		return fmt.Errorf("dataset_mountpoint_not_empty")
	}

	if data.Base == "" && !hasBase {
		return fmt.Errorf("base_download_uuid_required")
	}

	if !hasBase {
		dCount, err := sdb.Count(s.DB, &utilitiesModels.Downloads{}, "uuid = ?", data.Base)
		if err != nil {
			return fmt.Errorf("failed_to_count_downloads: %w", err)
		}

		if dCount == 0 {
			return fmt.Errorf("base_download_not_found")
		}

		_, err = s.FindBaseByUUID(data.Base)

		if err != nil {
			return fmt.Errorf("failed_to_find_base_by_uuid: %w", err)
		}
	}

	swAvailable := true
	mac := uint(0)
	dhcp := false
	slaac := false

	if data.SwitchName == "" {
		return fmt.Errorf("switch_name_required")
	} else if strings.ToLower(data.SwitchName) == "inherit" || strings.ToLower(data.SwitchName) == "none" {
		swAvailable = false
	}

	if swAvailable {
		found := false

		var stdSwitch networkModels.StandardSwitch
		if err := s.DB.First(&stdSwitch, "name = ?", data.SwitchName).Error; err == nil {
			found = true
		}

		var manualSwitch networkModels.ManualSwitch
		if err := s.DB.First(&manualSwitch, "name = ?", data.SwitchName).Error; err == nil {
			found = true
		}

		if !found {
			return fmt.Errorf("standard_switch_not_found")
		}
	}

	if data.MAC != nil {
		mac = uint(*data.MAC)
		if mac != 0 && swAvailable {
			used, err := s.NetworkService.IsObjectUsed(mac)
			if err != nil {
				return fmt.Errorf("failed_to_check_mac_usage: %w", err)
			}

			if used {
				return fmt.Errorf("mac_already_used")
			}
		}
	}

	if data.DHCP != nil {
		dhcp = *data.DHCP
	}

	if data.SLAAC != nil {
		slaac = *data.SLAAC
	}

	if swAvailable {
		if !dhcp {
			if data.IPv4 != nil {
				ipv4Id := uint(*data.IPv4)
				if ipv4Id != 0 && data.IPv4Gw != nil {
					ipv4GwId := uint(*data.IPv4Gw)
					if ipv4GwId == 0 {
						return fmt.Errorf("invalid_ipv4_gateway")
					}

					isUsed, err := s.NetworkService.IsObjectUsed(ipv4Id)
					if err != nil {
						return fmt.Errorf("failed_to_check_ipv4_usage: %w", err)
					}

					if isUsed {
						return fmt.Errorf("ipv4_already_used")
					}
				} else {
					return fmt.Errorf("invalid_ipv4_gateway_or_address")
				}
			} else {
				return fmt.Errorf("invalid_ipv4_address")
			}
		}

		if !slaac {
			if data.IPv6 != nil {
				ipv6Id := uint(*data.IPv6)

				if ipv6Id != 0 && data.IPv6Gw != nil {
					ipv6GwId := uint(*data.IPv6Gw)
					if ipv6GwId == 0 {
						return fmt.Errorf("invalid_ipv6_gateway")
					}

					isUsed, err := s.NetworkService.IsObjectUsed(ipv6Id)
					if err != nil {
						return fmt.Errorf("failed_to_check_ipv6_usage: %w", err)
					}

					if isUsed {
						return fmt.Errorf("ipv6_already_used")
					}
				} else {
					return fmt.Errorf("invalid_ipv6_gateway_or_address")
				}
			} else {
				return fmt.Errorf("invalid_ipv6_address")
			}
		}
	}

	if data.StartOrder < 0 {
		return fmt.Errorf("start_order_must_be_greater_than_or_equal_to_0")
	}

	return nil
}

func (s *Service) CreateJailConfig(data jailServiceInterfaces.CreateJailRequest, mountPoint string) (string, error) {
	if mountPoint == "" {
		return "", fmt.Errorf("mount_point_not_found")
	}

	var cfg string
	ctid := *data.CTID
	ctidHash := utils.HashIntToNLetters(ctid, 5)
	cfg += fmt.Sprintf("%s {\n", ctidHash)

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	logsPath := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid), fmt.Sprintf("%d.log", ctid))

	cfg += fmt.Sprintf("\texec.consolelog += \"%s\";\n\n", logsPath)
	cfg += fmt.Sprintf("\t$ctid = \"%s\";\n", ctidHash)

	cfg += fmt.Sprintf("\tpath = \"%s\";\n", mountPoint)
	cfg += fmt.Sprintf("\thost.hostname = \"%s\";\n", utils.MakeValidHostname(data.Name))
	cfg += fmt.Sprintf("\tpersist;\n")
	cfg += fmt.Sprintf("\texec.clean;\n\n")

	cfg += fmt.Sprintf("\tmount.devfs;\n")
	cfg += fmt.Sprintf("\tdevfs_ruleset=\"8181\";\n\n")

	cfg += fmt.Sprintf("\tallow.sysvipc;\n")
	cfg += fmt.Sprintf("\tallow.reserved_ports;\n")
	cfg += fmt.Sprintf("\tallow.raw_sockets;\n")
	cfg += fmt.Sprintf("\tallow.socket_af;\n\n")

	var jail jailModels.Jail
	err = s.DB.Preload("Networks").First(&jail, "ct_id = ?", ctid).Error
	if err != nil {
		return "", fmt.Errorf("failed to find jail with ct_id %d: %w", ctid, err)
	}

	cfg += fmt.Sprintf("\texec.start += \"/bin/sh /etc/rc\";\n")

	if len(jail.Networks) == 1 {
		network := jail.Networks[0]

		if network.SwitchID > 0 {
			networkId := fmt.Sprintf("%d", network.SwitchID)
			cfg += fmt.Sprintf("\tvnet;\n")
			cfg += fmt.Sprintf("\tvnet.interface = \"%s_%sb\";\n", ctidHash, networkId)

			err := s.NetworkService.SyncEpairs()
			if err != nil {
				return "", err
			}

			if network.MacID != nil && *network.MacID > 0 {
				mac, err := s.NetworkService.GetObjectEntryByID(*network.MacID)
				if err != nil {
					return "", fmt.Errorf("failed to get mac address: %w", err)
				}

				prevMAC, err := utils.PreviousMAC(mac)
				if err != nil {
					return "", fmt.Errorf("failed to get previous mac: %w", err)
				}

				cfg += fmt.Sprintf("\texec.prestart += \"ifconfig %s_%sa ether %s up\";\n", ctidHash, networkId, prevMAC)
				cfg += fmt.Sprintf("\texec.prestart += \"ifconfig %s_%sb ether %s up\";\n", ctidHash, networkId, mac)

				bridgeName, err := s.NetworkService.GetBridgeNameByIDType(network.SwitchID, network.SwitchType)

				if err != nil {
					return "", fmt.Errorf("failed to get bridge name: %w", err)
				}

				cfg += fmt.Sprintf("\texec.prestart += \"if ! ifconfig %s | grep -qw %s_%sa; then ifconfig %s addm %s_%sa; fi\";\n", bridgeName, ctidHash, networkId, bridgeName, ctidHash, networkId)

				if network.DHCP && network.SLAAC {
					cfg += fmt.Sprintf("\texec.start += \"dhclient %s_%sb\";\n", ctidHash, networkId)
					cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb=\\\"DHCP\\\"\";\n", ctidHash, networkId)
					cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb_ipv6=\\\"inet6 accept_rtadv\\\"\";\n", ctidHash, networkId)
				} else if network.DHCP {
					cfg += fmt.Sprintf("\texec.start += \"dhclient %s_%sb\";\n", ctidHash, networkId)
					cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb=\\\"DHCP\\\"\";\n", ctidHash, networkId)
				} else if network.SLAAC {
					cfg += fmt.Sprintf("\texec.start += \"ifconfig %s_%sb inet6 accept_rtadv up\";\n", ctidHash, networkId)
					cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb_ipv6=\\\"inet6 accept_rtadv\\\"\";\n", ctidHash, networkId)
				} else {
					if network.IPv4ID != nil && *network.IPv4ID > 0 && network.IPv4GwID != nil && *network.IPv4GwID > 0 {
						ipv4, err := s.NetworkService.GetObjectEntryByID(*network.IPv4ID)
						if err != nil {
							return "", fmt.Errorf("failed to get ipv4 address: %w", err)
						}

						ipv4Gw, err := s.NetworkService.GetObjectEntryByID(*network.IPv4GwID)
						if err != nil {
							return "", fmt.Errorf("failed to get ipv4 gateway: %w", err)
						}

						ip, mask, err := utils.SplitIPv4AndMask(ipv4)
						if err != nil {
							return "", fmt.Errorf("failed to split ipv4 address and mask: %w", err)
						}

						cfg += fmt.Sprintf("\texec.start += \"ifconfig %s_%sb inet %s netmask %s\";\n", ctidHash, networkId, ip, mask)
						cfg += fmt.Sprintf("\texec.start += \"route add default %s\";\n", ipv4Gw)
						cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb=\\\"inet %s netmask %s\\\"\";\n", ctidHash, networkId, ip, mask)
					}

					if network.IPv6ID != nil && *network.IPv6ID > 0 && network.IPv6GwID != nil && *network.IPv6GwID > 0 {
						ipv6, err := s.NetworkService.GetObjectEntryByID(*network.IPv6ID)
						if err != nil {
							return "", fmt.Errorf("failed to get ipv6 address: %w", err)
						}

						ipv6Gw, err := s.NetworkService.GetObjectEntryByID(*network.IPv6GwID)
						if err != nil {
							return "", fmt.Errorf("failed to get ipv6 gateway: %w", err)
						}

						cfg += fmt.Sprintf("\texec.start += \"ifconfig %s_%sb inet6 %s\";\n", ctidHash, networkId, ipv6)
						cfg += fmt.Sprintf("\texec.start += \"sysrc ipv6_defaultrouter=\\\"%s\\\"\";\n", ipv6Gw)
						cfg += fmt.Sprintf("\texec.start += \"sysrc ifconfig_%s_%sb_ipv6=\\\"inet6 %s\\\"\";\n", ctidHash, networkId, ipv6)
					}
				}
			}
		}
	} else {
		if data.InheritIPv4 != nil && *data.InheritIPv4 {
			cfg += fmt.Sprintf("\tip4=\"inherit\";\n")
		}

		if data.InheritIPv6 != nil && *data.InheritIPv6 {
			cfg += fmt.Sprintf("\tip6=\"inherit\";\n")
		}
	}

	var cpuCores int
	var memory int

	if data.Cores != nil {
		cpuCores = *data.Cores
	} else {
		cpuCores = 0
	}

	if data.Memory != nil {
		memory = *data.Memory
	} else {
		memory = 0
	}

	var currentJails []jailModels.Jail
	if err := s.DB.Find(&currentJails).Error; err != nil {
		logger.L.Error().Err(err).Msg("failed to fetch current jails")
		return "", fmt.Errorf("failed_to_fetch_current_jails: %w", err)
	}

	numLogicalCores := cpuid.CPU.LogicalCores
	coreUsage := map[int]int{}
	for _, jail := range currentJails {
		for _, core := range jail.CPUSet {
			coreUsage[core]++
		}
	}

	type coreCount struct {
		Core  int
		Count int
	}

	var allCores []coreCount
	for i := 0; i < numLogicalCores; i++ {
		allCores = append(allCores, coreCount{Core: i, Count: coreUsage[i]})
	}

	sort.Slice(allCores, func(i, j int) bool {
		return allCores[i].Count < allCores[j].Count
	})

	if cpuCores > 0 && len(allCores) > 0 {
		selectedCores := []int{}
		for i := 0; i < cpuCores && i < len(allCores); i++ {
			selectedCores = append(selectedCores, allCores[i].Core)
		}
		if len(selectedCores) > 0 {
			coreListStr := strings.Trim(strings.Replace(fmt.Sprint(selectedCores), " ", ",", -1), "[]")
			cfg += fmt.Sprintf("\texec.created += \"cpuset -l %s -j %s\";\n", coreListStr, ctidHash)
		}
	}

	if memory > 0 {
		memoryMB := memory / (1024 * 1024)
		cfg += fmt.Sprintf("\texec.poststart += \"rctl -a jail:%s:memoryuse:deny=%dM\";\n", ctidHash, memoryMB)
	}

	cfg += fmt.Sprintf("\texec.stop += \"/bin/sh /etc/rc.shutdown\";\n\n")

	if cpuCores > 0 || memory > 0 {
		cfg += fmt.Sprintf("\texec.poststop += \"rctl -r jail:%s\";\n", ctidHash)
	}

	cfg += fmt.Sprintf("}\n")

	return cfg, nil
}

func (s *Service) CreateJail(data jailServiceInterfaces.CreateJailRequest) error {
	if err := s.ValidateCreate(data); err != nil {
		logger.L.Debug().Err(err).Msg("create_jail: validation failed")
		return err
	}

	var jail jailModels.Jail

	jail.Name = data.Name
	jail.CTID = *data.CTID
	jail.Description = data.Description
	// jail.Dataset = data.Dataset
	// jail.Base = data.Base
	jail.StartAtBoot = data.StartAtBoot
	jail.StartOrder = data.StartOrder
	jail.ResourceLimits = data.ResourceLimits

	if *jail.ResourceLimits {
		jail.Cores = *data.Cores
		jail.Memory = *data.Memory
	} else {
		jail.Cores = 0
		jail.Memory = 0
	}

	if strings.ToLower(data.SwitchName) != "inherit" && strings.ToLower(data.SwitchName) != "none" {
		var mac uint
		if data.MAC != nil {
			mac = uint(*data.MAC)
		}

		swType := ""
		swName := ""
		swId := uint(0)

		var stdSwitch networkModels.StandardSwitch
		if err := s.DB.First(&stdSwitch, "name = ?", data.SwitchName).Error; err == nil {
			swType = "standard"
			swName = stdSwitch.Name
			swId = stdSwitch.ID
		}

		var manualSwitch networkModels.ManualSwitch
		if err := s.DB.First(&manualSwitch, "name = ?", data.SwitchName).Error; err == nil {
			swType = "manual"
			swName = manualSwitch.Name
			swId = manualSwitch.ID
		}

		if swType == "" {
			return fmt.Errorf("switch_not_found: %s", data.SwitchName)
		}

		if mac == 0 {
			base := fmt.Sprintf("%s-%s", data.Name, swName)
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

			macAddress := utils.GenerateRandomMAC()
			macObj := networkModels.Object{
				Type: "Mac",
				Name: name,
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

			mac = macObj.ID
		}

		var ipv4Id, ipv4GwId, ipv6Id, ipv6GwId *uint
		if data.IPv4 != nil {
			ipv4Id = new(uint)
			*ipv4Id = uint(*data.IPv4)
		}

		if data.IPv4Gw != nil {
			ipv4GwId = new(uint)
			*ipv4GwId = uint(*data.IPv4Gw)
		}

		if data.IPv6 != nil {
			ipv6Id = new(uint)
			*ipv6Id = uint(*data.IPv6)
		}

		if data.IPv6Gw != nil {
			ipv6GwId = new(uint)
			*ipv6GwId = uint(*data.IPv6Gw)
		}

		dhcp := false
		slaac := false

		if data.DHCP != nil && *data.DHCP {
			dhcp = *data.DHCP
		}

		if data.SLAAC != nil && *data.SLAAC {
			slaac = *data.SLAAC
		}

		jail.Networks = append(jail.Networks, jailModels.Network{
			SwitchID:   swId,
			SwitchType: swType,
			MacID:      &mac,
			IPv4ID:     ipv4Id,
			IPv4GwID:   ipv4GwId,
			IPv6ID:     ipv6Id,
			IPv6GwID:   ipv6GwId,
			DHCP:       dhcp,
			SLAAC:      slaac,
		})
	}

	if data.InheritIPv4 != nil {
		jail.InheritIPv4 = *data.InheritIPv4
	}

	if data.InheritIPv6 != nil {
		jail.InheritIPv6 = *data.InheritIPv6
	}

	if err := s.DB.Create(&jail).Error; err != nil {
		logger.L.Error().Err(err).Msg("create_jail: failed to create jail")
		return fmt.Errorf("failed_to_create_jail: %w", err)
	}

	var dataset *zfs.Dataset
	datasets, err := zfs.Datasets("")
	if err != nil {
		return fmt.Errorf("failed_to_get_datasets: %w", err)
	}

	for _, d := range datasets {
		if d.GUID == data.Dataset {
			dataset = d
			break
		}
	}

	mountPoint, err := dataset.GetProperty("mountpoint")
	if err != nil {
		return fmt.Errorf("failed_to_get_dataset_mountpoint: %w", err)
	}

	hasBase, err := utils.DoesPathHaveBase(mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_check_path_for_base: %w", err)
	}

	if !hasBase {
		base, err := s.FindBaseByUUID(data.Base)
		if err != nil {
			return fmt.Errorf("failed_to_find_base: %w", err)
		}

		isDir, _ := utils.IsDir(base)
		if isDir {
			if err := utils.CopyDirContents(base, mountPoint); err != nil {
				return fmt.Errorf("failed_to_copy_base: %w", err)
			}
		} else {
			return fmt.Errorf("base_is_not_a_directory")
		}

		if err := utils.CopyFile("/etc/resolv.conf", filepath.Join(mountPoint, "etc", "resolv.conf")); err != nil {
			return fmt.Errorf("failed_to_copy_resolv_conf: %w", err)
		}

		if err := utils.CopyFile("/etc/localtime", filepath.Join(mountPoint, "etc", "localtime")); err != nil {
			return fmt.Errorf("failed_to_copy_localtime: %w", err)
		}
	}

	jCfg, err := s.CreateJailConfig(data, mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_create_jail_config: %w", err)
	}

	jailsPath, err := config.GetJailsPath()
	if jailsPath == "" {
		return fmt.Errorf("jails_path_not_found")
	}

	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", *data.CTID))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_directory: %w", err)
	}

	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", *data.CTID))
	if err := os.WriteFile(jailConfigPath, []byte(jCfg), 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_config_file: %w", err)
	}

	logsPath := filepath.Join(jailDir, fmt.Sprintf("%d.log", *data.CTID))
	logFile, err := os.Create(logsPath)
	if err != nil {
		return fmt.Errorf("failed_to_create_log_file: %w", err)
	}
	logFile.Close()

	return nil
}

func (s *Service) DeleteJail(ctId uint, deleteMacs bool, deleteRootFS bool) error {
	if ctId == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	var jail jailModels.Jail
	if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jail).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("jail_not_found")
		}
		logger.L.Error().Err(err).Msg("delete_jail: failed to find jail")
		return fmt.Errorf("failed_to_find_jail: %w", err)
	}

	if len(jail.Networks) > 0 {
		for _, network := range jail.Networks {
			if err := s.DB.Delete(&network).Error; err != nil {
				logger.L.Error().Err(err).Msg("delete_jail: failed to delete network")
				return fmt.Errorf("failed_to_delete_network: %w", err)
			}
		}
	}

	if err := s.DB.Delete(&jail).Error; err != nil {
		return fmt.Errorf("failed_to_delete_jail: %w", err)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctId))
	if err := os.RemoveAll(jailDir); err != nil {
		return fmt.Errorf("failed_to_remove_jail_directory: %w", err)
	}

	// if deleteRootFS {
	// 	var dataset *zfs.Dataset
	// 	datasets, err := zfs.Datasets("")
	// 	if err != nil {
	// 		return fmt.Errorf("failed_to_get_datasets: %w", err)
	// 	}

	// 	for _, d := range datasets {
	// 		if d.GUID == jail.Dataset {
	// 			dataset = d
	// 			break
	// 		}
	// 	}

	// 	if dataset == nil {
	// 		return fmt.Errorf("dataset_not_found")
	// 	}

	// 	dProps, err := dataset.GetAllProperties()
	// 	if err != nil {
	// 		return fmt.Errorf("failed_to_get_dataset_properties: %w", err)
	// 	}

	// 	fsDestroyed := false

	// 	if err := dataset.Destroy(zfs.DestroyRecursive); err != nil {
	// 		logger.L.Error().Err(err).Msg("delete_jail: failed to destroy dataset")
	// 	} else {
	// 		fsDestroyed = true
	// 	}

	// 	if fsDestroyed {
	// 		allowedProps := map[string]struct{}{
	// 			"atime":       {},
	// 			"checksum":    {},
	// 			"compression": {},
	// 			"dedup":       {},
	// 			"encryption":  {},
	// 			"aclinherit":  {},
	// 			"aclmode":     {},
	// 			"keylocation": {},
	// 			"quota":       {},
	// 		}

	// 		props := make(map[string]string)
	// 		for k, v := range dProps {
	// 			if _, ok := allowedProps[strings.ToLower(k)]; ok {
	// 				if k == "quota" && v == "0" || v == "" || v == "-" {
	// 					continue
	// 				}

	// 				props[strings.ToLower(k)] = v
	// 			}
	// 		}

	// 		newDataset, err := zfs.CreateFilesystem(dataset.Name, props)
	// 		if err != nil {
	// 			return fmt.Errorf("failed_to_create_new_dataset: %w", err)
	// 		}

	// 		if newDataset == nil {
	// 			return fmt.Errorf("new_dataset_is_nil")
	// 		}
	// 	}
	// }

	if deleteMacs {
		var usedMACS []uint

		for _, network := range jail.Networks {
			macId := network.MacID
			if macId != nil {
				usedMACS = append(usedMACS, *macId)
			}
		}

		if len(usedMACS) > 0 {
			tx := s.DB.Begin()

			if err := tx.Where("object_id IN ?", usedMACS).
				Delete(&networkModels.ObjectEntry{}).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_object_entries: %w", err)
			}

			if err := tx.Where("object_id IN ?", usedMACS).
				Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_object_resolutions: %w", err)
			}

			if err := tx.Delete(&networkModels.Object{}, usedMACS).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_objects: %w", err)
			}

			if err := tx.Commit().Error; err != nil {
				return fmt.Errorf("failed_to_commit_cleanup: %w", err)
			}
		}
	}

	return nil
}

func (s *Service) UpdateDescription(id uint, description string) error {
	if id == 0 {
		return fmt.Errorf("invalid_jail_id")
	}

	if len(description) > 1024 {
		return fmt.Errorf("invalid_description")
	}

	var jail jailModels.Jail
	if err := s.DB.First(&jail, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("jail_not_found")
		}
		logger.L.Error().Err(err).Msg("update_jail_description: failed to find jail")
		return fmt.Errorf("failed_to_find_jail: %w", err)
	}

	jail.Description = description

	if err := s.DB.Save(&jail).Error; err != nil {
		logger.L.Error().Err(err).Msg("update_jail_description: failed to update jail description")
		return fmt.Errorf("failed_to_update_jail_description: %w", err)
	}

	return nil
}
