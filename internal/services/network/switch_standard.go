// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetStandardSwitches() ([]networkModels.StandardSwitch, error) {
	var switches []networkModels.StandardSwitch
	if err := s.DB.
		Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		Find(&switches).Error; err != nil {
		return nil, err
	}
	return switches, nil
}

func (s *Service) conflictingPortsForVLAN(ports []string, vlan int, excludeSwitchID *uint) ([]networkModels.NetworkPort, error) {
	var eps []networkModels.NetworkPort
	q := s.DB.Preload("Switch").Where("name IN ?", ports)
	if excludeSwitchID != nil {
		q = q.Where("switch_id <> ?", *excludeSwitchID)
	}

	if err := q.Find(&eps).Error; err != nil {
		return nil, fmt.Errorf("db_error_checking_ports: %w", err)
	}

	conflicts := make([]networkModels.NetworkPort, 0, len(eps))
	for _, ep := range eps {
		other := ep.Switch.VLAN
		if vlan == 0 || other == 0 || vlan == other {
			conflicts = append(conflicts, ep)
		}
	}

	return conflicts, nil
}

func (s *Service) NewStandardSwitch(
	name string,
	mtu int,
	vlan int,
	network4Id uint,
	network6Id uint,
	gateway4Id uint,
	gateway6Id uint,
	ports []string,
	private bool,
	dhcp bool,
	disableIPv6 bool,
	slaac bool,
	defaultRoute bool,
) error {
	var count int64
	if err := s.DB.Model(&networkModels.ManualSwitch{}).
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("switch_name_in_use")
	}

	if !utils.IsValidMTU(mtu) && mtu != 0 {
		return fmt.Errorf("invalid_mtu")
	}

	if !utils.IsValidVLAN(vlan) && vlan != 0 {
		return fmt.Errorf("invalid_vlan")
	}

	if conflicts, err := s.conflictingPortsForVLAN(ports, vlan, nil); err != nil {
		return err
	} else if len(conflicts) > 0 {
		var msgs []string
		for _, c := range conflicts {
			msgs = append(msgs, fmt.Sprintf("%s (used by switch %q vlan=%d)", c.Name, c.Switch.Name, c.Switch.VLAN))
		}
		return fmt.Errorf("port_overlap: %s", strings.Join(msgs, ", "))
	}

	if network4Id != 0 {
		var o4 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o4, network4Id).Error; err != nil {
			return fmt.Errorf("invalid_address_object: %v", err)
		}

		if o4.Type != "Network" {
			return fmt.Errorf("address_object must be Type=Network")
		}

		if len(o4.Entries) == 0 {
			return fmt.Errorf("address_object must have at least one entry")
		}

		if len(o4.Entries) > 1 {
			return fmt.Errorf("address_object must have only one entry")
		}
	}

	if gateway4Id != 0 {
		var o4 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o4, gateway4Id).Error; err != nil {
			return fmt.Errorf("invalid_gateway4_object: %v", err)
		}

		if o4.Type != "Host" {
			return fmt.Errorf("gateway4_object must be Type=Host")
		}

		if len(o4.Entries) == 0 {
			return fmt.Errorf("gateway4_object must have at least one entry")
		}

		if len(o4.Entries) > 1 {
			return fmt.Errorf("gateway4_object must have only one entry")
		}
	}

	if network6Id != 0 {
		var o6 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o6, network6Id).Error; err != nil {
			return fmt.Errorf("invalid_address6_object: %v", err)
		}

		if o6.Type != "Network" {
			return fmt.Errorf("address6_object must be Type=Network")
		}

		if len(o6.Entries) == 0 {
			return fmt.Errorf("address6_object must have at least one entry")
		}

		if len(o6.Entries) > 1 {
			return fmt.Errorf("address6_object must have only one entry")
		}
	}

	if gateway6Id != 0 {
		var o6 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o6, gateway6Id).Error; err != nil {
			return fmt.Errorf("invalid_gateway6_object: %v", err)
		}

		if o6.Type != "Host" {
			return fmt.Errorf("gateway6_object must be Type=Host")
		}

		if len(o6.Entries) == 0 {
			return fmt.Errorf("gateway6_object must have at least one entry")
		}

		if len(o6.Entries) > 1 {
			return fmt.Errorf("gateway6_object must have only one entry")
		}
	}

	sw := &networkModels.StandardSwitch{
		Name:              name,
		MTU:               mtu,
		VLAN:              vlan,
		BridgeName:        utils.ShortHash("vm-" + name),
		Private:           private,
		DHCP:              dhcp,
		DisableIPv6:       disableIPv6,
		SLAAC:             slaac,
		AddressID:         nil,
		Address6ID:        nil,
		NetworkID:         nil,
		Network6ID:        nil,
		GatewayAddressID:  nil,
		Gateway6AddressID: nil,
	}

	if network4Id != 0 {
		sw.NetworkID = &network4Id
	}

	if gateway4Id != 0 {
		sw.GatewayAddressID = &gateway4Id
	}

	if network6Id != 0 {
		sw.Network6ID = &network6Id
	}

	if gateway6Id != 0 {
		sw.Gateway6AddressID = &gateway6Id
	}

	sw.DefaultRoute = defaultRoute

	if err := s.DB.Create(sw).Error; err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed:") {
			return fmt.Errorf("switch_name_already_exists")
		}
		return fmt.Errorf("failed_to_create_switch: %v", err)
	}

	for _, p := range ports {
		port := &networkModels.NetworkPort{
			Name:     p,
			SwitchID: sw.ID,
		}
		if err := s.DB.Create(port).Error; err != nil {
			return fmt.Errorf("failed_to_create_port %s: %v", p, err)
		}
	}

	var fresh networkModels.StandardSwitch
	if err := s.DB.
		Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		First(&fresh, sw.ID).Error; err != nil {
		return fmt.Errorf("reload switch: %v", err)
	}

	return s.SyncStandardSwitches(&fresh, "create")
}

