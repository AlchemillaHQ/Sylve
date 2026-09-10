// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/network/iface"
)

type standardSwitchPortRuntimeSnapshot struct {
	Name                string
	MTU                 int
	WasUp               bool
	EnabledCapabilities uint32
	AutoLinkLocal       bool
	AcceptRTAdv         bool
	IPv4                []iface.IPv4
	IPv6                []iface.IPv6
	DHCPRunning         bool
	DHCPManaged         bool
	DHCPDefaultRoute    bool
}

func cloneIP(value net.IP) net.IP {
	return slices.Clone(value)
}

func snapshotStandardSwitchPortRuntime(name string) (standardSwitchPortRuntimeSnapshot, error) {
	state := standardSwitchPortRuntimeSnapshot{Name: name, DHCPDefaultRoute: true}
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		return state, fmt.Errorf("inspect port %s before claim: %w", name, err)
	}
	if interfaceObj == nil {
		return state, fmt.Errorf("inspect port %s before claim: interface not found", name)
	}

	state.MTU = interfaceObj.MTU
	state.WasUp = interfaceIsUp(interfaceObj)
	state.EnabledCapabilities = interfaceObj.Capabilities.Enabled.Raw
	state.AutoLinkLocal = interfaceFlagNamed(interfaceObj.ND6.Flags, "AUTO_LINKLOCAL")
	state.AcceptRTAdv = interfaceFlagNamed(interfaceObj.ND6.Flags, "ACCEPT_RTADV")
	state.IPv4 = make([]iface.IPv4, len(interfaceObj.IPv4))
	for index, address := range interfaceObj.IPv4 {
		state.IPv4[index] = address
		state.IPv4[index].IP = cloneIP(address.IP)
		state.IPv4[index].Broadcast = cloneIP(address.Broadcast)
	}
	state.IPv6 = make([]iface.IPv6, len(interfaceObj.IPv6))
	for index, address := range interfaceObj.IPv6 {
		state.IPv6[index] = address
		state.IPv6[index].IP = cloneIP(address.IP)
	}

	state.DHCPRunning, state.DHCPManaged, err = dhclientRunning(name)
	if err != nil {
		return state, fmt.Errorf("inspect DHCP on port %s before claim: %w", name, err)
	}
	if state.DHCPRunning && state.DHCPManaged {
		noDefault, policyErr := dhclientRoutePolicyMatches(name, false)
		if policyErr != nil {
			return state, fmt.Errorf("inspect DHCP route policy on port %s before claim: %w", name, policyErr)
		}
		state.DHCPDefaultRoute = !noDefault
	}
	return state, nil
}

