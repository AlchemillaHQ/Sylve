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
	"net/netip"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var hostInterfaceL3ForbiddenKinds = map[string]struct{}{
	"bridge": {}, "epair": {}, "tap": {}, "lo": {}, "wg": {}, "pfsync": {},
	"vnet": {}, "carp": {}, "tun": {}, "gif": {}, "gre": {}, "enc": {},
	"ipsec": {}, "vxlan": {}, "lagg": {},
}

var hostInterfaceL3EligibilitySeam func(*iface.Interface) (string, bool)

type hostInterfaceL3DesiredAddress struct {
	Family       string
	Address      netip.Addr
	PrefixLength uint8
}

type hostInterfaceL3PlannedChange struct {
	Plan     hostInterfaceL3ApplyPlan
	Spec     networkModels.HostInterfaceL3Spec
	Baseline networkModels.HostInterfaceL3Baseline
	Intended networkModels.HostInterfaceL3AppliedState
	// IdentityMAC is captured before the first apply so confirmation cannot
	// silently adopt replacement hardware.
	IdentityMAC string
}

func hostInterfaceL3EligibilityCode(interfaceObj *iface.Interface) string {
	if interfaceObj == nil {
		return "host_interface_l3_missing_interface"
	}
	if hostInterfaceL3EligibilitySeam != nil {
		if code, handled := hostInterfaceL3EligibilitySeam(interfaceObj); handled {
			return code
		}
	}

	// VLAN children are eligible when their parent is; the parent's own
	// eligibility is checked by the caller.
	if strings.TrimSpace(interfaceObj.VLANParent) != "" {
		return ""
	}

	if strings.TrimSpace(interfaceObj.Ether) == "" {
		return "host_interface_l3_ineligible_no_mac"
	}

	for _, group := range interfaceObj.Groups {
		normalized := strings.ToLower(strings.TrimSpace(group))
		if _, forbidden := hostInterfaceL3ForbiddenKinds[normalized]; forbidden {
			return "host_interface_l3_ineligible_" + normalized
		}
	}

	// Hardware allowlist: a physical NIC reports a driver, and every known
	// virtual/service interface is refused either by group or driver.
	driver := strings.ToLower(strings.TrimSpace(interfaceObj.Driver))
	if driver == "" {
		return "host_interface_l3_ineligible_no_driver"
	}
	if _, forbidden := hostInterfaceL3ForbiddenKinds[driver]; forbidden {
		return "host_interface_l3_ineligible_" + driver
	}

	return ""
}

func (s *Service) hostInterfaceL3BridgeMembers(name string) ([]string, error) {
	var standardSwitches []networkModels.StandardSwitch
	if err := s.DB.Model(&networkModels.StandardSwitch{}).Find(&standardSwitches).Error; err != nil {
		return nil, fmt.Errorf("load standard switches: %w", err)
	}
	var manualSwitches []networkModels.ManualSwitch
	if err := s.DB.Model(&networkModels.ManualSwitch{}).Find(&manualSwitches).Error; err != nil {
		return nil, fmt.Errorf("load manual switches: %w", err)
	}

	bridgeSet := make(map[string]struct{}, len(standardSwitches)+len(manualSwitches))
	for _, sw := range standardSwitches {
		bridgeSet[sw.BridgeName] = struct{}{}
	}
	for _, sw := range manualSwitches {
		bridgeSet[sw.Bridge] = struct{}{}
	}

	// Bridges Sylve does not know about still claim their members.
	liveInterfaces, err := hostInterfaceL3ListInterfaces()
	if err != nil {
		return nil, fmt.Errorf("inspect live bridges: %w", err)
	}
	for _, candidate := range liveInterfaces {
		if candidate == nil {
			continue
		}
		if utils.Contains(candidate.Groups, "bridge") {
			bridgeSet[candidate.Name] = struct{}{}
		}
	}

	members := make([]string, 0)
	for bridge := range bridgeSet {
		if bridge == "" {
			continue
		}
		bridgeObj, err := syncIfaceGet(bridge)
		if err != nil {
			if isInterfaceMissingError(err) {
				continue
			}
			return nil, fmt.Errorf("inspect bridge %s: %w", bridge, err)
		}
		if bridgeObj == nil {
			continue
		}
		for _, member := range bridgeObj.BridgeMembers {
			if member.Name == name {
				members = append(members, bridge)
			}
		}
	}
	return members, nil
}

