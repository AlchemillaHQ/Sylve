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
	"net/netip"
	"strconv"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

const (
	hostInterfaceL3DADPollInterval = 250 * time.Millisecond
	hostInterfaceL3DADMaxAttempts  = 12
)

var (
	errHostInterfaceL3DADSettling = errors.New("host interface l3 IPv6 address is still tentative")
	hostInterfaceL3DADWait        = time.Sleep
)

const (
	hostInterfaceL3ND6AcceptRTAdv   = 0x02
	hostInterfaceL3ND6IfDisabled    = 0x08
	hostInterfaceL3ND6AutoLinkLocal = 0x20
	hostInterfaceL3ND6NoRADR        = 0x40
)

type hostInterfaceL3ApplyAddress struct {
	Family  string
	Address string
	Alias   bool
}

type hostInterfaceL3ApplyPlan struct {
	Interface          string
	ExpectedMAC        string
	ExpectedVLANParent string
	ExpectedVLANTag    uint16
	Remove             []networkModels.HostInterfaceL3AppliedAddress
	Addresses          []hostInterfaceL3ApplyAddress
	MTU                *uint
	Metric             *uint
	DisableIPv6        *bool
	RestoreIPv6Flags   *uint32
}

func ensureHostInterfaceL3Identity(interfaceObj *iface.Interface, expectedMAC string) error {
	if interfaceObj == nil {
		return hostInterfaceL3Conflict("host_interface_l3_missing_interface", nil)
	}
	expectedMAC = strings.TrimSpace(expectedMAC)
	if expectedMAC == "" {
		return nil
	}
	liveMAC := strings.TrimSpace(interfaceObj.Ether)
	if liveMAC == "" || !strings.EqualFold(liveMAC, expectedMAC) {
		return hostInterfaceL3Conflict(
			"host_interface_l3_identity_mismatch",
			fmt.Errorf("interface %s has MAC %s, expected %s", interfaceObj.Name, liveMAC, expectedMAC),
		)
	}
	return nil
}

func ensureHostInterfaceL3VLANIdentity(interfaceObj *iface.Interface, expectedParent string, expectedTag uint16) error {
	if interfaceObj == nil {
		return hostInterfaceL3Conflict("host_interface_l3_missing_interface", nil)
	}
	if interfaceObj.VLANParent != expectedParent || interfaceObj.VLANTag != int(expectedTag) {
		return hostInterfaceL3Conflict(
			networkServiceInterfaces.HostInterfaceL3ConflictVLANIdentity,
			fmt.Errorf(
				"interface %s has VLAN parent/tag %s/%d, expected %s/%d",
				interfaceObj.Name,
				interfaceObj.VLANParent,
				interfaceObj.VLANTag,
				expectedParent,
				expectedTag,
			),
		)
	}
	return nil
}

func hostInterfaceL3IPv6Disabled(interfaceObj *iface.Interface) bool {
	if interfaceObj == nil {
		return false
	}
	for _, option := range interfaceObj.ND6.Desc {
		if strings.EqualFold(option, "IFDISABLED") {
			return true
		}
	}
	return false
}

func captureHostInterfaceL3Baseline(interfaceObj *iface.Interface) networkModels.HostInterfaceL3Baseline {
	baseline := networkModels.HostInterfaceL3Baseline{}
	if interfaceObj == nil {
		return baseline
	}

	baseline.Addresses = hostInterfaceL3LiveAddresses(interfaceObj)
	mtu := uint(interfaceObj.MTU)
	metric := uint(interfaceObj.Metric)
	nd6Flags := interfaceObj.ND6.Raw
	up := interfaceIsUp(interfaceObj)
	baseline.MTU = &mtu
	baseline.Metric = &metric
	baseline.ND6Flags = &nd6Flags
	baseline.Up = &up
	baseline.VLANParent = interfaceObj.VLANParent
	baseline.VLANTag = uint16(interfaceObj.VLANTag)
	return baseline
}

