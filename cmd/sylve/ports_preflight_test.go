// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestBuildPortRequirementsIncludesConfiguredAndFixedPorts(t *testing.T) {
	cfg := &internal.SylveConfig{
		Port:     8181,
		HTTPPort: 8182,
	}

	reqs, err := buildPortRequirements(cfg)
	if err != nil {
		t.Fatalf("buildPortRequirements returned error: %v", err)
	}

	rolesByPort := map[int]string{}
	for _, req := range reqs {
		rolesByPort[req.port] = req.role
	}

	expected := map[int]string{
		8181:                             "https",
		8182:                             "http",
		cluster.ClusterRaftPort:          "raft",
		cluster.ClusterEmbeddedSSHPort:   "cluster_ssh",
		cluster.ClusterEmbeddedHTTPSPort: "cluster_https",
	}

	if len(rolesByPort) != len(expected) {
		t.Fatalf("expected %d unique ports, got %d", len(expected), len(rolesByPort))
	}

	for port, role := range expected {
		gotRole, ok := rolesByPort[port]
		if !ok {
			t.Fatalf("missing required role %q on port %d", role, port)
		}
		if gotRole != role {
			t.Fatalf("unexpected role for port %d: expected %q got %q", port, role, gotRole)
		}
	}
}

func TestBuildPortRequirementsAllowsDisabledHTTPAndHTTPS(t *testing.T) {
	cfg := &internal.SylveConfig{Port: 0, HTTPPort: 0}

	reqs, err := buildPortRequirements(cfg)
	if err != nil {
		t.Fatalf("buildPortRequirements returned error: %v", err)
	}

	if len(reqs) != 3 {
		t.Fatalf("expected only fixed cluster ports when HTTP/HTTPS disabled, got %d", len(reqs))
	}

	ports := map[int]struct{}{}
	for _, req := range reqs {
		ports[req.port] = struct{}{}
	}

	for _, requiredPort := range []int{cluster.ClusterRaftPort, cluster.ClusterEmbeddedSSHPort, cluster.ClusterEmbeddedHTTPSPort} {
		if _, ok := ports[requiredPort]; !ok {
			t.Fatalf("missing required fixed port %d", requiredPort)
		}
	}
}

func TestBuildPortRequirementsDetectsRoleCollision(t *testing.T) {
	cfg := &internal.SylveConfig{
		Port:     cluster.ClusterEmbeddedHTTPSPort,
		HTTPPort: 8182,
	}

	_, err := buildPortRequirements(cfg)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}

	if !strings.Contains(err.Error(), "port_role_collision") {
		t.Fatalf("expected port_role_collision error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cluster_https") || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected collision error to include colliding roles, got: %v", err)
	}
}

func TestPreflightRequiredPortsFailsOnBindError(t *testing.T) {
	cfg := &internal.SylveConfig{
		Port:     8181,
		HTTPPort: 8182,
	}

	err := preflightRequiredPorts(cfg, func(_ string, port int, proto string) error {
		if proto != "tcp" {
			t.Fatalf("expected tcp bind checks, got %q", proto)
		}
		if port == cluster.ClusterRaftPort {
			return errors.New("already in use")
		}
		return nil
	})

	if err == nil {
		t.Fatal("expected preflight bind error, got nil")
	}
	if !strings.Contains(err.Error(), "role=raft") || !strings.Contains(err.Error(), "port=8180") {
		t.Fatalf("expected role-specific bind failure, got: %v", err)
	}
}

func TestPreflightRequiredPortsChecksAllExpectedRoles(t *testing.T) {
	cfg := &internal.SylveConfig{
		Port:     8181,
		HTTPPort: 8182,
	}

	called := map[int]struct{}{}
	err := preflightRequiredPorts(cfg, func(_ string, port int, proto string) error {
		if proto != "tcp" {
			t.Fatalf("expected tcp bind checks, got %q", proto)
		}
		called[port] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("preflightRequiredPorts returned error: %v", err)
	}

	for _, expectedPort := range []int{8181, 8182, cluster.ClusterRaftPort, cluster.ClusterEmbeddedSSHPort, cluster.ClusterEmbeddedHTTPSPort} {
		if _, ok := called[expectedPort]; !ok {
			t.Fatalf("missing bind check for port %d", expectedPort)
		}
	}
}
