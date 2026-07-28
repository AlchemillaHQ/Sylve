// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/asaskevich/govalidator"
)

var (
	ErrInvalidCertificate  = errors.New("invalid certificate")
	ErrCertificateNotFound = errors.New("certificate not found")
	ErrCertificateConflict = errors.New("certificate conflict")
	ErrNotRenewable        = errors.New("certificate is not renewable")
	ErrRenewalNotDue       = errors.New("certificate renewal is not due")
	ErrIssuanceFailed      = errors.New("certificate issuance failed")
	ErrDomainCheckFailed   = errors.New("domain check failed")
)

const (
	defaultCertificateName   = "System Default"
	defaultCertificateDomain = "sylve.lan"
	legacyMigrationMarker    = "migrate_legacy_tls_config_to_certificates_v1"
	renewalWindow            = 30 * 24 * time.Hour
	maxPEMBytes              = 1 << 20
	// MaxRequestBodyBytes allows two PEM fields plus JSON framing.
	MaxRequestBodyBytes = 2*maxPEMBytes + 64*1024
)

type CertificateInput struct {
	Name              string                 `json:"name"`
	Type              models.CertificateType `json:"type"`
	Domain            string                 `json:"domain"`
	DynamicDNSEntryID *uint                  `json:"dynamicDnsEntryId"`
	Staging           bool                   `json:"staging"`
	ValidateDomain    bool                   `json:"validateDomain"`
	Certificate       string                 `json:"certificate"`
	PrivateKey        string                 `json:"privateKey"`
}

type CertificateView struct {
	ID                uint                               `json:"id"`
	Name              string                             `json:"name"`
	Type              models.CertificateType             `json:"type"`
	Domain            string                             `json:"domain"`
	DynamicDNSEntryID *uint                              `json:"dynamicDnsEntryId"`
	Staging           bool                               `json:"staging"`
	Fingerprint       *string                            `json:"fingerprint"`
	NotBefore         *time.Time                         `json:"notBefore"`
	NotAfter          *time.Time                         `json:"notAfter"`
	UpdatedAt         time.Time                          `json:"updatedAt"`
	Active            bool                               `json:"active"`
	Pending           bool                               `json:"pending"`
	Ready             bool                               `json:"ready"`
	Renewable         bool                               `json:"renewable"`
	IssuanceStatus    string                             `json:"issuanceStatus"`
	IssuanceOperation models.ManagedCertificateOperation `json:"issuanceOperation"`
	IssuanceError     string                             `json:"issuanceError"`
	IssuanceRetryAt   *time.Time                         `json:"issuanceRetryAt"`
}

type DomainCheckResult struct {
	Domain          string   `json:"domain"`
	Resolved        []string `json:"resolved"`
	PublicAddresses []string `json:"publicAddresses"`
	Matches         bool     `json:"matches"`
	Warning         string   `json:"warning"`
}

type certificateMaterial struct {
	tlsCertificate tls.Certificate
	certificates   []*x509.Certificate
	leaf           *x509.Certificate
	certificatePEM string
	privateKeyPEM  string
	fingerprint    string
}

func invalidCertificate(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCertificate, fmt.Sprintf(format, args...))
}

