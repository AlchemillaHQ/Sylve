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
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

// The pin is the issuing member's, which the joiner checks before it sends anything.
type JoinKey struct {
	Secret string
	Pin    [sha256.Size]byte
}

func (k JoinKey) String() string {
	return k.Secret + "." + hex.EncodeToString(k.Pin[:])
}

func ParseJoinKey(value string) (JoinKey, error) {
	secret, pin, _ := strings.Cut(strings.TrimSpace(value), ".")
	key := JoinKey{Secret: secret}
	var ok bool
	if key.Pin, ok = parseCertPin(pin); secret == "" || !ok {
		return JoinKey{}, fmt.Errorf("cluster_join_key_invalid")
	}
	return key, nil
}

func (s *Service) GenerateJoinKey(ctx context.Context) (key JoinKey, address string, err error) {
	record, err := s.loadClusterRecord()
	if err != nil {
		return JoinKey{}, "", err
	}
	if !record.Enabled || s.clusterCert == nil {
		return JoinKey{}, "", fmt.Errorf("cluster_join_key_unavailable")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return JoinKey{}, "", err
	}
	key = JoinKey{Secret: base64.RawURLEncoding.EncodeToString(secret), Pin: CertPin(s.clusterCert.Leaf)}
	if err := s.Propose(ctx, "cluster_join_key", "set", clusterModels.ClusterJoinKey{
		Hash: clusterModels.JoinKeyHash(key.Secret),
	}); err != nil {
		return JoinKey{}, "", err
	}
	return key, record.RaftIP, nil
}

func (k JoinKey) VerifyPin(state tls.ConnectionState) error {
	if len(state.PeerCertificates) == 0 || CertPin(state.PeerCertificates[0]) != k.Pin {
		return fmt.Errorf("cluster_join_key_node_mismatch")
	}
	return nil
}
