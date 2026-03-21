// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package iface

import (
	"fmt"
	"net"
)

// List returns a basic slice of interfaces using the standard net package.
func List() ([]*Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	var result []*Interface
	for _, i := range ifaces {
		iface, err := Get(i.Name)
		if err != nil {
			continue // skip interfaces that fail to load
		}
		result = append(result, iface)
	}

	return result, nil
}

// Get returns basic interface information mapped from the standard net package.
func Get(name string) (*Interface, error) {
	ni, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface %s not found: %w", name, err)
	}

	iface := &Interface{
		Name:   ni.Name,
		MTU:    ni.MTU,
		Ether:  ni.HardwareAddr.String(),
		HWAddr: ni.HardwareAddr.String(),
		// We mock the raw flags here just to prevent nil pointers or panics in tests.
		// A full flag mapping isn't strictly necessary for non-target OS testing.
		Flags: Flags{Raw: uint32(ni.Flags)},
	}

	addrs, err := ni.Addrs()
	if err != nil {
		return iface, nil // return what we have so far
	}

	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipNet.IP
		if ip.To4() != nil {
			// IPv4
			mask := net.IP(ipNet.Mask).String()
			iface.IPv4 = append(iface.IPv4, IPv4{
				IP:      ip,
				Netmask: mask,
			})
		} else {
			// IPv6
			prefixLen, _ := ipNet.Mask.Size()

			// Extract zone/scope if present
			var scopeID uint32
			// net.Interface doesn't expose raw scope ID easily without parsing strings,
			// leaving it 0 for the stub is usually sufficient for testing.

			iface.IPv6 = append(iface.IPv6, IPv6{
				IP:           ip,
				PrefixLength: prefixLen,
				ScopeID:      scopeID,
			})
		}
	}

	return iface, nil
}
