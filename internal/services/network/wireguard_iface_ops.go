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
	"sync"
	"syscall"

	"github.com/alchemillahq/sylve/internal/logger"
)

var errWireGuardInterfaceOpsUnsupported = errors.New("wireguard_interface_ops_unsupported")

type wireGuardInterfaceOps interface {
	Exists(name string) (bool, error)
	Create(cloneType string) (string, error)
	Rename(currentName string, newName string) error
	Destroy(name string) error
	AddAddress(name string, hostCIDR string) error
	SetMTU(name string, mtu uint) error
	SetMetric(name string, metric uint) error
	SetFIB(name string, fib uint) error
	Up(name string) error
}

var (
	wireGuardInterfaceOpsBackend     = newWireGuardInterfaceOps()
	wireGuardShouldFallbackInterface = shouldFallbackWireGuardInterfaceOperation
	wireGuardInterfaceHasAddress     = wireGuardInterfaceHasAddressCIDR
	wireGuardFallbackWarnedOps       sync.Map
)

func shouldFallbackWireGuardInterfaceOperation(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, errWireGuardInterfaceOpsUnsupported) {
		return true
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.ENOSYS ||
			errno == syscall.ENOTSUP ||
			errno == syscall.EOPNOTSUPP ||
			errno == syscall.ENOTTY {
			return true
		}
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not supported") || strings.Contains(lower, "not implemented")
}

func logWireGuardInterfaceFallbackOnce(operation string, nativeErr error) {
	if _, loaded := wireGuardFallbackWarnedOps.LoadOrStore(operation, struct{}{}); loaded {
		return
	}

	logger.L.Warn().
		Err(nativeErr).
		Str("operation", operation).
		Msg("wireguard native interface operation failed; falling back to shell")
}

func wireGuardInterfaceExistsNativeOrShell(name string) (bool, error) {
	exists, err := wireGuardInterfaceOpsBackend.Exists(name)
	if err == nil {
		return exists, nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return false, fmt.Errorf("failed_to_check_wireguard_interface_%s: %w", name, err)
	}

	logWireGuardInterfaceFallbackOnce("exists", err)
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
	created, err := wireGuardInterfaceOpsBackend.Create(cloneType)
	if err == nil {
		return created, nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return "", fmt.Errorf("failed_to_create_wireguard_interface: %w", err)
	}

	logWireGuardInterfaceFallbackOnce("create", err)
	out, shellErr := wireGuardRunCommand("/sbin/ifconfig", cloneType, "create")
	if shellErr != nil {
		return "", fmt.Errorf("failed_to_create_wireguard_interface: %w", shellErr)
	}

	created = strings.TrimSpace(out)
	if idx := strings.IndexByte(created, '\n'); idx >= 0 {
		created = strings.TrimSpace(created[:idx])
	}
	if created == "" {
		return "", fmt.Errorf("failed_to_resolve_created_wireguard_interface")
	}

	return created, nil
}

func wireGuardRenameInterfaceNativeOrShell(currentName string, newName string) error {
	err := wireGuardInterfaceOpsBackend.Rename(currentName, newName)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return fmt.Errorf("failed_to_rename_wireguard_interface: %w", err)
	}

	logWireGuardInterfaceFallbackOnce("rename", err)
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", currentName, "name", newName); shellErr != nil {
		return fmt.Errorf("failed_to_rename_wireguard_interface: %w", shellErr)
	}

	return nil
}

func wireGuardDestroyInterfaceNativeOrShell(name string) error {
	err := wireGuardInterfaceOpsBackend.Destroy(name)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return fmt.Errorf("failed_to_destroy_wireguard_interface_%s: %w", name, err)
	}

	logWireGuardInterfaceFallbackOnce("destroy", err)
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

	err := wireGuardInterfaceOpsBackend.AddAddress(name, hostCIDR)
	if err == nil {
		return nil
	}

	if isWireGuardAddressExistsError(err) {
		// Kernel reported EEXIST. Verify the address is actually on this interface.
		if hasAddress, _ := wireGuardInterfaceHasAddress(name, hostCIDR); hasAddress {
			return nil
		}
		// EEXIST but the address is not visible on this interface (may be assigned
		// to a different interface). Fall through to the shell fallback only when
		// this error class is configured as fallback-eligible.
		if !wireGuardShouldFallbackInterface(err) {
			return err
		}
	} else if !wireGuardShouldFallbackInterface(err) {
		return err
	}

	logWireGuardInterfaceFallbackOnce("add-address", err)
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
	targetIP, targetNetwork, err := net.ParseCIDR(strings.TrimSpace(hostCIDR))
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

	targetOnes, targetBits := targetNetwork.Mask.Size()

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
			ones, bits := ipNet.Mask.Size()
			if ones != targetOnes || bits != targetBits {
				// FreeBSD can expose wg interface addresses through net.Interface with a
				// host mask even when the configured mask is wider. If IP matches, treat
				// it as already present to keep startup/apply idempotent.
				return true, nil
			}
			return true, nil
		}
	}

	return false, nil
}

func wireGuardSetMTUNativeOrShell(name string, mtu uint) error {
	err := wireGuardInterfaceOpsBackend.SetMTU(name, mtu)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return err
	}

	logWireGuardInterfaceFallbackOnce("set-mtu", err)
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "mtu", fmt.Sprintf("%d", mtu)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetMetricNativeOrShell(name string, metric uint) error {
	err := wireGuardInterfaceOpsBackend.SetMetric(name, metric)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return err
	}

	logWireGuardInterfaceFallbackOnce("set-metric", err)
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "metric", fmt.Sprintf("%d", metric)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetFIBNativeOrShell(name string, fib uint) error {
	err := wireGuardInterfaceOpsBackend.SetFIB(name, fib)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return err
	}

	logWireGuardInterfaceFallbackOnce("set-fib", err)
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "fib", fmt.Sprintf("%d", fib)); shellErr != nil {
		return shellErr
	}

	return nil
}

func wireGuardSetUpNativeOrShell(name string) error {
	err := wireGuardInterfaceOpsBackend.Up(name)
	if err == nil {
		return nil
	}

	if !wireGuardShouldFallbackInterface(err) {
		return err
	}

	logWireGuardInterfaceFallbackOnce("set-up", err)
	if _, shellErr := wireGuardRunCommand("/sbin/ifconfig", name, "up"); shellErr != nil {
		return shellErr
	}

	return nil
}
