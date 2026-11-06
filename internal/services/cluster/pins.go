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
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
)

// The pin covers only the SubjectPublicKeyInfo, so it outlives expiry and readdress.
func CertPin(cert *x509.Certificate) [sha256.Size]byte {
	return sha256.Sum256(cert.RawSubjectPublicKeyInfo)
}

func (s *Service) SetClusterCert(cert tls.Certificate) error {
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("cluster_cert_empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("cluster_cert_invalid: %w", err)
	}
	cert.Leaf = leaf
	s.clusterCert = &cert
	return nil
}

func (s *Service) localCertPin() string {
	if s.clusterCert == nil {
		return ""
	}
	pin := CertPin(s.clusterCert.Leaf)
	return hex.EncodeToString(pin[:])
}

func (s *Service) trackPins(fsm *clusterModels.FSMDispatcher) {
	fsm.AfterApply("cluster_ssh_identity", s.reloadPins)
	fsm.AfterApply("cluster_member", s.reloadPins)
	s.reloadPins()
}

// TLS handshakes read this cached set because they must never wait on SQLite:
// a long Apply holds its only connection.
func (s *Service) reloadPins() {
	var rows []clusterModels.ClusterSSHIdentity
	if err := s.DB.Select("node_uuid", "cert_pin").Where("cert_pin <> ''").Find(&rows).Error; err != nil {
		logger.L.Error().Err(err).Msg("cluster_pins_reload_failed")
		return
	}
	pins := make(map[[sha256.Size]byte]string, len(rows))
	caughtUp := false
	for _, row := range rows {
		if pin, ok := parseCertPin(row.CertPin); ok {
			pins[pin] = row.NodeUUID
		}
		caughtUp = caughtUp || row.NodeUUID == s.LocalNodeID()
	}
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	if caughtUp {
		s.joinPins = nil
	}
	for pin, nodeID := range s.joinPins {
		pins[pin] = nodeID
	}
	s.pins.Store(&pins)
}

// A joiner's replicated state starts empty, so without these it would turn away
// the leader bringing it the members. It needs them until its state holds its
// own identity, which the leader recorded after every member's.
func (s *Service) seedJoinPins(pins map[string]string) {
	seed := make(map[[sha256.Size]byte]string, len(pins))
	for nodeID, value := range pins {
		if pin, ok := parseCertPin(value); ok {
			seed[pin] = nodeID
		}
	}
	s.pinsMu.Lock()
	s.joinPins = seed
	s.pinsMu.Unlock()
	s.reloadPins()
}

func (s *Service) memberPins(ctx context.Context) (map[string]string, error) {
	var rows []clusterModels.ClusterSSHIdentity
	if err := s.DB.WithContext(ctx).Select("node_uuid", "cert_pin").Where("cert_pin <> ''").Find(&rows).Error; err != nil {
		return nil, err
	}
	pins := make(map[string]string, len(rows))
	for _, row := range rows {
		pins[row.NodeUUID] = row.CertPin
	}
	return pins, nil
}

func parseCertPin(value string) ([sha256.Size]byte, bool) {
	var pin [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(pin) {
		return pin, false
	}
	copy(pin[:], decoded)
	return pin, true
}

func (s *Service) PinNode(pin [sha256.Size]byte) (string, bool) {
	pins := s.pins.Load()
	if pins == nil {
		return "", false
	}
	nodeID, ok := (*pins)[pin]
	return nodeID, ok
}
