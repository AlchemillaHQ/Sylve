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
	"strconv"

	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	utils "github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetObjects() ([]networkModels.Object, error) {
	var objects []networkModels.Object

	err := s.DB.
		Preload("Entries").
		Preload("Resolutions").
		Find(&objects).Error

	if err != nil {
		return objects, fmt.Errorf("failed to retrieve network objects: %w", err)
	}

	for i := range objects {
		used, err := s.IsObjectUsed(objects[i].ID)
		if err != nil {
			return nil, err
		}

		objects[i].IsUsed = used
	}

	return objects, nil
}

func validateType(oType string) error {
	validTypes := map[string]bool{
		"Host":    true,
		"Network": true,
		"Port":    true,
		"Country": true,
		"List":    true,
		"Mac":     true,
		"FQDN":    true,
		"DUID":    true,
	}

	if !validTypes[oType] {
		return fmt.Errorf("invalid object type: %s", oType)
	}

	return nil
}

func validateValues(oType string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("values cannot be empty for type: %s", oType)
	}

	if oType == "Host" {
		isIPv4 := false
		isIPv6 := false

		for _, value := range values {
			if utils.IsValidIPv4(value) {
				isIPv4 = true
			} else if utils.IsValidIPv6(value) {
				isIPv6 = true
			} else {
				return fmt.Errorf("invalid host value: %s", value)
			}

			if isIPv4 && isIPv6 {
				return fmt.Errorf("cannot mix IPv4 and IPv6 in host values")
			}
		}
	}

	if oType == "Network" {
		isIPv4 := false
		isIPv6 := false

		for _, value := range values {
			if utils.IsValidIPv4CIDR(value) {
				isIPv4 = true
			} else if utils.IsValidIPv6CIDR(value) {
				isIPv6 = true
			} else {
				return fmt.Errorf("invalid network value: %s", value)
			}

			if isIPv4 && isIPv6 {
				return fmt.Errorf("cannot mix IPv4 and IPv6 in network values")
			}
		}
	}

	for _, value := range values {
		if value == "" {
			return fmt.Errorf("value cannot be empty for type: %s", oType)
		}

		if oType == "Port" {
			vInt, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid port value: %s", value)
			}

			if !utils.IsValidPort(vInt) {
				return fmt.Errorf("invalid port value: %d", vInt)
			}
		}

		if oType == "Country" {
			if !utils.IsValidCountryCode(value) {
				return fmt.Errorf("invalid country code: %s", value)
			}
		}

		if oType == "Mac" {
			if !utils.IsValidMAC(value) {
				return fmt.Errorf("invalid MAC address: %s", value)
			}
		}

		if oType == "FQDN" {
			if !utils.IsValidFQDN(value) {
				return fmt.Errorf("invalid FQDN: %s", value)
			}
		}

		if oType == "DUID" {
			if !utils.IsValidDUID(value) {
				return fmt.Errorf("invalid DUID: %s", value)
			}
		}
	}

	return nil
}