func (s *Service) DeleteStandardSwitch(id int) error {
	var vmCount, jailCount int64

	if err := s.DB.Model(&vmModels.Network{}).
		Where("switch_id = ? AND switch_type = ?", id, "standard").
		Count(&vmCount).Error; err != nil {
		return fmt.Errorf("db_error_checking_vm_switch: %v", err)
	}

	if vmCount > 0 {
		return fmt.Errorf("switch_in_use_by_vm")
	}

	if err := s.DB.Model(&jailModels.Network{}).
		Where("switch_id = ? AND switch_type = ?", id, "standard").
		Count(&jailCount).Error; err != nil {
		return fmt.Errorf("db_error_checking_jail_switch: %v", err)
	}

	if jailCount > 0 {
		return fmt.Errorf("switch_in_use_by_jail")
	}

	var oldSw networkModels.StandardSwitch

	var sw networkModels.StandardSwitch
	if err := s.DB.Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		First(&sw, id).Error; err != nil {
		return fmt.Errorf("switch_not_found")
	}

	oldSw = sw

	i, err := iface.Get(sw.BridgeName)
	ifaceMissing := false

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			ifaceMissing = true
		} else {
			return fmt.Errorf("delete_standard_bridge: failed to get interface %s: %v", sw.BridgeName, err)
		}
	}

	if !ifaceMissing {
		missing := []string{}

		for _, member := range i.BridgeMembers {
			found := false
			for _, port := range sw.Ports {
				if port.Name == member.Name {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, member.Name)
			}
		}

		if sw.VLAN > 0 {
			for i, port := range missing {
				if strings.Contains(port, fmt.Sprintf(".%d", sw.VLAN)) {
					missing = append(missing[:i], missing[i+1:]...)
					break
				}
			}
		}

		if len(missing) > 0 {
			return fmt.Errorf("delete_standard_bridge: missing_ports_in_db: %v", strings.Join(missing, ", "))
		}
	}

	if err := s.DB.Delete(&sw).Error; err != nil {
		return fmt.Errorf("failed_to_delete_switch: %v", err)
	}

	if err := s.DB.Where("switch_id = ?", id).
		Delete(&networkModels.NetworkPort{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_ports: %v", err)
	}

	return s.SyncStandardSwitches(&oldSw, "delete")
}

