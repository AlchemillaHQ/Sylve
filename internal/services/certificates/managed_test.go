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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/testutil"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
	"github.com/google/uuid"
)

const managedTestHostname = "node.example.com"

type managedTestCA struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	pool        *x509.CertPool
}

type managedTestRequest struct {
	ID  string `json:"id"`
	CSR string `json:"csr"`
}

type legacyCertificateTable struct {
	ID             uint                   `gorm:"primaryKey;autoIncrement"`
	Name           string                 `gorm:"uniqueIndex;not null"`
	Type           models.CertificateType `gorm:"not null;index"`
	Domain         string                 `gorm:"not null"`
	Staging        bool                   `gorm:"not null;default:false"`
	CertificatePEM string                 `gorm:"type:text;not null"`
	PrivateKeyPEM  string                 `gorm:"type:text;not null"`
	Fingerprint    string                 `gorm:"not null;index"`
	NotBefore      time.Time              `gorm:"not null"`
	NotAfter       time.Time              `gorm:"not null;index"`
	RenewedAt      *time.Time
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

func (legacyCertificateTable) TableName() string {
	return "certificates"
}

func TestManagedCertificateMigrationAllowsPendingMaterial(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &legacyCertificateTable{})
	if err := database.AutoMigrate(&models.Certificate{}, &models.ManagedCertificateOrder{}); err != nil {
		t.Fatalf("migrate managed certificate schema: %v", err)
	}
	entryID := uint(9)
	certificate := models.Certificate{
		Name:              "Pending Managed",
		Type:              models.CertificateTypeSylveManaged,
		Domain:            managedTestHostname,
		DynamicDNSEntryID: &entryID,
	}
	if err := database.Create(&certificate).Error; err != nil {
		t.Fatalf("insert pending managed certificate after migration: %v", err)
	}
}

func TestManagedCertificateCreatePersistsOrderBeforeSubmission(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	entry := createManagedTestEntry(t, service)

	view, err := service.CreateCertificate(ctx, CertificateInput{
		Name:              "Managed",
		Type:              models.CertificateTypeSylveManaged,
		DynamicDNSEntryID: &entry.ID,
	})
	if err != nil {
		t.Fatalf("create managed certificate: %v", err)
	}
	if view.Ready || view.Domain != managedTestHostname || view.DynamicDNSEntryID == nil || *view.DynamicDNSEntryID != entry.ID {
		t.Fatalf("unexpected pending managed certificate view: %#v", view)
	}
	if view.IssuanceStatus != string(models.ManagedCertificateOrderStatusSubmitting) || view.IssuanceOperation != models.ManagedCertificateOperationInitial {
		t.Fatalf("unexpected managed issuance state: %#v", view)
	}
	if view.Fingerprint != nil || view.NotBefore != nil || view.NotAfter != nil {
		t.Fatalf("pending certificate exposed material metadata: %#v", view)
	}

	order := loadManagedTestOrder(t, service, view.ID)
	if _, err := uuid.Parse(order.OrderID); err != nil {
		t.Fatalf("managed order ID is not a UUID: %v", err)
	}
	csrBlock, remainder := pem.Decode([]byte(order.CSRPEM))
	if csrBlock == nil || csrBlock.Type != "CERTIFICATE REQUEST" || strings.TrimSpace(string(remainder)) != "" {
		t.Fatal("managed order did not persist one certificate request")
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("parse managed CSR: %v", err)
	}
	if err := csr.CheckSignature(); err != nil || len(csr.DNSNames) != 1 || csr.DNSNames[0] != managedTestHostname {
		t.Fatalf("unexpected managed CSR: dns=%v error=%v", csr.DNSNames, err)
	}
	publicKey, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		t.Fatalf("managed CSR did not use ECDSA P-256: %T", csr.PublicKey)
	}
}

func TestManagedBrokerDoesNotFollowRedirects(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetCalls++
		if request.Header.Get("Authorization") != "" {
			t.Error("redirect target received broker authorization")
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	service := newTestService(t)
	service.managedBrokerURL = redirect.URL
	service.managedHTTPClient = redirect.Client()
	_, err := service.getManagedBrokerOrder(context.Background(), "broker-token", uuid.NewString())
	var responseErr *managedBrokerHTTPError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("expected redirect response error, got %v", err)
	}
	if targetCalls != 0 {
		t.Fatalf("redirect target was called %d times", targetCalls)
	}
}