func (s *Service) IsObjectUsed(id uint) (bool, error) {
	var object networkModels.Object

	if err := s.DB.First(&object, id).Error; err != nil {
		return false, fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}

	if object.Type == "Host" {
		var switches []networkModels.StandardSwitch
		var jailNetworks []jailModels.Network
		var dhcpLeases []networkModels.DHCPStaticLease

		if err := s.DB.
			Preload("NetworkObj.Entries").
			Preload("Network6Obj.Entries").
			Preload("GatewayAddressObj.Entries").
			Preload("Gateway6AddressObj.Entries").
			Find(&switches).Error; err != nil {
			return true, err
		}

		if err := s.DB.Find(&jailNetworks).Error; err != nil {
			return true, fmt.Errorf("failed to find jail networks: %d %w", id, err)
		}

		if err := s.DB.Preload("IPObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, sw := range switches {
			if sw.GatewayAddressObj != nil {
				if sw.GatewayAddressObj.ID == id {
					return true, nil
				}
			}

			if sw.Gateway6AddressObj != nil {
				if sw.Gateway6AddressObj.ID == id {
					return true, nil
				}
			}
		}

		for _, jn := range jailNetworks {
			if jn.IPv4ID != nil {
				if *jn.IPv4ID == id {
					return true, nil
				}
			}

			if jn.IPv4GwID != nil {
				if *jn.IPv4GwID == id {
					return true, nil
				}
			}

			if jn.IPv6ID != nil {
				if *jn.IPv6ID == id {
					return true, nil
				}
			}

			if jn.IPv6GwID != nil {
				if *jn.IPv6GwID == id {
					return true, nil
				}
			}

			if jn.MacID != nil {
				if *jn.MacID == id {
					return true, nil
				}
			}
		}

		for _, dl := range dhcpLeases {
			if dl.IPObject != nil {
				if dl.IPObject.ID == id {
					return true, nil
				}
			}
		}

		return false, nil
	}

	if object.Type == "Mac" {
		var vmNetworks []vmModels.Network
		var jailNetworks []jailModels.Network
		var dhcpLeases []networkModels.DHCPStaticLease

		if err := s.DB.Where("mac_id = ?", id).Find(&vmNetworks).Error; err != nil {
			return true, fmt.Errorf("failed to find VM networks using object %d: %w", id, err)
		}

		if len(vmNetworks) > 0 {
			return true, nil
		}

		if err := s.DB.Where("mac_id = ?", id).Find(&jailNetworks).Error; err != nil {
			return true, fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
		}

		if len(jailNetworks) > 0 {
			return true, nil
		}

		if err := s.DB.Preload("MACObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, dl := range dhcpLeases {
			if dl.MACObject != nil {
				if dl.MACObject.ID == id {
					return true, nil
				}
			}
		}

		return false, nil
	}

	if object.Type == "Network" {
		var jailNetworks []jailModels.Network
		if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
			return true, fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
		}

		if len(jailNetworks) > 0 {
			return true, nil
		}

		var switches []networkModels.StandardSwitch

		if err := s.DB.Where("network_object_id = ? OR network6_object_id = ?", id, id).Find(&switches).Error; err != nil {
			return true, fmt.Errorf("failed to find switches using object %d: %w", id, err)
		}

		if len(switches) > 0 {
			return true, nil
		}
	}

	if object.Type == "DUID" {
		var dhcpLeases []networkModels.DHCPStaticLease
		if err := s.DB.Preload("DUIDObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, dl := range dhcpLeases {
			if dl.DUIDObject != nil {
				if dl.DUIDObject.ID == id {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

func (s *Service) CreateObject(name string, oType string, values []string) error {
	if err := validateType(oType); err != nil {
		return err
	}

	if err := validateValues(oType, values); err != nil {
		return err
	}

	var count int64
	if err := s.DB.
		Model(&networkModels.Object{}).
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("object_with_name_already_exists: %s", name)
	}

	entries := make([]networkModels.ObjectEntry, len(values))
	for i, value := range values {
		entries[i] = networkModels.ObjectEntry{
			Value: value,
		}
	}

	object := networkModels.Object{
		Name:    name,
		Type:    oType,
		Entries: entries,
	}

	if err := s.DB.Create(&object).Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) DeleteObject(id uint) error {
	used, err := s.IsObjectUsed(id)
	if err != nil {
		return fmt.Errorf("failed to check if object %d is used: %w", id, err)
	}

	if used {
		return fmt.Errorf("object %d is currently in use and cannot be deleted", id)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
		return fmt.Errorf("failed to delete resolutions for object %d: %w", id, err)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return fmt.Errorf("failed to delete entries for object %d: %w", id, err)
	}

	if err := s.DB.Delete(&networkModels.Object{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete object %d: %w", id, err)
	}

	return nil
}

func (s *Service) IsObjectUsedByJail(id uint) (bool, []uint, error) {
	var jailNetworks []jailModels.Network
	var jailIds []uint

	if err := s.DB.Where("mac_id = ? OR ipv4_id = ? OR ipv6_id = ?", id, id, id).Find(&jailNetworks).Error; err != nil {
		return false, []uint{}, fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
	}

	if len(jailNetworks) > 0 {
		for _, jn := range jailNetworks {
			jailIds = append(jailIds, jn.JailID)
		}

		return true, jailIds, nil
	}

	return false, []uint{}, nil
}

func (s *Service) EditObject(id uint, name string, oType string, values []string) error {
	if err := validateType(oType); err != nil {
		return err
	}

	if err := validateValues(oType, values); err != nil {
		return err
	}

	var count int64
	if err := s.DB.
		Model(&networkModels.Object{}).
		Where("name = ? AND id != ?", name, id).
		Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("object_with_name_already_exists: %s", name)
	}

	used, err := s.IsObjectUsed(id)
	if err != nil {
		return fmt.Errorf("failed to check if object %d is used: %w", id, err)
	}

	var object networkModels.Object
	if err := s.DB.Preload("Entries").
		Preload("Resolutions").
		First(&object, id).Error; err != nil {
		return fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}

	/* This object isn't used anywhere, yay! It's going to be an easy edit */
	if !used {
		object.Name = name
		object.Type = oType

		if err := s.DB.Save(&object).Error; err != nil {
			return fmt.Errorf("failed to update object %d: %w", id, err)
		}

		if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
			return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
		}

		if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
			return fmt.Errorf("failed to delete resolutions for object %d: %w", id, err)
		}

		for _, value := range values {
			entry := networkModels.ObjectEntry{
				ObjectID: id,
				Value:    value,
			}

			if err := s.DB.Create(&entry).Error; err != nil {
				return fmt.Errorf("failed to create entry for object %d: %w", id, err)
			}
		}
	} else {
		if object.Type == "Mac" {
			var vmNetworks []vmModels.Network
			var jailNetworks []jailModels.Network
			var dhcpLeases []networkModels.DHCPStaticLease

			if err := s.DB.Preload("AddressObj.Entries").Where("mac_id = ?", id).Find(&vmNetworks).Error; err != nil {
				return fmt.Errorf("failed to find VM networks using object %d: %w", id, err)
			}

			if err := s.DB.Preload("MacAddressObj.Entries").Where("mac_id = ?", id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if err := s.DB.Preload("MACObject.Entries").Where("mac_object_id = ?", id).Find(&dhcpLeases).Error; err != nil {
				return fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
			}

			var vm vmModels.VM
			if len(vmNetworks) > 0 {
				if err := s.DB.First(&vm, vmNetworks[0].VMID).Error; err != nil {
					return fmt.Errorf("failed to find VM for network %d: %w", vmNetworks[0].ID, err)
				}
			}

			/* Object was used in a VM, but now we're changing it to something else, we can't do that */
			if len(vmNetworks) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_vm")
			}

			/* MAC Used in a VM */
			if len(vmNetworks) > 0 && oType == "Mac" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d VM networks, please ensure only one MAC is provided", id, len(vmNetworks))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if s.LibVirt != nil {
					active, err := s.LibVirt.IsDomainInactive(vm.RID)

					if err != nil {
						return fmt.Errorf("failed to check if VM %d is inactive: %w", vm.RID, err)
					}

					if !active {
						return fmt.Errorf("cannot_change_object_of_active_vm")
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				if s.LibVirt != nil {
					err = s.LibVirt.FindAndChangeMAC(vm.RID, object.Entries[0].Value, values[0])
					if err != nil {
						return fmt.Errorf("failed to change MAC address in VM %d: %w", vm.RID, err)
					}
				}
			}

			/* Object was used in a Jail, but now we're changing it to something else, we can't do that */
			if len(jailNetworks) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			/* MAC Used in a Jail */
			if len(jailNetworks) > 0 && oType == "Mac" {
				err := s.AddNetworkObjectEditJailTrigger(id, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}
			}

			/* Object was used in DHCP leases, but now we're changing it to something else, we can't do that */
			if len(dhcpLeases) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_dhcp")
			}

			/* MAC Used in DHCP leases */
			if len(dhcpLeases) > 0 && oType == "Mac" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d DHCP leases, please ensure only one MAC is provided", id, len(dhcpLeases))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}
					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.WriteDHCPConfig()
				if err != nil {
					logger.L.Error().Err(err).Msgf("failed to write DHCP config after editing object %d", id)
				}
			}
		}

		if object.Type == "Host" {
			/* IP used by switches */
			var switches []networkModels.StandardSwitch
			if err := s.DB.
				Preload("NetworkObj.Entries").
				Preload("Network6Obj.Entries").
				Preload("GatewayAddressObj.Entries").
				Preload("Gateway6AddressObj.Entries").
				Where("gateway_address_object_id = ? OR gateway6_address_object_id = ?", id, id).
				Find(&switches).Error; err != nil {
				return fmt.Errorf("failed to find standard switches using object %d: %w", id, err)
			}

			if len(switches) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_host_used_in_switches")
			}

			if len(switches) > 0 && oType == "Host" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d standard switches, please ensure only one IP is provided", id, len(switches))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.SyncStandardSwitches(nil, "sync")
				if err != nil {
					return fmt.Errorf("failed to sync standard switches after editing object %d: %w", id, err)
				}
			}

			/* IP used by jails */
			var jailNetworks []jailModels.Network
			if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if len(jailNetworks) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			if len(jailNetworks) > 0 && oType == "Host" {
				err := s.AddNetworkObjectEditJailTrigger(id, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}
			}

			var dhcpLeases []networkModels.DHCPStaticLease
			if err := s.DB.Preload("IPObject.Entries").Where("ip_object_id = ?", id).Find(&dhcpLeases).Error; err != nil {
				return fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
			}

			/* Object was used in DHCP leases, but now we're changing it to something else, we can't do that */
			if len(dhcpLeases) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_dhcp")
			}

			/* IP Used in DHCP leases */
			if len(dhcpLeases) > 0 && oType == "Host" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d DHCP leases, please ensure only one IP is provided", id, len(dhcpLeases))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}
					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.WriteDHCPConfig()
				if err != nil {
					logger.L.Error().Err(err).Msgf("failed to write DHCP config after editing object %d", id)
				}
			}
		}

		if object.Type == "Network" {
			/* Network used by switches */
			var switches []networkModels.StandardSwitch
			if err := s.DB.
				Preload("NetworkObj.Entries").
				Preload("Network6Obj.Entries").
				Preload("GatewayAddressObj.Entries").
				Preload("Gateway6AddressObj.Entries").
				Where("network_object_id = ? OR network6_object_id = ?", id, id).
				Find(&switches).Error; err != nil {
				return fmt.Errorf("failed to find standard switches using object %d: %w", id, err)
			}

			if len(switches) > 0 && oType != "Network" {
				return fmt.Errorf("cannot_change_object_type_network_used_in_switches")
			}

			if len(switches) > 0 && oType == "Network" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d standard switches, please ensure only one network is provided", id, len(switches))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.SyncStandardSwitches(nil, "sync")
				if err != nil {
					return fmt.Errorf("failed to sync standard switches after editing object %d: %w", id, err)
				}
			}

			/* Network used by jails */
			var jailNetworks []jailModels.Network
			if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if len(jailNetworks) > 0 && oType != "Network" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			if len(jailNetworks) > 0 && oType == "Network" {
				err := s.AddNetworkObjectEditJailTrigger(id, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}
			}
		}
	}

	return nil
}

