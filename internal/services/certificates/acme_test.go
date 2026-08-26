// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"crypto/tls"
	"testing"

	"golang.org/x/crypto/acme"
)

func TestChallengeManagerRequiresMatchingSNIAndALPN(t *testing.T) {
	manager := newChallengeManager()
	certificate := &tls.Certificate{}
	manager.set("Example.COM", certificate)

	if got := manager.get(&tls.ClientHelloInfo{
		ServerName:      "example.com",
		SupportedProtos: []string{acme.ALPNProto},
	}); got != certificate {
		t.Fatal("expected matching challenge certificate")
	}
	if got := manager.get(&tls.ClientHelloInfo{
		ServerName:      "other.example.com",
		SupportedProtos: []string{acme.ALPNProto},
	}); got != nil {
		t.Fatal("unexpected challenge certificate for another hostname")
	}
	if got := manager.get(&tls.ClientHelloInfo{
		ServerName:      "example.com",
		SupportedProtos: []string{"h2"},
	}); got != nil {
		t.Fatal("unexpected challenge certificate without ACME ALPN")
	}

	manager.remove("EXAMPLE.com")
	if got := manager.get(&tls.ClientHelloInfo{
		ServerName:      "example.com",
		SupportedProtos: []string{acme.ALPNProto},
	}); got != nil {
		t.Fatal("challenge certificate was not removed")
	}
}
