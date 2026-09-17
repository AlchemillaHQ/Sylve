// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

var hostInterfaceL3ListInterfaces = iface.List

func (s *Service) GetHostInterfaceL3() (networkServiceInterfaces.HostInterfaceL3List, error) {
	var list networkServiceInterfaces.HostInterfaceL3List

	var rows []networkModels.HostInterfaceL3
	if err := s.DB.
		Preload("Addresses", func(db *gorm.DB) *gorm.DB {
			return db.Order("ordering asc, id asc")
		}).
		Order("interface asc").
		Find(&rows).Error; err != nil {
		return list, fmt.Errorf("failed_to_list_host_interface_l3: %w", err)
	}

	entries := make([]networkServiceInterfaces.HostInterfaceL3Entry, 0, len(rows))

	liveInterfaces, err := hostInterfaceL3ListInterfaces()
	if err != nil {
		return list, fmt.Errorf("failed_to_inspect_interfaces_for_host_interface_l3: %w", err)
	}

	liveByName := make(map[string]*iface.Interface, len(liveInterfaces))
	for _, obj := range liveInterfaces {
		if obj != nil {
			liveByName[obj.Name] = obj
		}
	}

	var ports []networkModels.NetworkPort
	if err := s.DB.Model(&networkModels.NetworkPort{}).Find(&ports).Error; err != nil {
		return list, fmt.Errorf("failed_to_list_network_ports_for_host_interface_l3: %w", err)
	}
	standardPorts := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		standardPorts[port.Name] = struct{}{}
	}

	var standardSwitches []networkModels.StandardSwitch
	if err := s.DB.Model(&networkModels.StandardSwitch{}).Find(&standardSwitches).Error; err != nil {
		return list, fmt.Errorf("failed_to_list_standard_switches_for_host_interface_l3: %w", err)
	}
	var manualSwitches []networkModels.ManualSwitch
	if err := s.DB.Model(&networkModels.ManualSwitch{}).Find(&manualSwitches).Error; err != nil {
		return list, fmt.Errorf("failed_to_list_manual_switches_for_host_interface_l3: %w", err)
	}

	bridgeMembership := make(map[string]map[string]struct{})
	recordMembership := func(bridge string, member string) {
		if member == "" {
			return
		}
		if _, ok := bridgeMembership[member]; !ok {
			bridgeMembership[member] = make(map[string]struct{})
		}
		bridgeMembership[member][bridge] = struct{}{}
	}

	knownBridges := make(map[string]struct{}, len(standardSwitches)+len(manualSwitches))
	for _, sw := range standardSwitches {
		knownBridges[sw.BridgeName] = struct{}{}
	}
	for _, sw := range manualSwitches {
		knownBridges[sw.Bridge] = struct{}{}
	}
	// Bridges Sylve does not know about still claim their members.
	for name, obj := range liveByName {
		if utils.Contains(obj.Groups, "bridge") {
			knownBridges[name] = struct{}{}
		}
	}
	for bridge := range knownBridges {
		obj, ok := liveByName[bridge]
		if !ok || bridge == "" {
			continue
		}
		for _, member := range obj.BridgeMembers {
			recordMembership(bridge, member.Name)
		}
	}

	rowsByInterface := make(map[string]networkModels.HostInterfaceL3, len(rows))
	for _, row := range rows {
		rowsByInterface[row.Interface] = row
	}

	for _, row := range rows {
		entry := networkServiceInterfaces.HostInterfaceL3Entry{
			ID:          row.ID,
			Interface:   row.Interface,
			VLANParent:  row.VLANParent,
			VLANTag:     row.VLANTag,
			Lifecycle:   row.Lifecycle,
			IPv6Mode:    row.IPv6Mode,
			MTU:         row.MTU,
			MTUBaseline: row.MTUBaseline,
			Metric:      row.Metric,
			IdentityMAC: row.IdentityMAC,
			Revision:    row.Revision,
			Addresses:   row.Addresses,
			Conflicts:   make([]string, 0, 2),
		}

		live, present := liveByName[row.Interface]
		entry.Present = present
		if present {
			entry.LiveMAC = live.Ether
		}

		switch {
		case !present:
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictMissingInterface)
		case row.IdentityMAC != "" && live.Ether != "" && !strings.EqualFold(row.IdentityMAC, live.Ether):
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictIdentityMismatch)
		}

		if _, isPort := standardPorts[row.Interface]; isPort {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictStandardSwitchPort)
		} else if bridges, isMember := bridgeMembership[row.Interface]; isMember && len(bridges) > 0 {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictBridgeMember)
		}

		if row.VLANParent != "" {
			if _, ok := liveByName[row.VLANParent]; !ok {
				entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictVLANParentMissing)
			}
			if _, ok := rowsByInterface[row.VLANParent]; ok {
				entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictParentHasHostIP)
			}
		} else if hostInterfaceL3HasVLANChildren(row.Interface, rowsByInterface, liveByName) {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictParentHasChildren)
		}

		entries = append(entries, entry)
	}

	list.Rows = entries
	list.Targets = hostInterfaceL3Targets(rows, liveInterfaces, liveByName, standardPorts, bridgeMembership)
	return list, nil
}