func (s *Service) hostInterfaceL3MembershipGuard(name string) error {
	var portCount int64
	if err := s.DB.Model(&networkModels.NetworkPort{}).Where("name = ?", name).Count(&portCount).Error; err != nil {
		return fmt.Errorf("check standard switch ports for %s: %w", name, err)
	}
	if portCount > 0 {
		return hostInterfaceL3Conflict("host_interface_l3_standard_switch_port", nil)
	}

	members, err := s.hostInterfaceL3BridgeMembers(name)
	if err != nil {
		return err
	}
	if len(members) > 0 {
		return hostInterfaceL3Conflict(
			"host_interface_l3_bridge_member",
			fmt.Errorf("interface %s is a member of bridge %s", name, members[0]),
		)
	}

	return nil
}

func normalizeHostInterfaceL3Addresses(
	inputs []networkServiceInterfaces.HostInterfaceL3AddressInput,
) ([]hostInterfaceL3DesiredAddress, error) {
	normalized := make([]hostInterfaceL3DesiredAddress, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))

	for _, input := range inputs {
		raw := strings.TrimSpace(input.Address)
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, invalidHostInterfaceL3("host_interface_l3_invalid_address", fmt.Errorf("parse %q: %w", raw, err))
		}
		address := prefix.Addr().Unmap()
		if !isUsableHostAddress(address) {
			return nil, invalidHostInterfaceL3(
				"host_interface_l3_invalid_address",
				fmt.Errorf("address %s is not a usable host address", raw),
			)
		}

		family := "inet"
		if address.Is6() {
			family = "inet6"
		}
		key := family + "|" + address.String()
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidHostInterfaceL3("host_interface_l3_duplicate_address", nil)
		}
		seen[key] = struct{}{}

		normalized = append(normalized, hostInterfaceL3DesiredAddress{
			Family:       family,
			Address:      address,
			PrefixLength: uint8(prefix.Bits()),
		})
	}

	return normalized, nil
}

func isUsableHostAddress(address netip.Addr) bool {
	return address.IsValid() &&
		!address.IsUnspecified() &&
		!address.IsLoopback() &&
		!address.IsMulticast() &&
		!address.IsLinkLocalUnicast()
}

func hostInterfaceL3ValidateMTU(mtu *uint, desired []hostInterfaceL3DesiredAddress, ipv6Mode string) error {
	return hostInterfaceL3ValidateMTUWithLiveIPv6(mtu, desired, ipv6Mode, false)
}

func hostInterfaceL3ValidateMTUWithLiveIPv6(
	mtu *uint,
	desired []hostInterfaceL3DesiredAddress,
	ipv6Mode string,
	liveIPv6 bool,
) error {
	if mtu == nil {
		return nil
	}
	if *mtu < MinHostInterfaceL3MTU || *mtu > MaxHostInterfaceL3MTU {
		return invalidHostInterfaceL3("host_interface_l3_invalid_mtu", nil)
	}

	hasIPv6Address := false
	for _, address := range desired {
		if address.Family == "inet6" {
			hasIPv6Address = true
			break
		}
	}
	ipv6Active := hasIPv6Address ||
		ipv6Mode == networkModels.HostInterfaceL3IPv6ModeEnabled ||
		(ipv6Mode != networkModels.HostInterfaceL3IPv6ModeDisabled && liveIPv6)
	if ipv6Active && *mtu < MinHostInterfaceL3IPv6MTU {
		return invalidHostInterfaceL3("host_interface_l3_mtu_below_ipv6_floor", nil)
	}

	return nil
}

func hostInterfaceL3HasLiveIPv6(interfaceObj *iface.Interface) bool {
	if interfaceObj == nil {
		return false
	}
	return len(interfaceObj.IPv6) > 0
}

