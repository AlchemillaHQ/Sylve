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

// Raw ND6 option bits from pkg/network/iface's parser.
const (
	hostInterfaceL3ND6AcceptRTAdv   = 0x02
	hostInterfaceL3ND6IfDisabled    = 0x08
	hostInterfaceL3ND6AutoLinkLocal = 0x20
	hostInterfaceL3ND6NoRADR        = 0x40
)

type hostInterfaceL3ApplyAddress struct {
	Family       string
	Address      string
	PrefixLength uint8
	Alias        bool
}

type hostInterfaceL3ApplyPlan struct {
	Interface   string
	Remove      []networkModels.HostInterfaceL3AppliedAddress
	Addresses   []hostInterfaceL3ApplyAddress
	MTU         *uint
	Metric      *uint
	DisableIPv6 *bool
	// RestoreIPv6Flags is set when management of IPv6 stops and the exact ND6
	// option set found before Sylve's first apply must return.
	RestoreIPv6Flags *uint32
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

	mtu := uint(interfaceObj.MTU)
	metric := uint(interfaceObj.Metric)
	ipv6Disabled := hostInterfaceL3IPv6Disabled(interfaceObj)
	nd6Flags := interfaceObj.ND6.Raw
	up := interfaceIsUp(interfaceObj)
	baseline.MTU = &mtu
	baseline.Metric = &metric
	baseline.IPv6Disabled = &ipv6Disabled
	baseline.ND6Flags = &nd6Flags
	baseline.Up = &up
	return baseline
}