func applyHostInterfaceL3Plan(plan hostInterfaceL3ApplyPlan) (networkModels.HostInterfaceL3AppliedState, error) {
	applied := networkModels.HostInterfaceL3AppliedState{}

	interfaceObj, err := syncIfaceGet(plan.Interface)
	if err != nil {
		return applied, fmt.Errorf("inspect interface %s: %w", plan.Interface, err)
	}
	if err := ensureHostInterfaceL3Identity(interfaceObj, plan.ExpectedMAC); err != nil {
		return applied, err
	}
	if err := ensureHostInterfaceL3VLANIdentity(interfaceObj, plan.ExpectedVLANParent, plan.ExpectedVLANTag); err != nil {
		return applied, err
	}

	for _, address := range plan.Remove {
		if !hostInterfaceL3HasAddress(interfaceObj, address.Family, address.Address) {
			continue
		}
		ip := hostInterfaceL3AddressIP(address.Address)
		if ip == "" {
			continue
		}
		if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, address.Family, ip, "delete"); err != nil {
			return applied, fmt.Errorf("remove %s on %s: %w", address.Address, plan.Interface, err)
		}
	}
	if plan.MTU != nil && uint(interfaceObj.MTU) < *plan.MTU {
		if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "mtu", strconv.FormatUint(uint64(*plan.MTU), 10)); err != nil {
			return applied, fmt.Errorf("set MTU %d on %s: %w", *plan.MTU, plan.Interface, err)
		}
		interfaceObj.MTU = int(*plan.MTU)
		applied.MTU = plan.MTU
	}

	if plan.DisableIPv6 != nil && !*plan.DisableIPv6 {
		if err := applyHostInterfaceL3IPv6Mode(plan.Interface, false); err != nil {
			return applied, err
		}
		applied.IPv6Disabled = plan.DisableIPv6
	}

	for _, address := range plan.Addresses {
		if address.Family != "inet" && address.Family != "inet6" {
			return applied, fmt.Errorf("unsupported address family %q", address.Family)
		}

		observed := address.Address
		if address.Alias {
			observed = address.Address + " alias"
		}
		args := []string{plan.Interface, address.Family, address.Address}
		if address.Alias {
			args = append(args, "alias")
		}
		if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
			return applied, fmt.Errorf("add %s on %s: %w", observed, plan.Interface, err)
		}
		applied.Addresses = append(applied.Addresses, networkModels.HostInterfaceL3AppliedAddress{
			Family:  address.Family,
			Address: address.Address,
		})
	}

	if plan.MTU != nil {
		if uint(interfaceObj.MTU) != *plan.MTU {
			if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "mtu", strconv.FormatUint(uint64(*plan.MTU), 10)); err != nil {
				return applied, fmt.Errorf("set MTU %d on %s: %w", *plan.MTU, plan.Interface, err)
			}
			interfaceObj.MTU = int(*plan.MTU)
		}
		applied.MTU = plan.MTU
	}

	if plan.Metric != nil {
		if uint(interfaceObj.Metric) != *plan.Metric {
			if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "metric", strconv.FormatUint(uint64(*plan.Metric), 10)); err != nil {
				return applied, fmt.Errorf("set metric %d on %s: %w", *plan.Metric, plan.Interface, err)
			}
			interfaceObj.Metric = int(*plan.Metric)
		}
		applied.Metric = plan.Metric
	}

	if plan.DisableIPv6 != nil && *plan.DisableIPv6 {
		if err := applyHostInterfaceL3IPv6Mode(plan.Interface, *plan.DisableIPv6); err != nil {
			return applied, err
		}
		applied.IPv6Disabled = plan.DisableIPv6
	}
	if plan.RestoreIPv6Flags != nil {
		if err := restoreHostInterfaceL3IPv6Flags(plan.Interface, *plan.RestoreIPv6Flags); err != nil {
			return applied, err
		}
		applied.IPv6Disabled = nil
	}

	if !interfaceIsUp(interfaceObj) {
		if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "up"); err != nil {
			return applied, fmt.Errorf("bring %s up: %w", plan.Interface, err)
		}
		up := true
		applied.Up = &up
	}

	return applied, nil
}