func (s *Service) EditStandardSwitch(
	id uint,
	mtu int,
	vlan int,
	network4Id uint,
	network6Id uint,
	gateway4Id uint,
	gateway6Id uint,
	ports []string,
	private bool,
	dhcp bool,
	disableIPv6 bool,
	slaac bool,
	defaultRoute bool,
) error {
	if !utils.IsValidMTU(mtu) {
		return fmt.Errorf("invalid_mtu")
	}

	if !utils.IsValidVLAN(vlan) && vlan != 0 {
		return fmt.Errorf("invalid_vlan")
	}

	if conflicts, err := s.conflictingPortsForVLAN(ports, vlan, &id); err != nil {
		return err
	} else if len(conflicts) > 0 {
		var msgs []string
		for _, c := range conflicts {
			msgs = append(msgs, fmt.Sprintf("%s (used by switch %q vlan=%d)", c.Name, c.Switch.Name, c.Switch.VLAN))
		}
		return fmt.Errorf("port_overlap: %s", strings.Join(msgs, ", "))
	}

	if network4Id != 0 {
		var o4 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o4, network4Id).Error; err != nil {
			return fmt.Errorf("invalid_network4_object: %v", err)
		}

		if o4.Type != "Network" {
			return fmt.Errorf("network4_object must be Type=Network")
		}

		if len(o4.Entries) == 0 {
			return fmt.Errorf("network4_object must have at least one entry")
		}

		if len(o4.Entries) > 1 {
			return fmt.Errorf("network4_object must have only one entry")
		}
	}

	if gateway4Id != 0 {
		var o4 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o4, gateway4Id).Error; err != nil {
			return fmt.Errorf("invalid_gateway4_object: %v", err)
		}

		if o4.Type != "Host" {
			return fmt.Errorf("gateway4_object must be Type=Host")
		}

		if len(o4.Entries) == 0 {
			return fmt.Errorf("gateway4_object must have at least one entry")
		}

		if len(o4.Entries) > 1 {
			return fmt.Errorf("gateway4_object must have only one entry")
		}
	}

	if network6Id != 0 {
		var o6 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o6, network6Id).Error; err != nil {
			return fmt.Errorf("invalid_network6_object: %v", err)
		}

		if o6.Type != "Network" {
			return fmt.Errorf("network6_object must be Type=Network")
		}

		if len(o6.Entries) == 0 {
			return fmt.Errorf("network6_object must have at least one entry")
		}

		if len(o6.Entries) > 1 {
			return fmt.Errorf("network6_object must have only one entry")
		}
	}

	if gateway6Id != 0 {
		var o6 networkModels.Object
		if err := s.DB.Preload("Entries").First(&o6, gateway6Id).Error; err != nil {
			return fmt.Errorf("invalid_gateway6_object: %v", err)
		}

		if o6.Type != "Host" {
			return fmt.Errorf("gateway6_object must be Type=Host")
		}

		if len(o6.Entries) == 0 {
			return fmt.Errorf("gateway6_object must have at least one entry")
		}

		if len(o6.Entries) > 1 {
			return fmt.Errorf("gateway6_object must have only one entry")
		}
	}

	var loaded networkModels.StandardSwitch
	if err := s.DB.
		Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		First(&loaded, id).Error; err != nil {
		return fmt.Errorf("switch_not_found")
	}

	before := loaded

	loaded.MTU = mtu
	loaded.VLAN = vlan
	loaded.Private = private
	loaded.DHCP = dhcp
	loaded.DisableIPv6 = disableIPv6
	loaded.SLAAC = slaac

	if network4Id != 0 {
		loaded.NetworkID = &network4Id
	} else {
		loaded.NetworkID = nil
	}

	if gateway4Id != 0 {
		loaded.GatewayAddressID = &gateway4Id
	} else {
		loaded.GatewayAddressID = nil
	}

	if network6Id != 0 {
		loaded.Network6ID = &network6Id
	} else {
		loaded.Network6ID = nil
	}

	if gateway6Id != 0 {
		loaded.Gateway6AddressID = &gateway6Id
	} else {
		loaded.Gateway6AddressID = nil
	}

	loaded.DefaultRoute = defaultRoute

	if err := s.DB.Model(&loaded).
		Select("MTU", "VLAN", "Private", "DHCP", "DisableIPv6", "SLAAC", "NetworkID", "GatewayAddressID", "Network6ID", "Gateway6AddressID", "DefaultRoute").
		Updates(loaded).Error; err != nil {
		return fmt.Errorf("failed_to_update_switch: %v", err)
	}

	if err := s.DB.
		Where("switch_id = ?", id).
		Delete(&networkModels.NetworkPort{}).Error; err != nil {
		return fmt.Errorf("failed_to_clear_old_ports: %v", err)
	}
	for _, name := range ports {
		p := networkModels.NetworkPort{
			Name:     name,
			SwitchID: id,
		}
		if err := s.DB.Create(&p).Error; err != nil {
			return fmt.Errorf("failed_to_create_port %s: %v", name, err)
		}
	}

	return s.SyncStandardSwitches(&before, "edit")
}

