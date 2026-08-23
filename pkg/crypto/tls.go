// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

func GenerateSelfSignedCertificate() (certPEM, keyPEM []byte, err error) {
	return generateSelfSignedCertificate(
		"Sylve Self-Signed Certificate",
		[]string{"sylve.lan", "localhost"},
		[]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	)
}

func GenerateSelfSignedCertificateForDomain(domain string) (certPEM, keyPEM []byte, err error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, nil, fmt.Errorf("domain is required")
	}

	var dnsNames []string
	var ipAddresses []net.IP
	if ip := net.ParseIP(domain); ip != nil {
		ipAddresses = []net.IP{ip}
	} else {
		dnsNames = []string{domain}
	}

	return generateSelfSignedCertificate("Sylve Self-Signed Certificate", dnsNames, ipAddresses)
}

func GenerateClusterCertificate() (certPEM, keyPEM []byte, err error) {
	return generateSelfSignedCertificate(
		"Sylve Internal Cluster Certificate",
		[]string{"sylve-cluster.internal"},
		nil,
	)
}

func generateSelfSignedCertificate(organization string, dnsNames []string, ipAddresses []net.IP) (certPEM, keyPEM []byte, err error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour)
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{Organization: []string{organization}},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  false,
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	privBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %v", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return certPEM, keyPEM, nil
}
