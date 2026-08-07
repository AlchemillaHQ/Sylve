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
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func routeAlreadyExists(output string, err error) bool {
	message := strings.ToLower(output)
	if err != nil {
		message += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(message, "already in table") || strings.Contains(message, "file exists")
}

func routeIsMissing(output string, err error) bool {
	message := strings.ToLower(output)
	if err != nil {
		message += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(message, "not in table") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "no such process")
}

func deleteRouteIfPresent(args ...string) error {
	output, err := syncRunCommand("/sbin/route", args...)
	if err != nil && !routeIsMissing(output, err) {
		return err
	}
	return nil
}

func removeStandardSwitchRoutes(sw networkModels.StandardSwitch) error {
	var routeErrors []error
	network4, gateway4 := sw.Network(4), sw.Gateway(4)
	if sw.DefaultRoute && gateway4 != "" {
		if err := deleteRouteIfPresent("delete", "default", gateway4); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 default route: %w", err))
		}
	}
	if network4 != "" && gateway4 != "" {
		if err := deleteRouteIfPresent("delete", "-net", network4, gateway4); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 network route: %w", err))
		}
	}

	network6, gateway6 := sw.Network(6), sw.Gateway(6)
	if network6 != "" && gateway6 != "" {
		gateway6 = normalizeIPv6GatewayForRoute(gateway6, sw.BridgeName)
		if err := deleteRouteIfPresent("-6", "delete", "-net", network6, gateway6); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv6 network route: %w", err))
		}
	}

	return errors.Join(routeErrors...)
}

func isManagedStandardSwitchVLAN(name string) (bool, error) {
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return false, nil
		}
		return false, err
	}
	if interfaceObj == nil {
		return false, nil
	}
	return utils.Contains(interfaceObj.Groups, "svm-vlan"), nil
}

func destroyStandardSwitchRuntimeInterfaces(sw networkModels.StandardSwitch) error {
	var destroyErrors []error
	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "destroy"); err != nil && !isInterfaceMissingError(err) {
		destroyErrors = append(destroyErrors, fmt.Errorf("destroy bridge %s: %w", sw.BridgeName, err))
	}

	if sw.VLAN > 0 {
		seen := make(map[string]struct{}, len(sw.Ports))
		for _, port := range sw.Ports {
			vlanName := fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
			if _, duplicate := seen[vlanName]; duplicate {
				continue
			}
			seen[vlanName] = struct{}{}

			managed, err := isManagedStandardSwitchVLAN(vlanName)
			if err != nil {
				destroyErrors = append(destroyErrors, fmt.Errorf("inspect VLAN interface %s: %w", vlanName, err))
				continue
			}
			if !managed {
				continue
			}
			if _, err := syncRunCommand("/sbin/ifconfig", vlanName, "destroy"); err != nil && !isInterfaceMissingError(err) {
				destroyErrors = append(destroyErrors, fmt.Errorf("destroy VLAN interface %s: %w", vlanName, err))
			}
		}
	}

	return errors.Join(destroyErrors...)
}

func stopDhclient(br string) error {
	output, err := syncRunCommand("/sbin/dhclient", "-r", br)
	if err == nil {
		return nil
	}
	message := strings.ToLower(output + " " + err.Error())
	if strings.Contains(message, "not running") ||
		strings.Contains(message, "no such process") ||
		strings.Contains(message, "no such file") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "not found") {
		return nil
	}
	return fmt.Errorf("stop dhclient for %s: %w", br, err)
}