func (s *Service) SyncStandardSwitches(sw *networkModels.StandardSwitch, action string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	switch action {
	case "sync":
		var switches []networkModels.StandardSwitch
		if err := s.DB.Preload("Ports").
			Preload("NetworkObj.Entries").
			Preload("Network6Obj.Entries").
			Preload("GatewayAddressObj.Entries").
			Preload("Gateway6AddressObj.Entries").
			Find(&switches).Error; err != nil {
			return fmt.Errorf("db_error_checking_switches: %v", err)
		}

		var nonDbPorts = make(map[string][]string)

		for _, sw := range switches {
			var dbPorts []string

			for _, port := range sw.Ports {
				dbPorts = append(dbPorts, port.Name)
			}

			iface, _ := iface.Get(sw.BridgeName)
			if iface != nil {
				for _, member := range iface.BridgeMembers {
					if utils.Contains(dbPorts, member.Name) {
						continue
					}

					if _, exists := nonDbPorts[sw.BridgeName]; !exists {
						nonDbPorts[sw.BridgeName] = []string{}
					}

					nonDbPorts[sw.BridgeName] = append(nonDbPorts[sw.BridgeName], member.Name)
				}
			}
		}

		for _, sw := range switches {
			if err := deleteStandardBridge(sw); err != nil {
				return fmt.Errorf("sync_standard_switches: failed_to_delete: %v", err)
			}

			if err := createStandardBridge(sw); err != nil {
				return fmt.Errorf("sync_standard_switches: failed_to_create: %v", err)
			}
		}

		for br, members := range nonDbPorts {
			ifaceObj, err := iface.Get(br)
			if err != nil {
				return fmt.Errorf("sync_standard_switches: get %s: %v", br, err)
			}

			existingMembers := make(map[string]bool)
			for _, m := range ifaceObj.BridgeMembers {
				existingMembers[m.Name] = true
			}

			for _, member := range members {
				if _, exists := existingMembers[member]; !exists {
					if _, err := utils.RunCommand("ifconfig", br, "addm", member, "up"); err != nil {
						return fmt.Errorf("sync_standard_switches: add member %s to %s: %v", member, br, err)
					}

					if _, err := utils.RunCommand("ifconfig", member, "up"); err != nil {
						return fmt.Errorf("sync_standard_switches: bring up member %s: %v", member, err)
					}
				}
			}
		}

	case "create":
		if err := createStandardBridge(*sw); err != nil {
			return err
		}

	case "delete":
		if err := deleteStandardBridge(*sw); err != nil {
			return err
		}

	case "edit":
		var newSw networkModels.StandardSwitch
		if err := s.DB.Preload("Ports").
			Preload("NetworkObj.Entries").
			Preload("Network6Obj.Entries").
			Preload("GatewayAddressObj.Entries").
			Preload("Gateway6AddressObj.Entries").
			First(&newSw, sw.ID).Error; err != nil {
			return fmt.Errorf("switch_not_found")
		}
		if err := editStandardBridge(*sw, newSw); err != nil {
			return err
		}
	}

	return nil
}

