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
	"net"
	"strings"
	"testing"
)

func TestGetReturnsErrorForMissingInterface(t *testing.T) {
	name := "sylve-missing-iface-test"

	got, err := Get(name)
	if err == nil {
		t.Fatalf("expected error for missing interface, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil interface when lookup fails, got %+v", got)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("expected error to include interface name %q, got %q", name, err.Error())
	}
}

func TestGetMapsCoreFieldsForExistingInterface(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to enumerate host interfaces: %v", err)
	}
	if len(ifaces) == 0 {
		t.Fatal("no host interfaces found for test")
	}

	src := ifaces[0]
	got, err := Get(src.Name)
	if err != nil {
		t.Fatalf("Get(%q) returned error: %v", src.Name, err)
	}

	if got.Name != src.Name {
		t.Fatalf("expected Name %q, got %q", src.Name, got.Name)
	}
	if got.MTU != src.MTU {
		t.Fatalf("expected MTU %d, got %d", src.MTU, got.MTU)
	}
	if got.Ether != src.HardwareAddr.String() {
		t.Fatalf("expected Ether %q, got %q", src.HardwareAddr.String(), got.Ether)
	}
	if got.HWAddr != src.HardwareAddr.String() {
		t.Fatalf("expected HWAddr %q, got %q", src.HardwareAddr.String(), got.HWAddr)
	}
	if got.Flags.Raw != uint32(src.Flags) {
		t.Fatalf("expected raw flags %d, got %d", uint32(src.Flags), got.Flags.Raw)
	}

	for _, v4 := range got.IPv4 {
		if v4.IP.To4() == nil {
			t.Fatalf("expected IPv4 address, got %q", v4.IP.String())
		}
		if v4.Netmask == "" {
			t.Fatalf("expected non-empty IPv4 netmask for %q", v4.IP.String())
		}
	}

	for _, v6 := range got.IPv6 {
		if v6.IP.To4() != nil {
			t.Fatalf("expected IPv6 address, got %q", v6.IP.String())
		}
		if v6.PrefixLength < 0 || v6.PrefixLength > 128 {
			t.Fatalf("expected IPv6 prefix in range [0,128], got %d", v6.PrefixLength)
		}
	}
}

func TestListIncludesInterfaceFromHost(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to enumerate host interfaces: %v", err)
	}
	if len(ifaces) == 0 {
		t.Fatal("no host interfaces found for test")
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least one interface from List()")
	}

	var hasTarget bool
	target := ifaces[0].Name
	for _, i := range list {
		if i == nil {
			t.Fatal("List() returned nil interface entry")
		}
		if i.Name == target {
			hasTarget = true
		}
	}

	if !hasTarget {
		t.Fatalf("expected List() to include host interface %q", target)
	}
}
