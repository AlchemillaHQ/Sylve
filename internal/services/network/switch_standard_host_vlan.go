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
	"net/netip"
	"strconv"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const standardSwitchHostVLANGroup = "svm-host-vlan"

func standardSwitchHostVLANMTU(sw networkModels.StandardSwitch) int {
	return standardSwitchRuntimeMTU(sw) - 4
}

func standardSwitchHostInterfaceName(sw networkModels.StandardSwitch) string {
	if !sw.VLANFiltering {
		return sw.BridgeName
	}
	if sw.HostVLAN == nil || !bridgevlan.ValidVLAN(*sw.HostVLAN) {
		return ""
	}
	return fmt.Sprintf("%s.%d", sw.BridgeName, *sw.HostVLAN)
}

func standardSwitchHostVLANDescription(sw networkModels.StandardSwitch) string {
	return fmt.Sprintf("%s/%s/%d", standardSwitchHostVLANGroup, sw.BridgeName, *sw.HostVLAN)
}

func managedStandardSwitchHostVLAN(interfaceObj *iface.Interface) bool {
	return interfaceObj != nil && utils.Contains(interfaceObj.Groups, standardSwitchHostVLANGroup)
}

func ensureStandardSwitchHostVLAN(sw networkModels.StandardSwitch) (string, error) {
	name := standardSwitchHostInterfaceName(sw)
	if !sw.VLANFiltering || name == "" {
		return "", nil
	}

	interfaceObj, err := syncIfaceGet(name)
	if err != nil && !isInterfaceMissingError(err) {
		return "", fmt.Errorf("inspect host VLAN interface %s: %w", name, err)
	}
	if err == nil && interfaceObj != nil {
		if !managedStandardSwitchHostVLAN(interfaceObj) {
			return "", standardSwitchConflict(
				"standard_switch_host_vlan_interface_conflict",
				fmt.Errorf("interface %s is not owned by Sylve", name),
			)
		}
		if interfaceObj.VLANParent != sw.BridgeName || interfaceObj.VLANTag != *sw.HostVLAN {
			if _, err := syncRunCommand("/sbin/ifconfig", name, "-vlandev"); err != nil {
				return "", fmt.Errorf("reset host VLAN interface %s: %w", name, err)
			}
			if _, err := syncRunCommand(
				"/sbin/ifconfig", name,
				"vlandev", sw.BridgeName,
				"vlan", strconv.Itoa(*sw.HostVLAN),
			); err != nil {
				return "", fmt.Errorf("repair host VLAN interface %s: %w", name, err)
			}
		}
	} else {
		args := []string{
			"vlan", "create",
			"vlandev", sw.BridgeName,
			"vlan", strconv.Itoa(*sw.HostVLAN),
			"descr", standardSwitchHostVLANDescription(sw),
			"name", name,
			"group", standardSwitchHostVLANGroup,
			"up",
		}
		if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
			return "", fmt.Errorf("create host VLAN interface %s: %w", name, err)
		}
	}

	mtu := standardSwitchHostVLANMTU(sw)
	if _, err := syncRunCommand(
		"/sbin/ifconfig", name,
		"descr", standardSwitchHostVLANDescription(sw),
		"mtu", strconv.Itoa(mtu),
		"up",
	); err != nil {
		return "", fmt.Errorf("configure host VLAN interface %s: %w", name, err)
	}

	verified, err := syncIfaceGet(name)
	if err != nil {
		return "", fmt.Errorf("verify host VLAN interface %s: %w", name, err)
	}
	if !managedStandardSwitchHostVLAN(verified) || verified.VLANParent != sw.BridgeName ||
		verified.VLANTag != *sw.HostVLAN {
		return "", fmt.Errorf(
			"verify host VLAN interface %s: parent=%q vlan=%d groups=%v",
			name,
			verified.VLANParent,
			verified.VLANTag,
			verified.Groups,
		)
	}
	return name, nil
}

