// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
)

func testClusterCert(t *testing.T) tls.Certificate {
	t.Helper()
	certPEM, keyPEM, err := sylveCrypto.GenerateClusterCertificate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestCertPinSurvivesReissueWithTheSameKey(t *testing.T) {
	cert := testClusterCert(t)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	template := *leaf
	template.NotAfter = leaf.NotAfter.Add(24 * time.Hour)
	signer := cert.PrivateKey.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	reissued, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if CertPin(reissued) != CertPin(leaf) {
		t.Fatal("reissuing with the same key changed the pin")
	}
	if other := testClusterCert(t); CertPin(other.Leaf) == CertPin(leaf) {
		t.Fatal("two keys share a pin")
	}
}

func TestPinSetFollowsIdentityRows(t *testing.T) {
	db := newClusterServiceTestDB(t)
	service := NewClusterService(db, nil, nil).(*Service)
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterCert(testClusterCert(t)); err != nil {
		t.Fatal(err)
	}
	pin := CertPin(service.clusterCert.Leaf)
	if _, ok := service.PinNode(pin); ok {
		t.Fatal("an unpublished pin is trusted")
	}

	if err := service.UpsertClusterSSHIdentity(t.Context(), clusterModels.ClusterSSHIdentity{
		NodeUUID: "node-a", SSHHost: "10.0.0.1", PublicKey: "ssh-ed25519 AAAA", CertPin: service.localCertPin(),
	}); err != nil {
		t.Fatal(err)
	}
	if node, ok := service.PinNode(pin); !ok || node != "node-a" {
		t.Fatalf("published pin maps to %q (%t), want node-a", node, ok)
	}
	if node, ok := NewClusterService(db, nil, nil).(*Service).PinNode(pin); !ok || node != "node-a" {
		t.Fatalf("a fresh start maps the pin to %q (%t), want node-a", node, ok)
	}

	if err := service.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: "node-a"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.PinNode(pin); ok {
		t.Fatal("a removed member's pin is still trusted")
	}
}
