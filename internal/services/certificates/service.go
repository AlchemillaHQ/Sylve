// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
	"golang.org/x/crypto/acme"
	"gorm.io/gorm"
)

type issueCertificateFunc func(context.Context, string, bool) ([]byte, []byte, error)

type certificateOperationLock struct {
	mu   sync.Mutex
	refs uint
}

type Service struct {
	DB *gorm.DB

	activeCertificate  atomic.Pointer[tls.Certificate]
	challenges         *challengeManager
	createMu           sync.Mutex
	acmeMu             sync.Mutex
	selectionMu        sync.Mutex
	managedProcessing  sync.Mutex
	certificateLocksMu sync.Mutex
	certificateLocks   map[uint]*certificateOperationLock

	now               func() time.Time
	issueCertificate  issueCertificateFunc
	resolver          *net.Resolver
	stunResolver      dynamicdns.IPSourceResolver
	interfaceAddrs    func() ([]net.Addr, error)
	managedBrokerURL  string
	managedHTTPClient *http.Client
	managedRootCAs    *x509.CertPool
}

func NewService(db *gorm.DB) *Service {
	service := &Service{
		DB:                db,
		challenges:        newChallengeManager(),
		certificateLocks:  make(map[uint]*certificateOperationLock),
		now:               time.Now,
		resolver:          net.DefaultResolver,
		stunResolver:      dynamicdns.NewSTUNResolver(),
		interfaceAddrs:    net.InterfaceAddrs,
		managedBrokerURL:  managedBrokerBaseURL,
		managedHTTPClient: &http.Client{Timeout: managedBrokerRequestTimeout},
	}
	service.issueCertificate = service.obtainLetsEncryptCertificate
	return service
}

func (s *Service) lockCertificateOperation(id uint) func() {
	s.certificateLocksMu.Lock()
	if s.certificateLocks == nil {
		s.certificateLocks = make(map[uint]*certificateOperationLock)
	}
	lock := s.certificateLocks[id]
	if lock == nil {
		lock = &certificateOperationLock{}
		s.certificateLocks[id] = lock
	}
	lock.refs++
	s.certificateLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		s.certificateLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && s.certificateLocks[id] == lock {
			delete(s.certificateLocks, id)
		}
		s.certificateLocksMu.Unlock()
	}
}

func (s *Service) issueCertificateSerially(ctx context.Context, domain string, staging bool) ([]byte, []byte, error) {
	s.acmeMu.Lock()
	defer s.acmeMu.Unlock()
	return s.issueCertificate(ctx, domain, staging)
}

func (s *Service) Initialize(ctx context.Context, legacy *internal.TLSConfig) error {
	if err := s.migrateLegacyConfiguration(ctx, legacy); err != nil {
		return err
	}
	if err := s.ensureActiveCertificate(ctx); err != nil {
		return err
	}
	if err := s.applyPendingCertificate(ctx); err != nil {
		return err
	}
	return s.reloadActiveCertificate(ctx)
}

func (s *Service) applyPendingCertificate(ctx context.Context) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settings models.CertificateSettings
		if err := tx.First(&settings, 1).Error; err != nil {
			return fmt.Errorf("load certificate settings: %w", err)
		}
		if settings.PendingCertificateID == nil {
			return nil
		}

		pendingID := *settings.PendingCertificateID
		clearPending := func() error {
			if err := tx.Model(&settings).Update("pending_certificate_id", nil).Error; err != nil {
				return fmt.Errorf("clear invalid pending certificate: %w", err)
			}
			return nil
		}
		var pending models.Certificate
		if err := tx.First(&pending, pendingID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.L.Warn().Uint("certificateID", pendingID).Msg("pending_certificate_not_found")
				return clearPending()
			}
			return fmt.Errorf("load pending certificate: %w", err)
		}
		material, err := parseCertificateMaterial(pending.CertificatePEM, pending.PrivateKeyPEM)
		if err == nil {
			err = validateServerMaterial(material, pending.Domain, s.currentTime(), true)
		}
		if err != nil {
			logger.L.Warn().Err(err).Uint("certificateID", pendingID).Msg("pending_certificate_invalid")
			return clearPending()
		}

		if err := tx.Model(&settings).Updates(map[string]any{
			"active_certificate_id":  pendingID,
			"pending_certificate_id": nil,
		}).Error; err != nil {
			return fmt.Errorf("apply pending certificate: %w", err)
		}
		return nil
	})
}

