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
	"sort"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	standardSwitchRCInspectionTimeout = 5 * time.Second
	standardSwitchRCModesMarker       = "__sylve_standard_switch_rc_modes__"
)

const standardSwitchRCBridgeScanScript = `
_sylve_rc_bridges=""

_sylve_args_add_member()
{
	_sylve_expect_member=0
	for _sylve_arg in $1; do
		if [ "$_sylve_expect_member" -eq 1 ]; then
			[ "$_sylve_arg" = "$_sylve_if" ] && return 0
			_sylve_expect_member=0
		fi
		case "$_sylve_arg" in
		addm)	_sylve_expect_member=1 ;;
		esac
	done
	return 1
}

for _sylve_clone in ${cloned_interfaces}; do
	_sylve_bridge=${_sylve_clone%%:*}
	_sylve_args="$(_ifconfig_getargs "$_sylve_bridge") $(get_if_var "$_sylve_bridge" create_args_IF)"
	if _sylve_args_add_member "$_sylve_args"; then
		case " $_sylve_rc_bridges " in
		*" $_sylve_bridge "*) ;;
		*) _sylve_rc_bridges="${_sylve_rc_bridges}${_sylve_rc_bridges:+ }$_sylve_bridge" ;;
		esac
	fi
done
`

const standardSwitchRCInspectionScript = `
_sylve_if="$1"
. /etc/rc.subr || exit 1
. /etc/network.subr || exit 1

_sylve_dhcp=0
_sylve_slaac=0
_sylve_static4=0
_sylve_static6=0
_sylve_alias4=0
_sylve_alias6=0
(
	load_rc_config dhclient
	load_rc_config network
	load_rc_config netif
	dhcpif "$_sylve_if"
) && _sylve_dhcp=1
(
	load_rc_config network
	load_rc_config netif
	ipv6_autoconfif "$_sylve_if"
) && _sylve_slaac=1

load_rc_config network
load_rc_config netif
for _sylve_arg in $(_ifconfig_getargs "$_sylve_if"); do
	case "$_sylve_arg" in
	[Ii][Nn][Ee][Tt]|[0-9]*.[0-9]*.[0-9]*.[0-9]*) _sylve_static4=1 ;;
	esac
done
for _sylve_arg in $(_ifconfig_getargs "$_sylve_if" ipv6); do
	case "$_sylve_arg" in
	[Ii][Nn][Ee][Tt]6|*:* ) _sylve_static6=1 ;;
	esac
done
ifalias_af_common_handler() { return 0; }
ifalias "$_sylve_if" inet alias && _sylve_alias4=1
ifalias "$_sylve_if" inet6 alias && _sylve_alias6=1
` + standardSwitchRCBridgeScanScript + `
printf '__sylve_standard_switch_rc_modes__ dhcp=%s slaac=%s static4=%s static6=%s alias4=%s alias6=%s' \
	"$_sylve_dhcp" "$_sylve_slaac" "$_sylve_static4" "$_sylve_static6" \
	"$_sylve_alias4" "$_sylve_alias6"
for _sylve_bridge in $_sylve_rc_bridges; do
	printf ' bridge=%s' "$_sylve_bridge"
done
printf '\n'
`

type standardSwitchMemberRCModes struct {
	DHCP              bool
	SLAAC             bool
	StaticIPv4        bool
	StaticIPv6        bool
	AliasesIPv4       bool
	AliasesIPv6       bool
	ConfiguredBridges []string
}

var (
	runStandardSwitchRCInspection    = utils.RunCommandWithContext
	syncInspectStandardSwitchRCModes = inspectStandardSwitchMemberRCModes
	syncListStandardSwitchInterfaces = iface.List
)

func parseStandardSwitchMemberRCModes(output string) (standardSwitchMemberRCModes, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || fields[0] != standardSwitchRCModesMarker {
			continue
		}

		var modes standardSwitchMemberRCModes
		seenDHCP := false
		seenSLAAC := false
		seenBridges := make(map[string]struct{})
		for _, field := range fields[1:] {
			key, value, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			switch key {
			case "dhcp":
				if value != "0" && value != "1" {
					continue
				}
				modes.DHCP = value == "1"
				seenDHCP = true
			case "slaac":
				if value != "0" && value != "1" {
					continue
				}
				modes.SLAAC = value == "1"
				seenSLAAC = true
			case "static4":
				modes.StaticIPv4 = value == "1"
			case "static6":
				modes.StaticIPv6 = value == "1"
			case "alias4":
				modes.AliasesIPv4 = value == "1"
			case "alias6":
				modes.AliasesIPv6 = value == "1"
			case "bridge":
				if value == "" || len(value) > MaxStandardSwitchPortNameBytes ||
					!standardSwitchPortPattern.MatchString(value) {
					continue
				}
				if _, duplicate := seenBridges[value]; duplicate {
					continue
				}
				seenBridges[value] = struct{}{}
				modes.ConfiguredBridges = append(modes.ConfiguredBridges, value)
			}
		}
		if !seenDHCP || !seenSLAAC {
			return standardSwitchMemberRCModes{}, fmt.Errorf("incomplete rc mode result %q", line)
		}
		return modes, nil
	}

	return standardSwitchMemberRCModes{}, fmt.Errorf("rc mode marker missing from command output")
}

