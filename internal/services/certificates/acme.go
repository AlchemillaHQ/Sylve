// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"golang.org/x/crypto/acme"
	"gorm.io/gorm"
)

const (
	letsEncryptStagingURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	acmeAccountProduction = "public_tls_acme_account_production"
	acmeAccountStaging    = "public_tls_acme_account_staging"
	acmeIssuanceTimeout   = 5 * time.Minute
)

type challengeManager struct {
	mu           sync.RWMutex
	certificates map[string]*tls.Certificate
}

func newChallengeManager() *challengeManager {
	return &challengeManager{certificates: make(map[string]*tls.Certificate)}
}

func (m *challengeManager) set(domain string, certificate *tls.Certificate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.certificates[strings.ToLower(strings.TrimSuffix(domain, "."))] = certificate
}

func (m *challengeManager) remove(domain string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.certificates, strings.ToLower(strings.TrimSuffix(domain, ".")))
}

func (m *challengeManager) get(hello *tls.ClientHelloInfo) *tls.Certificate {
	if hello == nil || !containsProtocol(hello.SupportedProtos, acme.ALPNProto) {
		return nil
	}
	domain := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.certificates[domain]
}

func containsProtocol(protocols []string, expected string) bool {
	for _, protocol := range protocols {
		if protocol == expected {
			return true
		}
	}
	return false
}

func (s *Service) obtainLetsEncryptCertificate(ctx context.Context, domain string, staging bool) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, acmeIssuanceTimeout)
	defer cancel()

	accountKey, err := s.loadOrCreateACMEAccountKey(ctx, staging)
	if err != nil {
		return nil, nil, err
	}
	directoryURL := acme.LetsEncryptURL
	if staging {
		directoryURL = letsEncryptStagingURL
	}
	client := &acme.Client{Key: accountKey, DirectoryURL: directoryURL}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, nil, fmt.Errorf("register ACME account: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return nil, nil, fmt.Errorf("create ACME order: %w", err)
	}
	for _, authorizationURL := range order.AuthzURLs {
		authorization, err := client.GetAuthorization(ctx, authorizationURL)
		if err != nil {
			return nil, nil, fmt.Errorf("load ACME authorization: %w", err)
		}
		if authorization.Status == acme.StatusValid {
			continue
		}

		var challenge *acme.Challenge
		for _, candidate := range authorization.Challenges {
			if candidate.Type == "tls-alpn-01" {
				challenge = candidate
				break
			}
		}
		if challenge == nil {
			return nil, nil, fmt.Errorf("ACME server did not offer tls-alpn-01 for %s", domain)
		}

		challengeCertificate, err := client.TLSALPN01ChallengeCert(challenge.Token, domain)
		if err != nil {
			return nil, nil, fmt.Errorf("create TLS-ALPN-01 challenge certificate: %w", err)
		}
		s.challenges.set(domain, &challengeCertificate)
		challengeErr := func() error {
			defer s.challenges.remove(domain)
			if _, err := client.Accept(ctx, challenge); err != nil {
				return fmt.Errorf("accept TLS-ALPN-01 challenge: %w", err)
			}
			if _, err := client.WaitAuthorization(ctx, authorizationURL); err != nil {
				return fmt.Errorf("complete TLS-ALPN-01 challenge: %w", err)
			}
			return nil
		}()
		if challengeErr != nil {
			return nil, nil, challengeErr
		}
	}
	readyOrder, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, nil, fmt.Errorf("wait for ACME order: %w", err)
	}
	if readyOrder.Status != acme.StatusReady {
		return nil, nil, fmt.Errorf("ACME order reached unexpected status %q before finalization", readyOrder.Status)
	}

	certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate certificate private key: %w", err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{domain}}, certificateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate request: %w", err)
	}
	chain, _, err := client.CreateOrderCert(ctx, readyOrder.FinalizeURL, request, true)
	if err != nil {
		return nil, nil, fmt.Errorf("finalize ACME order: %w", err)
	}
	if len(chain) == 0 {
		return nil, nil, fmt.Errorf("ACME server returned an empty certificate chain")
	}

	var certificatePEM []byte
	for index, certificate := range chain {
		if _, err := x509.ParseCertificate(certificate); err != nil {
			return nil, nil, fmt.Errorf("parse ACME certificate %d: %w", index, err)
		}
		certificatePEM = append(certificatePEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})...)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(certificateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode certificate private key: %w", err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		return nil, nil, fmt.Errorf("validate ACME certificate: %w", err)
	}
	if err := validateServerMaterial(material, domain, s.currentTime(), true); err != nil {
		return nil, nil, fmt.Errorf("validate ACME certificate: %w", err)
	}
	return certificatePEM, privateKeyPEM, nil
}

func (s *Service) loadOrCreateACMEAccountKey(ctx context.Context, staging bool) (crypto.Signer, error) {
	secretName := acmeAccountProduction
	if staging {
		secretName = acmeAccountStaging
	}

	var secret models.SystemSecrets
	err := s.DB.WithContext(ctx).Where("name = ?", secretName).First(&secret).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		key, generateErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if generateErr != nil {
			return nil, fmt.Errorf("generate ACME account key: %w", generateErr)
		}
		encoded, marshalErr := x509.MarshalPKCS8PrivateKey(key)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode ACME account key: %w", marshalErr)
		}
		secret = models.SystemSecrets{
			Name: secretName,
			Data: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})),
		}
		if createErr := s.DB.WithContext(ctx).Create(&secret).Error; createErr != nil {
			return nil, fmt.Errorf("save ACME account key: %w", createErr)
		}
		return key, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load ACME account key: %w", err)
	}

	block, _ := pem.Decode([]byte(secret.Data))
	if block == nil {
		return nil, fmt.Errorf("stored ACME account key is invalid")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ACME account key: %w", err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("stored ACME account key is not a signing key")
	}
	return signer, nil
}
