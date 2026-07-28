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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	managedPollInterval        = 5 * time.Second
	managedRenewalScanInterval = 24 * time.Hour
	managedAttemptLease        = managedBrokerRequestTimeout + managedPollInterval
	managedTransientRetryDelay = time.Minute
	managedBlockedRetryDelay   = time.Minute
	managedBrokerTimeFormat    = "2006-01-02T15:04:05Z"
)

var subjectAlternativeNameOID = asn1.ObjectIdentifier{2, 5, 29, 17}

func (s *Service) createManagedCertificate(ctx context.Context, name string, input CertificateInput, selection certificateSelection) (*CertificateView, error) {
	entry, err := s.managedDynamicDNSEntry(ctx, input.DynamicDNSEntryID)
	if err != nil {
		return nil, err
	}
	order, err := newManagedCertificateOrder(0, entry.Hostname, models.ManagedCertificateOperationInitial)
	if err != nil {
		return nil, err
	}

	entryID := entry.ID
	certificate := models.Certificate{
		Name:              name,
		Type:              models.CertificateTypeSylveManaged,
		Domain:            entry.Hostname,
		DynamicDNSEntryID: &entryID,
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var currentEntry dynamicDNSModels.Entry
		if err := tx.First(&currentEntry, entry.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return invalidCertificate("the selected Sylve.app Dynamic DNS entry was not found")
			}
			return err
		}
		currentDomain, err := normalizeDomain(currentEntry.Hostname)
		if err != nil || currentEntry.Provider != dynamicDNSModels.ProviderSylve ||
			strings.TrimSpace(currentEntry.ProviderSecret) == "" || currentDomain != entry.Hostname {
			return fmt.Errorf("%w: the selected Sylve.app Dynamic DNS entry changed", ErrCertificateConflict)
		}
		if err := tx.Create(&certificate).Error; err != nil {
			return err
		}
		order.CertificateID = certificate.ID
		return tx.Create(&order).Error
	}); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
		}
		return nil, fmt.Errorf("create managed certificate: %w", err)
	}

	view := certificateView(certificate, selection.activeID, selection.pendingID, &order, s.currentTime())
	return &view, nil
}

func (s *Service) updateManagedCertificate(ctx context.Context, certificate models.Certificate, input CertificateInput) (*CertificateView, error) {
	name, err := normalizeName(input.Name)
	if err != nil {
		return nil, err
	}
	if input.DynamicDNSEntryID != nil && (certificate.DynamicDNSEntryID == nil || *input.DynamicDNSEntryID != *certificate.DynamicDNSEntryID) {
		return nil, fmt.Errorf("%w: a managed certificate's Dynamic DNS entry cannot be changed", ErrCertificateConflict)
	}

	var nameOwner models.Certificate
	err = s.DB.WithContext(ctx).Where("name = ? AND id != ?", name, certificate.ID).First(&nameOwner).Error
	if err == nil {
		return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check certificate name: %w", err)
	}

	certificate.Name = name
	if err := s.DB.WithContext(ctx).Model(&certificate).Update("name", name).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("%w: a certificate named %q already exists", ErrCertificateConflict, name)
		}
		return nil, fmt.Errorf("update managed certificate: %w", err)
	}
	selection, err := s.certificateSelection(ctx)
	if err != nil {
		return nil, err
	}
	order, err := s.managedOrderForCertificate(ctx, certificate.ID)
	if err != nil {
		return nil, err
	}
	view := certificateView(certificate, selection.activeID, selection.pendingID, order, s.currentTime())
	return &view, nil
}

