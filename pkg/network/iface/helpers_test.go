// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package iface

import (
	"reflect"
	"testing"
)

func TestParseFlagsReturnsDescriptionsAndRemaining(t *testing.T) {
	descriptors := []FlagDescriptor{
		{Mask: 0x1, Name: "ONE"},
		{Mask: 0x4, Name: "FOUR"},
	}

	desc, remaining := parseFlags(0x1|0x2|0x4, descriptors)
	expected := []string{"ONE", "FOUR", "UNKNOWN_0x2"}

	if !reflect.DeepEqual(desc, expected) {
		t.Fatalf("expected descriptions %v, got %v", expected, desc)
	}
	if remaining != 0x2 {
		t.Fatalf("expected remaining 0x2, got 0x%x", remaining)
	}
}

func TestParseFlagsDescIncludesUnknownBits(t *testing.T) {
	desc := parseFlagsDesc(0x1 | 0x8 | 0x4)
	expected := []string{"UP", "LOOPBACK", "UNKNOWN_0x4"}

	if !reflect.DeepEqual(desc, expected) {
		t.Fatalf("expected %v, got %v", expected, desc)
	}
}

func TestParseCapabilitiesDesc(t *testing.T) {
	desc := parseCapabilitiesDesc((1 << 0) | (1 << 9))
	expected := []string{"RXCSUM", "TSO6"}

	if !reflect.DeepEqual(desc, expected) {
		t.Fatalf("expected %v, got %v", expected, desc)
	}
}

func TestKnownFlagMask(t *testing.T) {
	mask := knownFlagMask()

	if mask&0x1 == 0 {
		t.Fatal("expected mask to include 0x1 (UP)")
	}
	if mask&0x8 == 0 {
		t.Fatal("expected mask to include 0x8 (LOOPBACK)")
	}
	if mask&0x4 != 0 {
		t.Fatal("expected mask to exclude 0x4")
	}
}

func TestParseSTPProto(t *testing.T) {
	if got := parseSTPProto(0); got != "stp" {
		t.Fatalf("expected stp, got %q", got)
	}
	if got := parseSTPProto(1); got != "-" {
		t.Fatalf("expected -, got %q", got)
	}
	if got := parseSTPProto(2); got != "rstp" {
		t.Fatalf("expected rstp, got %q", got)
	}
	if got := parseSTPProto(7); got != "7" {
		t.Fatalf("expected numeric fallback 7, got %q", got)
	}
}

func TestParseMediaOptions(t *testing.T) {
	if got := parseMediaOptions(0x00100000 | 0x00200000); !reflect.DeepEqual(got, []string{"full-duplex", "half-duplex"}) {
		t.Fatalf("unexpected media options: %v", got)
	}
}

func TestParseMediaTypeBase(t *testing.T) {
	cases := []struct {
		active int
		want   string
	}{
		{0x20, "Ethernet"},
		{0x40, "Token Ring"},
		{0x60, "FDDI"},
		{0x80, "Wi-Fi"},
		{0xa0, "ATM"},
		{0x00, "Unknown (0x0)"},
	}

	for _, tc := range cases {
		if got := parseMediaTypeBase(tc.active); got != tc.want {
			t.Fatalf("active=0x%x expected %q, got %q", tc.active, tc.want, got)
		}
	}
}

func TestParseMediaSubtype(t *testing.T) {
	if got := parseMediaSubtype(16); got != "1000baseT" {
		t.Fatalf("expected 1000baseT, got %q", got)
	}
	if got := parseMediaSubtype(31); got != "" {
		t.Fatalf("expected empty subtype for unknown value, got %q", got)
	}
}

func TestParseMediaMode(t *testing.T) {
	if got := parseMediaMode(0); got != "autoselect" {
		t.Fatalf("expected autoselect, got %q", got)
	}
	if got := parseMediaMode(1); got != "manual" {
		t.Fatalf("expected manual, got %q", got)
	}
	if got := parseMediaMode(2); got != "none" {
		t.Fatalf("expected none, got %q", got)
	}
	if got := parseMediaMode(5); got != "mode-0x5" {
		t.Fatalf("expected mode fallback mode-0x5, got %q", got)
	}
}

func TestParseND6Options(t *testing.T) {
	flags := uint32(0x01 | 0x08 | 0x80)
	got := parseND6Options(flags)
	want := []string{"PERFORMNUD", "IFDISABLED", "NO_PREFER_IFACE"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