func createStandardBridge(sw networkModels.StandardSwitch) error {
	raw, err := utils.RunCommand("ifconfig", "bridge", "create")
	if err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_create: %v", err)
	}

	raw = strings.TrimSpace(raw)

	if _, err := utils.RunCommand("ifconfig", raw, "name", sw.BridgeName); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_rename: %v", err)
	}

	if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "descr", sw.Name); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_set_descr: %v", err)
	}

	if sw.MTU != 0 {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "mtu", strconv.Itoa(sw.MTU)); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_mtu: %v", err)
		}
	}

	network4 := sw.Network(4)
	gateway4 := sw.Gateway(4)

	if network4 != "" && gateway4 != "" {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "inet", sw.Network(4)); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_network: %v", err)
		}

		if gwOut, err := utils.RunCommand("route", "add", "-net", network4, gateway4); err != nil {
			if !strings.Contains(gwOut, "route already in table") {
				return fmt.Errorf("create_standard_bridge: failed_to_add_network_route: %v", err)
			}
		}

		if sw.DefaultRoute {
			dR, err := utils.RunCommand("route", "add", "default", gateway4)
			if err != nil {
				if !strings.Contains(dR, "route already in table") {
					return fmt.Errorf("create_standard_bridge: failed_to_add_default_route: %v", err)
				}
			}
		}
	}

	network6 := sw.Network(6)
	gateway6 := sw.Gateway(6)

	if network6 != "" && gateway6 != "" && !sw.DisableIPv6 {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "inet6", network6); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_address6: %v", err)
		}

		if !strings.HasPrefix(gateway6, "fe80::") {
			if gwOut, err := utils.RunCommand("route", "-6", "add", "-net", network6, gateway6); err != nil {
				if !strings.Contains(gwOut, "route already in table") {
					return fmt.Errorf("create_standard_bridge: failed_to_add_network6_route: %v", err)
				}
			}
		}
	}

	if !sw.DisableIPv6 {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "inet6", "auto_linklocal"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_enable_linklocal: %v", err)
		}
	}

	if !sw.DisableIPv6 && sw.SLAAC {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "inet6", "auto_linklocal", "-ifdisabled", "accept_rtadv"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_enable_slaac: %v", err)
		}
	}

	if sw.DisableIPv6 {
		if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "inet6", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_disable_ipv6_flags: %v", err)
		}
	}

	if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "up"); err != nil {
		return fmt.Errorf("create_standard_bridge: failed to bring up bridge: %v", err)
	}

	for _, port := range sw.Ports {
		if err := addBridgeMember(sw.BridgeName, port.Name, sw.MTU, sw.VLAN); err != nil {
			return fmt.Errorf("create_standard_bridge: %v", err)
		}
	}

	if sw.DHCP {
		runDhclient(sw.BridgeName, 10)
	}

	return nil
}