func (s *Service) checkHostInterfaceL3AddressAvailability(
	desired []hostInterfaceL3DesiredAddress,
	excludeID uint,
) error {
	for _, address := range desired {
		query := s.DB.Model(&networkModels.HostInterfaceL3Address{}).
			Where("family = ? AND address = ?", address.Family, address.Address.String())
		if excludeID != 0 {
			query = query.Where("interface_l3_id <> ?", excludeID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return fmt.Errorf("check host address %s: %w", address.Address, err)
		}
		if count > 0 {
			return hostInterfaceL3Conflict(
				"host_interface_l3_duplicate_address",
				fmt.Errorf("%s is already configured on another interface", address.Address),
			)
		}

		inRange, err := s.hostInterfaceL3AddressInDHCPServerRange(address.Address)
		if err != nil {
			return err
		}
		if inRange {
			return hostInterfaceL3Conflict(
				"host_interface_l3_address_in_dhcp_pool",
				fmt.Errorf("%s falls inside a configured DHCP range", address.Address),
			)
		}
	}
	return nil
}

func (s *Service) hostInterfaceL3AddressInDHCPServerRange(address netip.Addr) (bool, error) {
	var ranges []networkModels.DHCPRange
	if err := s.DB.Model(&networkModels.DHCPRange{}).Find(&ranges).Error; err != nil {
		return false, fmt.Errorf("load DHCP ranges: %w", err)
	}

	for _, dhcpRange := range ranges {
		start, err := netip.ParseAddr(strings.TrimSpace(dhcpRange.StartIP))
		if err != nil {
			continue
		}
		end, err := netip.ParseAddr(strings.TrimSpace(dhcpRange.EndIP))
		if err != nil {
			continue
		}
		start = start.Unmap()
		end = end.Unmap()
		if start.Is4() != address.Is4() {
			continue
		}
		if address.Compare(start) >= 0 && address.Compare(end) <= 0 {
			return true, nil
		}
	}

	return false, nil
}

func (s *Service) validateHostInterfaceL3VLANBoundaries(
	name string,
	live *iface.Interface,
	mtu *uint,
) error {
	if live.VLANParent == "" {
		var childCount int64
		if err := s.DB.Model(&networkModels.HostInterfaceL3{}).
			Where("vlan_parent = ? AND interface <> ?", name, name).
			Count(&childCount).Error; err != nil {
			return fmt.Errorf("check VLAN children of %s: %w", name, err)
		}
		if childCount == 0 {
			liveInterfaces, err := hostInterfaceL3ListInterfaces()
			if err != nil {
				return fmt.Errorf("inspect VLAN children of %s: %w", name, err)
			}
			for _, candidate := range liveInterfaces {
				if candidate != nil && candidate.VLANParent == name {
					childCount++
					break
				}
			}
		}
		if childCount > 0 {
			return hostInterfaceL3Conflict("host_interface_l3_parent_has_vlan_children", nil)
		}
		return nil
	}

	parent, err := syncIfaceGet(live.VLANParent)
	if err != nil {
		if isInterfaceMissingError(err) {
			return hostInterfaceL3Conflict("host_interface_l3_vlan_parent_missing", err)
		}
		return fmt.Errorf("inspect VLAN parent %s: %w", live.VLANParent, err)
	}
	if parent == nil {
		return hostInterfaceL3Conflict("host_interface_l3_vlan_parent_missing", nil)
	}
	if code := hostInterfaceL3EligibilityCode(parent); code != "" {
		return hostInterfaceL3Conflict("host_interface_l3_vlan_parent_ineligible", nil)
	}

	var parentRows int64
	if err := s.DB.Model(&networkModels.HostInterfaceL3{}).
		Where("interface = ?", live.VLANParent).
		Count(&parentRows).Error; err != nil {
		return fmt.Errorf("check VLAN parent rows for %s: %w", live.VLANParent, err)
	}
	if parentRows > 0 {
		return hostInterfaceL3Conflict("host_interface_l3_parent_has_host_ip", nil)
	}

	if mtu != nil && uint(parent.MTU) > 0 && *mtu > uint(parent.MTU) {
		return hostInterfaceL3Conflict(
			"host_interface_l3_child_mtu_above_parent",
			fmt.Errorf("child MTU %d exceeds parent MTU %d", *mtu, parent.MTU),
		)
	}

	return nil
}

func (s *Service) planHostInterfaceL3Change(
	name string,
	current *networkModels.HostInterfaceL3,
	req networkServiceInterfaces.HostInterfaceL3UpdateRequest,
) (hostInterfaceL3PlannedChange, error) {
	var change hostInterfaceL3PlannedChange

	live, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return change, hostInterfaceL3Conflict("host_interface_l3_missing_interface", err)
		}
		return change, fmt.Errorf("inspect interface %s: %w", name, err)
	}
	if live == nil {
		return change, hostInterfaceL3Conflict("host_interface_l3_missing_interface", nil)
	}
	if code := hostInterfaceL3EligibilityCode(live); code != "" {
		return change, hostInterfaceL3Conflict(code, nil)
	}
	if current != nil && strings.TrimSpace(current.IdentityMAC) != "" &&
		strings.TrimSpace(live.Ether) != "" &&
		!strings.EqualFold(current.IdentityMAC, live.Ether) {
		return change, hostInterfaceL3Conflict(
			"host_interface_l3_identity_mismatch",
			fmt.Errorf("interface %s has MAC %s, expected %s", name, live.Ether, current.IdentityMAC),
		)
	}
	if err := s.hostInterfaceL3MembershipGuard(name); err != nil {
		return change, err
	}

	ipv6Mode := networkModels.HostInterfaceL3IPv6ModeInherit
	if req.IPv6Mode != nil {
		ipv6Mode = strings.ToLower(strings.TrimSpace(*req.IPv6Mode))
	}
	switch ipv6Mode {
	case networkModels.HostInterfaceL3IPv6ModeInherit,
		networkModels.HostInterfaceL3IPv6ModeEnabled,
		networkModels.HostInterfaceL3IPv6ModeDisabled:
	default:
		return change, invalidHostInterfaceL3("host_interface_l3_invalid_ipv6_mode", nil)
	}

	desired, err := normalizeHostInterfaceL3Addresses(req.Addresses)
	if err != nil {
		return change, err
	}

	excludeID := uint(0)
	if current != nil {
		excludeID = current.ID
	}
	if err := s.checkHostInterfaceL3AddressAvailability(desired, excludeID); err != nil {
		return change, err
	}

	if req.MTU != nil {
		if err := hostInterfaceL3ValidateMTUWithLiveIPv6(
			req.MTU,
			desired,
			ipv6Mode,
			hostInterfaceL3HasLiveIPv6(live),
		); err != nil {
			return change, err
		}
	} else if live.MTU > 0 {
		observedMTU := uint(live.MTU)
		if err := hostInterfaceL3ValidateMTU(&observedMTU, desired, ipv6Mode); err != nil {
			return change, err
		}
	}

	if ipv6Mode == networkModels.HostInterfaceL3IPv6ModeDisabled {
		for _, address := range desired {
			if address.Family == "inet6" {
				return change, invalidHostInterfaceL3("host_interface_l3_ipv6_address_with_disabled", nil)
			}
		}
	}

	if err := s.validateHostInterfaceL3VLANBoundaries(name, live, req.MTU); err != nil {
		return change, err
	}

	return buildHostInterfaceL3PlannedChange(current, req, desired, live, ipv6Mode), nil
}

