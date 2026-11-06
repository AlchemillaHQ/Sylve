// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/hashicorp/raft"
)

// Both ends accept the other only if its pin is a member's, so a removed node
// is cut off at once.
type raftStream struct {
	net.Listener
	client *tls.Config
}

func (s *Service) newRaftStream(listener net.Listener) *raftStream {
	server := s.ServerTLSConfig()
	server.ClientAuth = tls.RequireAnyClientCert
	server.VerifyConnection = s.verifyMemberPeer
	return &raftStream{Listener: tls.NewListener(listener, server), client: s.PeerTLSConfig()}
}

func (r *raftStream) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", string(address), r.client)
}