func TestManagedCertificateIssuanceInstallsVerifiedMaterial(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	ca := newManagedTestCA(t, now)
	service, created := newPendingManagedTestCertificate(t, now)
	service.now = func() time.Time { return now }
	service.managedRootCAs = ca.pool

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/tls/orders" {
			http.NotFound(writer, request)
			return
		}
		payload, ok := decodeManagedTestRequest(writer, request)
		if !ok {
			return
		}
		certificatePEM, leaf, err := issueManagedTestCertificate(ca, payload.CSR, managedTestHostname, now)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writeManagedTestOrder(writer, http.StatusAccepted, managedBrokerOrder{
			ID:             payload.ID,
			Hostname:       managedTestHostname,
			Status:         string(models.ManagedCertificateOrderStatusIssued),
			NotBefore:      leaf.NotBefore.UTC().Format(managedBrokerTimeFormat),
			NotAfter:       leaf.NotAfter.UTC().Format(managedBrokerTimeFormat),
			CertificatePEM: certificatePEM,
		})
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()

	service.processManagedOrders(context.Background())
	view := findManagedTestCertificate(t, service, created.ID)
	if !view.Ready || view.IssuanceStatus != "ready" || view.Fingerprint == nil || view.NotAfter == nil {
		t.Fatalf("managed certificate was not installed: %#v", view)
	}
	order := loadManagedTestOrder(t, service, created.ID)
	if order.Status != models.ManagedCertificateOrderStatusIssued || order.CSRPEM != "" || order.PrivateKeyPEM != "" {
		t.Fatalf("managed order was not finalized safely: %#v", order)
	}
}