func inspectStandardSwitchMemberRCModes(member string) (standardSwitchMemberRCModes, error) {
	member = strings.TrimSpace(member)
	if member == "" {
		return standardSwitchMemberRCModes{}, fmt.Errorf("member interface is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), standardSwitchRCInspectionTimeout)
	defer cancel()

	output, err := runStandardSwitchRCInspection(
		ctx,
		"/bin/sh",
		"-c",
		standardSwitchRCInspectionScript,
		"sylve-standard-switch-rc-inspection",
		member,
	)
	if err != nil {
		return standardSwitchMemberRCModes{}, fmt.Errorf("inspect effective network rc configuration: %w", err)
	}

	modes, err := parseStandardSwitchMemberRCModes(output)
	if err != nil {
		return standardSwitchMemberRCModes{}, fmt.Errorf("inspect effective network rc configuration: %w", err)
	}
	return modes, nil
}

const (
	standardSwitchRCConflictL3                 = "standard_switch_member_rc_l3_conflict"
	standardSwitchRCConflictBridge             = "standard_switch_member_bridge_ownership_conflict"
	standardSwitchRCInspectionUnavailable      = "standard_switch_member_rc_inspection_unavailable"
	standardSwitchRuntimeInspectionUnavailable = "standard_switch_runtime_bridge_inspection_unavailable"
)

type StandardSwitchRCConflict struct {
	Code              string `json:"code"`
	Port              string `json:"port,omitempty"`
	Member            string `json:"member,omitempty"`
	ConflictingBridge string `json:"conflictingBridge,omitempty"`
	DHCP              bool   `json:"dhcp,omitempty"`
	SLAAC             bool   `json:"slaac,omitempty"`
	StaticIPv4        bool   `json:"staticIPv4,omitempty"`
	StaticIPv6        bool   `json:"staticIPv6,omitempty"`
	AliasesIPv4       bool   `json:"aliasesIPv4,omitempty"`
	AliasesIPv6       bool   `json:"aliasesIPv6,omitempty"`
	RCConfigured      bool   `json:"rcConfigured,omitempty"`
	RuntimeAttached   bool   `json:"runtimeAttached,omitempty"`
}

type standardSwitchBridgeOwnershipConflict struct {
	RCConfigured    bool
	RuntimeAttached bool
}

func runtimeStandardSwitchMemberBridges(sw networkModels.StandardSwitch) (map[string][]string, error) {
	interfaces, err := syncListStandardSwitchInterfaces()
	if err != nil {
		return nil, err
	}

	memberBridges := make(map[string][]string)
	for _, interfaceObj := range interfaces {
		if interfaceObj == nil || interfaceObj.Name == sw.BridgeName {
			continue
		}
		for _, member := range interfaceObj.BridgeMembers {
			memberBridges[member.Name] = append(memberBridges[member.Name], interfaceObj.Name)
		}
	}
	return memberBridges, nil
}

func inspectStandardSwitchRCConflicts(sw networkModels.StandardSwitch) []StandardSwitchRCConflict {
	if len(sw.Ports) == 0 {
		return []StandardSwitchRCConflict{}
	}

	warnings := make([]StandardSwitchRCConflict, 0)
	runtimeBridges, runtimeErr := runtimeStandardSwitchMemberBridges(sw)
	if runtimeErr != nil {
		warnings = append(warnings, StandardSwitchRCConflict{Code: standardSwitchRuntimeInspectionUnavailable})
		runtimeBridges = map[string][]string{}
	}

	for _, port := range sw.Ports {
		member := port.Name
		if sw.VLAN > 0 {
			member = fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
		}

		modes, rcErr := syncInspectStandardSwitchRCModes(member)
		if rcErr != nil {
			warnings = append(warnings, StandardSwitchRCConflict{
				Code: standardSwitchRCInspectionUnavailable, Port: port.Name, Member: member,
			})
		} else if modes.DHCP || modes.SLAAC || modes.StaticIPv4 || modes.StaticIPv6 || modes.AliasesIPv4 || modes.AliasesIPv6 {
			warnings = append(warnings, StandardSwitchRCConflict{
				Code: standardSwitchRCConflictL3, Port: port.Name, Member: member,
				DHCP: modes.DHCP, SLAAC: modes.SLAAC,
				StaticIPv4: modes.StaticIPv4, StaticIPv6: modes.StaticIPv6,
				AliasesIPv4: modes.AliasesIPv4, AliasesIPv6: modes.AliasesIPv6,
			})
		}

		bridgeConflicts := make(map[string]standardSwitchBridgeOwnershipConflict)
		if rcErr == nil {
			for _, bridge := range modes.ConfiguredBridges {
				conflict := bridgeConflicts[bridge]
				conflict.RCConfigured = true
				bridgeConflicts[bridge] = conflict
			}
		}
		for _, bridge := range runtimeBridges[member] {
			conflict := bridgeConflicts[bridge]
			conflict.RuntimeAttached = true
			bridgeConflicts[bridge] = conflict
		}

		bridgeNames := make([]string, 0, len(bridgeConflicts))
		for bridge := range bridgeConflicts {
			bridgeNames = append(bridgeNames, bridge)
		}
		sort.Strings(bridgeNames)
		for _, bridge := range bridgeNames {
			conflict := bridgeConflicts[bridge]
			warnings = append(warnings, StandardSwitchRCConflict{
				Code: standardSwitchRCConflictBridge, Port: port.Name, Member: member,
				ConflictingBridge: bridge, RCConfigured: conflict.RCConfigured,
				RuntimeAttached: conflict.RuntimeAttached,
			})
		}
	}
	return warnings
}

func (s *Service) StandardSwitchRCConflicts(id uint, name string, vlan int, ports []string) ([]StandardSwitchRCConflict, error) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	normalizedPorts, err := normalizeStandardSwitchPorts(ports)
	if err != nil {
		return nil, err
	}
	if !utils.IsValidVLAN(vlan) {
		return nil, invalidStandardSwitch("invalid_standard_switch_vlan", nil)
	}

	var sw networkModels.StandardSwitch
	if id == 0 {
		normalizedName, err := normalizeStandardSwitchName(name)
		if err != nil {
			return nil, err
		}
		sw.Name = normalizedName
		sw.BridgeName = utils.ShortHash("vm-" + normalizedName)
	} else {
		sw, err = loadStandardSwitch(s.DB, id)
		if err != nil {
			return nil, err
		}
	}
	sw.VLAN = vlan
	sw.Ports = standardSwitchPorts(sw.ID, normalizedPorts)
	return inspectStandardSwitchRCConflicts(sw), nil
}

func warnStandardSwitchMemberRCConflicts(sw networkModels.StandardSwitch) {
	runtimeBridges, runtimeErr := runtimeStandardSwitchMemberBridges(sw)
	if runtimeErr != nil {
		logger.L.Debug().
			Err(runtimeErr).
			Uint("switch_id", sw.ID).
			Str("switch", sw.Name).
			Str("bridge", sw.BridgeName).
			Msg("standard_switch_runtime_bridge_inspection_failed")
	}

	for _, port := range sw.Ports {
		member := port.Name
		if sw.VLAN > 0 {
			member = fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
		}

		modes, rcErr := syncInspectStandardSwitchRCModes(member)
		if rcErr != nil {
			logger.L.Debug().
				Err(rcErr).
				Uint("switch_id", sw.ID).
				Str("switch", sw.Name).
				Str("bridge", sw.BridgeName).
				Str("port", port.Name).
				Str("member", member).
				Msg("standard_switch_member_rc_config_inspection_failed")
		} else if modes.DHCP || modes.SLAAC || modes.StaticIPv4 || modes.StaticIPv6 || modes.AliasesIPv4 || modes.AliasesIPv6 {
			logger.L.Warn().
				Uint("switch_id", sw.ID).
				Str("switch", sw.Name).
				Str("bridge", sw.BridgeName).
				Str("port", port.Name).
				Str("member", member).
				Bool("dhcp", modes.DHCP).
				Bool("slaac", modes.SLAAC).
				Bool("static_ipv4", modes.StaticIPv4).
				Bool("static_ipv6", modes.StaticIPv6).
				Bool("aliases_ipv4", modes.AliasesIPv4).
				Bool("aliases_ipv6", modes.AliasesIPv6).
				Msg("standard_switch_member_rc_l3_conflict")
		}

		conflicts := make(map[string]standardSwitchBridgeOwnershipConflict)
		if rcErr == nil {
			for _, bridge := range modes.ConfiguredBridges {
				conflict := conflicts[bridge]
				conflict.RCConfigured = true
				conflicts[bridge] = conflict
			}
		}
		for _, bridge := range runtimeBridges[member] {
			conflict := conflicts[bridge]
			conflict.RuntimeAttached = true
			conflicts[bridge] = conflict
		}

		bridgeNames := make([]string, 0, len(conflicts))
		for bridge := range conflicts {
			bridgeNames = append(bridgeNames, bridge)
		}
		sort.Strings(bridgeNames)
		for _, bridge := range bridgeNames {
			conflict := conflicts[bridge]
			logger.L.Warn().
				Uint("switch_id", sw.ID).
				Str("switch", sw.Name).
				Str("bridge", sw.BridgeName).
				Str("port", port.Name).
				Str("member", member).
				Str("conflicting_bridge", bridge).
				Bool("rc_configured", conflict.RCConfigured).
				Bool("runtime_attached", conflict.RuntimeAttached).
				Msg("standard_switch_member_bridge_ownership_conflict")
		}
	}
}