func certificateView(certificate models.Certificate, activeID, pendingID uint, order *models.ManagedCertificateOrder, now time.Time) CertificateView {
	ready := certificateMaterialAvailable(certificate)
	view := CertificateView{
		ID:                certificate.ID,
		Name:              certificate.Name,
		Type:              certificate.Type,
		Domain:            certificate.Domain,
		DynamicDNSEntryID: cloneUint(certificate.DynamicDNSEntryID),
		Staging:           certificate.Staging,
		Fingerprint:       stringPointer(certificate.Fingerprint),
		NotBefore:         cloneTime(certificate.NotBefore),
		NotAfter:          cloneTime(certificate.NotAfter),
		UpdatedAt:         certificate.UpdatedAt,
		Active:            certificate.ID == activeID,
		Pending:           certificate.ID == pendingID,
		Ready:             ready,
		Renewable:         certificate.Type == models.CertificateTypeLetsEncrypt && certificateRenewalDue(certificate, now),
		IssuanceStatus:    "ready",
	}
	if certificate.Type != models.CertificateTypeSylveManaged {
		return view
	}

	view.Renewable = ready && certificateRenewalDue(certificate, now) && (order == nil || !managedOrderActive(order.Status))
	if order == nil {
		if !ready {
			view.IssuanceStatus = string(models.ManagedCertificateOrderStatusFailed)
			view.IssuanceError = "managed certificate issuance state is unavailable"
		}
		return view
	}
	view.IssuanceOperation = order.Operation
	view.IssuanceError = order.Error
	view.IssuanceRetryAt = cloneTime(order.RetryAt)
	if order.Status == models.ManagedCertificateOrderStatusIssued {
		view.IssuanceStatus = "ready"
	} else {
		view.IssuanceStatus = string(order.Status)
	}
	return view
}

func certificateRenewalDue(certificate models.Certificate, now time.Time) bool {
	return certificate.NotAfter != nil && !certificate.NotAfter.After(now.Add(renewalWindow))
}

func validateCertificateRenewalDue(certificate models.Certificate, now time.Time) error {
	if certificateRenewalDue(certificate, now) {
		return nil
	}
	if certificate.NotAfter == nil {
		return ErrNotRenewable
	}
	eligibleAt := certificate.NotAfter.Add(-renewalWindow).UTC()
	return fmt.Errorf("%w: certificate can be renewed on or after %s", ErrRenewalNotDue, eligibleAt.Format(time.RFC3339))
}

func certificateMaterialAvailable(certificate models.Certificate) bool {
	return strings.TrimSpace(certificate.CertificatePEM) != "" &&
		strings.TrimSpace(certificate.PrivateKeyPEM) != "" &&
		certificate.NotBefore != nil && certificate.NotAfter != nil
}

func managedOrderActive(status models.ManagedCertificateOrderStatus) bool {
	return status == models.ManagedCertificateOrderStatusSubmitting ||
		status == models.ManagedCertificateOrderStatusQueued ||
		status == models.ManagedCertificateOrderStatusProcessing ||
		status == models.ManagedCertificateOrderStatusBlocked
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := value
	return &copy
}

func cloneUint(value *uint) *uint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func parseCertificateMaterial(certificatePEM, privateKeyPEM string) (certificateMaterial, error) {
	certificatePEM = strings.TrimSpace(certificatePEM)
	privateKeyPEM = strings.TrimSpace(privateKeyPEM)
	if certificatePEM == "" || privateKeyPEM == "" {
		return certificateMaterial{}, invalidCertificate("certificate and private key are required")
	}
	if len(certificatePEM) > maxPEMBytes || len(privateKeyPEM) > maxPEMBytes {
		return certificateMaterial{}, invalidCertificate("certificate and private key must not exceed %d bytes each", maxPEMBytes)
	}

	certificates, err := parseCertificatePEM(certificatePEM)
	if err != nil {
		return certificateMaterial{}, err
	}
	privateKeyBlock, privateKeyRest := pem.Decode([]byte(privateKeyPEM))
	if privateKeyBlock == nil || !strings.HasSuffix(privateKeyBlock.Type, "PRIVATE KEY") || len(bytes.TrimSpace(privateKeyRest)) != 0 {
		return certificateMaterial{}, invalidCertificate("private key PEM must contain exactly one private key")
	}

	pair, err := tls.X509KeyPair([]byte(certificatePEM), []byte(privateKeyPEM))
	if err != nil {
		return certificateMaterial{}, invalidCertificate("certificate and private key do not form a valid pair: %v", err)
	}
	leaf := certificates[0]
	pair.Leaf = leaf

	fingerprint := sha256.Sum256(leaf.Raw)
	return certificateMaterial{
		tlsCertificate: pair,
		certificates:   certificates,
		leaf:           leaf,
		certificatePEM: certificatePEM + "\n",
		privateKeyPEM:  privateKeyPEM + "\n",
		fingerprint:    strings.ToUpper(hex.EncodeToString(fingerprint[:])),
	}, nil
}