func TestManagedCertificateRetriesAmbiguousSubmissionWithGETFirst(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	ca := newManagedTestCA(t, now)
	service, created := newPendingManagedTestCertificate(t, now)
	service.managedRootCAs = ca.pool
	order := loadManagedTestOrder(t, service, created.ID)
	if err := service.DB.Model(&order).Updates(map[string]any{"submitted_at": now, "retry_at": nil}).Error; err != nil {
		t.Fatalf("mark managed submission ambiguous: %v", err)
	}

	var mutex sync.Mutex
	methods := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		methods = append(methods, request.Method)
		mutex.Unlock()
		switch request.Method {
		case http.MethodGet:
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(writer).Encode(map[string]string{"error": "Certificate order not found"})
		case http.MethodPost:
			payload, ok := decodeManagedTestRequest(writer, request)
			if !ok {
				return
			}
			certificatePEM, leaf, err := issueManagedTestCertificate(ca, payload.CSR, managedTestHostname, now)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
			writeManagedTestOrder(writer, http.StatusAccepted, managedBrokerOrder{
				ID: payload.ID, Hostname: managedTestHostname, Status: string(models.ManagedCertificateOrderStatusIssued),
				NotBefore: leaf.NotBefore.UTC().Format(managedBrokerTimeFormat), NotAfter: leaf.NotAfter.UTC().Format(managedBrokerTimeFormat), CertificatePEM: certificatePEM,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()

	service.processManagedOrders(context.Background())
	mutex.Lock()
	gotMethods := append([]string(nil), methods...)
	mutex.Unlock()
	if strings.Join(gotMethods, ",") != "GET,POST" {
		t.Fatalf("ambiguous submission did not use GET before POST: %v", gotMethods)
	}
	if !findManagedTestCertificate(t, service, created.ID).Ready {
		t.Fatal("GET-first recovery did not complete managed issuance")
	}
}

func TestManagedCertificateWaitsForBlockingOrder(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	ca := newManagedTestCA(t, now)
	service, created := newPendingManagedTestCertificate(t, now)
	service.now = func() time.Time { return now }
	service.managedRootCAs = ca.pool
	blockingID := uuid.NewString()
	postCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			postCount++
			payload, ok := decodeManagedTestRequest(writer, request)
			if !ok {
				return
			}
			if postCount == 1 {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"error": "A certificate order is already in progress",
					"order": map[string]string{"id": blockingID, "hostname": managedTestHostname, "status": "processing"},
				})
				return
			}
			certificatePEM, leaf, err := issueManagedTestCertificate(ca, payload.CSR, managedTestHostname, now)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
			writeManagedTestOrder(writer, http.StatusAccepted, managedBrokerOrder{
				ID: payload.ID, Hostname: managedTestHostname, Status: "issued",
				NotBefore: leaf.NotBefore.UTC().Format(managedBrokerTimeFormat), NotAfter: leaf.NotAfter.UTC().Format(managedBrokerTimeFormat), CertificatePEM: certificatePEM,
			})
			return
		}

		requestedID := strings.TrimPrefix(request.URL.Path, "/api/tls/orders/")
		if requestedID == blockingID {
			writeManagedTestOrder(writer, http.StatusOK, managedBrokerOrder{ID: blockingID, Hostname: managedTestHostname, Status: "issued"})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": "Certificate order not found"})
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()

	service.processManagedOrders(context.Background())
	blocked := loadManagedTestOrder(t, service, created.ID)
	if blocked.Status != models.ManagedCertificateOrderStatusBlocked || blocked.BlockedByOrderID != blockingID {
		t.Fatalf("managed order did not persist blocking order: %#v", blocked)
	}
	now = now.Add(managedPollInterval)
	service.processManagedOrders(context.Background())
	resubmitting := loadManagedTestOrder(t, service, created.ID)
	if resubmitting.Status != models.ManagedCertificateOrderStatusSubmitting || resubmitting.BlockedByOrderID != "" {
		t.Fatalf("managed order did not clear terminal blocker: %#v", resubmitting)
	}
	now = now.Add(managedPollInterval)
	service.processManagedOrders(context.Background())
	if !findManagedTestCertificate(t, service, created.ID).Ready || postCount != 2 {
		t.Fatalf("managed order was not resubmitted after blocker completed; posts=%d", postCount)
	}
}

func TestRetryManagedCertificateUsesFreshOrderAndKey(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service, created := newPendingManagedTestCertificate(t, now)
	original := loadManagedTestOrder(t, service, created.ID)
	if err := service.DB.Model(&original).Updates(map[string]any{
		"status": models.ManagedCertificateOrderStatusFailed,
		"error":  "issuance_failed",
	}).Error; err != nil {
		t.Fatalf("fail managed order: %v", err)
	}

	view, err := service.RetryManagedCertificate(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("retry managed certificate: %v", err)
	}
	retried := loadManagedTestOrder(t, service, created.ID)
	if view.IssuanceStatus != string(models.ManagedCertificateOrderStatusSubmitting) || retried.OrderID == original.OrderID || retried.CSRPEM == original.CSRPEM || retried.PrivateKeyPEM == original.PrivateKeyPEM {
		t.Fatalf("managed retry reused order identity or key: original=%#v retried=%#v", original, retried)
	}
}

func TestDeleteManagedCertificateCancelsSubmittedOrder(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service, created := newPendingManagedTestCertificate(t, now)
	order := loadManagedTestOrder(t, service, created.ID)
	if err := service.DB.Model(&order).Update("submitted_at", now).Error; err != nil {
		t.Fatalf("mark managed order submitted: %v", err)
	}

	cancelled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/api/tls/orders/"+order.OrderID {
			http.NotFound(writer, request)
			return
		}
		cancelled = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()

	if err := service.DeleteCertificate(context.Background(), created.ID); err != nil {
		t.Fatalf("delete managed certificate: %v", err)
	}
	if !cancelled {
		t.Fatal("submitted managed order was not cancelled")
	}
	if _, err := service.getCertificate(context.Background(), created.ID); !errors.Is(err, ErrCertificateNotFound) {
		t.Fatalf("managed certificate still exists: %v", err)
	}
}

