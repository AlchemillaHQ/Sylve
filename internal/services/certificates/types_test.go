// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestValidateServerMaterial(t *testing.T) {
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		notBefore time.Time
		notAfter  time.Time
		usages    []x509.ExtKeyUsage
		wantError bool
	}{
		{name: "valid unrestricted", notBefore: now.Add(-time.Hour), notAfter: now.Add(time.Hour)},
		{name: "future", notBefore: now.Add(time.Minute), notAfter: now.Add(time.Hour), wantError: true},
		{name: "expired", notBefore: now.Add(-2 * time.Hour), notAfter: now, wantError: true},
		{name: "client only", notBefore: now.Add(-time.Hour), notAfter: now.Add(time.Hour), usages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificatePEM, privateKeyPEM := makeTestCertificate(t, "node.example.com", test.notBefore, test.notAfter, test.usages)
			material, err := parseCertificateMaterial(certificatePEM, privateKeyPEM)
			if err != nil {
				t.Fatalf("parse certificate: %v", err)
			}
			err = validateServerMaterial(material, "node.example.com", now, true)
			if test.wantError && err == nil {
				t.Fatal("expected validation error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("validate certificate: %v", err)
			}
		})
	}
}

func TestParseCertificateMaterialRejectsTrailingDataAndOversizedInput(t *testing.T) {
	now := time.Now()
	certificatePEM, privateKeyPEM := makeTestCertificate(t, "node.example.com", now.Add(-time.Hour), now.Add(time.Hour), nil)
	if _, err := parseCertificateMaterial(certificatePEM+"trailing-data", privateKeyPEM); err == nil {
		t.Fatal("expected trailing certificate data to be rejected")
	}
	if err := validateCertificateInputSize(CertificateInput{Certificate: strings.Repeat("x", maxPEMBytes+1)}); err == nil {
		t.Fatal("expected oversized certificate input to be rejected")
	}
}

func makeTestCertificate(t *testing.T, domain string, notBefore, notAfter time.Time, usages []x509.ExtKeyUsage) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}))
}