func currentBaselineMTU(current *networkModels.HostInterfaceL3) *uint {
	if current == nil {
		return nil
	}
	if current.AppliedState.MTU == nil {
		return nil
	}
	return current.AdoptionBaseline.MTU
}

func buildHostInterfaceL3PlannedChange(
	current *networkModels.HostInterfaceL3,
	req networkServiceInterfaces.HostInterfaceL3UpdateRequest,
	desired []hostInterfaceL3DesiredAddress,
	live *iface.Interface,
	ipv6Mode string,
) hostInterfaceL3PlannedChange {
	change := hostInterfaceL3PlannedChange{
		Baseline: captureHostInterfaceL3Baseline(live),
	}
	if live != nil {
		change.IdentityMAC = live.Ether
	}

	applied := networkModels.HostInterfaceL3AppliedState{}
	if current != nil {
		applied = current.AppliedState
	}

	desiredIndexByKey := make(map[string]int, len(desired))
	for index, address := range desired {
		desiredIndexByKey[address.Family+"|"+address.Address.String()] = index
	}
	appliedHostKeys := make(map[string]struct{}, len(applied.Addresses))
	for _, existing := range applied.Addresses {
		appliedHostKeys[existing.Family+"|"+hostInterfaceL3AddressIP(existing.Address)] = struct{}{}
	}

	// Prefix ownership is decided from live state, ignoring addresses Sylve
	// already owns: an owned address being replaced never counts as a foreign
	// primary, and a policy-driven /32 alias is compared against the planned
	// prefix rather than the desired spec prefix.
	finalPrefixes := hostInterfaceL3FinalIPv4Prefixes(live, desired, appliedHostKeys)

	keptByKey := make(map[string]networkModels.HostInterfaceL3AppliedAddress, len(applied.Addresses))
	removalKeys := make(map[string]struct{})
	removals := make([]networkModels.HostInterfaceL3AppliedAddress, 0)
	for _, existing := range applied.Addresses {
		key := existing.Family + "|" + hostInterfaceL3AddressIP(existing.Address)
		index, desiredAddress := desiredIndexByKey[key]
		plannedPrefix := existing.PrefixLength
		if desiredAddress {
			plannedPrefix = finalPrefixes[index]
		}

		if desiredAddress && plannedPrefix == existing.PrefixLength {
			if hostInterfaceL3HasAddress(live, existing.Family, existing.Address) {
				keptByKey[key] = existing
			}
			continue
		}
		if hostInterfaceL3HasAddress(live, existing.Family, existing.Address) {
			removals = append(removals, existing)
			removalKeys[key] = struct{}{}
		}
	}

	intended := networkModels.HostInterfaceL3AppliedState{
		Addresses: make([]networkModels.HostInterfaceL3AppliedAddress, 0, len(desired)),
	}

	plan := hostInterfaceL3ApplyPlan{
		Interface: live.Name,
		Remove:    removals,
	}

	occupied := hostInterfaceL3OccupiedFamilies(live, removalKeys)
	addedInFamily := map[string]int{}
	for index, address := range desired {
		key := address.Family + "|" + address.Address.String()
		prefixLength := finalPrefixes[index]
		cidr := fmt.Sprintf("%s/%d", address.Address.String(), prefixLength)

		if kept, ok := keptByKey[key]; ok {
			intended.Addresses = append(intended.Addresses, kept)
			continue
		}
		if hostInterfaceL3HasAddress(live, address.Family, cidr) {
			// Present with the planned mask but not owned by Sylve: leave it.
			continue
		}

		alias := occupied[address.Family] || addedInFamily[address.Family] > 0
		plan.Addresses = append(plan.Addresses, hostInterfaceL3ApplyAddress{
			Family:       address.Family,
			Address:      cidr,
			PrefixLength: prefixLength,
			Alias:        alias,
		})
		intended.Addresses = append(intended.Addresses, networkModels.HostInterfaceL3AppliedAddress{
			Family:       address.Family,
			Address:      cidr,
			PrefixLength: prefixLength,
			Alias:        alias,
		})
		addedInFamily[address.Family]++
	}

	managedMTU := req.MTU
	if req.MTU != nil {
		plan.MTU = req.MTU
		intended.MTU = req.MTU
	} else if applied.MTU != nil {
		plan.MTU = currentBaselineMTU(current)
	}
	managedMetric := req.Metric
	if req.Metric != nil {
		plan.Metric = req.Metric
		intended.Metric = req.Metric
	} else if applied.Metric != nil {
		plan.Metric = nil
		if current != nil {
			plan.Metric = current.AdoptionBaseline.Metric
		}
	}

	switch ipv6Mode {
	case networkModels.HostInterfaceL3IPv6ModeDisabled:
		disabled := true
		plan.DisableIPv6 = &disabled
		intended.IPv6Disabled = &disabled
	case networkModels.HostInterfaceL3IPv6ModeEnabled:
		disabled := false
		plan.DisableIPv6 = &disabled
		intended.IPv6Disabled = &disabled
	default:
		if applied.IPv6Disabled != nil {
			restore := false
			if current != nil && current.AdoptionBaseline.IPv6Disabled != nil {
				restore = *current.AdoptionBaseline.IPv6Disabled
			}
			plan.DisableIPv6 = &restore
			if current != nil && current.AdoptionBaseline.ND6Flags != nil {
				plan.RestoreIPv6Flags = current.AdoptionBaseline.ND6Flags
			}
		}
	}

	if !interfaceIsUp(live) {
		up := true
		intended.Up = &up
	}

	change.Plan = plan
	change.Intended = intended
	change.Spec = networkModels.HostInterfaceL3Spec{
		IPv6Mode:  &ipv6Mode,
		MTU:       managedMTU,
		Metric:    managedMetric,
		Addresses: make([]networkModels.HostInterfaceL3AddressSpec, 0, len(desired)),
	}
	for _, address := range desired {
		change.Spec.Addresses = append(change.Spec.Addresses, networkModels.HostInterfaceL3AddressSpec{
			Family:       address.Family,
			Address:      address.Address.String(),
			PrefixLength: address.PrefixLength,
		})
	}

	return change
}

