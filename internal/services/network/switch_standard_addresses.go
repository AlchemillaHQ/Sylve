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
	"slices"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func normalizeStandardSwitchCIDRs(values []string, family int) ([]string, error) {
	assignable := utils.IsAssignableIPv4CIDR
	if family == 6 {
		assignable = utils.IsAssignableIPv6CIDR
	}
	invalidCode := fmt.Sprintf("invalid_standard_switch_network%d_manual", family)
	addresses := make([]string, 0, len(values))
	seen := make(map[netip.Addr]int, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if len(value) > MaxStandardSwitchManualAddressBytes {
			return nil, invalidStandardSwitch("standard_switch_manual_address_too_long", nil)
		}
		if !assignable(value) {
			return nil, invalidStandardSwitch(invalidCode, nil)
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, invalidStandardSwitch(invalidCode, err)
		}
		if bits, exists := seen[prefix.Addr()]; exists {
			if bits != prefix.Bits() {
				return nil, invalidStandardSwitch("standard_switch_address_prefix_conflict", nil)
			}
			continue
		}
		seen[prefix.Addr()] = prefix.Bits()
		if len(addresses) == MaxStandardSwitchAddresses {
			return nil, invalidStandardSwitch("standard_switch_too_many_addresses", nil)
		}
		addresses = append(addresses, prefix.String())
	}
	return addresses, nil
}

func legacyStandardSwitchAddressSources(objectID uint, cidr string) []networkModels.StandardSwitchAddressSource {
	if objectID != 0 {
		return []networkModels.StandardSwitchAddressSource{{ObjectID: objectID}}
	}
	if cidr != "" {
		return []networkModels.StandardSwitchAddressSource{{CIDR: cidr}}
	}
	return []networkModels.StandardSwitchAddressSource{}
}

func normalizeStandardSwitchAddressSources(sources []networkModels.StandardSwitchAddressSource, family int) ([]networkModels.StandardSwitchAddressSource, error) {
	if len(sources) > MaxStandardSwitchAddresses {
		return nil, invalidStandardSwitch("standard_switch_too_many_addresses", nil)
	}
	normalized := make([]networkModels.StandardSwitchAddressSource, 0, len(sources))
	for _, source := range sources {
		source.CIDR = strings.TrimSpace(source.CIDR)
		if (source.ObjectID == 0) == (source.CIDR == "") {
			return nil, invalidStandardSwitch("invalid_standard_switch_address_source", nil)
		}
		if source.ObjectID == 0 {
			values, err := normalizeStandardSwitchCIDRs([]string{source.CIDR}, family)
			if err != nil {
				return nil, err
			}
			source.CIDR = values[0]
		}
		if !slices.Contains(normalized, source) {
			normalized = append(normalized, source)
		}
	}
	return normalized, nil
}

func (s *Service) validateStandardSwitchAddressSources(sources []networkModels.StandardSwitchAddressSource, family int) ([]string, error) {
	values := make([]string, 0)
	for _, source := range sources {
		if source.ObjectID == 0 {
			values = append(values, source.CIDR)
			continue
		}
		resolved, err := s.validateStandardSwitchObject(source.ObjectID, "Network", family, fmt.Sprintf("network%d", family))
		if err != nil {
			return nil, err
		}
		values = append(values, resolved...)
	}
	return normalizeStandardSwitchCIDRs(values, family)
}

func standardSwitchAddresses(switchID uint, sources4, sources6 []networkModels.StandardSwitchAddressSource) []networkModels.StandardSwitchAddress {
	addresses := make([]networkModels.StandardSwitchAddress, 0, len(sources4)+len(sources6))
	for _, family := range []int{4, 6} {
		sources := sources4
		if family == 6 {
			sources = sources6
		}
		for ordering, source := range sources {
			address := networkModels.StandardSwitchAddress{SwitchID: switchID, Family: family, Ordering: ordering, CIDR: source.CIDR}
			if source.ObjectID != 0 {
				objectID := source.ObjectID
				address.ObjectID = &objectID
			}
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// Ordinary bridges remove only former managed addresses. Host VLAN interfaces
// are entirely managed by Sylve and also remove unconfigured addresses.
func reconcileStandardSwitchStaticAddresses(
	name string, family int, interfaceObj *iface.Interface,
	previous, desired []string, clean bool, flags ...string,
) error {
	type observedAddress struct {
		prefix    netip.Prefix
		automatic bool
	}
	observed := make([]observedAddress, 0)
	af := "inet"
	assignable := utils.IsAssignableIPv4CIDR
	if family == 4 {
		for _, address := range interfaceObj.IPv4 {
			if prefix, ok := interfaceIPv4Prefix(address); ok {
				observed = append(observed, observedAddress{prefix: prefix})
			}
		}
	} else {
		af = "inet6"
		assignable = utils.IsAssignableIPv6CIDR
		for _, address := range interfaceObj.IPv6 {
			if address.AutoConf && !clean {
				continue
			}
			if ip, ok := netip.AddrFromSlice(address.IP); ok {
				observed = append(observed, observedAddress{
					prefix: netip.PrefixFrom(ip.Unmap(), address.PrefixLength), automatic: address.AutoConf,
				})
			}
		}
	}
	current := make(map[string]bool, len(observed))
	assigned := 0
	for _, address := range observed {
		prefix := address.prefix
		value := prefix.String()
		keep := slices.Contains(desired, value) && !address.automatic
		linkLocal := family == 6 && prefix.Addr().IsLinkLocalUnicast()
		if !keep && ((clean && !linkLocal) || slices.Contains(previous, value) || standardSwitchHasAddress(desired, prefix.Addr())) {
			deleteValue := value
			if clean {
				deleteValue = prefix.Addr().String()
			}
			if linkLocal {
				deleteValue = prefix.Addr().WithZone(name).String()
			}
			if _, err := syncRunCommand("/sbin/ifconfig", name, af, deleteValue, "delete"); err != nil {
				return fmt.Errorf("delete %s address %s from %s: %w", af, value, name, err)
			}
			continue
		}
		current[value] = true
		if !linkLocal {
			assigned++
		}
	}
	for _, value := range desired {
		if current[value] || !assignable(value) {
			continue
		}
		args := []string{name, af, value}
		if assigned > 0 {
			args = append(args, "alias")
		}
		args = append(args, flags...)
		if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
			return fmt.Errorf("set %s address %s on %s: %w", af, value, name, err)
		}
		current[value] = true
		prefix, _ := netip.ParsePrefix(value)
		if family == 4 || !prefix.Addr().IsLinkLocalUnicast() {
			assigned++
		}
	}
	return nil
}

func standardSwitchHasAddress(values []string, address netip.Addr) bool {
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Addr() == address {
			return true
		}
	}
	return false
}