func (s *Service) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1", acme.ALPNProto},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if challenge := s.challenges.get(hello); challenge != nil {
				return challenge, nil
			}
			certificate := s.activeCertificate.Load()
			if certificate == nil {
				return nil, fmt.Errorf("no active public TLS certificate")
			}
			return certificate, nil
		},
	}
}

func (s *Service) ListCertificates(ctx context.Context) ([]CertificateView, error) {
	var views []CertificateView
	now := s.currentTime()
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var certificates []models.Certificate
		if err := tx.Order("id ASC").Find(&certificates).Error; err != nil {
			return fmt.Errorf("list certificates: %w", err)
		}

		selection, err := certificateSelectionFromDB(tx)
		if err != nil {
			return err
		}
		var orders []models.ManagedCertificateOrder
		if err := tx.Find(&orders).Error; err != nil {
			return fmt.Errorf("list managed certificate orders: %w", err)
		}
		ordersByCertificate := make(map[uint]*models.ManagedCertificateOrder, len(orders))
		for index := range orders {
			ordersByCertificate[orders[index].CertificateID] = &orders[index]
		}
		views = make([]CertificateView, len(certificates))
		for index, certificate := range certificates {
			views[index] = certificateView(certificate, selection.activeID, selection.pendingID, ordersByCertificate[certificate.ID], now)
		}
		return nil
	})
	return views, err
}

func (s *Service) ExportCertificateArchive(ctx context.Context, id uint) ([]byte, error) {
	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !certificateMaterialAvailable(certificate) {
		return nil, fmt.Errorf("%w: certificate material is not ready", ErrCertificateConflict)
	}

	material, err := parseCertificateMaterial(certificate.CertificatePEM, certificate.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("prepare certificate archive: %w", err)
	}

	var archive bytes.Buffer
	archiveWriter := zip.NewWriter(&archive)
	files := []struct {
		name    string
		content string
		mode    fs.FileMode
	}{
		{name: "certificate.pem", content: material.certificatePEM, mode: 0o644},
		{name: "private-key.pem", content: material.privateKeyPEM, mode: 0o600},
	}
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(file.mode)
		entry, err := archiveWriter.CreateHeader(header)
		if err != nil {
			_ = archiveWriter.Close()
			return nil, fmt.Errorf("create %s in certificate archive: %w", file.name, err)
		}
		if _, err := entry.Write([]byte(strings.TrimSpace(file.content) + "\n")); err != nil {
			_ = archiveWriter.Close()
			return nil, fmt.Errorf("write %s to certificate archive: %w", file.name, err)
		}
	}
	if err := archiveWriter.Close(); err != nil {
		return nil, fmt.Errorf("finish certificate archive: %w", err)
	}
	return archive.Bytes(), nil
}

func (s *Service) CreateCertificate(ctx context.Context, input CertificateInput) (*CertificateView, error) {
	s.createMu.Lock()
	defer s.createMu.Unlock()
	if err := validateCertificateInputSize(input); err != nil {
		return nil, err
	}

	name, err := normalizeName(input.Name)
	if err != nil {
		return nil, err
	}
	if input.Type == models.CertificateTypeSystemDefault || !isUserCertificateType(input.Type) {
		return nil, invalidCertificate("unsupported certificate type %q", input.Type)
	}

	var existingCount int64
	if err := s.DB.WithContext(ctx).Model(&models.Certificate{}).
		Where("name = ?", name).Count(&existingCount).Error; err != nil {
		return nil, fmt.Errorf("check certificate name: %w", err)
	}
	if existingCount > 0 {
		return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
	}

	if input.Type == models.CertificateTypeSylveManaged {
		selection, err := s.certificateSelection(ctx)
		if err != nil {
			return nil, err
		}
		return s.createManagedCertificate(ctx, name, input, selection)
	}

	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		return nil, err
	}
	material, renewedAt, err := s.materialForCreate(ctx, input.Type, domain, input)
	if err != nil {
		return nil, err
	}
	selection, err := s.certificateSelection(ctx)
	if err != nil {
		return nil, err
	}
	certificate := modelFromMaterial(name, input.Type, domain, input.Staging, material, renewedAt)
	if err := s.DB.WithContext(ctx).Create(&certificate).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
		}
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	view := certificateView(certificate, selection.activeID, selection.pendingID, nil, s.currentTime())
	return &view, nil
}