func (s *Service) RetryManagedCertificate(ctx context.Context, id uint) (*CertificateView, error) {
	s.mutations.Lock()
	defer s.mutations.Unlock()

	certificate, err := s.getCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if certificate.Type != models.CertificateTypeSylveManaged {
		return nil, ErrNotRenewable
	}
	if certificateMaterialAvailable(certificate) {
		return nil, fmt.Errorf("%w: use renewal for a ready managed certificate", ErrCertificateConflict)
	}
	s.managedBrokerMu.Lock()
	defer s.managedBrokerMu.Unlock()
	order, err := s.managedOrderForCertificate(ctx, certificate.ID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.Operation != models.ManagedCertificateOperationInitial || order.Status != models.ManagedCertificateOrderStatusFailed {
		return nil, fmt.Errorf("%w: the managed certificate does not have a failed initial issuance", ErrCertificateConflict)
	}
	entry, err := s.managedDynamicDNSEntry(ctx, certificate.DynamicDNSEntryID)
	if err != nil {
		return nil, err
	}
	if order.SubmittedAt != nil {
		if err := s.cancelManagedBrokerOrder(ctx, entry.ProviderSecret, order.OrderID); err != nil {
			return nil, fmt.Errorf("cancel previous managed certificate order: %w", err)
		}
	}

	newOrder, err := newManagedCertificateOrder(certificate.ID, certificate.Domain, models.ManagedCertificateOperationInitial)
	if err != nil {
		return nil, err
	}
	if err := s.replaceManagedOrder(ctx, certificate.ID, newOrder); err != nil {
		return nil, err
	}
	selection, err := s.certificateSelection(ctx)
	if err != nil {
		return nil, err
	}
	view := certificateView(certificate, selection.activeID, selection.pendingID, &newOrder, s.currentTime())
	return &view, nil
}

func (s *Service) queueManagedRenewal(ctx context.Context, certificate models.Certificate) (*models.ManagedCertificateOrder, error) {
	if certificate.Type != models.CertificateTypeSylveManaged || !certificateMaterialAvailable(certificate) {
		return nil, ErrNotRenewable
	}
	if err := validateCertificateRenewalDue(certificate, s.currentTime()); err != nil {
		return nil, err
	}
	entry, err := s.managedDynamicDNSEntry(ctx, certificate.DynamicDNSEntryID)
	if err != nil {
		return nil, err
	}
	s.managedBrokerMu.Lock()
	defer s.managedBrokerMu.Unlock()
	existing, err := s.managedOrderForCertificate(ctx, certificate.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil && managedOrderActive(existing.Status) {
		return nil, fmt.Errorf("%w: managed certificate issuance is already in progress", ErrCertificateConflict)
	}
	if existing != nil && existing.Status == models.ManagedCertificateOrderStatusFailed && existing.SubmittedAt != nil {
		if err := s.cancelManagedBrokerOrder(ctx, entry.ProviderSecret, existing.OrderID); err != nil {
			return nil, fmt.Errorf("cancel previous managed certificate renewal: %w", err)
		}
	}

	order, err := newManagedCertificateOrder(certificate.ID, certificate.Domain, models.ManagedCertificateOperationRenewal)
	if err != nil {
		return nil, err
	}
	if err := s.replaceManagedOrder(ctx, certificate.ID, order); err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Service) replaceManagedOrder(ctx context.Context, certificateID uint, order models.ManagedCertificateOrder) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("certificate_id = ?", certificateID).Delete(&models.ManagedCertificateOrder{}).Error; err != nil {
			return fmt.Errorf("remove previous managed certificate order: %w", err)
		}
		if err := tx.Create(&order).Error; err != nil {
			return fmt.Errorf("create managed certificate order: %w", err)
		}
		return nil
	})
}

func newManagedCertificateOrder(certificateID uint, domain string, operation models.ManagedCertificateOperation) (models.ManagedCertificateOrder, error) {
	csrPEM, privateKeyPEM, err := generateManagedCSR(domain)
	if err != nil {
		return models.ManagedCertificateOrder{}, err
	}
	return models.ManagedCertificateOrder{
		CertificateID: certificateID,
		OrderID:       uuid.NewString(),
		Operation:     operation,
		Status:        models.ManagedCertificateOrderStatusSubmitting,
		CSRPEM:        csrPEM,
		PrivateKeyPEM: privateKeyPEM,
	}, nil
}