func TestManagedBrokerAttemptReservationFencesStaleWorker(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service, created := newPendingManagedTestCertificate(t, now)
	first := loadManagedTestOrder(t, service, created.ID)
	stale := first

	reserved, err := service.reserveManagedBrokerAttempt(context.Background(), &first)
	if err != nil || !reserved {
		t.Fatalf("reserve first broker attempt: reserved=%v error=%v", reserved, err)
	}
	reserved, err = service.reserveManagedBrokerAttempt(context.Background(), &stale)
	if err != nil {
		t.Fatalf("reserve stale broker attempt: %v", err)
	}
	if reserved {
		t.Fatal("stale worker acquired an existing broker attempt lease")
	}
	if err := service.failManagedOrder(context.Background(), stale, "stale failure"); err != nil {
		t.Fatalf("apply stale worker result: %v", err)
	}
	current := loadManagedTestOrder(t, service, created.ID)
	if current.Status != models.ManagedCertificateOrderStatusSubmitting {
		t.Fatalf("stale worker changed managed order status: %#v", current)
	}
}

func TestManagedRenewalFailurePreservesCurrentMaterial(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	ca := newManagedTestCA(t, now)
	service, created := newPendingManagedTestCertificate(t, now)
	service.managedRootCAs = ca.pool
	wrongHostname := false

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, ok := decodeManagedTestRequest(writer, request)
		if !ok {
			return
		}
		hostname := managedTestHostname
		if wrongHostname {
			hostname = "wrong.example.com"
		}
		certificatePEM, leaf, err := issueManagedTestCertificate(ca, payload.CSR, hostname, now)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writeManagedTestOrder(writer, http.StatusAccepted, managedBrokerOrder{
			ID: payload.ID, Hostname: managedTestHostname, Status: "issued",
			NotBefore: leaf.NotBefore.UTC().Format(managedBrokerTimeFormat), NotAfter: leaf.NotAfter.UTC().Format(managedBrokerTimeFormat), CertificatePEM: certificatePEM,
		})
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()
	service.processManagedOrders(context.Background())

	var before models.Certificate
	if err := service.DB.First(&before, created.ID).Error; err != nil {
		t.Fatalf("load issued managed certificate: %v", err)
	}
	if err := service.DB.Model(&models.Certificate{}).
		Where("id = ?", created.ID).
		Update("not_after", now.Add(renewalWindow)).Error; err != nil {
		t.Fatalf("move managed certificate into renewal window: %v", err)
	}
	wrongHostname = true
	if _, err := service.RenewCertificate(context.Background(), created.ID); err != nil {
		t.Fatalf("queue managed renewal: %v", err)
	}
	service.processManagedOrders(context.Background())

	var after models.Certificate
	if err := service.DB.First(&after, created.ID).Error; err != nil {
		t.Fatalf("reload managed certificate: %v", err)
	}
	if after.CertificatePEM != before.CertificatePEM || after.PrivateKeyPEM != before.PrivateKeyPEM || after.Fingerprint != before.Fingerprint {
		t.Fatal("failed managed renewal replaced current certificate material")
	}
	order := loadManagedTestOrder(t, service, created.ID)
	if order.Status != models.ManagedCertificateOrderStatusFailed {
		t.Fatalf("invalid managed renewal was not marked failed: %#v", order)
	}
}

func TestManagedCertificateRejectsRenewalOutsideWindow(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service := newTestService(t)
	if err := service.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	service.now = func() time.Time { return now }
	entry := createManagedTestEntry(t, service)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain(managedTestHostname)
	if err != nil {
		t.Fatalf("generate managed certificate material: %v", err)
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		t.Fatalf("parse managed certificate material: %v", err)
	}
	certificate := modelFromMaterial("Managed Renewal", models.CertificateTypeSylveManaged, managedTestHostname, false, material, nil)
	certificate.DynamicDNSEntryID = &entry.ID
	if err := service.DB.Create(&certificate).Error; err != nil {
		t.Fatalf("save ready managed certificate: %v", err)
	}

	if _, err := service.RenewCertificate(context.Background(), certificate.ID); !errors.Is(err, ErrRenewalNotDue) {
		t.Fatalf("expected early managed renewal rejection, got %v", err)
	}
	var orderCount int64
	if err := service.DB.Model(&models.ManagedCertificateOrder{}).Where("certificate_id = ?", certificate.ID).Count(&orderCount).Error; err != nil {
		t.Fatalf("count managed renewal orders: %v", err)
	}
	if orderCount != 0 {
		t.Fatalf("early managed renewal created %d broker orders", orderCount)
	}

	if err := service.DB.Model(&certificate).Update("not_after", now.Add(renewalWindow)).Error; err != nil {
		t.Fatalf("move managed certificate into renewal window: %v", err)
	}
	view, err := service.RenewCertificate(context.Background(), certificate.ID)
	if err != nil {
		t.Fatalf("queue eligible managed renewal: %v", err)
	}
	if view.IssuanceOperation != models.ManagedCertificateOperationRenewal || view.Renewable {
		t.Fatalf("unexpected queued managed renewal view: %#v", view)
	}
}

