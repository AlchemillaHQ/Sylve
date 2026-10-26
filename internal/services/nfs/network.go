// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

func nativeAddresses() ([]string, error) {
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	addresses := []string{}
	for _, item := range interfaces {
		prefix, err := netip.ParsePrefix(item.String())
		if err != nil {
			continue
		}
		address := prefix.Addr().Unmap()
		if address.IsLinkLocalUnicast() || (!address.IsGlobalUnicast() && !address.IsLoopback()) {
			continue
		}
		addresses = append(addresses, address.String())
	}
	slices.Sort(addresses)
	return slices.Compact(addresses), nil
}

func (d *nativeRuntime) bindings(config nfsModels.NFSConfig) ([]string, error) {
	if len(config.BindAddresses) == 0 && !config.UDP {
		return nil, nil
	}
	assigned, err := d.addresses()
	if err != nil {
		return nil, err
	}
	if len(config.BindAddresses) == 0 {
		// FreeBSD NFS/UDP requires concrete local addresses, not wildcard sockets.
		if len(assigned) == 0 {
			return nil, errors.New("no local addresses are available for NFS/UDP")
		}
		return assigned, nil
	}
	for _, address := range config.BindAddresses {
		if !slices.Contains(assigned, address) {
			return nil, fmt.Errorf("NFS bind address is not assigned locally: %s", address)
		}
	}
	return slices.Clone(config.BindAddresses), nil
}

func (d *nativeRuntime) checkRPCServices(ctx context.Context, config nfsModels.NFSConfig) error {
	hosts := config.BindAddresses
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1"}
	}
	for _, host := range hosts {
		if err := d.checkRPCAddress(ctx, config, host); err != nil {
			return err
		}
	}
	return nil
}

func (d *nativeRuntime) checkRPCAddress(ctx context.Context, config nfsModels.NFSConfig, host string) error {
	transport := "tcp"
	if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
		transport = "tcp6"
	}
	if config.Protocol != "v4" {
		var err error
		port := config.MountdPort
		if port == 0 && transport == "tcp6" {
			// FreeBSD rpcinfo's implicit IPv6 lookup can select the wrong port.
			// Resolve the registered TCP endpoint and probe it directly instead.
			port, err = d.mountdIPv6Port(ctx, host)
		}
		if err != nil {
			return err
		}
		if port == 0 {
			err = d.execute(ctx, "/usr/bin/rpcinfo", "-T", transport, host, "100005", "3")
		} else {
			err = d.execute(ctx, "/usr/bin/rpcinfo", "-a", host+fmt.Sprintf(".%d.%d", port/256, port%256), "-T", transport, "100005", "3")
		}
		if err != nil {
			return fmt.Errorf("NFSv3 mount service is unavailable: %w", err)
		}
	}
	for version := uint32(3); version <= 4; version++ {
		if version == 3 && config.Protocol == "v4" || version == 4 && config.Protocol == "v3" {
			continue
		}
		if err := d.execute(ctx, "/usr/bin/rpcinfo", "-a", host+".8.1", "-T", transport, "100003", strconv.FormatUint(uint64(version), 10)); err != nil {
			return fmt.Errorf("NFSv%d is not responding: %w", version, err)
		}
	}
	if config.UDP {
		transport = "udp"
		if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
			transport = "udp6"
		}
		if err := d.execute(ctx, "/usr/bin/rpcinfo", "-a", host+".8.1", "-T", transport, "100003", "3"); err != nil {
			return fmt.Errorf("NFSv3 UDP is not responding: %w", err)
		}
	}
	return nil
}

func (d *nativeRuntime) mountdIPv6Port(ctx context.Context, host string) (uint16, error) {
	result, err := d.command(ctx, "/usr/bin/rpcinfo", "-l", host, "100005", "3")
	if err != nil {
		return 0, fmt.Errorf("discover IPv6 mount service: %w", err)
	}
	for _, line := range strings.Split(result.Output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "100005" || fields[1] != "3" || fields[2] != "inet6/tcp/cots_ord" {
			continue
		}
		address, suffix, ok := strings.Cut(fields[3], ".")
		if !ok {
			continue
		}
		high, low, ok := strings.Cut(suffix, ".")
		if !ok {
			continue
		}
		ip, ipErr := netip.ParseAddr(address)
		hi, hiErr := strconv.ParseUint(high, 10, 8)
		lo, loErr := strconv.ParseUint(low, 10, 8)
		if ipErr != nil || ip.String() != host || hiErr != nil || loErr != nil || hi*256+lo == 0 {
			continue
		}
		return uint16(hi*256 + lo), nil
	}
	return 0, errors.New("IPv6 mount service has no registered TCP endpoint")
}