func generateManagedCSR(domain string) (string, string, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(domain, "*.") {
		return "", "", invalidCertificate("Sylve.app Managed requires an exact DNS hostname")
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate managed certificate private key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, privateKey)
	if err != nil {
		return "", "", fmt.Errorf("generate managed certificate signing request: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("encode managed certificate private key: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	return string(csrPEM), string(privateKeyPEM), nil
}

func (s *Service) managedDynamicDNSEntry(ctx context.Context, id *uint) (dynamicDNSModels.Entry, error) {
	if id == nil || *id == 0 {
		return dynamicDNSModels.Entry{}, invalidCertificate("a Sylve.app Dynamic DNS entry is required")
	}
	var entry dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).First(&entry, *id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dynamicDNSModels.Entry{}, invalidCertificate("the selected Sylve.app Dynamic DNS entry was not found")
		}
		return dynamicDNSModels.Entry{}, fmt.Errorf("load Sylve.app Dynamic DNS entry: %w", err)
	}
	if entry.Provider != dynamicDNSModels.ProviderSylve {
		return dynamicDNSModels.Entry{}, invalidCertificate("the selected Dynamic DNS entry is not managed by Sylve.app")
	}
	if strings.TrimSpace(entry.ProviderSecret) == "" {
		return dynamicDNSModels.Entry{}, invalidCertificate("the selected Sylve.app Dynamic DNS entry has no update token")
	}
	domain, err := normalizeDomain(entry.Hostname)
	if err != nil || strings.HasPrefix(domain, "*.") {
		return dynamicDNSModels.Entry{}, invalidCertificate("the selected Sylve.app Dynamic DNS hostname is invalid")
	}
	entry.Hostname = domain
	return entry, nil
}

func (s *Service) managedOrderForCertificate(ctx context.Context, certificateID uint) (*models.ManagedCertificateOrder, error) {
	var order models.ManagedCertificateOrder
	if err := s.DB.WithContext(ctx).Where("certificate_id = ?", certificateID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load managed certificate order: %w", err)
	}
	return &order, nil
}

func (s *Service) StartManagedWorker(ctx context.Context) {
	s.queueDueManagedRenewals(ctx)
	s.processManagedOrders(ctx)
	orderTicker := time.NewTicker(managedPollInterval)
	renewalTicker := time.NewTicker(managedRenewalScanInterval)
	defer orderTicker.Stop()
	defer renewalTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("stopping_managed_certificate_worker")
			return
		case <-orderTicker.C:
			s.processManagedOrders(ctx)
		case <-renewalTicker.C:
			s.queueDueManagedRenewals(ctx)
		}
	}
}

func (s *Service) processManagedOrders(ctx context.Context) {
	if !s.managedProcessing.TryLock() {
		return
	}
	defer s.managedProcessing.Unlock()

	now := s.currentTime().UTC()
	var orders []models.ManagedCertificateOrder
	if err := s.DB.WithContext(ctx).
		Where("status IN ? AND (retry_at IS NULL OR retry_at <= ?)", []models.ManagedCertificateOrderStatus{
			models.ManagedCertificateOrderStatusSubmitting,
			models.ManagedCertificateOrderStatusQueued,
			models.ManagedCertificateOrderStatusProcessing,
			models.ManagedCertificateOrderStatusBlocked,
		}, now).
		Order("updated_at ASC").
		Find(&orders).Error; err != nil {
		logger.L.Error().Err(err).Msg("managed_certificate_order_scan_failed")
		return
	}

	for _, order := range orders {
		if ctx.Err() != nil {
			return
		}
		if err := s.processManagedOrder(ctx, order.ID); err != nil {
			logger.L.Error().Err(err).Uint("certificateID", order.CertificateID).Str("orderID", order.OrderID).Msg("managed_certificate_order_processing_failed")
		}
	}
}