func editStandardBridge(oldSw, newSw networkModels.StandardSwitch) error {
	br := oldSw.BridgeName

	// 1) snapshot existing members
	ifaceObj, err := iface.Get(br)
	if err != nil {
		return fmt.Errorf("edit_standard_bridge: get %s: %v", br, err)
	}
	var original []string
	for _, m := range ifaceObj.BridgeMembers {
		original = append(original, m.Name)
	}

	// 2) build sets of old & new DB ports (incl. VLAN ifaces)
	oldSet := make(map[string]bool, len(oldSw.Ports)*2)
	for _, p := range oldSw.Ports {
		oldSet[p.Name] = true
		if oldSw.VLAN > 0 {
			oldSet[fmt.Sprintf("%s.%d", p.Name, oldSw.VLAN)] = true
		}
	}
	newSet := make(map[string]bool, len(newSw.Ports)*2)
	for _, p := range newSw.Ports {
		newSet[p.Name] = true
		if newSw.VLAN > 0 {
			newSet[fmt.Sprintf("%s.%d", p.Name, newSw.VLAN)] = true
		}
	}

	// 3) remove only the *old* DB ports (and their VLAN sub-ifs)
	for _, p := range oldSw.Ports {
		if err := removeBridgeMember(br, p.Name, oldSw.VLAN); err != nil {
			return fmt.Errorf("edit_standard_bridge: remove old port %s: %v", p.Name, err)
		}
	}

	// 4) reconfigure bridge in place
	if _, err := utils.RunCommand("ifconfig", br, "descr", newSw.Name); err != nil {
		return fmt.Errorf("edit_standard_bridge: set descr: %v", err)
	}

	if oldSw.MTU != newSw.MTU && newSw.MTU > 0 {
		if _, err := utils.RunCommand("ifconfig", br, "mtu", strconv.Itoa(newSw.MTU)); err != nil {
			return fmt.Errorf("edit_standard_bridge: set mtu: %v", err)
		}
	}

	// Replace the IPv4 handling section in editStandardBridge with this:

	old4Network, new4Network := oldSw.Network(4), newSw.Network(4)
	old4Gateway, new4Gateway := oldSw.Gateway(4), newSw.Gateway(4)

	// Always clean up old IPv4 configuration
	if old4Network != "" {
		if _, err := utils.RunCommand("ifconfig", br, "inet", old4Network, "delete"); err != nil {
			logger.L.Warn().Msgf("edit_standard_bridge: del old inet %s: %v", old4Network, err)
		}
	}

	// Clean up old route if it existed
	if old4Gateway != "" && old4Network != "" {
		if _, err := utils.RunCommand("route", "delete", "-net", old4Network, old4Gateway); err != nil {
			logger.L.Warn().Msgf("edit_standard_bridge: del route %s via %s: %v", old4Network, old4Gateway, err)
		}
	}

	// Always apply new IPv4 address if specified
	if new4Network != "" {
		if _, err := utils.RunCommand("ifconfig", br, "inet", new4Network); err != nil {
			return fmt.Errorf("edit_standard_bridge: set inet %s: %v", new4Network, err)
		}
	}

	// Add route if both network and gateway are specified
	if new4Network != "" && new4Gateway != "" {
		if gwOut, err := utils.RunCommand("route", "add", "-net", new4Network, new4Gateway); err != nil {
			if !strings.Contains(gwOut, "route already in table") {
				return fmt.Errorf("edit_standard_bridge: add route %s via %s: %v", new4Network, new4Gateway, err)
			}
		}

		if newSw.DefaultRoute {
			dR, err := utils.RunCommand("route", "add", "default", new4Gateway)
			if err != nil {
				if !strings.Contains(dR, "route already in table") {
					return fmt.Errorf("create_standard_bridge: failed_to_add_default_route: %v", err)
				}
			}
		}
	}

	old6Network, new6Network := oldSw.Network(6), newSw.Network(6)
	old6Gateway, new6Gateway := oldSw.Gateway(6), newSw.Gateway(6)

	if old6Network != new6Network {
		if old6Network != "" {
			if _, err := utils.RunCommand("ifconfig", br, "inet6", old6Network, "delete"); err != nil {
				logger.L.Warn().Msgf("edit_standard_bridge: del old inet6 %s: %v", old6Network, err)
			}
		}

		if new6Network != "" {
			if _, err := utils.RunCommand("ifconfig", br, "inet6", new6Network); err != nil {
				logger.L.Warn().Msgf("edit_standard_bridge: set inet6 %s: %v", new6Network, err)
			}
		}
	}

	if old6Gateway != new6Gateway {
		if old6Gateway != "" {
			if _, err := utils.RunCommand("route", "-6", "delete", "-net", old6Network, old6Gateway); err != nil {
				logger.L.Warn().Msgf("edit_standard_bridge: del route %s via %s: %v", old6Network, old6Gateway, err)
			}
		}

		if new6Gateway != "" {
			if _, err := utils.RunCommand("route", "-6", "add", "-net", new6Network, new6Gateway); err != nil {
				logger.L.Warn().Msgf("edit_standard_bridge: add route %s via %s: %v", new6Network, new6Gateway, err)
			}
		}
	}

	if newSw.DisableIPv6 {
		if _, err := utils.RunCommand("ifconfig", br, "inet6", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("edit_standard_bridge: disable IPv6: %v", err)
		}

		for _, addr := range ifaceObj.IPv6 {
			ip := addr.IP.String()
			if strings.HasPrefix(ip, "fe80::") {
				ip += "%" + br
			}

			if _, err := utils.RunCommand("ifconfig", br, "inet6", ip, "delete"); err != nil {
				return fmt.Errorf("edit_standard_bridge: delete IPv6 address %s: %v", ip, err)
			}
		}
	}

	if !newSw.DisableIPv6 && newSw.SLAAC {
		if _, err := utils.RunCommand("ifconfig", br, "inet6", "auto_linklocal", "-ifdisabled", "accept_rtadv"); err != nil {
			return fmt.Errorf("edit_standard_bridge: enable SLAAC: %v", err)
		}
	} else if !newSw.DisableIPv6 {
		if _, err := utils.RunCommand("ifconfig", br, "inet6", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("edit_standard_bridge: disable SLAAC: %v", err)
		}
	}

	if !newSw.SLAAC {
		ifaceObj, err := iface.Get(br)
		if err != nil {
			return fmt.Errorf("edit_standard_bridge: get %s: %v", br, err)
		}

		for _, addr := range ifaceObj.IPv6 {
			if addr.AutoConf {
				ip := addr.IP.String()
				if strings.HasPrefix(ip, "fe80::") {
					ip += "%" + br
				}

				if _, err := utils.RunCommand("ifconfig", br, "inet6", ip, "delete"); err != nil {
					return fmt.Errorf("edit_standard_bridge: delete SLAAC address %s: %v", ip, err)
				}
			}
		}
	}

	if newSw.DHCP {
		runDhclient(newSw.BridgeName, 10)
	} else {
		if newSw.Network(4) == "" {
			ifaceObj, err := iface.Get(br)
			if err != nil {
				return fmt.Errorf("edit_standard_bridge: get %s: %v", br, err)
			}

			for _, addr := range ifaceObj.IPv4 {
				if _, err := utils.RunCommand("ifconfig", br, "inet", addr.IP.String(), "delete"); err != nil {
					return fmt.Errorf("edit_standard_bridge: delete IPv4 address %s: %v", addr.IP.String(), err)
				}
			}
		}
	}

	// 5) add the *new* DB ports (and VLAN sub-ifs)
	for _, p := range newSw.Ports {
		if err := addBridgeMember(br, p.Name, newSw.MTU, newSw.VLAN); err != nil {
			return fmt.Errorf("edit_standard_bridge: add port %s: %v", p.Name, err)
		}
	}

	// 6) re-attach only non-DB members (e.g. taps), skip old/new DB ports
	for _, m := range original {
		if oldSet[m] || newSet[m] {
			continue
		}

		oif, err := iface.Get(m)
		if err != nil {
			continue
		}
		if strings.Contains(oif.Driver, "tap") || utils.Contains(oif.Groups, "tap") || utils.Contains(oif.Groups, "vnet") {
			if _, err := utils.RunCommand("ifconfig", br, "addm", m, "up"); err != nil {
				if !strings.Contains(err.Error(), "BRDGADD "+m+": File exists") {
					return fmt.Errorf("edit_standard_bridge: re-add tap %s: %v", m, err)
				}
			}

			if _, err := utils.RunCommand("ifconfig", m, "up"); err != nil {
				return fmt.Errorf("edit_standard_bridge: bring up tap %s: %v", m, err)
			}
		}
	}

	if _, err := utils.RunCommand("ifconfig", br, "up"); err != nil {
		return fmt.Errorf("edit_standard_bridge: failed to bring up bridge: %v", err)
	}

	return nil
}

