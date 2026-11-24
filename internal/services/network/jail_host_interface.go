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
	"slices"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

var jailHostInterfaceForbiddenGroups = map[string]struct{}{
	"bridge": {}, "epair": {}, "tap": {}, "lo": {}, "wg": {}, "pfsync": {},
	"vnet": {}, "carp": {}, "tun": {}, "gif": {}, "gre": {}, "enc": {},
	"ipsec": {}, "vxlan": {}, "svm-vlan": {}, standardSwitchHostVLANGroup: {}, sylveEpairGroup: {},
}

func (s *Service) JailHostInterfaceTargets() ([]networkServiceInterfaces.JailHostInterfaceTarget, error) {
	liveInterfaces, err := hostInterfaceL3ListInterfaces()
	if err != nil {
		return nil, fmt.Errorf("failed_to_inspect_interfaces: %w", err)
	}
	liveByName := make(map[string]*iface.Interface, len(liveInterfaces))
	vlanParents := make(map[string]struct{})
	for _, obj := range liveInterfaces {
		liveByName[obj.Name] = obj
		if obj.VLANParent != "" {
			vlanParents[obj.VLANParent] = struct{}{}
		}
	}

	var portNames []string
	if err := s.DB.Model(&networkModels.NetworkPort{}).Pluck("name", &portNames).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_network_ports: %w", err)
	}
	var bridges []string
	if err := s.DB.Model(&networkModels.StandardSwitch{}).Pluck("bridge_name", &bridges).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_standard_switches: %w", err)
	}
	var manualBridges []string
	if err := s.DB.Model(&networkModels.ManualSwitch{}).Pluck("bridge", &manualBridges).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_manual_switches: %w", err)
	}
	bridges = append(bridges, manualBridges...)
	for name, obj := range liveByName {
		if slices.Contains(obj.Groups, "bridge") {
			bridges = append(bridges, name)
		}
	}
	bridgeMembers := make(map[string]struct{})
	for _, bridge := range bridges {
		if obj, ok := liveByName[bridge]; ok {
			for _, member := range obj.BridgeMembers {
				bridgeMembers[member.Name] = struct{}{}
			}
		}
	}
	var hostIPInterfaces []string
	if err := s.DB.Model(&networkModels.HostInterfaceL3{}).Pluck("interface", &hostIPInterfaces).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_host_interface_l3: %w", err)
	}
	var wanInterfaces []string
	if config.ParsedConfig != nil {
		wanInterfaces = config.ParsedConfig.WANInterfaces
	}

	targets := make([]networkServiceInterfaces.JailHostInterfaceTarget, 0, len(liveByName))
	for name, obj := range liveByName {
		reason := jailHostInterfaceIneligibility(obj)
		switch {
		case reason != "":
		case slices.Contains(portNames, name):
			reason = "host_interface_switch_port"
		case hasKey(bridgeMembers, name):
			reason = "host_interface_bridge_member"
		case hasKey(vlanParents, name):
			reason = "host_interface_vlan_parent"
		case slices.Contains(hostIPInterfaces, name):
			reason = "host_interface_host_ip_configured"
		case jailHostInterfaceHasAddresses(obj):
			reason = "host_interface_has_addresses"
		case slices.Contains(wanInterfaces, name):
			reason = "host_interface_in_use_by_container_nat"
		default:
			usage, err := s.interfaceExternalUsage(name)
			if err != nil {
				return nil, fmt.Errorf("failed_to_check_interface_usage: %s: %w", name, err)
			}
			if usage != "" {
				reason = "host_interface_in_use_by_" + usage
			}
		}
		targets = append(targets, networkServiceInterfaces.JailHostInterfaceTarget{
			Interface: name,
			Eligible:  reason == "",
			Reason:    reason,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Interface < targets[j].Interface })
	return targets, nil
}

func jailHostInterfaceIneligibility(obj *iface.Interface) string {
	if !networkAttachment.ValidHostInterfaceName(obj.Name) {
		return "host_interface_invalid_name"
	}
	for _, group := range obj.Groups {
		if _, forbidden := jailHostInterfaceForbiddenGroups[strings.ToLower(strings.TrimSpace(group))]; forbidden {
			return "host_interface_unsupported_type"
		}
	}
	if obj.LaggPort {
		return "host_interface_lagg_member"
	}
	if strings.TrimSpace(obj.Ether) == "" {
		return "host_interface_unsupported_type"
	}
	return ""
}

func jailHostInterfaceHasAddresses(obj *iface.Interface) bool {
	if len(obj.IPv4) > 0 {
		return true
	}
	for _, address := range obj.IPv6 {
		if !address.IP.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}

func (s *Service) jailClaimedHostInterfaces() (map[string]struct{}, error) {
	claimed := make(map[string]struct{})
	if !s.DB.Migrator().HasTable(&jailModels.Network{}) {
		return claimed, nil
	}
	var names []string
	if err := s.DB.Model(&jailModels.Network{}).
		Where("switch_type = ?", networkAttachment.SwitchTypeHostInterface).
		Pluck("host_interface", &names).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_jail_host_interfaces: %w", err)
	}
	for _, name := range names {
		claimed[name] = struct{}{}
	}
	return claimed, nil
}

func hasKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
