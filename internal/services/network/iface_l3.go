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
		liveByName[obj.Name] = obj
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
	reservations, err := s.activeHostInterfaceL3Reservations()
	if err != nil {
		return list, err
	}

	for _, row := range rows {
		addresses := make([]networkServiceInterfaces.HostInterfaceL3AddressEntry, 0, len(row.Addresses))
		for _, address := range row.Addresses {
			addresses = append(addresses, networkServiceInterfaces.HostInterfaceL3AddressEntry{
				Family:       address.Family,
				Address:      address.Address,
				PrefixLength: address.PrefixLength,
			})
		}
		managedAddresses := append(
			make([]networkModels.HostInterfaceL3AppliedAddress, 0, len(row.AppliedState.Addresses)),
			row.AppliedState.Addresses...,
		)
		entry := networkServiceInterfaces.HostInterfaceL3Entry{
			Interface:        row.Interface,
			VLANParent:       row.VLANParent,
			VLANTag:          row.VLANTag,
			IPv6Mode:         row.IPv6Mode,
			MTU:              row.MTU,
			MTUBaseline:      row.AdoptionBaseline.MTU,
			Metric:           row.Metric,
			MetricBaseline:   row.AdoptionBaseline.Metric,
			IdentityMAC:      row.IdentityMAC,
			Revision:         row.Revision,
			Addresses:        addresses,
			ManagedAddresses: managedAddresses,
			Conflicts:        make([]string, 0, 3),
		}

		live, present := liveByName[row.Interface]
		entry.Present = present
		if present {
			entry.LiveMAC = live.Ether
		}

		switch {
		case !present:
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictMissingInterface)
		case strings.TrimSpace(row.IdentityMAC) != "" &&
			!strings.EqualFold(strings.TrimSpace(row.IdentityMAC), strings.TrimSpace(live.Ether)):
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictIdentityMismatch)
		}
		if present && (row.VLANParent != live.VLANParent || int(row.VLANTag) != live.VLANTag) {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictVLANIdentity)
		}

		if _, isPort := standardPorts[row.Interface]; isPort {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictStandardSwitchPort)
		} else if bridges, isMember := bridgeMembership[row.Interface]; isMember && len(bridges) > 0 {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictBridgeMember)
		}

		if row.VLANParent != "" {
			parent, parentPresent := liveByName[row.VLANParent]
			if !parentPresent {
				entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictVLANParentMissing)
			} else if s.hostInterfaceL3EligibilityCode(parent) != "" ||
				standardPortContains(standardPorts, row.VLANParent) ||
				bridgeMembershipContains(bridgeMembership, row.VLANParent) {
				entry.Conflicts = append(entry.Conflicts, "host_interface_l3_vlan_parent_ineligible")
			}
			if _, ok := rowsByInterface[row.VLANParent]; ok {
				entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictParentHasHostIP)
			}
		} else if hostInterfaceL3HasVLANChildren(row.Interface, rowsByInterface, liveByName) {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictParentHasChildren)
		}
		if present && hostInterfaceL3PrefixOwnershipChanged(&row, live) {
			entry.Conflicts = append(entry.Conflicts, networkServiceInterfaces.HostInterfaceL3ConflictPrefixOwnerChanged)
		}

		entries = append(entries, entry)
	}

	list.Rows = entries
	list.Targets = hostInterfaceL3Targets(
		rows,
		liveInterfaces,
		liveByName,
		standardPorts,
		bridgeMembership,
		reservations,
		s.hostInterfaceL3EligibilityCode,
	)
	return list, nil
}

func hostInterfaceL3Targets(
	rows []networkModels.HostInterfaceL3,
	liveInterfaces []*iface.Interface,
	liveByName map[string]*iface.Interface,
	standardPorts map[string]struct{},
	bridgeMembership map[string]map[string]struct{},
	reservations map[string]struct{},
	eligibility func(*iface.Interface) string,
) []networkServiceInterfaces.HostInterfaceL3TargetEntry {
	rowsByInterface := make(map[string]networkModels.HostInterfaceL3, len(rows))
	for _, row := range rows {
		rowsByInterface[row.Interface] = row
	}

	hasConfiguredChildren := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.VLANParent != "" {
			hasConfiguredChildren[row.VLANParent] = true
		}
	}
	hasLiveChildren := make(map[string]bool)
	for _, obj := range liveInterfaces {
		if obj.VLANParent != "" {
			hasLiveChildren[obj.VLANParent] = true
		}
	}

	targets := make([]networkServiceInterfaces.HostInterfaceL3TargetEntry, 0, len(liveInterfaces))
	seen := make(map[string]struct{}, len(liveInterfaces))
	for _, obj := range liveInterfaces {
		if _, duplicate := seen[obj.Name]; duplicate {
			continue
		}
		seen[obj.Name] = struct{}{}

		reason := ""

		switch code := eligibility(obj); {
		case hostInterfaceL3Reserved(reservations, obj.Name):
			reason = networkServiceInterfaces.HostInterfaceL3ConflictPending
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
			case eligibility(parent) != "":
				reason = "host_interface_l3_vlan_parent_ineligible"
			case standardPortContains(standardPorts, obj.VLANParent):
				reason = "host_interface_l3_vlan_parent_ineligible"
			case bridgeMembershipContains(bridgeMembership, obj.VLANParent):
				reason = "host_interface_l3_vlan_parent_ineligible"
			case rowsByInterface[obj.VLANParent].Interface != "":
				reason = networkServiceInterfaces.HostInterfaceL3ConflictParentHasHostIP
			}
		case hasConfiguredChildren[obj.Name] || hasLiveChildren[obj.Name]:
			reason = networkServiceInterfaces.HostInterfaceL3ConflictParentHasChildren
		}

		targets = append(targets, networkServiceInterfaces.HostInterfaceL3TargetEntry{
			Interface: obj.Name,
			Eligible:  reason == "",
			Reason:    reason,
		})
	}

	return targets
}

func hostInterfaceL3Reserved(reservations map[string]struct{}, name string) bool {
	_, reserved := reservations[name]
	return reserved
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
		if obj.VLANParent == name {
			return true
		}
	}
	return false
}