func applyHostInterfaceL3IPv6Mode(name string, disabled bool) error {
	if disabled {
		if _, err := syncRunCommand(
			"/sbin/ifconfig", name,
			"inet6", "no_radr", "-accept_rtadv", "ifdisabled",
		); err != nil {
			return fmt.Errorf("disable IPv6 on %s: %w", name, err)
		}
		return nil
	}

	if _, err := syncRunCommand("/sbin/ifconfig", name, "inet6", "auto_linklocal", "-ifdisabled"); err != nil {
		return fmt.Errorf("enable IPv6 on %s: %w", name, err)
	}
	return nil
}

func restoreHostInterfaceL3IPv6Flags(name string, flags uint32) error {
	args := []string{name, "inet6"}
	if flags&hostInterfaceL3ND6NoRADR != 0 {
		args = append(args, "no_radr")
	} else {
		args = append(args, "-no_radr")
	}
	if flags&hostInterfaceL3ND6AcceptRTAdv != 0 {
		args = append(args, "accept_rtadv")
	} else {
		args = append(args, "-accept_rtadv")
	}
	if flags&hostInterfaceL3ND6IfDisabled != 0 {
		args = append(args, "ifdisabled")
	} else {
		args = append(args, "-ifdisabled")
	}
	if flags&hostInterfaceL3ND6AutoLinkLocal != 0 {
		args = append(args, "auto_linklocal")
	} else {
		args = append(args, "-auto_linklocal")
	}

	if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
		return fmt.Errorf("restore IPv6 flags on %s: %w", name, err)
	}
	return nil
}

func verifyHostInterfaceL3Plan(plan hostInterfaceL3ApplyPlan, applied networkModels.HostInterfaceL3AppliedState) error {
	interfaceObj, err := syncIfaceGet(plan.Interface)
	if err != nil {
		return fmt.Errorf("verify interface %s: %w", plan.Interface, err)
	}
	if err := ensureHostInterfaceL3Identity(interfaceObj, plan.ExpectedMAC); err != nil {
		return err
	}
	if err := ensureHostInterfaceL3VLANIdentity(interfaceObj, plan.ExpectedVLANParent, plan.ExpectedVLANTag); err != nil {
		return err
	}

	for _, address := range applied.Addresses {
		switch address.Family {
		case "inet":
			if !interfaceHasIPv4Prefix(interfaceObj, address.Address) {
				return fmt.Errorf(
					"verify interface %s: IPv4 address %s is not present after apply",
					plan.Interface, address.Address,
				)
			}
		case "inet6":
			if !interfaceHasIPv6Prefix(interfaceObj, address.Address) {
				return fmt.Errorf(
					"verify interface %s: IPv6 address %s is not present after apply",
					plan.Interface, address.Address,
				)
			}
			if err := verifyHostInterfaceL3DAD(plan.Interface, address, interfaceObj); err != nil {
				return err
			}
		}
	}

	if applied.MTU != nil && uint(interfaceObj.MTU) != *applied.MTU {
		return fmt.Errorf(
			"verify interface %s: MTU is %d after requesting %d",
			plan.Interface, interfaceObj.MTU, *applied.MTU,
		)
	}
	if applied.Metric != nil && uint(interfaceObj.Metric) != *applied.Metric {
		return fmt.Errorf(
			"verify interface %s: metric is %d after requesting %d",
			plan.Interface, interfaceObj.Metric, *applied.Metric,
		)
	}
	if applied.IPv6Disabled != nil && hostInterfaceL3IPv6Disabled(interfaceObj) != *applied.IPv6Disabled {
		return fmt.Errorf(
			"verify interface %s: IPv6 disabled state does not match the requested value",
			plan.Interface,
		)
	}
	if plan.RestoreIPv6Flags != nil && interfaceObj.ND6.Raw != *plan.RestoreIPv6Flags {
		return fmt.Errorf(
			"verify interface %s: ND6 options are %#x after restoring %#x",
			plan.Interface, interfaceObj.ND6.Raw, *plan.RestoreIPv6Flags,
		)
	}
	if applied.Up != nil && *applied.Up && !interfaceIsUp(interfaceObj) {
		return fmt.Errorf("verify interface %s: interface did not come up", plan.Interface)
	}

	return nil
}

