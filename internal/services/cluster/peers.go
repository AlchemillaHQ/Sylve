// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
)

// A client cert is asked for but not required: a joining node isn't pinned yet.
func (s *Service) ServerTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{*s.clusterCert},
		ClientAuth:   tls.RequestClientCert,
	}
}

// The member pin check stands in for the chain and name checks.
func (s *Service) PeerTLSConfig() *tls.Config {
	config := s.JoinTLSConfig()
	config.VerifyConnection = s.verifyMemberPeer
	return config
}

func (s *Service) verifyMemberPeer(state tls.ConnectionState) error {
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("cluster_peer_cert_missing")
	}
	if _, ok := s.PinNode(CertPin(state.PeerCertificates[0])); !ok {
		return fmt.Errorf("cluster_peer_not_member")
	}
	return nil
}

// JoinTLSConfig is for calls before this node knows the members' pins. It
// presents this node's cert, which the leader records as its pin.
func (s *Service) JoinTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{*s.clusterCert},
		InsecureSkipVerify: true,
	}
}

type peerNodeKey struct{}

func (s *Service) PeerHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			if nodeID, ok := s.PinNode(CertPin(r.TLS.PeerCertificates[0])); ok {
				r = r.WithContext(context.WithValue(r.Context(), peerNodeKey{}, nodeID))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func PeerNode(r *http.Request) string {
	nodeID, _ := r.Context().Value(peerNodeKey{}).(string)
	return nodeID
}

// The leader trusts JoinerPinHeader only from a member forwarding a joiner's admission.
const JoinerPinHeader = "X-Sylve-Joiner-Pin"