func (s *Service) UpdateCertificate(ctx context.Context, id uint, input CertificateInput) (*CertificateView, error) {
	unlockCertificate := s.lockCertificateOperation(id)
	defer unlockCertificate()
	if err := validateCertificateInputSize(input); err != nil {
		return nil, err
	}

	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if certificate.Type == models.CertificateTypeSystemDefault {
		return nil, fmt.Errorf("%w: the system default certificate cannot be edited", ErrCertificateConflict)
	}
	if input.Type != "" && input.Type != certificate.Type {
		return nil, invalidCertificate("certificate type cannot be changed")
	}
	if certificate.Type == models.CertificateTypeSylveManaged {
		return s.updateManagedCertificate(ctx, certificate, input)
	}

	name, err := normalizeName(input.Name)
	if err != nil {
		return nil, err
	}
	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		return nil, err
	}

	var nameOwner models.Certificate
	err = s.DB.WithContext(ctx).Where("name = ? AND id != ?", name, id).First(&nameOwner).Error
	if err == nil {
		return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check certificate name: %w", err)
	}

	selection, err := s.certificateSelection(ctx)
	if err != nil {
		return nil, err
	}
	validateDomain := input.ValidateDomain || certificate.Type != models.CertificateTypeImported ||
		selection.activeID == certificate.ID || selection.pendingID == certificate.ID
	material, renewedAt, err := s.materialForUpdate(ctx, certificate, domain, input, validateDomain)
	if err != nil {
		return nil, err
	}
	certificate.Name = name
	certificate.Domain = domain
	certificate.Staging = certificate.Type == models.CertificateTypeLetsEncrypt && input.Staging
	applyMaterial(&certificate, material, renewedAt)
	if err := s.DB.WithContext(ctx).Save(&certificate).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
		}
		return nil, fmt.Errorf("update certificate: %w", err)
	}

	view := certificateView(certificate, selection.activeID, selection.pendingID, nil, s.currentTime())
	return &view, nil
}

func (s *Service) DeleteCertificate(ctx context.Context, id uint) error {
	unlockCertificate := s.lockCertificateOperation(id)
	defer unlockCertificate()

	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return err
	}
	if certificate.Type == models.CertificateTypeSystemDefault {
		return fmt.Errorf("%w: the system default certificate cannot be deleted", ErrCertificateConflict)
	}
	s.selectionMu.Lock()
	selection, err := s.certificateSelection(ctx)
	if err == nil && selection.activeID == id {
		err = fmt.Errorf("%w: the active certificate cannot be deleted", ErrCertificateConflict)
	}
	if err == nil && selection.pendingID == id {
		err = fmt.Errorf("%w: cancel pending activation before deleting the certificate", ErrCertificateConflict)
	}
	s.selectionMu.Unlock()
	if err != nil {
		return err
	}
	if certificate.Type == models.CertificateTypeSylveManaged {
		order, err := s.managedOrderForCertificate(ctx, id)
		if err != nil {
			return err
		}
		if order != nil && order.SubmittedAt != nil && order.Status != models.ManagedCertificateOrderStatusIssued {
			entry, err := s.managedDynamicDNSEntry(ctx, certificate.DynamicDNSEntryID)
			if err != nil {
				return err
			}
			if err := s.cancelManagedBrokerOrder(ctx, entry.ProviderSecret, order.OrderID); err != nil {
				return fmt.Errorf("cancel managed certificate order: %w", err)
			}
		}
	}

	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if certificate.Type == models.CertificateTypeSylveManaged {
			if err := tx.Where("certificate_id = ?", id).Delete(&models.ManagedCertificateOrder{}).Error; err != nil {
				return fmt.Errorf("delete managed certificate order: %w", err)
			}
		}
		result := tx.Delete(&models.Certificate{}, id)
		if result.Error != nil {
			return fmt.Errorf("delete certificate: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrCertificateNotFound
		}
		return nil
	})
}