func verifyHostInterfaceL3DAD(
	name string,
	address networkModels.HostInterfaceL3AppliedAddress,
	interfaceObj *iface.Interface,
) error {
	prefix, err := netip.ParsePrefix(address.Address)
	if err != nil {
		return fmt.Errorf("verify interface %s: invalid IPv6 address %s", name, address.Address)
	}

	for _, candidate := range interfaceObj.IPv6 {
		ip, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			continue
		}
		if netip.PrefixFrom(ip.Unmap(), candidate.PrefixLength) != prefix {
			continue
		}
		if candidate.Duplicated {
			return fmt.Errorf(
				"verify interface %s: IPv6 address %s is duplicated",
				name, address.Address,
			)
		}
		if candidate.Tentative {
			return fmt.Errorf(
				"verify interface %s: IPv6 address %s is still tentative: %w",
				name, address.Address, errHostInterfaceL3DADSettling,
			)
		}
	}

	return nil
}

func waitForHostInterfaceL3DAD(
	plan hostInterfaceL3ApplyPlan,
	applied networkModels.HostInterfaceL3AppliedState,
) error {
	hasIPv6 := false
	for _, address := range applied.Addresses {
		if address.Family == "inet6" {
			hasIPv6 = true
			break
		}
	}
	if !hasIPv6 {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < hostInterfaceL3DADMaxAttempts; attempt++ {
		interfaceObj, err := syncIfaceGet(plan.Interface)
		if err != nil {
			return fmt.Errorf("verify interface %s: %w", plan.Interface, err)
		}
		if err := ensureHostInterfaceL3Identity(interfaceObj, plan.ExpectedMAC); err != nil {
			return err
		}
		if err := ensureHostInterfaceL3VLANIdentity(interfaceObj, plan.ExpectedVLANParent, plan.ExpectedVLANTag); err != nil {
			return err
		}

		lastErr = nil
		for _, address := range applied.Addresses {
			if address.Family != "inet6" {
				continue
			}
			if err := verifyHostInterfaceL3DAD(plan.Interface, address, interfaceObj); err != nil {
				lastErr = err
				break
			}
		}
		if lastErr == nil {
			return nil
		}
		if !errors.Is(lastErr, errHostInterfaceL3DADSettling) {
			return lastErr
		}
		if attempt < hostInterfaceL3DADMaxAttempts-1 {
			hostInterfaceL3DADWait(hostInterfaceL3DADPollInterval)
		}
	}

	return lastErr
}

func revertHostInterfaceL3Runtime(
	name string,
	expectedMAC string,
	snapshot networkModels.HostInterfaceL3Baseline,
	target networkModels.HostInterfaceL3AppliedState,
	candidate networkModels.HostInterfaceL3AppliedState,
) error {
	var revertErrors []error

	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return hostInterfaceL3Conflict("host_interface_l3_missing_interface", err)
		}
		return fmt.Errorf("inspect interface %s for restore: %w", name, err)
	}
	if err := ensureHostInterfaceL3Identity(interfaceObj, expectedMAC); err != nil {
		return err
	}
	if err := ensureHostInterfaceL3VLANIdentity(interfaceObj, snapshot.VLANParent, snapshot.VLANTag); err != nil {
		return err
	}
	restoreMTU := (target.MTU != nil || candidate.MTU != nil) && snapshot.MTU != nil
	if restoreMTU && uint(interfaceObj.MTU) < *snapshot.MTU {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "mtu", strconv.FormatUint(uint64(*snapshot.MTU), 10)); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore MTU on %s: %w", name, err))
		} else {
			interfaceObj.MTU = int(*snapshot.MTU)
		}
	}
	restoreIPv6 := target.IPv6Disabled != nil || candidate.IPv6Disabled != nil
	if restoreIPv6 && snapshot.ND6Flags == nil {
		return fmt.Errorf("restore IPv6 on %s: baseline ND6 flags are missing", name)
	}
	restoreIPv6BeforeAddresses := false
	if restoreIPv6 {
		restoreIPv6BeforeAddresses = *snapshot.ND6Flags&hostInterfaceL3ND6IfDisabled == 0
	}
	if restoreIPv6BeforeAddresses {
		if interfaceObj.ND6.Raw != *snapshot.ND6Flags {
			if err := restoreHostInterfaceL3IPv6Flags(name, *snapshot.ND6Flags); err != nil {
				revertErrors = append(revertErrors, err)
			} else {
				interfaceObj.ND6.Raw = *snapshot.ND6Flags
			}
		}
	}
	restoreUp := target.Up != nil || candidate.Up != nil
	expectedUp := snapshot.Up
	if restoreUp && expectedUp != nil && *expectedUp && !interfaceIsUp(interfaceObj) {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "up"); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore link state on %s: %w", name, err))
		} else {
			interfaceObj.Flags.Desc = append(interfaceObj.Flags.Desc, "UP")
		}
	}

	expectedAddresses, managedHosts := hostInterfaceL3RestoreAddressState(snapshot, target, candidate)
	expectedAddressKeys := make(map[string]struct{}, len(expectedAddresses))
	for _, address := range expectedAddresses {
		expectedAddressKeys[address.Family+"|"+address.Address] = struct{}{}
	}

	liveAddresses := hostInterfaceL3LiveAddresses(interfaceObj)
	remainingByFamily := make(map[string]int)
	for _, address := range liveAddresses {
		hostKey := address.Family + "|" + hostInterfaceL3AddressIP(address.Address)
		_, managed := managedHosts[hostKey]
		_, expected := expectedAddressKeys[address.Family+"|"+address.Address]
		if !managed || expected {
			remainingByFamily[address.Family]++
			continue
		}
		ip := hostInterfaceL3AddressIP(address.Address)
		if ip == "" {
			continue
		}
		if _, err := syncRunCommand("/sbin/ifconfig", name, address.Family, ip, "delete"); err != nil &&
			!isInterfaceMissingError(err) {
			revertErrors = append(revertErrors, fmt.Errorf("delete %s on %s: %w", address.Address, name, err))
		}
	}

	for _, address := range expectedAddresses {
		if hostInterfaceL3HasAddress(interfaceObj, address.Family, address.Address) {
			continue
		}
		args := []string{name, address.Family, address.Address}
		if remainingByFamily[address.Family] > 0 {
			args = append(args, "alias")
		}
		if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore %s on %s: %w", address.Address, name, err))
			continue
		}
		remainingByFamily[address.Family]++
	}

	if restoreMTU && uint(interfaceObj.MTU) > *snapshot.MTU {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "mtu", strconv.FormatUint(uint64(*snapshot.MTU), 10)); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore MTU on %s: %w", name, err))
		}
	}

	if (target.Metric != nil || candidate.Metric != nil) && snapshot.Metric != nil && uint(interfaceObj.Metric) != *snapshot.Metric {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "metric", strconv.FormatUint(uint64(*snapshot.Metric), 10)); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore metric on %s: %w", name, err))
		}
	}

	if restoreIPv6 && !restoreIPv6BeforeAddresses {
		if interfaceObj.ND6.Raw != *snapshot.ND6Flags {
			if err := restoreHostInterfaceL3IPv6Flags(name, *snapshot.ND6Flags); err != nil {
				revertErrors = append(revertErrors, err)
			}
		}
	}

	if restoreUp && expectedUp != nil && !*expectedUp && interfaceIsUp(interfaceObj) {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "down"); err != nil {
			revertErrors = append(revertErrors, fmt.Errorf("restore link state on %s: %w", name, err))
		}
	}

	if err := errors.Join(revertErrors...); err != nil {
		return err
	}
	return verifyHostInterfaceL3Restore(name, expectedMAC, snapshot, target, candidate)
}