func hostInterfaceL3FinalIPv4Prefixes(
	live *iface.Interface,
	desired []hostInterfaceL3DesiredAddress,
	ownedHostKeys map[string]struct{},
) map[int]uint8 {
	final := make(map[int]uint8, len(desired))

	groups := make(map[string][]int)
	for index, address := range desired {
		if address.Family != "inet" {
			final[index] = address.PrefixLength
			continue
		}
		key := netip.PrefixFrom(address.Address, int(address.PrefixLength)).Masked().String()
		groups[key] = append(groups[key], index)
	}

	for key, indexes := range groups {
		hosts := make(map[string]struct{}, len(indexes))
		for _, index := range indexes {
			hosts[desired[index].Address.String()] = struct{}{}
		}

		foreign := false
		for _, address := range live.IPv4 {
			prefix, ok := interfaceIPv4Prefix(address)
			if !ok || prefix.Masked().String() != key {
				continue
			}
			host := prefix.Addr().Unmap().String()
			if _, isDesired := hosts[host]; isDesired {
				continue
			}
			if _, isOwned := ownedHostKeys["inet|"+host]; isOwned {
				// A Sylve-owned address being replaced is not a foreign primary.
				continue
			}
			foreign = true
			break
		}

		owner := indexes[0]
		for _, index := range indexes {
			if index < owner {
				owner = index
			}
		}

		for _, index := range indexes {
			if foreign || index != owner {
				final[index] = 32
				continue
			}
			final[index] = desired[index].PrefixLength
		}
	}

	return final
}

func hostInterfaceL3OccupiedFamilies(
	live *iface.Interface,
	removalKeys map[string]struct{},
) map[string]bool {
	occupied := map[string]bool{}
	for _, address := range live.IPv4 {
		prefix, ok := interfaceIPv4Prefix(address)
		if !ok {
			continue
		}
		if _, isRemoved := removalKeys["inet|"+prefix.Addr().Unmap().String()]; isRemoved {
			continue
		}
		occupied["inet"] = true
	}
	for _, address := range live.IPv6 {
		if address.IP.IsLinkLocalUnicast() {
			continue
		}
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok {
			continue
		}
		if _, isRemoved := removalKeys["inet6|"+ip.Unmap().String()]; isRemoved {
			continue
		}
		occupied["inet6"] = true
	}

	return occupied
}