func (s *Service) AddNetworkObjectEditJailTrigger(id uint, values []string) error {
	if len(values) != 1 {
		return fmt.Errorf("at_most_1_entry_allowed")
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
	}

	for _, value := range values {
		entry := networkModels.ObjectEntry{
			ObjectID: id,
			Value:    value,
		}

		if err := s.DB.Create(&entry).Error; err != nil {
			return fmt.Errorf("failed to create entry for object %d: %w", id, err)
		}
	}

	used, jailIds, err := s.IsObjectUsedByJail(id)

	if err != nil {
		return fmt.Errorf("failed to check if object %d is used by a jail: %w", id, err)
	}

	if used {
		var trigger models.Triggers

		slice, err := utils.UintSliceToJSON(jailIds)
		if err != nil {
			return fmt.Errorf("failed to convert jail IDs to JSON: %w", err)
		}

		trigger = models.Triggers{
			Action:    "edit_network_object_used_by_jails",
			Completed: false,
			Data:      slice,
		}

		if err := s.DB.Create(&trigger).Error; err != nil {
			return fmt.Errorf("failed to create trigger for object %d: %w", id, err)
		}
	}

	return nil
}

func (s *Service) GetObjectEntryByID(id uint) (string, error) {
	var object networkModels.Object

	if err := s.DB.Preload("Entries").First(&object, id).Error; err != nil {
		return "", fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}

	if len(object.Entries) == 0 {
		return "", fmt.Errorf("no entries found for object with ID %d", id)
	}

	if len(object.Entries) > 1 {
		return "", fmt.Errorf("multiple entries found for object with ID %d, expected only one", id)
	}

	return object.Entries[0].Value, nil
}