func destroyStandardSwitchHostVLAN(sw networkModels.StandardSwitch) error {
	name := standardSwitchHostInterfaceName(sw)
	if !sw.VLANFiltering || name == "" {
		return nil
	}
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return nil
		}
		return fmt.Errorf("inspect host VLAN interface %s: %w", name, err)
	}
	if interfaceObj == nil {
		return nil
	}
	if !managedStandardSwitchHostVLAN(interfaceObj) {
		return standardSwitchConflict(
			"standard_switch_host_vlan_interface_conflict",
			fmt.Errorf("refusing to destroy unowned interface %s", name),
		)
	}
	if _, err := syncRunCommand("/sbin/ifconfig", name, "destroy"); err != nil && !isInterfaceMissingError(err) {
		return fmt.Errorf("destroy host VLAN interface %s: %w", name, err)
	}
	return nil
}

func interfaceIPv4Prefix(address iface.IPv4) (netip.Prefix, bool) {
	ip, ok := netip.AddrFromSlice(address.IP.To4())
	if !ok {
		return netip.Prefix{}, false
	}
	mask := net.ParseIP(address.Netmask).To4()
	if mask == nil {
		return netip.Prefix{}, false
	}
	ones, bits := net.IPMask(mask).Size()
	if bits != 32 || ones < 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(ip, ones), true
}

func interfaceHasIPv4Prefix(interfaceObj *iface.Interface, desired string) bool {
	prefix, err := netip.ParsePrefix(desired)
	if err != nil {
		return false
	}
	for _, address := range interfaceObj.IPv4 {
		observed, ok := interfaceIPv4Prefix(address)
		if ok && observed == prefix {
			return true
		}
	}
	return false
}

func interfaceHasIPv6Prefix(interfaceObj *iface.Interface, desired string) bool {
	prefix, err := netip.ParsePrefix(desired)
	if err != nil {
		return false
	}
	for _, address := range interfaceObj.IPv6 {
		observed, ok := netip.AddrFromSlice(address.IP)
		if ok && netip.PrefixFrom(observed.Unmap(), address.PrefixLength) == prefix {
			return true
		}
	}
	return false
}

