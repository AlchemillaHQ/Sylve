// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

const (
	ClusterRaftPort          = 8180
	ClusterEmbeddedSSHPort   = 8183
	ClusterEmbeddedHTTPSPort = 8184
)

func ClusterAPIHost(ip string) string {
	return net.JoinHostPort(strings.TrimSpace(ip), strconv.Itoa(ClusterEmbeddedHTTPSPort))
}

func RaftServerAddress(ip string) string {
	return net.JoinHostPort(strings.TrimSpace(ip), strconv.Itoa(ClusterRaftPort))
}

func normalizeClusterIPv4(value string, invalidCode string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return "", errors.New(invalidCode)
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return "", errors.New("cluster_ipv6_unsupported")
	}
	return ipv4.String(), nil
}