func (s *Service) ActivateCertificate(ctx context.Context, id uint) (*CertificateView, error) {
	unlockCertificate := s.lockCertificateOperation(id)
	defer unlockCertificate()

	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	material, err := parseCertificateMaterial(certificate.CertificatePEM, certificate.PrivateKeyPEM)
	if err == nil {
		err = validateServerMaterial(material, certificate.Domain, s.currentTime(), true)
	}
	if err != nil {
		return nil, fmt.Errorf("activate certificate: %w", err)
	}

	var activeID uint
	s.selectionMu.Lock()
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settings models.CertificateSettings
		if err := tx.First(&settings, 1).Error; err != nil {
			return err
		}
		if settings.ActiveCertificateID == id {
			return fmt.Errorf("%w: the certificate is already active", ErrCertificateConflict)
		}
		activeID = settings.ActiveCertificateID
		return tx.Model(&settings).Update("pending_certificate_id", id).Error
	})
	s.selectionMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("activate certificate: %w", err)
	}

	order, err := s.managedOrderForCertificate(ctx, certificate.ID)
	if err != nil {
		return nil, err
	}
	view := certificateView(certificate, activeID, id, order, s.currentTime())
	return &view, nil
}

func (s *Service) CancelPendingActivation(ctx context.Context, id uint) error {
	unlockCertificate := s.lockCertificateOperation(id)
	defer unlockCertificate()

	if _, err := s.getCertificate(ctx, id); err != nil {
		return err
	}

	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var settings models.CertificateSettings
		if err := tx.First(&settings, 1).Error; err != nil {
			return fmt.Errorf("load certificate settings: %w", err)
		}
		if settings.PendingCertificateID == nil || *settings.PendingCertificateID != id {
			return fmt.Errorf("%w: the certificate is not pending activation", ErrCertificateConflict)
		}
		if err := tx.Model(&settings).Update("pending_certificate_id", nil).Error; err != nil {
			return fmt.Errorf("cancel pending certificate activation: %w", err)
		}
		return nil
	})
}

func (s *Service) RenewCertificate(ctx context.Context, id uint) (*CertificateView, error) {
	unlockCertificate := s.lockCertificateOperation(id)
	defer unlockCertificate()

	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if certificate.Type == models.CertificateTypeSylveManaged {
		order, err := s.queueManagedRenewal(ctx, certificate)
		if err != nil {
			return nil, err
		}
		selection, err := s.certificateSelection(ctx)
		if err != nil {
			return nil, err
		}
		view := certificateView(certificate, selection.activeID, selection.pendingID, order, s.currentTime())
		return &view, nil
	}

	var certificatePEM, privateKeyPEM []byte
	switch certificate.Type {
	case models.CertificateTypeLetsEncrypt:
		if err := validateCertificateRenewalDue(certificate, s.currentTime()); err != nil {
			return nil, err
		}
		certificatePEM, privateKeyPEM, err = s.issueCertificateSerially(ctx, certificate.Domain, certificate.Staging)
		if err != nil {
			err = fmt.Errorf("%w: %v", ErrIssuanceFailed, err)
		}
	default:
		return nil, ErrNotRenewable
	}
	if err != nil {
		return nil, err
	}

	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	if err := validateServerMaterial(material, certificate.Domain, now, true); err != nil {
		return nil, err
	}
	applyMaterial(&certificate, material, &now)
	selection, err := s.certificateSelection(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Save(&certificate).Error; err != nil {
		return nil, fmt.Errorf("save renewed certificate: %w", err)
	}

	if selection.activeID == certificate.ID {
		s.publishActiveCertificate(material.tlsCertificate)
	}
	view := certificateView(certificate, selection.activeID, selection.pendingID, nil, s.currentTime())
	return &view, nil
}

func (s *Service) StartRenewalWorker(ctx context.Context) {
	s.renewDueCertificates(ctx)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("stopping_certificate_renewal_worker")
			return
		case <-ticker.C:
			s.renewDueCertificates(ctx)
		}
	}
}