func deleteStandardBridge(sw networkModels.StandardSwitch) error {
	if _, err := utils.RunCommand("ifconfig", sw.BridgeName, "destroy"); err != nil {
		if !strings.Contains(err.Error(), "does not exist") {
			return fmt.Errorf("delete_standard_bridge: failed_to_destroy: %v", err)
		}
	}

	for _, port := range sw.Ports {
		vif := fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
		if _, err := utils.RunCommand("ifconfig", vif); err == nil {
			if _, err := utils.RunCommand("ifconfig", vif, "destroy"); err != nil {
				return fmt.Errorf("delete_standard_bridge: failed to destroy VLAN iface %s: %v", vif, err)
			}
		}
	}

	return nil
}

func addBridgeMember(br, portName string, mtu, vlan int) error {
	if mtu > 0 {
		if _, err := utils.RunCommand("ifconfig", portName, "mtu", strconv.Itoa(mtu)); err != nil {
			return fmt.Errorf("set mtu for %s: %v", portName, err)
		}
	}

	targetPort := portName
	if vlan > 0 {
		vif := fmt.Sprintf("%s.%d", portName, vlan)
		targetPort = vif

		if _, err := utils.RunCommand("ifconfig", vif); err != nil {
			args := []string{
				"vlan", "create",
				"vlandev", portName,
				"vlan", strconv.Itoa(vlan),
				"descr", fmt.Sprintf("svm-vlan/%s/%s", br, vif),
				"name", vif,
				"group", "svm-vlan",
				"up",
			}
			if _, err := utils.RunCommand("ifconfig", args...); err != nil {
				return fmt.Errorf("create vlan %s: %v", vif, err)
			}
		}
	}

	portsToClear := map[string]bool{portName: true}
	if targetPort != portName {
		portsToClear[targetPort] = true
	}

	for port := range portsToClear {
		if _, err := utils.RunCommand("ifconfig", port, "inet", "-alias"); err != nil {
			return fmt.Errorf("clear inet on %s: %v", port, err)
		}
		if _, err := utils.RunCommand("ifconfig", port, "inet6", "-alias"); err != nil &&
			!strings.Contains(err.Error(), "Can't assign requested address") {
			return fmt.Errorf("clear inet6 on %s: %v", port, err)
		}
	}

	if _, err := utils.RunCommand("ifconfig", br, "addm", targetPort, "up"); err != nil {
		return fmt.Errorf("add %s to bridge %s: %v", targetPort, br, err)
	}
	if _, err := utils.RunCommand("ifconfig", targetPort, "up"); err != nil {
		return fmt.Errorf("bring up %s: %v", targetPort, err)
	}

	return nil
}