func captureFilteredStandardSwitchPortClaims(names []string) ([]standardSwitchPortRuntimeSnapshot, error) {
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			unique[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for name := range unique {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	snapshots := make([]standardSwitchPortRuntimeSnapshot, 0, len(ordered))
	for _, name := range ordered {
		snapshot, err := snapshotStandardSwitchPortRuntime(name)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func restoreOffloadArgs(previous, current uint32) []string {
	missing := previous &^ current
	args := make([]string, 0, 8)
	if missing&ifcapTXCSUM != 0 {
		args = append(args, "txcsum")
	}
	if missing&ifcapTXCSUMIPv6 != 0 {
		args = append(args, "txcsum6")
	}
	if missing&ifcapTSO4 != 0 {
		args = append(args, "tso4")
	}
	if missing&ifcapTSO6 != 0 {
		args = append(args, "tso6")
	}
	if missing&ifcapLRO != 0 {
		args = append(args, "lro")
	}
	if missing&(ifcapTOE4|ifcapTOE6) != 0 {
		args = append(args, "toe")
	}
	if missing&ifcapMEXTPG != 0 {
		args = append(args, "mextpg")
	}
	return args
}

func interfaceHasIPv4(interfaceObj *iface.Interface, address net.IP) bool {
	for _, candidate := range interfaceObj.IPv4 {
		if candidate.IP.Equal(address) {
			return true
		}
	}
	return false
}

func interfaceHasIPv6(interfaceObj *iface.Interface, address net.IP) bool {
	for _, candidate := range interfaceObj.IPv6 {
		if candidate.IP.Equal(address) {
			return true
		}
	}
	return false
}

func restoreIPv4Address(name string, address iface.IPv4) error {
	args := []string{name, "inet", address.IP.String()}
	if mask := net.ParseIP(address.Netmask).To4(); mask != nil {
		prefix, _ := net.IPMask(mask).Size()
		args[2] += "/" + strconv.Itoa(prefix)
	} else if strings.TrimSpace(address.Netmask) != "" {
		args = append(args, "netmask", address.Netmask)
	}
	args = append(args, "alias")
	_, err := syncRunCommand("/sbin/ifconfig", args...)
	return err
}

func restoreIPv6Address(name string, address iface.IPv6) error {
	ip := address.IP.String()
	if address.IP.IsLinkLocalUnicast() {
		ip += "%" + name
	}
	args := []string{name, "inet6", ip + "/" + strconv.Itoa(address.PrefixLength), "alias"}
	_, err := syncRunCommand("/sbin/ifconfig", args...)
	return err
}

func restoreStandardSwitchPortRuntime(snapshot standardSwitchPortRuntimeSnapshot) error {
	current, err := syncIfaceGet(snapshot.Name)
	if err != nil {
		return fmt.Errorf("inspect port %s for restoration: %w", snapshot.Name, err)
	}
	if current == nil {
		return fmt.Errorf("inspect port %s for restoration: interface not found", snapshot.Name)
	}

	var restoreErrs []error
	if snapshot.MTU > 0 && current.MTU != snapshot.MTU {
		if _, err := syncRunCommand("/sbin/ifconfig", snapshot.Name, "mtu", strconv.Itoa(snapshot.MTU)); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore MTU on %s: %w", snapshot.Name, err))
		}
	}
	if args := restoreOffloadArgs(snapshot.EnabledCapabilities, current.Capabilities.Enabled.Raw); len(args) != 0 {
		if _, err := syncRunCommand("/sbin/ifconfig", append([]string{snapshot.Name}, args...)...); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore capabilities on %s: %w", snapshot.Name, err))
		}
	}
	if !interfaceIsUp(current) {
		if _, err := syncRunCommand("/sbin/ifconfig", snapshot.Name, "up"); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("bring %s up for restoration: %w", snapshot.Name, err))
		}
	}

	autoLinkLocal := "-auto_linklocal"
	if snapshot.AutoLinkLocal {
		autoLinkLocal = "auto_linklocal"
	}
	acceptRTAdv := "-accept_rtadv"
	if snapshot.AcceptRTAdv {
		acceptRTAdv = "accept_rtadv"
	}
	if _, err := syncRunCommand("/sbin/ifconfig", snapshot.Name, "inet6", autoLinkLocal, acceptRTAdv); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore IPv6 flags on %s: %w", snapshot.Name, err))
	}

	fresh, inspectErr := syncIfaceGet(snapshot.Name)
	if inspectErr != nil || fresh == nil {
		if inspectErr == nil {
			inspectErr = fmt.Errorf("interface not found")
		}
		restoreErrs = append(restoreErrs, fmt.Errorf("inspect addresses on %s for restoration: %w", snapshot.Name, inspectErr))
		fresh = current
	}
	for _, address := range snapshot.IPv4 {
		if interfaceHasIPv4(fresh, address.IP) {
			continue
		}
		if err := restoreIPv4Address(snapshot.Name, address); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore IPv4 address %s on %s: %w", address.IP, snapshot.Name, err))
		}
	}
	for _, address := range snapshot.IPv6 {
		if address.AutoConf || (snapshot.AutoLinkLocal && address.IP.IsLinkLocalUnicast()) ||
			interfaceHasIPv6(fresh, address.IP) {
			continue
		}
		if err := restoreIPv6Address(snapshot.Name, address); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore IPv6 address %s on %s: %w", address.IP, snapshot.Name, err))
		}
	}

	if snapshot.DHCPRunning {
		if snapshot.DHCPManaged {
			if err := runDhclient(snapshot.Name, 10, snapshot.DHCPDefaultRoute); err != nil {
				restoreErrs = append(restoreErrs, fmt.Errorf("restore DHCP on %s: %w", snapshot.Name, err))
			}
		} else if _, err := syncRunCommand("/sbin/dhclient", "-b", snapshot.Name); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore external DHCP on %s: %w", snapshot.Name, err))
		}
	}
	if snapshot.AcceptRTAdv && snapshot.WasUp {
		if err := syncSolicitRouterAdvertisement(snapshot.Name); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore SLAAC on %s: %w", snapshot.Name, err))
		}
	}
	if !snapshot.WasUp {
		if _, err := syncRunCommand("/sbin/ifconfig", snapshot.Name, "down"); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore down state on %s: %w", snapshot.Name, err))
		}
	}
	return errors.Join(restoreErrs...)
}

func restoreFilteredStandardSwitchPortClaims(
	bridge string,
	snapshots []standardSwitchPortRuntimeSnapshot,
) error {
	var restoreErrs []error
	for _, snapshot := range snapshots {
		detached, err := filteredBridgeMemberDetached(bridge, snapshot.Name)
		if err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("verify %s before restoring claimed state: %w", snapshot.Name, err))
			continue
		}
		if !detached {
			restoreErrs = append(restoreErrs, fmt.Errorf("refuse to restore Layer-3 state while %s remains attached to %s", snapshot.Name, bridge))
			continue
		}
		if err := restoreStandardSwitchPortRuntime(snapshot); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
	}
	return errors.Join(restoreErrs...)
}