func (s *Service) renewDueCertificates(ctx context.Context) {
	threshold := s.currentTime().Add(renewalWindow)
	var certificates []models.Certificate
	if err := s.DB.WithContext(ctx).
		Where("type = ? AND not_after <= ?", models.CertificateTypeLetsEncrypt, threshold).
		Order("not_after ASC").
		Find(&certificates).Error; err != nil {
		logger.L.Error().Err(err).Msg("certificate_renewal_scan_failed")
		return
	}

	for _, certificate := range certificates {
		if _, err := s.RenewCertificate(ctx, certificate.ID); err != nil {
			logger.L.Error().Err(err).Uint("certificateID", certificate.ID).Msg("certificate_renewal_failed")
		}
	}
}

func (s *Service) materialForCreate(ctx context.Context, certificateType models.CertificateType, domain string, input CertificateInput) (certificateMaterial, *time.Time, error) {
	var certificatePEM, privateKeyPEM []byte
	var renewedAt *time.Time
	var err error

	switch certificateType {
	case models.CertificateTypeImported:
		certificatePEM = []byte(input.Certificate)
		privateKeyPEM = []byte(input.PrivateKey)
	case models.CertificateTypeSelfSigned:
		certificatePEM, privateKeyPEM, err = sylveCrypto.GenerateSelfSignedCertificateForDomain(domain)
	case models.CertificateTypeLetsEncrypt:
		if net.ParseIP(domain) != nil || strings.HasPrefix(domain, "*.") {
			return certificateMaterial{}, nil, invalidCertificate("Let's Encrypt requires a non-wildcard DNS hostname")
		}
		certificatePEM, privateKeyPEM, err = s.issueCertificateSerially(ctx, domain, input.Staging)
		if err != nil {
			return certificateMaterial{}, nil, fmt.Errorf("%w: %v", ErrIssuanceFailed, err)
		}
		now := s.currentTime()
		renewedAt = &now
	}
	if err != nil {
		return certificateMaterial{}, nil, err
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		return certificateMaterial{}, nil, err
	}
	validateDomain := input.ValidateDomain || certificateType != models.CertificateTypeImported
	if err := validateServerMaterial(material, domain, s.currentTime(), validateDomain); err != nil {
		return certificateMaterial{}, nil, err
	}
	return material, renewedAt, nil
}

func (s *Service) materialForUpdate(ctx context.Context, certificate models.Certificate, domain string, input CertificateInput, validateDomain bool) (certificateMaterial, *time.Time, error) {
	existing, err := parseCertificateMaterial(certificate.CertificatePEM, certificate.PrivateKeyPEM)
	if err != nil {
		return certificateMaterial{}, nil, err
	}
	renewedAt := cloneTime(certificate.RenewedAt)

	switch certificate.Type {
	case models.CertificateTypeImported:
		hasCertificate := strings.TrimSpace(input.Certificate) != ""
		hasPrivateKey := strings.TrimSpace(input.PrivateKey) != ""
		if hasCertificate != hasPrivateKey {
			return certificateMaterial{}, nil, invalidCertificate("certificate and private key must be supplied together")
		}
		if hasCertificate {
			existing, err = parseCertificateMaterial(input.Certificate, input.PrivateKey)
			if err != nil {
				return certificateMaterial{}, nil, err
			}
		}
	case models.CertificateTypeSelfSigned:
		if domain != certificate.Domain {
			certificatePEM, privateKeyPEM, generateErr := sylveCrypto.GenerateSelfSignedCertificateForDomain(domain)
			if generateErr != nil {
				return certificateMaterial{}, nil, generateErr
			}
			existing, err = parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
			if err != nil {
				return certificateMaterial{}, nil, err
			}
			now := s.currentTime()
			renewedAt = &now
		}
	case models.CertificateTypeLetsEncrypt:
		if net.ParseIP(domain) != nil || strings.HasPrefix(domain, "*.") {
			return certificateMaterial{}, nil, invalidCertificate("Let's Encrypt requires a non-wildcard DNS hostname")
		}
		if domain != certificate.Domain || input.Staging != certificate.Staging {
			certificatePEM, privateKeyPEM, issueErr := s.issueCertificateSerially(ctx, domain, input.Staging)
			if issueErr != nil {
				return certificateMaterial{}, nil, fmt.Errorf("%w: %v", ErrIssuanceFailed, issueErr)
			}
			existing, err = parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
			if err != nil {
				return certificateMaterial{}, nil, err
			}
			now := s.currentTime()
			renewedAt = &now
		}
	}
	if err := validateServerMaterial(existing, domain, s.currentTime(), validateDomain); err != nil {
		return certificateMaterial{}, nil, err
	}
	return existing, renewedAt, nil
}