func deleteStandardSwitchHostIPv4(name string, interfaceObj *iface.Interface, keep string) error {
	var cleanupErrors []error
	for _, address := range interfaceObj.IPv4 {
		if keep != "" {
			observed, ok := interfaceIPv4Prefix(address)
			desired, parseErr := netip.ParsePrefix(keep)
			if parseErr == nil && ok && observed == desired {
				continue
			}
		}
		if _, err := syncRunCommand("/sbin/ifconfig", name, "inet", address.IP.String(), "delete"); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete IPv4 address %s from %s: %w", address.IP, name, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func deleteStandardSwitchHostIPv6(
	name string,
	interfaceObj *iface.Interface,
	keep string,
	keepSLAAC bool,
	keepLinkLocal bool,
) error {
	var cleanupErrors []error
	for _, address := range interfaceObj.IPv6 {
		if (keepLinkLocal && address.IP.IsLinkLocalUnicast()) || (keepSLAAC && address.AutoConf) {
			continue
		}
		if keep != "" && !address.AutoConf {
			desired, parseErr := netip.ParsePrefix(keep)
			observed, ok := netip.AddrFromSlice(address.IP)
			if parseErr == nil && ok && netip.PrefixFrom(observed.Unmap(), address.PrefixLength) == desired {
				continue
			}
		}
		ip := address.IP.String()
		if address.IP.IsLinkLocalUnicast() {
			ip += "%" + name
		}
		if _, err := syncRunCommand("/sbin/ifconfig", name, "inet6", ip, "delete"); err != nil &&
			!ignorableBridgeMemberIPv6CleanupError(err) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete IPv6 address %s from %s: %w", ip, name, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func standardSwitchIPv4RouteChanged(oldSw, newSw networkModels.StandardSwitch, oldName, newName string) bool {
	return oldName != newName || oldSw.Network(4) != newSw.Network(4) ||
		oldSw.Gateway(4) != newSw.Gateway(4) || oldSw.DefaultRoute != newSw.DefaultRoute
}

func standardSwitchIPv6RouteChanged(oldSw, newSw networkModels.StandardSwitch, oldName, newName string) bool {
	return oldName != newName || oldSw.Network(6) != newSw.Network(6) ||
		oldSw.Gateway(6) != newSw.Gateway(6) || oldSw.DefaultRoute6 != newSw.DefaultRoute6 ||
		oldSw.SLAAC != newSw.SLAAC || oldSw.DisableIPv6 != newSw.DisableIPv6
}

func reconcileFilteredStandardSwitchHost(oldSw, newSw networkModels.StandardSwitch) (retErr error) {
	oldName := standardSwitchHostInterfaceName(oldSw)
	newName := standardSwitchHostInterfaceName(newSw)
	addedNetwork4Route := false
	addedDefault4Route := false
	addedNetwork6Route := false
	addedDefault6Route := false
	defer func() {
		if retErr == nil {
			return
		}

		var cleanupErrors []error
		if addedDefault4Route {
			if err := deleteRouteIfPresent("delete", "default", newSw.Gateway(4)); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove added host VLAN IPv4 default route: %w", err))
			}
		}
		if addedDefault6Route {
			gateway := normalizeIPv6GatewayForRoute(newSw.Gateway(6), newName)
			if err := deleteRouteIfPresent("-6", "delete", "default", gateway); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove added host VLAN IPv6 default route: %w", err))
			}
		}
		if addedNetwork4Route {
			if err := deleteRouteIfPresent("delete", "-net", newSw.Network(4), newSw.Gateway(4)); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove added host VLAN IPv4 network route: %w", err))
			}
		}
		if addedNetwork6Route {
			gateway := normalizeIPv6GatewayForRoute(newSw.Gateway(6), newName)
			if err := deleteRouteIfPresent("-6", "delete", "-net", newSw.Network(6), gateway); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove added host VLAN IPv6 network route: %w", err))
			}
		}
		retErr = errors.Join(retErr, errors.Join(cleanupErrors...))
	}()

	if oldName == oldSw.BridgeName {
		oldName = ""
	}
	if newName == newSw.BridgeName {
		newName = ""
	}

	if newName != "" {
		if _, err := ensureStandardSwitchHostVLAN(newSw); err != nil {
			return err
		}
	}
	if oldName != "" && oldName != newName {
		if err := stopDhclient(oldName); err != nil {
			return fmt.Errorf("stop DHCP on old host VLAN %s: %w", oldName, err)
		}
		if err := removeStandardSwitchRoutes(oldSw); err != nil {
			return err
		}
		if err := destroyStandardSwitchHostVLAN(oldSw); err != nil {
			return err
		}
	}
	if newName == "" {
		return nil
	}

	interfaceObj, err := syncIfaceGet(newName)
	if err != nil || interfaceObj == nil {
		if err == nil {
			err = fmt.Errorf("interface not found")
		}
		return fmt.Errorf("inspect host VLAN interface %s: %w", newName, err)
	}

	if oldName == newName && standardSwitchIPv4RouteChanged(oldSw, newSw, oldName, newName) {
		if oldSw.DefaultRoute {
			if _, err := removeDefaultRouteForInterface("", oldName); err != nil {
				return fmt.Errorf("remove old IPv4 default route: %w", err)
			}
		}
		if oldSw.Network(4) != "" && oldSw.Gateway(4) != "" {
			if err := deleteRouteIfPresent("delete", "-net", oldSw.Network(4), oldSw.Gateway(4)); err != nil {
				return fmt.Errorf("remove old IPv4 network route: %w", err)
			}
		}
	}
	if oldName == newName && standardSwitchIPv6RouteChanged(oldSw, newSw, oldName, newName) {
		if oldSw.DefaultRoute6 {
			if _, err := removeDefaultRouteForInterface("-6", oldName); err != nil {
				return fmt.Errorf("remove old IPv6 default route: %w", err)
			}
		}
		if oldSw.Network(6) != "" && oldSw.Gateway(6) != "" {
			gateway := normalizeIPv6GatewayForRoute(oldSw.Gateway(6), oldName)
			if err := deleteRouteIfPresent("-6", "delete", "-net", oldSw.Network(6), gateway); err != nil {
				return fmt.Errorf("remove old IPv6 network route: %w", err)
			}
		}
	}

	newNetwork4 := newSw.Network(4)
	if newSw.DHCP {
		if oldName != newName || !oldSw.DHCP {
			if err := deleteStandardSwitchHostIPv4(newName, interfaceObj, ""); err != nil {
				return err
			}
		}
	} else {
		if err := stopDhclient(newName); err != nil {
			return fmt.Errorf("stop DHCP on host VLAN %s: %w", newName, err)
		}
		if err := deleteStandardSwitchHostIPv4(newName, interfaceObj, newNetwork4); err != nil {
			return err
		}
		if newNetwork4 != "" && !interfaceHasIPv4Prefix(interfaceObj, newNetwork4) {
			if _, err := syncRunCommand("/sbin/ifconfig", newName, "inet", newNetwork4); err != nil {
				return fmt.Errorf("set host VLAN IPv4 address %s: %w", newNetwork4, err)
			}
		}
	}

	newNetwork6 := newSw.Network(6)
	switch {
	case newSw.DisableIPv6:
		if _, err := syncRunCommand("/sbin/ifconfig", newName, "inet6", "no_radr", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("disable IPv6 on host VLAN %s: %w", newName, err)
		}
		if err := deleteStandardSwitchHostIPv6(newName, interfaceObj, "", false, false); err != nil {
			return err
		}
	case newSw.SLAAC:
		routerPolicy := "no_radr"
		if newSw.DefaultRoute6 {
			if err := ensureStandardSwitchIPv6RADefaultRouteSupport(); err != nil {
				return err
			}
			routerPolicy = "-no_radr"
		}
		if _, err := syncRunCommand("/sbin/ifconfig", newName, "inet6", "auto_linklocal", "-ifdisabled", routerPolicy, "accept_rtadv"); err != nil {
			return fmt.Errorf("enable SLAAC on host VLAN %s: %w", newName, err)
		}
		if err := deleteStandardSwitchHostIPv6(newName, interfaceObj, "", true, true); err != nil {
			return err
		}
	default:
		if _, err := syncRunCommand("/sbin/ifconfig", newName, "inet6", "auto_linklocal", "-ifdisabled", "no_radr", "-accept_rtadv"); err != nil {
			return fmt.Errorf("configure IPv6 flags on host VLAN %s: %w", newName, err)
		}
		if err := deleteStandardSwitchHostIPv6(newName, interfaceObj, newNetwork6, false, true); err != nil {
			return err
		}
		if newNetwork6 != "" && !interfaceHasIPv6Prefix(interfaceObj, newNetwork6) {
			if _, err := syncRunCommand("/sbin/ifconfig", newName, "inet6", newNetwork6, "-no_dad"); err != nil {
				return fmt.Errorf("set host VLAN IPv6 address %s: %w", newNetwork6, err)
			}
		}
	}

	if _, err := syncRunCommand("/sbin/ifconfig", newName, "up"); err != nil {
		return fmt.Errorf("bring up host VLAN %s: %w", newName, err)
	}
	if newNetwork4 != "" && newSw.Gateway(4) != "" && !newSw.DHCP {
		added, err := addRouteIfMissing("add", "-net", newNetwork4, newSw.Gateway(4))
		if err != nil {
			return fmt.Errorf("add host VLAN IPv4 network route: %w", err)
		}
		addedNetwork4Route = added
		if newSw.DefaultRoute {
			added, err := addDefaultRouteIfMissing(newSw.Gateway(4), newName)
			if err != nil {
				return fmt.Errorf("add host VLAN IPv4 default route: %w", err)
			}
			addedDefault4Route = added
		}
	}
	if newNetwork6 != "" && newSw.Gateway(6) != "" && !newSw.DisableIPv6 && !newSw.SLAAC {
		gateway := normalizeIPv6GatewayForRoute(newSw.Gateway(6), newName)
		added, err := addRouteIfMissing("-6", "add", "-net", newNetwork6, gateway)
		if err != nil {
			return fmt.Errorf("add host VLAN IPv6 network route: %w", err)
		}
		addedNetwork6Route = added
		if newSw.DefaultRoute6 {
			added, err := addDefaultRoute6IfMissing(gateway, newName)
			if err != nil {
				return fmt.Errorf("add host VLAN IPv6 default route: %w", err)
			}
			addedDefault6Route = added
		}
	}
	if newSw.SLAAC && !newSw.DisableIPv6 {
		if err := syncSolicitRouterAdvertisement(newName); err != nil {
			logger.L.Warn().Err(err).Str("interface", newName).
				Msg("standard_switch_slaac_router_solicitation_failed")
		}
	}
	if newSw.DHCP {
		if err := runDhclient(newName, 10, newSw.DefaultRoute); err != nil {
			return err
		}
	}
	return nil
}
