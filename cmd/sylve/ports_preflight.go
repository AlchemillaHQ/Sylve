// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type portRequirement struct {
	role string
	port int
}

type portBinder func(ip string, port int, proto string) error

func preflightRequiredPorts(cfg *internal.SylveConfig, binder portBinder) error {
	reqs, err := buildPortRequirements(cfg)
	if err != nil {
		return err
	}

	for _, req := range reqs {
		if err := binder("", req.port, "tcp"); err != nil {
			return fmt.Errorf("required_port_not_bindable role=%s port=%d: %w", req.role, req.port, err)
		}
	}

	return nil
}

func buildPortRequirements(cfg *internal.SylveConfig) ([]portRequirement, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config_required")
	}

	reqs := make([]portRequirement, 0, 5)

	if cfg.HTTPPort != 0 {
		if !utils.IsValidPort(cfg.HTTPPort) {
			return nil, fmt.Errorf("invalid_http_port: %d", cfg.HTTPPort)
		}
		reqs = append(reqs, portRequirement{role: "http", port: cfg.HTTPPort})
	}

	if cfg.Port != 0 {
		if !utils.IsValidPort(cfg.Port) {
			return nil, fmt.Errorf("invalid_https_port: %d", cfg.Port)
		}
		reqs = append(reqs, portRequirement{role: "https", port: cfg.Port})
	}

	reqs = append(reqs,
		portRequirement{role: "cluster_ssh", port: cluster.ClusterEmbeddedSSHPort},
		portRequirement{role: "raft", port: cluster.ClusterRaftPort},
		portRequirement{role: "cluster_https", port: cluster.ClusterEmbeddedHTTPSPort},
	)

	for _, req := range reqs {
		if !utils.IsValidPort(req.port) {
			return nil, fmt.Errorf("invalid_required_port role=%s port=%d", req.role, req.port)
		}
	}

	roleByPort := make(map[int][]string, len(reqs))
	for _, req := range reqs {
		roleByPort[req.port] = append(roleByPort[req.port], req.role)
	}

	ports := make([]int, 0, len(roleByPort))
	for port := range roleByPort {
		ports = append(ports, port)
	}
	sort.Ints(ports)

	conflicts := make([]string, 0)
	for _, port := range ports {
		roles := roleByPort[port]
		if len(roles) <= 1 {
			continue
		}
		sort.Strings(roles)
		conflicts = append(conflicts, fmt.Sprintf("port=%d roles=%s", port, strings.Join(roles, ",")))
	}

	if len(conflicts) > 0 {
		return nil, fmt.Errorf("port_role_collision: %s", strings.Join(conflicts, "; "))
	}

	return reqs, nil
}