func applyHostInterfaceL3Plan(plan hostInterfaceL3ApplyPlan) (networkModels.HostInterfaceL3AppliedState, error) {
	applied := networkModels.HostInterfaceL3AppliedState{}

	interfaceObj, err := syncIfaceGet(plan.Interface)
	if err != nil {
		return applied, fmt.Errorf("inspect interface %s: %w", plan.Interface, err)
	}
	if interfaceObj == nil {
		return applied, fmt.Errorf("inspect interface %s: interface missing", plan.Interface)
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
			Family:       address.Family,
			Address:      address.Address,
			PrefixLength: address.PrefixLength,
			Alias:        address.Alias,
		})
	}

	if plan.MTU != nil {
		if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "mtu", strconv.FormatUint(uint64(*plan.MTU), 10)); err != nil {
			return applied, fmt.Errorf("set MTU %d on %s: %w", *plan.MTU, plan.Interface, err)
		}
		applied.MTU = plan.MTU
	}

	if plan.Metric != nil {
		if _, err := syncRunCommand("/sbin/ifconfig", plan.Interface, "metric", strconv.FormatUint(uint64(*plan.Metric), 10)); err != nil {
			return applied, fmt.Errorf("set metric %d on %s: %w", *plan.Metric, plan.Interface, err)
		}
		applied.Metric = plan.Metric
	}

	if plan.DisableIPv6 != nil {
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

// restoreHostInterfaceL3IPv6Flags reinstates the exact ND6 options Sylve found
// (IFDISABLED, NO_RADR, ACCEPT_RTADV and AUTO_LINKLOCAL), so rollback does not
// leave router advertisements suppressed.
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
	if interfaceObj == nil {
		return fmt.Errorf("verify interface %s: interface missing", plan.Interface)
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
		if interfaceObj == nil {
			return fmt.Errorf("verify interface %s: interface missing", plan.Interface)
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
	snapshot networkModels.HostInterfaceL3Baseline,
	target networkModels.HostInterfaceL3AppliedState,
	candidate networkModels.HostInterfaceL3AppliedState,
) error {
	var revertErrors []error

	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if !isInterfaceMissingError(err) {
			revertErrors = append(revertErrors, fmt.Errorf("inspect interface %s: %w", name, err))
		}
		interfaceObj = nil
	}

	if interfaceObj != nil {
		targetKeys := make(map[string]struct{}, len(target.Addresses))
		for _, address := range target.Addresses {
			targetKeys[address.Family+"|"+address.Address] = struct{}{}
		}

		for _, address := range candidate.Addresses {
			if _, keep := targetKeys[address.Family+"|"+address.Address]; keep {
				continue
			}
			ip := hostInterfaceL3AddressIP(address.Address)
			if ip == "" || !hostInterfaceL3HasAddress(interfaceObj, address.Family, address.Address) {
				continue
			}
			if _, err := syncRunCommand("/sbin/ifconfig", name, address.Family, ip, "delete"); err != nil &&
				!isInterfaceMissingError(err) {
				revertErrors = append(revertErrors, fmt.Errorf("delete %s on %s: %w", address.Address, name, err))
			}
		}

		candidateKeys := make(map[string]struct{}, len(candidate.Addresses))
		for _, address := range candidate.Addresses {
			candidateKeys[address.Family+"|"+address.Address] = struct{}{}
		}
		for _, address := range target.Addresses {
			if _, present := candidateKeys[address.Family+"|"+address.Address]; present {
				continue
			}
			if hostInterfaceL3HasAddress(interfaceObj, address.Family, address.Address) {
				continue
			}
			args := []string{name, address.Family, address.Address}
			if address.Alias {
				args = append(args, "alias")
			}
			if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
				revertErrors = append(revertErrors, fmt.Errorf("restore %s on %s: %w", address.Address, name, err))
			}
		}

		restoreMTU := target.MTU
		if restoreMTU == nil && candidate.MTU != nil {
			restoreMTU = snapshot.MTU
		}
		if restoreMTU != nil {
			if _, err := syncRunCommand("/sbin/ifconfig", name, "mtu", strconv.FormatUint(uint64(*restoreMTU), 10)); err != nil {
				revertErrors = append(revertErrors, fmt.Errorf("restore MTU on %s: %w", name, err))
			}
		}

		restoreMetric := target.Metric
		if restoreMetric == nil && candidate.Metric != nil {
			restoreMetric = snapshot.Metric
		}
		if restoreMetric != nil {
			if _, err := syncRunCommand("/sbin/ifconfig", name, "metric", strconv.FormatUint(uint64(*restoreMetric), 10)); err != nil {
				revertErrors = append(revertErrors, fmt.Errorf("restore metric on %s: %w", name, err))
			}
		}

		switch {
		case target.IPv6Disabled != nil:
			if err := applyHostInterfaceL3IPv6Mode(name, *target.IPv6Disabled); err != nil {
				revertErrors = append(revertErrors, err)
			}
		case candidate.IPv6Disabled != nil:
			// Sylve changed the flags; reinstate exactly what it found.
			if snapshot.ND6Flags != nil {
				if err := restoreHostInterfaceL3IPv6Flags(name, *snapshot.ND6Flags); err != nil {
					revertErrors = append(revertErrors, err)
				}
			} else if snapshot.IPv6Disabled != nil {
				if err := applyHostInterfaceL3IPv6Mode(name, *snapshot.IPv6Disabled); err != nil {
					revertErrors = append(revertErrors, err)
				}
			}
		}

		if target.Up != nil && *target.Up && !interfaceIsUp(interfaceObj) {
			if _, err := syncRunCommand("/sbin/ifconfig", name, "up"); err != nil {
				revertErrors = append(revertErrors, fmt.Errorf("restore link state on %s: %w", name, err))
			}
		} else if candidate.Up != nil && *candidate.Up && (target.Up == nil) &&
			snapshot.Up != nil && !*snapshot.Up {
			if _, err := syncRunCommand("/sbin/ifconfig", name, "down"); err != nil {
				revertErrors = append(revertErrors, fmt.Errorf("restore link state on %s: %w", name, err))
			}
		}
	}

	return errors.Join(revertErrors...)
}

func hostInterfaceL3AddressIP(cidr string) string {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return ""
	}
	return prefix.Addr().Unmap().String()
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

// hostInterfaceL3HasHostAddress reports whether any live address in the family
// carries the given host IP, regardless of prefix. Removal verification uses
// it so a stale alias with an unexpected mask still counts as not removed.
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

// verifyHostInterfaceL3Removals checks that every address Sylve owned is gone
// and that managed baseline values were restored.
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
			// The same host IP is installed again with a new mask
			// (prefix-owner change); only the old mask had to disappear.
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

// hostInterfaceL3ReAddedHostKeys lists the host IPs a plan installs again, so
// removal verification does not flag a prefix-owner change as a failed delete.
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

func hostInterfaceL3FamilyHasAddress(interfaceObj *iface.Interface, family string) bool {
	if interfaceObj == nil {
		return false
	}
	switch family {
	case "inet":
		return len(interfaceObj.IPv4) > 0
	case "inet6":
		for _, address := range interfaceObj.IPv6 {
			if !address.IP.IsLinkLocalUnicast() {
				return true
			}
		}
		return false
	default:
		return false
	}
}