func modelFromMaterial(name string, certificateType models.CertificateType, domain string, staging bool, material certificateMaterial, renewedAt *time.Time) models.Certificate {
	certificate := models.Certificate{
		Name:      name,
		Type:      certificateType,
		Domain:    domain,
		Staging:   certificateType == models.CertificateTypeLetsEncrypt && staging,
		RenewedAt: cloneTime(renewedAt),
	}
	applyMaterial(&certificate, material, renewedAt)
	return certificate
}

func applyMaterial(certificate *models.Certificate, material certificateMaterial, renewedAt *time.Time) {
	certificate.CertificatePEM = material.certificatePEM
	certificate.PrivateKeyPEM = material.privateKeyPEM
	certificate.Fingerprint = material.fingerprint
	notBefore := material.leaf.NotBefore
	notAfter := material.leaf.NotAfter
	certificate.NotBefore = &notBefore
	certificate.NotAfter = &notAfter
	certificate.RenewedAt = cloneTime(renewedAt)
}

func isUserCertificateType(certificateType models.CertificateType) bool {
	return certificateType == models.CertificateTypeImported ||
		certificateType == models.CertificateTypeSelfSigned ||
		certificateType == models.CertificateTypeLetsEncrypt ||
		certificateType == models.CertificateTypeSylveManaged
}

func (s *Service) getCertificate(ctx context.Context, id uint) (models.Certificate, error) {
	var certificate models.Certificate
	if err := s.DB.WithContext(ctx).First(&certificate, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Certificate{}, ErrCertificateNotFound
		}
		return models.Certificate{}, fmt.Errorf("load certificate: %w", err)
	}
	return certificate, nil
}

type certificateSelection struct {
	activeID  uint
	pendingID uint
}

func (s *Service) certificateSelection(ctx context.Context) (certificateSelection, error) {
	return certificateSelectionFromDB(s.DB.WithContext(ctx))
}

func certificateSelectionFromDB(db *gorm.DB) (certificateSelection, error) {
	var settings models.CertificateSettings
	if err := db.First(&settings, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return certificateSelection{}, nil
		}
		return certificateSelection{}, fmt.Errorf("load certificate settings: %w", err)
	}
	selection := certificateSelection{activeID: settings.ActiveCertificateID}
	if settings.PendingCertificateID != nil {
		selection.pendingID = *settings.PendingCertificateID
	}
	return selection, nil
}

func (s *Service) activeCertificateID(ctx context.Context) (uint, error) {
	selection, err := s.certificateSelection(ctx)
	return selection.activeID, err
}

func (s *Service) reloadActiveCertificate(ctx context.Context) error {
	activeID, err := s.activeCertificateID(ctx)
	if err != nil {
		return err
	}
	if activeID == 0 {
		return fmt.Errorf("no active public TLS certificate is configured")
	}
	certificate, err := s.getCertificate(ctx, activeID)
	if err != nil {
		return fmt.Errorf("load active public TLS certificate: %w", err)
	}
	material, err := parseCertificateMaterial(certificate.CertificatePEM, certificate.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("load active public TLS certificate: %w", err)
	}
	if err := validateServerMaterialForServing(material, certificate.Domain, true); err != nil {
		return fmt.Errorf("load active public TLS certificate: %w", err)
	}
	s.publishActiveCertificate(material.tlsCertificate)
	return nil
}

func (s *Service) publishActiveCertificate(certificate tls.Certificate) {
	s.activeCertificate.Store(&certificate)
}

func (s *Service) currentTime() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}
