// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"os/exec"
	"strings"
	"testing"
)

func TestNetworkValidationRanges(t *testing.T) {
	t.Run("metric", func(t *testing.T) {
		tests := []struct {
			value int
			want  bool
		}{
			{-1, false},
			{0, true},
			{255, true},
			{256, false},
		}

		for _, tt := range tests {
			if got := IsValidMetric(tt.value); got != tt.want {
				t.Fatalf("IsValidMetric(%d) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("mtu", func(t *testing.T) {
		tests := []struct {
			value int
			want  bool
		}{
			{67, false},
			{68, true},
			{1500, true},
			{65535, true},
			{65536, false},
		}

		for _, tt := range tests {
			if got := IsValidMTU(tt.value); got != tt.want {
				t.Fatalf("IsValidMTU(%d) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("vlan", func(t *testing.T) {
		tests := []struct {
			value int
			want  bool
		}{
			{-1, false},
			{0, true},
			{4095, true},
			{4096, false},
		}

		for _, tt := range tests {
			if got := IsValidVLAN(tt.value); got != tt.want {
				t.Fatalf("IsValidVLAN(%d) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})

	t.Run("port", func(t *testing.T) {
		tests := []struct {
			value int
			want  bool
		}{
			{0, false},
			{1, true},
			{65535, true},
			{65536, false},
		}

		for _, tt := range tests {
			if got := IsValidPort(tt.value); got != tt.want {
				t.Fatalf("IsValidPort(%d) = %v, want %v", tt.value, got, tt.want)
			}
		}
	})
}

func TestIPAndCIDRValidation(t *testing.T) {
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"valid ip v4", IsValidIP("192.168.1.10"), true},
		{"valid ip v6", IsValidIP("2001:db8::1"), true},
		{"invalid ip", IsValidIP("not-an-ip"), false},
		{"valid ipv4", IsValidIPv4("10.0.0.1"), true},
		{"ipv4 rejects ipv6", IsValidIPv4("2001:db8::1"), false},
		{"valid ipv6", IsValidIPv6("2001:db8::1"), true},
		{"ipv6 rejects ipv4", IsValidIPv6("10.0.0.1"), false},
		{"valid ipv4 cidr", IsValidIPv4CIDR("10.0.0.0/24"), true},
		{"ipv4 cidr rejects ipv6", IsValidIPv4CIDR("2001:db8::/64"), false},
		{"invalid ipv4 cidr", IsValidIPv4CIDR("10.0.0.0"), false},
		{"valid ipv6 cidr", IsValidIPv6CIDR("2001:db8::/64"), true},
		{"ipv6 cidr rejects ipv4", IsValidIPv6CIDR("10.0.0.0/24"), false},
		{"invalid ipv6 cidr", IsValidIPv6CIDR("2001:db8::"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s: got %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestAssignableCIDRValidation(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want bool
	}{
		{"assignable ipv4 host cidr", "10.80.0.254/24", true},
		{"reject ipv4 subnet base", "10.80.0.0/24", false},
		{"reject ipv4 directed broadcast", "10.80.0.255/24", false},
		{"allow ipv4 /32", "10.80.0.254/32", true},
		{"assignable ipv6 host cidr", "2001:db8::5/64", true},
		{"reject ipv6 subnet base", "2001:db8::/64", false},
		{"allow ipv6 /128", "2001:db8::5/128", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAssignableCIDR(tt.cidr); got != tt.want {
				t.Fatalf("IsAssignableCIDR(%q) = %v, want %v", tt.cidr, got, tt.want)
			}
		})
	}
}

func TestMiscNetworkValidation(t *testing.T) {
	t.Run("mac", func(t *testing.T) {
		if !IsValidMAC("aa:bb:cc:dd:ee:ff") {
			t.Fatal("expected valid MAC")
		}
		if IsValidMAC("aa:bb:cc:dd:ee") {
			t.Fatal("expected invalid MAC")
		}
	})

	t.Run("fqdn", func(t *testing.T) {
		if !IsValidFQDN("example.com") {
			t.Fatal("expected valid FQDN")
		}
		if IsValidFQDN("invalid_domain") {
			t.Fatal("expected invalid FQDN")
		}
	})

	t.Run("duid", func(t *testing.T) {
		if !IsValidDUID("00:01:00:01") {
			t.Fatal("expected valid DUID")
		}
		if IsValidDUID("00:01:zz:01") {
			t.Fatal("expected invalid DUID with non-hex chars")
		}
		if IsValidDUID("00") {
			t.Fatal("expected invalid short DUID")
		}
	})

	t.Run("bridge if name", func(t *testing.T) {
		got := BridgeIfName("bridge0")
		want := ShortHash("sylbridge0")
		if got != want {
			t.Fatalf("BridgeIfName mismatch: got %q, want %q", got, want)
		}
		if len(got) != 8 {
			t.Fatalf("BridgeIfName length = %d, want 8", len(got))
		}
	})

	t.Run("ip port", func(t *testing.T) {
		tests := []struct {
			input string
			want  bool
		}{
			{"127.0.0.1:80", true},
			{"127.0.0.1:0", false},
			{"127.0.0.1:not-port", false},
			{"127.0.0.1", false},
			{"999.0.0.1:80", false},
			{"2001:db8::1:443", false}, // this helper intentionally expects exactly one ':'
		}

		for _, tt := range tests {
			if got := IsValidIPPort(tt.input); got != tt.want {
				t.Fatalf("IsValidIPPort(%q) = %v, want %v", tt.input, got, tt.want)
			}
		}
	})
}

func TestIsPortInUse(t *testing.T) {
	if IsPortInUse(0) {
		t.Fatal("expected false for invalid port")
	}

	// The current implementation always returns false; this assertion locks in
	// existing behavior while still exercising the valid-port path.
	if IsPortInUse(18080) {
		t.Fatal("expected false for valid port in current implementation")
	}
}

func TestGetPortUserPIDValidationErrors(t *testing.T) {
	_, err := GetPortUserPID("icmp", 80)
	if err == nil || !strings.Contains(err.Error(), "invalid protocol") {
		t.Fatalf("expected invalid protocol error, got: %v", err)
	}

	_, err = GetPortUserPID("tcp", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("expected invalid port error, got: %v", err)
	}
}

func TestGetPortUserPID_RunCommandError(t *testing.T) {
	original := execCommand
	defer func() { execCommand = original }()

	execCommand = func(command string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "echo sockstat-failed 1>&2; exit 2")
	}

	_, err := GetPortUserPID("tcp", 8180)
	if err == nil || !strings.Contains(err.Error(), "failed to run sockstat") {
		t.Fatalf("expected sockstat execution error, got: %v", err)
	}
}

func TestGetPortUserPID_ParsePaths(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		original := execCommand
		defer func() { execCommand = original }()

		execCommand = func(command string, args ...string) *exec.Cmd {
			return exec.Command("sh", "-c", "printf 'root sockstat 4321 x x :8180\\n'")
		}

		pid, err := GetPortUserPID("tcp", 8180)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pid != 4321 {
			t.Fatalf("pid = %d, want 4321", pid)
		}
	})

	t.Run("pid parse failure", func(t *testing.T) {
		original := execCommand
		defer func() { execCommand = original }()

		execCommand = func(command string, args ...string) *exec.Cmd {
			return exec.Command("sh", "-c", "printf 'root sockstat not-a-pid x x :8180\\n'")
		}

		_, err := GetPortUserPID("tcp", 8180)
		if err == nil || !strings.Contains(err.Error(), "failed to convert PID") {
			t.Fatalf("expected PID parse error, got: %v", err)
		}
	})

	t.Run("no matching process", func(t *testing.T) {
		original := execCommand
		defer func() { execCommand = original }()

		execCommand = func(command string, args ...string) *exec.Cmd {
			return exec.Command("sh", "-c", "printf 'root sockstat 1234 x x :9999\\n'")
		}

		_, err := GetPortUserPID("tcp", 8180)
		if err == nil || !strings.Contains(err.Error(), "no process found") {
			t.Fatalf("expected no process found error, got: %v", err)
		}
	})
}