func (s *Service) queueDueManagedRenewals(ctx context.Context) {
	threshold := s.currentTime().UTC().Add(renewalWindow)
	var certificates []models.Certificate
	if err := s.DB.WithContext(ctx).
		Where("type = ? AND not_after IS NOT NULL AND not_after <= ?", models.CertificateTypeSylveManaged, threshold).
		Order("not_after ASC").
		Find(&certificates).Error; err != nil {
		logger.L.Error().Err(err).Msg("managed_certificate_renewal_scan_failed")
		return
	}

	for _, certificate := range certificates {
		s.mutations.Lock()
		current, err := s.getCertificate(ctx, certificate.ID)
		if err == nil {
			existing, stateErr := s.managedOrderForCertificate(ctx, current.ID)
			if stateErr != nil {
				err = stateErr
			} else if existing == nil || existing.Status == models.ManagedCertificateOrderStatusIssued {
				_, err = s.queueManagedRenewal(ctx, current)
			}
		}
		s.mutations.Unlock()
		if err != nil {
			logger.L.Error().Err(err).Uint("certificateID", certificate.ID).Msg("managed_certificate_renewal_queue_failed")
		}
	}
}

func (s *Service) processManagedOrder(ctx context.Context, orderID uint) error {
	s.managedBrokerMu.Lock()
	defer s.managedBrokerMu.Unlock()

	var order models.ManagedCertificateOrder
	if err := s.DB.WithContext(ctx).First(&order, orderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("load managed certificate order: %w", err)
	}
	if !managedOrderActive(order.Status) {
		return nil
	}

	certificate, err := s.getCertificate(ctx, order.CertificateID)
	if err != nil {
		if errors.Is(err, ErrCertificateNotFound) {
			return s.DB.WithContext(ctx).Delete(&order).Error
		}
		return err
	}
	entry, err := s.managedDynamicDNSEntry(ctx, certificate.DynamicDNSEntryID)
	if err != nil {
		return s.failManagedOrder(ctx, order, err.Error())
	}
	if entry.Hostname != certificate.Domain {
		return s.failManagedOrder(ctx, order, "the associated Sylve.app Dynamic DNS hostname changed")
	}
	if order.Status == models.ManagedCertificateOrderStatusBlocked {
		reserved, err := s.reserveManagedBrokerAttempt(ctx, &order)
		if err != nil || !reserved {
			return err
		}
		return s.processBlockedManagedOrder(ctx, order, certificate, entry.ProviderSecret)
	}

	if order.Status == models.ManagedCertificateOrderStatusSubmitting {
		if order.SubmittedAt != nil {
			reserved, err := s.reserveManagedBrokerAttempt(ctx, &order)
			if err != nil || !reserved {
				return err
			}
			brokerOrder, getErr := s.getManagedBrokerOrder(ctx, entry.ProviderSecret, order.OrderID)
			if getErr == nil {
				return s.applyManagedBrokerOrder(ctx, order, certificate, brokerOrder)
			}
			var responseErr *managedBrokerHTTPError
			if !errors.As(getErr, &responseErr) || responseErr.StatusCode != http.StatusNotFound {
				return s.handleManagedBrokerFailure(ctx, order, getErr)
			}
		} else {
			now := s.currentTime().UTC()
			nextAttempt := now.Add(managedAttemptLease)
			query := managedOrderVersionQuery(s.DB.WithContext(ctx), order).
				Where("submitted_at IS NULL")
			result := query.
				Updates(map[string]any{"submitted_at": now, "retry_at": nextAttempt})
			if result.Error != nil {
				return fmt.Errorf("record managed certificate submission: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				return nil
			}
			order.SubmittedAt = &now
			order.RetryAt = &nextAttempt
		}

		brokerOrder, createErr := s.createManagedBrokerOrder(ctx, entry.ProviderSecret, order.OrderID, order.CSRPEM)
		if createErr != nil {
			var responseErr *managedBrokerHTTPError
			if errors.As(createErr, &responseErr) && responseErr.StatusCode == http.StatusConflict {
				return s.blockManagedOrder(ctx, order, certificate, responseErr)
			}
			return s.handleManagedBrokerFailure(ctx, order, createErr)
		}
		return s.applyManagedBrokerOrder(ctx, order, certificate, brokerOrder)
	}

	reserved, err := s.reserveManagedBrokerAttempt(ctx, &order)
	if err != nil || !reserved {
		return err
	}
	brokerOrder, err := s.getManagedBrokerOrder(ctx, entry.ProviderSecret, order.OrderID)
	if err != nil {
		return s.handleManagedBrokerFailure(ctx, order, err)
	}
	return s.applyManagedBrokerOrder(ctx, order, certificate, brokerOrder)
}

func (s *Service) reserveManagedBrokerAttempt(ctx context.Context, order *models.ManagedCertificateOrder) (bool, error) {
	nextAttempt := s.currentTime().UTC().Add(managedAttemptLease)
	result := managedOrderVersionQuery(s.DB.WithContext(ctx), *order).
		Update("retry_at", nextAttempt)
	if result.Error != nil {
		return false, fmt.Errorf("reserve managed certificate broker attempt: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	order.RetryAt = &nextAttempt
	return true, nil
}

func (s *Service) processBlockedManagedOrder(ctx context.Context, order models.ManagedCertificateOrder, certificate models.Certificate, token string) error {
	if strings.TrimSpace(order.BlockedByOrderID) == "" {
		nextAttempt := s.currentTime().UTC().Add(managedPollInterval)
		return s.updateManagedOrder(ctx, order, map[string]any{
			"status":              models.ManagedCertificateOrderStatusSubmitting,
			"blocked_by_order_id": "",
			"retry_at":            nextAttempt,
		})
	}

	blockedOrder, err := s.getManagedBrokerOrder(ctx, token, order.BlockedByOrderID)
	if err != nil {
		var responseErr *managedBrokerHTTPError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			nextAttempt := s.currentTime().UTC().Add(managedPollInterval)
			return s.updateManagedOrder(ctx, order, map[string]any{
				"status":              models.ManagedCertificateOrderStatusSubmitting,
				"blocked_by_order_id": "",
				"retry_at":            nextAttempt,
				"error":               "",
			})
		}
		return s.handleManagedBrokerFailure(ctx, order, err)
	}
	if err := validateManagedBrokerIdentity(blockedOrder, order.BlockedByOrderID, certificate.Domain); err != nil {
		return s.failManagedOrder(ctx, order, err.Error())
	}
	if blockedOrder.Status == string(models.ManagedCertificateOrderStatusQueued) || blockedOrder.Status == string(models.ManagedCertificateOrderStatusProcessing) {
		retryAt, err := s.managedBrokerRetryAt(blockedOrder.RetryAt)
		if err != nil {
			return s.failManagedOrder(ctx, order, err.Error())
		}
		return s.updateManagedOrder(ctx, order, map[string]any{
			"retry_at": retryAt,
			"error":    managedOrderError(blockedOrder.Error, "another certificate order is in progress"),
		})
	}
	if blockedOrder.Status != string(models.ManagedCertificateOrderStatusIssued) &&
		blockedOrder.Status != string(models.ManagedCertificateOrderStatusFailed) &&
		blockedOrder.Status != "cancelled" {
		return s.failManagedOrder(ctx, order, "Sylve.app returned an invalid blocking certificate order status")
	}

	nextAttempt := s.currentTime().UTC().Add(managedPollInterval)
	return s.updateManagedOrder(ctx, order, map[string]any{
		"status":              models.ManagedCertificateOrderStatusSubmitting,
		"blocked_by_order_id": "",
		"retry_at":            nextAttempt,
		"error":               "",
	})
}

func (s *Service) blockManagedOrder(ctx context.Context, order models.ManagedCertificateOrder, certificate models.Certificate, responseErr *managedBrokerHTTPError) error {
	if responseErr.ActiveOrder == nil && !strings.Contains(strings.ToLower(responseErr.Message), "already in progress") {
		return s.failManagedOrder(ctx, order, responseErr.Error())
	}
	nextAttempt := s.currentTime().UTC().Add(managedBlockedRetryDelay)
	blockedBy := ""
	if responseErr.RetryAfter > 0 {
		nextAttempt = s.currentTime().UTC().Add(max(responseErr.RetryAfter, managedPollInterval))
	}
	if responseErr.ActiveOrder != nil {
		summary := responseErr.ActiveOrder
		parsedID, err := uuid.Parse(strings.TrimSpace(summary.ID))
		if err != nil || parsedID == uuid.Nil || parsedID.String() == order.OrderID {
			return s.failManagedOrder(ctx, order, "Sylve.app returned an invalid blocking certificate order")
		}
		hostname, err := normalizeDomain(summary.Hostname)
		if err != nil || hostname != certificate.Domain ||
			(summary.Status != string(models.ManagedCertificateOrderStatusQueued) && summary.Status != string(models.ManagedCertificateOrderStatusProcessing)) {
			return s.failManagedOrder(ctx, order, "Sylve.app returned an invalid blocking certificate order")
		}
		blockedBy = parsedID.String()
		nextAttempt = s.currentTime().UTC().Add(managedPollInterval)
	}
	return s.updateManagedOrder(ctx, order, map[string]any{
		"status":              models.ManagedCertificateOrderStatusBlocked,
		"blocked_by_order_id": blockedBy,
		"retry_at":            nextAttempt,
		"error":               managedOrderError(responseErr.Message, "another certificate order is in progress"),
	})
}

func (s *Service) applyManagedBrokerOrder(ctx context.Context, order models.ManagedCertificateOrder, certificate models.Certificate, brokerOrder managedBrokerOrder) error {
	if err := validateManagedBrokerIdentity(brokerOrder, order.OrderID, certificate.Domain); err != nil {
		return s.failManagedOrder(ctx, order, err.Error())
	}

	switch brokerOrder.Status {
	case string(models.ManagedCertificateOrderStatusQueued), string(models.ManagedCertificateOrderStatusProcessing):
		retryAt, err := s.managedBrokerRetryAt(brokerOrder.RetryAt)
		if err != nil {
			return s.failManagedOrder(ctx, order, err.Error())
		}
		return s.updateManagedOrder(ctx, order, map[string]any{
			"status":   models.ManagedCertificateOrderStatus(brokerOrder.Status),
			"retry_at": retryAt,
			"error":    strings.TrimSpace(brokerOrder.Error),
		})
	case string(models.ManagedCertificateOrderStatusFailed):
		return s.failManagedOrder(ctx, order, managedOrderError(brokerOrder.Error, "Sylve.app certificate issuance failed"))
	case string(models.ManagedCertificateOrderStatusIssued):
		return s.installManagedCertificate(ctx, order, certificate, brokerOrder)
	default:
		return s.failManagedOrder(ctx, order, "Sylve.app returned an invalid certificate order status")
	}
}

func validateManagedBrokerIdentity(order managedBrokerOrder, expectedID, expectedDomain string) error {
	parsedID, err := uuid.Parse(strings.TrimSpace(order.ID))
	if err != nil || parsedID == uuid.Nil || parsedID.String() != expectedID {
		return fmt.Errorf("Sylve.app returned an unexpected certificate order ID")
	}
	hostname, err := normalizeDomain(order.Hostname)
	if err != nil || hostname != expectedDomain {
		return fmt.Errorf("Sylve.app returned an unexpected certificate order hostname")
	}
	return nil
}

func (s *Service) managedBrokerRetryAt(raw string) (time.Time, error) {
	nextAttempt := s.currentTime().UTC().Add(managedPollInterval)
	if strings.TrimSpace(raw) == "" {
		return nextAttempt, nil
	}
	retryAt, err := time.Parse(managedBrokerTimeFormat, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("Sylve.app returned an invalid certificate order retry time")
	}
	if retryAt.After(nextAttempt) {
		return retryAt, nil
	}
	return nextAttempt, nil
}

func (s *Service) handleManagedBrokerFailure(ctx context.Context, order models.ManagedCertificateOrder, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var permanentErr *permanentManagedBrokerError
	if errors.As(err, &permanentErr) {
		return s.failManagedOrder(ctx, order, permanentErr.Error())
	}
	delay := managedTransientRetryDelay
	var responseErr *managedBrokerHTTPError
	if errors.As(err, &responseErr) {
		if responseErr.RetryAfter > 0 {
			delay = max(responseErr.RetryAfter, managedPollInterval)
		}
		transient := responseErr.StatusCode == http.StatusRequestTimeout ||
			responseErr.StatusCode == http.StatusTooEarly ||
			responseErr.StatusCode == http.StatusTooManyRequests ||
			responseErr.StatusCode >= http.StatusInternalServerError
		if !transient {
			return s.failManagedOrder(ctx, order, responseErr.Error())
		}
	}
	nextAttempt := s.currentTime().UTC().Add(delay)
	return s.updateManagedOrder(ctx, order, map[string]any{
		"retry_at": nextAttempt,
		"error":    managedOrderError(err.Error(), "temporary Sylve.app certificate broker failure"),
	})
}

func (s *Service) failManagedOrder(ctx context.Context, order models.ManagedCertificateOrder, message string) error {
	return s.updateManagedOrder(ctx, order, map[string]any{
		"status":   models.ManagedCertificateOrderStatusFailed,
		"retry_at": nil,
		"error":    managedOrderError(message, "managed certificate issuance failed"),
	})
}

func (s *Service) updateManagedOrder(ctx context.Context, order models.ManagedCertificateOrder, updates map[string]any) error {
	result := managedOrderVersionQuery(s.DB.WithContext(ctx), order).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update managed certificate order: %w", result.Error)
	}
	return nil
}

func managedOrderVersionQuery(db *gorm.DB, order models.ManagedCertificateOrder) *gorm.DB {
	query := db.Model(&models.ManagedCertificateOrder{}).
		Where("id = ? AND order_id = ? AND status = ?", order.ID, order.OrderID, order.Status)
	if order.RetryAt == nil {
		return query.Where("retry_at IS NULL")
	}
	return query.Where("retry_at = ?", *order.RetryAt)
}

func managedOrderError(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}

func (s *Service) installManagedCertificate(ctx context.Context, order models.ManagedCertificateOrder, certificate models.Certificate, brokerOrder managedBrokerOrder) error {
	material, err := s.validateManagedCertificate(certificate.Domain, order.PrivateKeyPEM, brokerOrder)
	if err != nil {
		return s.failManagedOrder(ctx, order, err.Error())
	}

	now := s.currentTime().UTC()
	active := false
	installed := false
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := managedOrderVersionQuery(tx, order).Updates(map[string]any{
			"status":              models.ManagedCertificateOrderStatusIssued,
			"csr_pem":             "",
			"private_key_pem":     "",
			"blocked_by_order_id": "",
			"retry_at":            nil,
			"error":               "",
		})
		if result.Error != nil {
			return fmt.Errorf("complete managed certificate order: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var currentCertificate models.Certificate
		if err := tx.First(&currentCertificate, certificate.ID).Error; err != nil {
			return err
		}
		if currentCertificate.Type != models.CertificateTypeSylveManaged || currentCertificate.Domain != certificate.Domain {
			return fmt.Errorf("managed certificate changed while issuance was in progress")
		}

		applyMaterial(&currentCertificate, material, &now)
		if err := tx.Save(&currentCertificate).Error; err != nil {
			return fmt.Errorf("save issued managed certificate: %w", err)
		}

		var settings models.CertificateSettings
		if err := tx.First(&settings, 1).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		active = settings.ActiveCertificateID == currentCertificate.ID
		installed = true
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("install managed certificate: %w", err)
	}
	if installed && active {
		s.publishActiveCertificate(material.tlsCertificate)
	}
	return nil
}

func (s *Service) validateManagedCertificate(domain, privateKeyPEM string, order managedBrokerOrder) (certificateMaterial, error) {
	if strings.TrimSpace(order.CertificatePEM) == "" || strings.TrimSpace(order.NotBefore) == "" || strings.TrimSpace(order.NotAfter) == "" {
		return certificateMaterial{}, fmt.Errorf("Sylve.app issued an incomplete certificate order")
	}
	notBefore, err := time.Parse(managedBrokerTimeFormat, order.NotBefore)
	if err != nil {
		return certificateMaterial{}, fmt.Errorf("Sylve.app returned an invalid certificate not-before time")
	}
	notAfter, err := time.Parse(managedBrokerTimeFormat, order.NotAfter)
	if err != nil || !notAfter.After(notBefore) {
		return certificateMaterial{}, fmt.Errorf("Sylve.app returned an invalid certificate not-after time")
	}

	material, err := parseCertificateMaterial(order.CertificatePEM, privateKeyPEM)
	if err != nil {
		return certificateMaterial{}, fmt.Errorf("parse Sylve.app certificate material: %w", err)
	}
	certificates := material.certificates
	leaf := material.leaf
	if err := validateManagedCertificateIdentity(leaf, domain); err != nil {
		return certificateMaterial{}, err
	}
	serverAuth := false
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			serverAuth = true
			break
		}
	}
	if !serverAuth {
		return certificateMaterial{}, fmt.Errorf("Sylve.app issued a certificate without server authentication usage")
	}
	if !leaf.NotBefore.UTC().Equal(notBefore) || !leaf.NotAfter.UTC().Equal(notAfter) {
		return certificateMaterial{}, fmt.Errorf("Sylve.app returned certificate validity metadata that does not match the certificate")
	}

	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		intermediates.AddCert(certificate)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       domain,
		Roots:         s.managedRootCAs,
		Intermediates: intermediates,
		CurrentTime:   s.currentTime().UTC(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return certificateMaterial{}, fmt.Errorf("verify Sylve.app certificate chain: %w", err)
	}
	if !managedCertificateChainMatches(certificates, chains) {
		return certificateMaterial{}, fmt.Errorf("Sylve.app returned certificates outside the verified certificate chain")
	}
	return material, nil
}