func hostInterfaceL3Targets(
	rows []networkModels.HostInterfaceL3,
	liveInterfaces []*iface.Interface,
	liveByName map[string]*iface.Interface,
	standardPorts map[string]struct{},
	bridgeMembership map[string]map[string]struct{},
) []networkServiceInterfaces.HostInterfaceL3TargetEntry {
	rowsByInterface := make(map[string]networkModels.HostInterfaceL3, len(rows))
	for _, row := range rows {
		rowsByInterface[row.Interface] = row
	}

	hasRows := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.VLANParent != "" {
			hasRows[row.VLANParent] = true
		}
	}
	hasLiveChildren := make(map[string]bool)
	for _, obj := range liveInterfaces {
		if obj != nil && obj.VLANParent != "" {
			hasLiveChildren[obj.VLANParent] = true
		}
	}

	targets := make([]networkServiceInterfaces.HostInterfaceL3TargetEntry, 0, len(liveInterfaces))
	seen := make(map[string]struct{}, len(liveInterfaces))
	for _, obj := range liveInterfaces {
		if obj == nil {
			continue
		}
		if _, duplicate := seen[obj.Name]; duplicate {
			continue
		}
		seen[obj.Name] = struct{}{}

		_, hasConfig := rowsByInterface[obj.Name]
		reason := ""

		switch code := hostInterfaceL3EligibilityCode(obj); {
		case code != "":
			reason = code
		case standardPortContains(standardPorts, obj.Name):
			reason = networkServiceInterfaces.HostInterfaceL3ConflictStandardSwitchPort
		case bridgeMembershipContains(bridgeMembership, obj.Name):
			reason = networkServiceInterfaces.HostInterfaceL3ConflictBridgeMember
		case obj.VLANParent != "":
			parent, parentLive := liveByName[obj.VLANParent]
			switch {
			case !parentLive:
				reason = networkServiceInterfaces.HostInterfaceL3ConflictVLANParentMissing
			case hostInterfaceL3EligibilityCode(parent) != "":
				reason = "host_interface_l3_vlan_parent_ineligible"
			case hasRows[obj.VLANParent]:
				reason = networkServiceInterfaces.HostInterfaceL3ConflictParentHasHostIP
			}
		case hasRows[obj.Name] || hasLiveChildren[obj.Name]:
			reason = networkServiceInterfaces.HostInterfaceL3ConflictParentHasChildren
		}

		targets = append(targets, networkServiceInterfaces.HostInterfaceL3TargetEntry{
			Interface: obj.Name,
			Eligible:  reason == "",
			Reason:    reason,
			HasConfig: hasConfig,
		})
	}

	return targets
}

func standardPortContains(ports map[string]struct{}, name string) bool {
	_, ok := ports[name]
	return ok
}

func bridgeMembershipContains(membership map[string]map[string]struct{}, name string) bool {
	bridges, ok := membership[name]
	return ok && len(bridges) > 0
}

func hostInterfaceL3HasVLANChildren(
	name string,
	rows map[string]networkModels.HostInterfaceL3,
	live map[string]*iface.Interface,
) bool {
	for _, row := range rows {
		if row.VLANParent == name {
			return true
		}
	}
	for _, obj := range live {
		if obj != nil && obj.VLANParent == name {
			return true
		}
	}
	return false
}