func TestDueManagedRenewalsAreQueuedByRenewalScan(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service := newTestService(t)
	if err := service.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	service.now = func() time.Time { return now }
	entry := createManagedTestEntry(t, service)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain(managedTestHostname)
	if err != nil {
		t.Fatalf("generate managed certificate material: %v", err)
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		t.Fatalf("parse managed certificate material: %v", err)
	}
	certificate := modelFromMaterial("Managed Renewal", models.CertificateTypeSylveManaged, managedTestHostname, false, material, nil)
	certificate.DynamicDNSEntryID = &entry.ID
	dueAt := now.Add(renewalWindow)
	certificate.NotAfter = &dueAt
	if err := service.DB.Create(&certificate).Error; err != nil {
		t.Fatalf("save ready managed certificate: %v", err)
	}
	previousOrder := models.ManagedCertificateOrder{
		CertificateID: certificate.ID,
		OrderID:       uuid.NewString(),
		Operation:     models.ManagedCertificateOperationInitial,
		Status:        models.ManagedCertificateOrderStatusIssued,
	}
	if err := service.DB.Create(&previousOrder).Error; err != nil {
		t.Fatalf("save issued managed order: %v", err)
	}

	service.queueDueManagedRenewals(context.Background())

	order := loadManagedTestOrder(t, service, certificate.ID)
	if order.OrderID == previousOrder.OrderID || order.Operation != models.ManagedCertificateOperationRenewal || order.Status != models.ManagedCertificateOrderStatusSubmitting {
		t.Fatalf("unexpected scanned renewal order: %#v", order)
	}
}

func TestManagedCertificateRejectsUnexpectedSANUsageAndChainMaterial(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	ca := newManagedTestCA(t, now)
	service, created := newPendingManagedTestCertificate(t, now)
	service.managedRootCAs = ca.pool
	order := loadManagedTestOrder(t, service, created.ID)

	tests := []struct {
		name     string
		mutate   func(*x509.Certificate)
		alterPEM func(string) string
	}{
		{
			name: "unknown SAN identity",
			mutate: func(template *x509.Certificate) {
				san, err := asn1.Marshal([]asn1.RawValue{
					{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(managedTestHostname)},
					{Class: asn1.ClassContextSpecific, Tag: 8, Bytes: []byte{42, 3, 4}},
				})
				if err != nil {
					t.Fatalf("marshal test SAN: %v", err)
				}
				template.ExtraExtensions = []pkix.Extension{{Id: subjectAlternativeNameOID, Value: san}}
			},
		},
		{
			name: "unused chain certificate",
			alterPEM: func(value string) string {
				root := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.certificate.Raw}))
				return value + root + root
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificatePEM, leaf, err := issueManagedTestCertificateWithTemplate(ca, order.CSRPEM, managedTestHostname, now, test.mutate)
			if err != nil {
				t.Fatalf("issue test certificate: %v", err)
			}
			if test.alterPEM != nil {
				certificatePEM = test.alterPEM(certificatePEM)
			}
			_, err = service.validateManagedCertificate(managedTestHostname, order.PrivateKeyPEM, managedBrokerOrder{
				CertificatePEM: certificatePEM,
				NotBefore:      leaf.NotBefore.UTC().Format(managedBrokerTimeFormat),
				NotAfter:       leaf.NotAfter.UTC().Format(managedBrokerTimeFormat),
			})
			if err == nil {
				t.Fatal("expected managed certificate validation to fail")
			}
		})
	}

	certificatePEM, leaf, err := issueManagedTestCertificateWithTemplate(ca, order.CSRPEM, managedTestHostname, now, func(template *x509.Certificate) {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	})
	if err != nil {
		t.Fatalf("issue certificate with additional standard usage: %v", err)
	}
	if _, err := service.validateManagedCertificate(managedTestHostname, order.PrivateKeyPEM, managedBrokerOrder{
		CertificatePEM: certificatePEM,
		NotBefore:      leaf.NotBefore.UTC().Format(managedBrokerTimeFormat),
		NotAfter:       leaf.NotAfter.UTC().Format(managedBrokerTimeFormat),
	}); err != nil {
		t.Fatalf("certificate with server authentication and an additional standard usage was rejected: %v", err)
	}
}