func validateManagedCertificateIdentity(certificate *x509.Certificate, domain string) error {
	if len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != domain ||
		len(certificate.IPAddresses) != 0 || len(certificate.EmailAddresses) != 0 || len(certificate.URIs) != 0 {
		return fmt.Errorf("Sylve.app issued a certificate with unexpected subject alternative names")
	}
	commonName := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(certificate.Subject.CommonName), "."))
	if commonName != "" && commonName != domain {
		return fmt.Errorf("Sylve.app issued a certificate with an unexpected common name")
	}

	var extension *pkix.Extension
	for index := range certificate.Extensions {
		if certificate.Extensions[index].Id.Equal(subjectAlternativeNameOID) {
			if extension != nil {
				return fmt.Errorf("Sylve.app issued duplicate subject alternative name extensions")
			}
			extension = &certificate.Extensions[index]
		}
	}
	if extension == nil {
		return fmt.Errorf("Sylve.app issued a certificate without a subject alternative name extension")
	}
	var names []asn1.RawValue
	rest, err := asn1.Unmarshal(extension.Value, &names)
	if err != nil || len(rest) != 0 || len(names) != 1 {
		return fmt.Errorf("Sylve.app issued a certificate with unexpected subject alternative names")
	}
	name := names[0]
	if name.Class != asn1.ClassContextSpecific || name.Tag != 2 || name.IsCompound || string(name.Bytes) != domain {
		return fmt.Errorf("Sylve.app issued a certificate with unexpected subject alternative names")
	}
	return nil
}

func managedCertificateChainMatches(certificates []*x509.Certificate, verifiedChains [][]*x509.Certificate) bool {
	for _, chain := range verifiedChains {
		if len(certificates) > len(chain) {
			continue
		}
		matches := true
		for index := range certificates {
			if !bytes.Equal(certificates[index].Raw, chain[index].Raw) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