func removeBridgeMember(br, portName string, vlan int) error {
	if vlan > 0 {
		vif := fmt.Sprintf("%s.%d", portName, vlan)
		if _, err := utils.RunCommand("ifconfig", br, "deletem", vif); err != nil {
			return fmt.Errorf("remove vlan member %s: %v", vif, err)
		}

		if _, err := utils.RunCommand("ifconfig", vif, "destroy"); err != nil {
			return fmt.Errorf("destroy vlan iface %s: %v", vif, err)
		}

	} else {
		if _, err := utils.RunCommand("ifconfig", br, "deletem", portName); err != nil {
			return fmt.Errorf("remove port member %s: %v", portName, err)
		}
	}
	return nil
}

func runDhclient(br string, timeout int) error {
	_, err := iface.Get(br)
	if err != nil {
		return fmt.Errorf("dhclient: failed to get interface %s: %v", br, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	_, err = utils.RunCommandWithContext(ctx, "dhclient", "-b", br)
	if err != nil {
		logger.L.Debug().Msgf("dhclient: failed to run dhclient for %s: %v", br, err)
		if strings.Contains(err.Error(), "dhclient already running") {
			return nil
		}

		return fmt.Errorf("dhclient: failed to run dhclient for %s: %v", br, err)
	}

	return nil
}
