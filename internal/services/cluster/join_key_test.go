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
	"crypto/x509"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseJoinKey(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	key, err := ParseJoinKey(" secret." + pin + "\n")
	if err != nil || key.Secret != "secret" || hex.EncodeToString(key.Pin[:]) != pin || key.String() != "secret."+pin {
		t.Fatalf("key = %+v, %v", key, err)
	}
	for _, value := range []string{
		"", "secret", "secret" + pin, "." + pin, "secret.", "secret." + pin[:62], "secret." + pin + "ab", "secret.zz" + pin[2:],
	} {
		if _, err := ParseJoinKey(value); err == nil || err.Error() != "cluster_join_key_invalid" {
			t.Errorf("%q: %v", value, err)
		}
	}
}

func TestJoinKeyVerifyPin(t *testing.T) {
	issuer, other := testClusterCert(t).Leaf, testClusterCert(t).Leaf
	key := JoinKey{Secret: "secret", Pin: CertPin(issuer)}
	if err := key.VerifyPin(tls.ConnectionState{PeerCertificates: []*x509.Certificate{issuer, other}}); err != nil {
		t.Fatalf("the issuing node: %v", err)
	}
	for _, chain := range [][]*x509.Certificate{nil, {other}, {other, issuer}} {
		if err := key.VerifyPin(tls.ConnectionState{PeerCertificates: chain}); err == nil ||
			err.Error() != "cluster_join_key_node_mismatch" {
			t.Errorf("chain of %d: %v", len(chain), err)
		}
	}
}