func hostInterfaceL3RestoreAddressState(
	snapshot networkModels.HostInterfaceL3Baseline,
	target networkModels.HostInterfaceL3AppliedState,
	candidate networkModels.HostInterfaceL3AppliedState,
) ([]networkModels.HostInterfaceL3AppliedAddress, map[string]struct{}) {
	managedHosts := make(map[string]struct{}, len(target.Addresses)+len(candidate.Addresses))
	for _, state := range []networkModels.HostInterfaceL3AppliedState{target, candidate} {
		for _, address := range state.Addresses {
			managedHosts[address.Family+"|"+hostInterfaceL3AddressIP(address.Address)] = struct{}{}
		}
	}

	expected := make([]networkModels.HostInterfaceL3AppliedAddress, 0, len(snapshot.Addresses))
	for _, address := range snapshot.Addresses {
		if _, managed := managedHosts[address.Family+"|"+hostInterfaceL3AddressIP(address.Address)]; managed {
			expected = append(expected, address)
		}
	}
	return expected, managedHosts
}

func verifyHostInterfaceL3Restore(
	name string,
	expectedMAC string,
	snapshot networkModels.HostInterfaceL3Baseline,
	target networkModels.HostInterfaceL3AppliedState,
	candidate networkModels.HostInterfaceL3AppliedState,
) error {
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return hostInterfaceL3Conflict("host_interface_l3_missing_interface", err)
		}
		return fmt.Errorf("verify restore on %s: %w", name, err)
	}
	if err := ensureHostInterfaceL3Identity(interfaceObj, expectedMAC); err != nil {
		return err
	}
	if err := ensureHostInterfaceL3VLANIdentity(interfaceObj, snapshot.VLANParent, snapshot.VLANTag); err != nil {
		return err
	}

	expectedAddresses, managedHosts := hostInterfaceL3RestoreAddressState(snapshot, target, candidate)
	expectedAddressKeys := make(map[string]struct{}, len(expectedAddresses))
	for _, address := range expectedAddresses {
		expectedAddressKeys[address.Family+"|"+address.Address] = struct{}{}
		if !hostInterfaceL3HasAddress(interfaceObj, address.Family, address.Address) {
			return fmt.Errorf("verify restore on %s: address %s is missing", name, address.Address)
		}
	}
	for _, address := range hostInterfaceL3LiveAddresses(interfaceObj) {
		if _, managed := managedHosts[address.Family+"|"+hostInterfaceL3AddressIP(address.Address)]; !managed {
			continue
		}
		if _, expected := expectedAddressKeys[address.Family+"|"+address.Address]; !expected {
			return fmt.Errorf("verify restore on %s: address %s is still present", name, address.Address)
		}
	}

	if (target.MTU != nil || candidate.MTU != nil) && snapshot.MTU != nil && uint(interfaceObj.MTU) != *snapshot.MTU {
		return fmt.Errorf("verify restore on %s: MTU is %d, expected %d", name, interfaceObj.MTU, *snapshot.MTU)
	}

	if (target.Metric != nil || candidate.Metric != nil) && snapshot.Metric != nil && uint(interfaceObj.Metric) != *snapshot.Metric {
		return fmt.Errorf("verify restore on %s: metric is %d, expected %d", name, interfaceObj.Metric, *snapshot.Metric)
	}

	if target.IPv6Disabled != nil || candidate.IPv6Disabled != nil {
		if snapshot.ND6Flags == nil {
			return fmt.Errorf("verify restore on %s: baseline ND6 flags are missing", name)
		}
		if interfaceObj.ND6.Raw != *snapshot.ND6Flags {
			return fmt.Errorf("verify restore on %s: ND6 options are %#x, expected %#x", name, interfaceObj.ND6.Raw, *snapshot.ND6Flags)
		}
	}

	if target.Up != nil || candidate.Up != nil {
		expectedUp := snapshot.Up
		if expectedUp != nil && interfaceIsUp(interfaceObj) != *expectedUp {
			return fmt.Errorf("verify restore on %s: link state is incorrect", name)
		}
	}

	return nil
}

