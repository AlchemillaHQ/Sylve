// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
)

func TestBuildPortRequirementsIncludesConfiguredPorts(t *testing.T) {
	cfg := &internal.SylveConfig{
		IP:       "192.168.1.1",
		Port:     8181,
		HTTPPort: 8182,
	}

	reqs, err := buildPortRequirements(cfg)
	if err != nil {
		t.Fatalf("buildPortRequirements returned error: %v", err)
	}

	rolesByPort := map[int]portRequirement{}
	for _, req := range reqs {
		rolesByPort[req.port] = req
	}

	expected := map[int]portRequirement{
		8181: {role: "https", ip: "192.168.1.1", port: 8181},
		8182: {role: "http", ip: "192.168.1.1", port: 8182},
	}

	if len(rolesByPort) != len(expected) {
		t.Fatalf("expected %d unique ports, got %d", len(expected), len(rolesByPort))
	}

	for port, want := range expected {
		got, ok := rolesByPort[port]
		if !ok {
			t.Fatalf("missing required role %q on port %d", want.role, port)
		}
		if got.role != want.role {
			t.Fatalf("unexpected role for port %d: expected %q got %q", port, want.role, got.role)
		}
		if got.ip != want.ip {
			t.Fatalf("unexpected ip for role %q port %d: expected %q got %q", want.role, port, want.ip, got.ip)
		}
	}
}

func TestBuildPortRequirementsAllowsDisabledHTTPAndHTTPS(t *testing.T) {
	cfg := &internal.SylveConfig{Port: 0, HTTPPort: 0}

	reqs, err := buildPortRequirements(cfg)
	if err != nil {
		t.Fatalf("buildPortRequirements returned error: %v", err)
	}

	if len(reqs) != 0 {
		t.Fatalf("expected no port requirements when HTTP/HTTPS disabled, got %d", len(reqs))
	}
}

func TestBuildPortRequirementsDetectsRoleCollision(t *testing.T) {
	cfg := &internal.SylveConfig{
		Port:     8182,
		HTTPPort: 8182,
	}

	_, err := buildPortRequirements(cfg)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}

	if !strings.Contains(err.Error(), "port_role_collision") {
		t.Fatalf("expected port_role_collision error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "http") || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected collision error to include colliding roles, got: %v", err)
	}
}

func TestPreflightRequiredPortsFailsOnBindError(t *testing.T) {
	cfg := &internal.SylveConfig{
		IP:       "192.168.1.1",
		Port:     8181,
		HTTPPort: 8182,
	}

	err := preflightRequiredPorts(cfg, func(ip string, port int, proto string) error {
		if proto != "tcp" {
			t.Fatalf("expected tcp bind checks, got %q", proto)
		}
		if port == 8181 {
			return errors.New("already in use")
		}
		return nil
	})

	if err == nil {
		t.Fatal("expected preflight bind error, got nil")
	}
	if !strings.Contains(err.Error(), "role=https") || !strings.Contains(err.Error(), "port=8181") {
		t.Fatalf("expected role-specific bind failure, got: %v", err)
	}
}

func TestPreflightRequiredPortsChecksAllExpectedRoles(t *testing.T) {
	cfg := &internal.SylveConfig{
		IP:       "192.168.1.1",
		Port:     8181,
		HTTPPort: 8182,
	}

	called := map[int]struct{}{}
	err := preflightRequiredPorts(cfg, func(ip string, port int, proto string) error {
		if proto != "tcp" {
			t.Fatalf("expected tcp bind checks, got %q", proto)
		}
		if ip != "192.168.1.1" {
			t.Fatalf("expected cfg.IP for port %d, got %q", port, ip)
		}
		called[port] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("preflightRequiredPorts returned error: %v", err)
	}

	for _, expectedPort := range []int{8181, 8182} {
		if _, ok := called[expectedPort]; !ok {
			t.Fatalf("missing bind check for port %d", expectedPort)
		}
	}
}

func TestManagementHTTPServerAddresses(t *testing.T) {
	cases := []struct {
		name      string
		ip        string
		httpsAddr string
		httpAddr  string
	}{
		{name: "IPv4 loopback", ip: "127.0.0.1", httpsAddr: "127.0.0.1:8181", httpAddr: "127.0.0.1:8182"},
		{name: "IPv4 wildcard", ip: "0.0.0.0", httpsAddr: "0.0.0.0:8181", httpAddr: "0.0.0.0:8182"},
		{name: "empty host", ip: "", httpsAddr: ":8181", httpAddr: ":8182"},
		{name: "IPv6 loopback", ip: "::1", httpsAddr: "[::1]:8181", httpAddr: "[::1]:8182"},
		{name: "IPv6 wildcard", ip: "::", httpsAddr: "[::]:8181", httpAddr: "[::]:8182"},
		{name: "IPv6 address", ip: "2001:db8::10", httpsAddr: "[2001:db8::10]:8181", httpAddr: "[2001:db8::10]:8182"},
		{name: "scoped IPv6", ip: "fe80::1%em0", httpsAddr: "[fe80::1%em0]:8181", httpAddr: "[fe80::1%em0]:8182"},
		{name: "IPv4-mapped IPv6", ip: "::ffff:127.0.0.1", httpsAddr: "[::ffff:127.0.0.1]:8181", httpAddr: "[::ffff:127.0.0.1]:8182"},
	}
	handler := http.NewServeMux()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &internal.SylveConfig{IP: tc.ip, Port: 8181, HTTPPort: 8182}
			httpsServer, httpServer := newManagementHTTPServers(cfg, handler, tlsConfig)
			if httpsServer.Addr != tc.httpsAddr {
				t.Errorf("HTTPS address = %q, want %q", httpsServer.Addr, tc.httpsAddr)
			}
			if httpServer.Addr != tc.httpAddr {
				t.Errorf("HTTP address = %q, want %q", httpServer.Addr, tc.httpAddr)
			}
			if httpsServer.Handler != handler || httpServer.Handler != handler {
				t.Error("expected both servers to use the supplied handler")
			}
			if httpsServer.TLSConfig != tlsConfig {
				t.Error("expected HTTPS server to use the supplied TLS configuration")
			}
			if httpServer.TLSConfig != nil {
				t.Error("expected HTTP server to have no TLS configuration")
			}
		})
	}
}
