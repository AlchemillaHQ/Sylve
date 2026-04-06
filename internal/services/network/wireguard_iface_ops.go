// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

var wireGuardInterfaceHasAddress = wireGuardInterfaceHasAddressCIDR

func wireGuardInterfaceExistsNativeOrShell(name string) (bool, error) {
	_, shellErr := wireGuardRunCommand("/sbin/ifconfig", name)
	if shellErr == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(shellErr.Error()), "does not exist") {
		return false, nil
	}

	return false, fmt.Errorf("failed_to_check_wireguard_interface_%s: %w", name, shellErr)
}

func wireGuardCreateInterfaceNativeOrShell(cloneType string) (string, error) {
	out, shellErr := wireGuardRunCommand("/sbin/ifconfig", cloneType, "create")
	if shellErr != nil {
		return "", fmt.Errorf("failed_to_create_wireguard_interface: %w", shellErr)
	}

	created := strings.TrimSpace(out)
	if idx := strings.IndexByte(created, '\n'); idx >= 0 {
		created = strings.TrimSpace(created[:idx])
	}
	if created == "" {
		return "", fmt.Errorf("failed_to_resolve_created_wireguard_interface")
	}

	return created, nil
}

func wireGuardRenameInterfaceNativeOrShell(currentName string, newName string) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", currentName, "name", newName); shellErr != nil {
		return fmt.Errorf("failed_to_rename_wireguard_interface: %w", shellErr)
	}

	return nil
}

func wireGuardDestroyInterfaceNativeOrShell(name string) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "destroy"); shellErr != nil {
		return fmt.Errorf("failed_to_destroy_wireguard_interface_%s: %w", name, shellErr)
	}

	return nil
}

func wireGuardAddAddressNativeOrShell(name string, hostCIDR string, ipv6 bool) error {
	hasAddress, hasAddressErr := wireGuardInterfaceHasAddress(name, hostCIDR)
	if hasAddressErr == nil && hasAddress {
		return nil
	}

	args := []string{name}
	if ipv6 {
		args = append(args, "inet6", hostCIDR, "alias")
	} else {
		args = append(args, "inet", hostCIDR, "alias")
	}

	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", args...); shellErr != nil {
		if isWireGuardAddressExistsError(shellErr) {
			if hasAddr, _ := wireGuardInterfaceHasAddress(name, hostCIDR); hasAddr {
				return nil
			}
			return shellErr
		}
		return shellErr
	}

	return nil
}

func isWireGuardAddressExistsError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.EEXIST) {
		return true
	}

	return strings.Contains(strings.ToLower(err.Error()), "file exists")
}

func wireGuardInterfaceHasAddressCIDR(name string, hostCIDR string) (bool, error) {
	targetIP, _, err := net.ParseCIDR(strings.TrimSpace(hostCIDR))
	if err != nil {
		return false, err
	}

	targetIsIPv4 := targetIP.To4() != nil
	if targetIsIPv4 {
		targetIP = targetIP.To4()
	} else {
		targetIP = targetIP.To16()
	}
	if targetIP == nil {
		return false, nil
	}

	iface, err := net.InterfaceByName(name)
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "no such network interface") {
			return false, nil
		}
		return false, err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return false, err
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet == nil {
			continue
		}

		candidateIP := ipNet.IP
		if targetIsIPv4 {
			candidateIP = candidateIP.To4()
		} else {
			candidateIP = candidateIP.To16()
			if candidateIP != nil && candidateIP.To4() != nil {
				continue
			}
		}
		if candidateIP == nil {
			continue
		}

		if candidateIP.Equal(targetIP) {
			// FreeBSD can expose wg interface addresses through net.Interface with a
			// host mask even when the configured mask is wider. If IP matches, treat
			// it as already present to keep startup/apply idempotent.
			return true, nil
		}
	}

	return false, nil
}

func wireGuardSetMTUNativeOrShell(name string, mtu uint) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "mtu", fmt.Sprintf("%d", mtu)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetMetricNativeOrShell(name string, metric uint) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "metric", fmt.Sprintf("%d", metric)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetFIBNativeOrShell(name string, fib uint) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "fib", fmt.Sprintf("%d", fib)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetUpNativeOrShell(name string) error {
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "up"); shellErr != nil {
		return shellErr
	}

	return nil
}
