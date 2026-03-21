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