func hostInterfaceL3AddressIP(cidr string) string {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return ""
	}
	return prefix.Addr().Unmap().String()
}

func hostInterfaceL3AddressPrefixLength(cidr string) uint8 {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return 0
	}
	return uint8(prefix.Bits())
}

func hostInterfaceL3LiveAddresses(interfaceObj *iface.Interface) []networkModels.HostInterfaceL3AppliedAddress {
	if interfaceObj == nil {
		return nil
	}

	addresses := make([]networkModels.HostInterfaceL3AppliedAddress, 0, len(interfaceObj.IPv4)+len(interfaceObj.IPv6))
	for _, address := range interfaceObj.IPv4 {
		prefix, ok := interfaceIPv4Prefix(address)
		if !ok {
			continue
		}
		addresses = append(addresses, networkModels.HostInterfaceL3AppliedAddress{
			Family:  "inet",
			Address: prefix.String(),
		})
	}
	for _, address := range interfaceObj.IPv6 {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsLinkLocalUnicast() {
			continue
		}
		prefix := netip.PrefixFrom(ip, address.PrefixLength)
		addresses = append(addresses, networkModels.HostInterfaceL3AppliedAddress{
			Family:  "inet6",
			Address: prefix.String(),
		})
	}
	return addresses
}

func hostInterfaceL3LiveAddressByHost(
	interfaceObj *iface.Interface,
	family string,
	address string,
) (networkModels.HostInterfaceL3AppliedAddress, bool) {
	for _, candidate := range hostInterfaceL3LiveAddresses(interfaceObj) {
		if candidate.Family == family && hostInterfaceL3AddressIP(candidate.Address) == address {
			return candidate, true
		}
	}
	return networkModels.HostInterfaceL3AppliedAddress{}, false
}

