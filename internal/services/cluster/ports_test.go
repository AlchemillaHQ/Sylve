// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"net"
	"strconv"
	"testing"
)

func TestRaftServerAddressUsesFixedPort(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{name: "ipv4", ip: "10.20.30.40"},
		{name: "ipv6", ip: "::1"},
		{name: "trimmed", ip: " 192.168.1.50 "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := RaftServerAddress(tt.ip)
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("SplitHostPort failed for %q: %v", addr, err)
			}
			if port != strconv.Itoa(ClusterRaftPort) {
				t.Fatalf("expected raft port %d, got %s", ClusterRaftPort, port)
			}
			if host == "" {
				t.Fatal("expected non-empty host")
			}
		})
	}
}

func TestClusterAPIHostUsesFixedHTTPSPort(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{name: "ipv4", ip: "10.20.30.41"},
		{name: "ipv6", ip: "fd00::abcd"},
		{name: "trimmed", ip: " 192.168.1.51 "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostPort := ClusterAPIHost(tt.ip)
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				t.Fatalf("SplitHostPort failed for %q: %v", hostPort, err)
			}
			if port != strconv.Itoa(ClusterEmbeddedHTTPSPort) {
				t.Fatalf("expected cluster API port %d, got %s", ClusterEmbeddedHTTPSPort, port)
			}
			if host == "" {
				t.Fatal("expected non-empty host")
			}
		})
	}
}