func newPendingManagedTestCertificate(t *testing.T, now time.Time) (*Service, *CertificateView) {
	t.Helper()
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	service.now = func() time.Time { return now }
	entry := createManagedTestEntry(t, service)
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:              "Managed",
		Type:              models.CertificateTypeSylveManaged,
		DynamicDNSEntryID: &entry.ID,
	})
	if err != nil {
		t.Fatalf("create managed certificate: %v", err)
	}
	return service, created
}

func createManagedTestEntry(t *testing.T, service *Service) dynamicDNSModels.Entry {
	t.Helper()
	entry := dynamicDNSModels.Entry{
		Enabled:          true,
		Provider:         dynamicDNSModels.ProviderSylve,
		ProviderSecret:   "update-token",
		ProviderSettings: map[string]string{},
		Hostname:         managedTestHostname,
		RecordType:       dynamicDNSModels.RecordTypeA,
		IntervalMinutes:  10,
		SourceType:       dynamicDNSModels.SourceTypeManual,
		SourceSettings:   map[string]string{},
	}
	if err := service.DB.Create(&entry).Error; err != nil {
		t.Fatalf("create managed Dynamic DNS entry: %v", err)
	}
	return entry
}

func loadManagedTestOrder(t *testing.T, service *Service, certificateID uint) models.ManagedCertificateOrder {
	t.Helper()
	var order models.ManagedCertificateOrder
	if err := service.DB.Where("certificate_id = ?", certificateID).First(&order).Error; err != nil {
		t.Fatalf("load managed order: %v", err)
	}
	return order
}

func findManagedTestCertificate(t *testing.T, service *Service, certificateID uint) CertificateView {
	t.Helper()
	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	for _, item := range items {
		if item.ID == certificateID {
			return item
		}
	}
	t.Fatalf("certificate %d not found", certificateID)
	return CertificateView{}
}

func newManagedTestCA(t *testing.T, now time.Time) managedTestCA {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Sylve Managed Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test CA: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse test CA: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return managedTestCA{certificate: certificate, privateKey: privateKey, pool: pool}
}

func issueManagedTestCertificate(ca managedTestCA, csrPEM, hostname string, now time.Time) (string, *x509.Certificate, error) {
	return issueManagedTestCertificateWithTemplate(ca, csrPEM, hostname, now, nil)
}

func issueManagedTestCertificateWithTemplate(ca managedTestCA, csrPEM, hostname string, now time.Time, mutate func(*x509.Certificate)) (string, *x509.Certificate, error) {
	block, remainder := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || strings.TrimSpace(string(remainder)) != "" {
		return "", nil, errors.New("invalid test CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return "", nil, err
	}
	notBefore := now.Add(-time.Minute).UTC().Truncate(time.Second)
	notAfter := now.Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if mutate != nil {
		mutate(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, csr.PublicKey, ca.privateKey)
	if err != nil {
		return "", nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return "", nil, err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), leaf, nil
}

func decodeManagedTestRequest(writer http.ResponseWriter, request *http.Request) (managedTestRequest, bool) {
	if request.Header.Get("Authorization") != "Bearer update-token" {
		http.Error(writer, "missing bearer token", http.StatusUnauthorized)
		return managedTestRequest{}, false
	}
	var payload managedTestRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.ID == "" || payload.CSR == "" {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return managedTestRequest{}, false
	}
	return payload, true
}

func writeManagedTestOrder(writer http.ResponseWriter, status int, order managedBrokerOrder) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(order)
}