func hostInterfaceL3HasAddress(interfaceObj *iface.Interface, family string, cidr string) bool {
	if interfaceObj == nil {
		return false
	}
	switch family {
	case "inet":
		return interfaceHasIPv4Prefix(interfaceObj, cidr)
	case "inet6":
		return interfaceHasIPv6Prefix(interfaceObj, cidr)
	default:
		return false
	}
}

func hostInterfaceL3HasHostAddress(interfaceObj *iface.Interface, family string, address string) bool {
	if interfaceObj == nil {
		return false
	}
	switch family {
	case "inet":
		for _, candidate := range interfaceObj.IPv4 {
			if candidate.IP.String() == address {
				return true
			}
		}
	case "inet6":
		for _, candidate := range interfaceObj.IPv6 {
			if candidate.IP.String() == address {
				return true
			}
		}
	}
	return false
}

func verifyHostInterfaceL3Removals(
	name string,
	applied networkModels.HostInterfaceL3AppliedState,
	baseline networkModels.HostInterfaceL3Baseline,
	readded map[string]struct{},
) error {
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return nil
		}
		return fmt.Errorf("verify removals on %s: %w", name, err)
	}
	if interfaceObj == nil {
		return nil
	}

	for _, address := range applied.Addresses {
		ip := hostInterfaceL3AddressIP(address.Address)
		if _, reAdded := readded[address.Family+"|"+ip]; reAdded {
			continue
		}
		if hostInterfaceL3HasHostAddress(interfaceObj, address.Family, ip) {
			return fmt.Errorf(
				"verify removals on %s: address %s is still present",
				name, address.Address,
			)
		}
	}

	if applied.MTU != nil && baseline.MTU != nil && uint(interfaceObj.MTU) != *baseline.MTU {
		return fmt.Errorf(
			"verify removals on %s: MTU is %d, expected baseline %d",
			name, interfaceObj.MTU, *baseline.MTU,
		)
	}
	if applied.Metric != nil && baseline.Metric != nil && uint(interfaceObj.Metric) != *baseline.Metric {
		return fmt.Errorf(
			"verify removals on %s: metric is %d, expected baseline %d",
			name, interfaceObj.Metric, *baseline.Metric,
		)
	}
	if applied.IPv6Disabled != nil && baseline.ND6Flags != nil && interfaceObj.ND6.Raw != *baseline.ND6Flags {
		return fmt.Errorf(
			"verify removals on %s: ND6 options are %#x, expected baseline %#x",
			name, interfaceObj.ND6.Raw, *baseline.ND6Flags,
		)
	}

	return nil
}

func hostInterfaceL3ReAddedHostKeys(addresses []hostInterfaceL3ApplyAddress) map[string]struct{} {
	if len(addresses) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		ip := hostInterfaceL3AddressIP(address.Address)
		if ip == "" {
			continue
		}
		keys[address.Family+"|"+ip] = struct{}{}
	}
	return keys
}
