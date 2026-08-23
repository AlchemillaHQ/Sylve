// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type InterfaceResolver struct {
	interfaceByName func(string) (*net.Interface, error)
}

func NewInterfaceResolver() *InterfaceResolver {
	return &InterfaceResolver{interfaceByName: net.InterfaceByName}
}

func (r *InterfaceResolver) Type() string {
	return "interface"
}

func (r *InterfaceResolver) Resolve(_ context.Context, settings map[string]string) (AddressSet, error) {
	name := strings.TrimSpace(settings[SourceSettingInterface])
	if name == "" {
		return AddressSet{}, fmt.Errorf("network interface is required")
	}

	interfaceByName := r.interfaceByName
	if interfaceByName == nil {
		interfaceByName = net.InterfaceByName
	}
	iface, err := interfaceByName(name)
	if err != nil {
		return AddressSet{}, fmt.Errorf("failed to find interface %q: %w", name, err)
	}

	addresses, err := iface.Addrs()
	if err != nil {
		return AddressSet{}, fmt.Errorf("failed to list addresses for interface %q: %w", name, err)
	}

	var resolved AddressSet
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
			continue
		}

		parsed, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		parsed = parsed.Unmap()
		if parsed.Is4() && !resolved.IPv4.IsValid() {
			resolved.IPv4 = parsed
		}
		if parsed.Is6() && !resolved.IPv6.IsValid() {
			resolved.IPv6 = parsed
		}
	}

	return resolved, nil
}

type ManualResolver struct{}

func (ManualResolver) Type() string {
	return "manual"
}

func (ManualResolver) Resolve(_ context.Context, settings map[string]string) (AddressSet, error) {
	var resolved AddressSet

	if raw := strings.TrimSpace(settings[SourceSettingIPv4]); raw != "" {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is4() {
			return AddressSet{}, fmt.Errorf("manual IPv4 address is invalid")
		}
		resolved.IPv4 = address.Unmap()
	}
	if raw := strings.TrimSpace(settings[SourceSettingIPv6]); raw != "" {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is6() {
			return AddressSet{}, fmt.Errorf("manual IPv6 address is invalid")
		}
		resolved.IPv6 = address
	}

	return resolved, nil
}

type STUNResolver struct {
	discover func(context.Context, string, string) (netip.Addr, error)
}

func NewSTUNResolver() *STUNResolver {
	return &STUNResolver{discover: discoverSTUNAddress}
}

func (r *STUNResolver) Type() string {
	return "stun"
}

func (r *STUNResolver) Resolve(ctx context.Context, settings map[string]string) (AddressSet, error) {
	server, err := normalizeSTUNServer(settings[SourceSettingSTUNServer])
	if err != nil {
		return AddressSet{}, err
	}

	discover := r.discover
	if discover == nil {
		discover = discoverSTUNAddress
	}

	var resolved AddressSet
	if address, err := discover(ctx, "udp4", server); err == nil {
		if address.Is4() {
			resolved.IPv4 = address.Unmap()
		}
	}
	if address, err := discover(ctx, "udp6", server); err == nil {
		if address.Is6() {
			resolved.IPv6 = address
		}
	}

	return resolved, nil
}

func normalizeSTUNServer(raw string) (string, error) {
	server := strings.TrimSpace(raw)
	if server == "" {
		return DefaultSTUNServer, nil
	}

	if host, port, err := net.SplitHostPort(server); err == nil {
		if strings.TrimSpace(host) == "" {
			return "", fmt.Errorf("STUN server host is required")
		}
		if err := validateSTUNPort(port); err != nil {
			return "", err
		}
		return net.JoinHostPort(host, port), nil
	}

	if net.ParseIP(server) != nil {
		return net.JoinHostPort(server, "3478"), nil
	}
	if strings.Contains(server, ":") {
		return "", fmt.Errorf("STUN server must use host:port or [IPv6]:port")
	}

	return net.JoinHostPort(server, "3478"), nil
}

func validateSTUNPort(raw string) error {
	port, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("STUN server port must be between 1 and 65535")
	}
	return nil
}