func parseCertificatePEM(value string) ([]*x509.Certificate, error) {
	remaining := []byte(value)
	certificates := make([]*x509.Certificate, 0, 1)
	for len(bytes.TrimSpace(remaining)) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, invalidCertificate("certificate PEM must contain only certificates")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, invalidCertificate("parse certificate: %v", err)
		}
		certificates = append(certificates, certificate)
		remaining = rest
	}
	if len(certificates) == 0 {
		return nil, invalidCertificate("certificate PEM does not contain a certificate")
	}
	return certificates, nil
}

func validateServerMaterial(material certificateMaterial, domain string, now time.Time, validateDomain bool) error {
	if err := validateServerMaterialForServing(material, domain, validateDomain); err != nil {
		return err
	}
	if now.Before(material.leaf.NotBefore) {
		return invalidCertificate("certificate is not valid before %s", material.leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(material.leaf.NotAfter) {
		return invalidCertificate("certificate expired at %s", material.leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

// validateServerMaterialForServing accepts an otherwise valid certificate even
// when it is outside its validity window. Serving an expired certificate keeps
// the daemon reachable so renewal or administrative recovery can proceed.
func validateServerMaterialForServing(material certificateMaterial, domain string, validateDomain bool) error {
	leaf := material.leaf
	if leaf == nil || !leaf.NotAfter.After(leaf.NotBefore) {
		return invalidCertificate("certificate validity period is invalid")
	}

	serverAuth := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
			serverAuth = true
			break
		}
	}
	if !serverAuth {
		return invalidCertificate("certificate is not valid for TLS server authentication")
	}
	if validateDomain {
		if err := verifyCertificateDomain(leaf, domain); err != nil {
			return invalidCertificate("certificate does not match %q: %v", domain, err)
		}
	}
	return nil
}

func validateCertificateInputSize(input CertificateInput) error {
	if len(input.Certificate) > maxPEMBytes || len(input.PrivateKey) > maxPEMBytes {
		return invalidCertificate("certificate and private key must not exceed %d bytes each", maxPEMBytes)
	}
	return nil
}

func normalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalidCertificate("name is required")
	}
	if len(name) > 128 {
		return "", invalidCertificate("name must not exceed 128 characters")
	}
	return name, nil
}

func normalizeDomain(raw string) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if domain == "" {
		return "", invalidCertificate("domain is required")
	}
	validationDomain := domain
	if strings.HasPrefix(domain, "*.") {
		validationDomain = strings.TrimPrefix(domain, "*.")
		if net.ParseIP(validationDomain) != nil {
			return "", invalidCertificate("wildcard IP addresses are not supported")
		}
	}
	if net.ParseIP(validationDomain) == nil && !govalidator.IsDNSName(validationDomain) {
		return "", invalidCertificate("domain is invalid")
	}
	return domain, nil
}

func verifyCertificateDomain(certificate *x509.Certificate, domain string) error {
	hostname := domain
	if strings.HasPrefix(hostname, "*.") {
		hostname = "sylve-validation." + strings.TrimPrefix(hostname, "*.")
	}
	return certificate.VerifyHostname(hostname)
}

func domainFromCertificate(certificate *x509.Certificate) string {
	if len(certificate.DNSNames) > 0 {
		return strings.ToLower(strings.TrimSuffix(certificate.DNSNames[0], "."))
	}
	if len(certificate.IPAddresses) > 0 {
		return certificate.IPAddresses[0].String()
	}
	if name := strings.TrimSpace(certificate.Subject.CommonName); name != "" {
		return strings.ToLower(strings.TrimSuffix(name, "."))
	}
	return "legacy-certificate"
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
