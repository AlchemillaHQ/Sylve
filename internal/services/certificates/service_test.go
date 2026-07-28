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
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/testutil"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&models.Certificate{},
		&models.ManagedCertificateOrder{},
		&models.CertificateSettings{},
		&models.Migrations{},
		&models.SystemSecrets{},
		&dynamicDNSModels.Entry{},
	)
	return NewService(db)
}

func TestInitializeCreatesActiveSystemDefault(t *testing.T) {
	service := newTestService(t)
	if err := service.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}

	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one certificate, got %d", len(items))
	}
	if items[0].Type != models.CertificateTypeSystemDefault || !items[0].Active {
		t.Fatalf("expected active system default, got %#v", items[0])
	}

	tlsConfig := service.TLSConfig()
	certificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("get active certificate: %v", err)
	}
	if certificate == nil || certificate.Leaf == nil {
		t.Fatal("expected parsed active certificate")
	}
}

func TestCertificateActivationAppliesOnRestart(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	items, _ := service.ListCertificates(ctx)
	defaultID := items[0].ID
	runtimeConfig := service.TLSConfig()

	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Dashboard",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "one.example.com",
	})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if created.Renewable {
		t.Fatal("expected self-signed certificate not to be renewable")
	}
	if _, err := service.RenewCertificate(ctx, created.ID); !errors.Is(err, ErrNotRenewable) {
		t.Fatalf("expected self-signed renewal to be rejected, got %v", err)
	}
	pending, err := service.ActivateCertificate(ctx, created.ID)
	if err != nil {
		t.Fatalf("activate certificate: %v", err)
	}
	if pending.Active || !pending.Pending {
		t.Fatalf("expected pending certificate, got %#v", pending)
	}
	assertRuntimeDomain(t, runtimeConfig, defaultCertificateDomain)
	assertCertificateState(t, service, defaultID, true, false)
	assertCertificateState(t, service, created.ID, false, true)
	if err := service.DeleteCertificate(ctx, created.ID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected pending delete conflict, got %v", err)
	}

	service = restartTestService(t, service)
	runtimeConfig = service.TLSConfig()
	assertRuntimeDomain(t, runtimeConfig, "one.example.com")
	assertCertificateState(t, service, defaultID, false, false)
	assertCertificateState(t, service, created.ID, true, false)

	updated, err := service.UpdateCertificate(ctx, created.ID, CertificateInput{
		Name:   "Dashboard Updated",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "two.example.com",
	})
	if err != nil {
		t.Fatalf("update certificate: %v", err)
	}
	if !updated.Active {
		t.Fatal("expected updated certificate to remain active")
	}
	assertRuntimeDomain(t, runtimeConfig, "one.example.com")

	if err := service.DeleteCertificate(ctx, created.ID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected active delete conflict, got %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, defaultID); err != nil {
		t.Fatalf("reactivate default certificate: %v", err)
	}
	assertRuntimeDomain(t, runtimeConfig, "one.example.com")
	service = restartTestService(t, service)
	assertRuntimeDomain(t, service.TLSConfig(), defaultCertificateDomain)
	if err := service.DeleteCertificate(ctx, created.ID); err != nil {
		t.Fatalf("delete inactive certificate: %v", err)
	}
	if err := service.DeleteCertificate(ctx, defaultID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected system default delete conflict, got %v", err)
	}
}

func TestPendingActivationCanBeReplacedAndCancelled(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	first, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "First",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "first.example.com",
	})
	if err != nil {
		t.Fatalf("create first certificate: %v", err)
	}
	second, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Second",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "second.example.com",
	})
	if err != nil {
		t.Fatalf("create second certificate: %v", err)
	}

	if _, err := service.ActivateCertificate(ctx, first.ID); err != nil {
		t.Fatalf("schedule first certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, second.ID); err != nil {
		t.Fatalf("replace pending certificate: %v", err)
	}
	assertCertificateState(t, service, first.ID, false, false)
	assertCertificateState(t, service, second.ID, false, true)
	if err := service.CancelPendingActivation(ctx, first.ID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected mismatched cancellation conflict, got %v", err)
	}
	if err := service.DeleteCertificate(ctx, second.ID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected pending delete conflict, got %v", err)
	}
	if err := service.CancelPendingActivation(ctx, second.ID); err != nil {
		t.Fatalf("cancel pending activation: %v", err)
	}
	assertCertificateState(t, service, second.ID, false, false)
	if err := service.DeleteCertificate(ctx, second.ID); err != nil {
		t.Fatalf("delete cancelled pending certificate: %v", err)
	}
}

func TestInvalidPendingCertificateIsClearedAndKeepsCurrentCertificateActive(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Invalid Pending",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "pending.example.com",
	})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, created.ID); err != nil {
		t.Fatalf("schedule certificate: %v", err)
	}
	if err := service.DB.Model(&models.Certificate{}).Where("id = ?", created.ID).
		Update("certificate_pem", "invalid").Error; err != nil {
		t.Fatalf("corrupt pending certificate: %v", err)
	}

	restarted := restartTestService(t, service)
	assertRuntimeDomain(t, restarted.TLSConfig(), defaultCertificateDomain)
	assertCertificateState(t, restarted, created.ID, false, false)
}

func TestActiveCertificateUpdateAppliesAfterRestart(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Dashboard",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "dashboard.example.com",
	})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, created.ID); err != nil {
		t.Fatalf("schedule certificate: %v", err)
	}
	service = restartTestService(t, service)
	assertRuntimeDomain(t, service.TLSConfig(), "dashboard.example.com")

	if _, err := service.UpdateCertificate(ctx, created.ID, CertificateInput{
		Name:   "Dashboard",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "updated.example.com",
	}); err != nil {
		t.Fatalf("update active certificate: %v", err)
	}
	assertRuntimeDomain(t, service.TLSConfig(), "dashboard.example.com")

	service = restartTestService(t, service)
	assertRuntimeDomain(t, service.TLSConfig(), "updated.example.com")
}

func TestExpiredActiveCertificateDoesNotBlockStartup(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Expired Active",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "expired.example.com",
	})
	if err != nil {
		t.Fatalf("create active certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, created.ID); err != nil {
		t.Fatalf("schedule active certificate: %v", err)
	}
	service = restartTestService(t, service)
	active, err := activeCertificateFromDB(service.DB)
	if err != nil {
		t.Fatalf("load active certificate: %v", err)
	}
	if active == nil || active.NotAfter == nil {
		t.Fatal("active certificate has no expiration")
	}

	restarted := NewService(service.DB)
	restarted.now = func() time.Time { return active.NotAfter.Add(time.Hour) }
	if err := restarted.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize with expired active certificate: %v", err)
	}
	assertRuntimeDomain(t, restarted.TLSConfig(), active.Domain)
}

func TestMalformedActiveCertificateStillBlocksStartup(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	active, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Corrupted Active",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "corrupted.example.com",
	})
	if err != nil {
		t.Fatalf("create active certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, active.ID); err != nil {
		t.Fatalf("schedule active certificate: %v", err)
	}
	service = restartTestService(t, service)
	if err := service.DB.Model(&models.Certificate{}).
		Where("id = ?", active.ID).
		Update("certificate_pem", "invalid").Error; err != nil {
		t.Fatalf("corrupt active certificate: %v", err)
	}

	if err := NewService(service.DB).Initialize(ctx, nil); err == nil {
		t.Fatal("expected malformed active certificate to block startup")
	}
}

func TestImportedCertificateDomainValidation(t *testing.T) {
	service := newTestService(t)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("one.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}

	_, err = service.CreateCertificate(context.Background(), CertificateInput{
		Name:           "Imported",
		Type:           models.CertificateTypeImported,
		Domain:         "two.example.com",
		ValidateDomain: true,
		Certificate:    string(certificatePEM),
		PrivateKey:     string(privateKeyPEM),
	})
	if !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("expected domain validation error, got %v", err)
	}
}

func TestImportedCertificateMustMatchDomainBeforeActivation(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("one.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:        "Imported",
		Type:        models.CertificateTypeImported,
		Domain:      "two.example.com",
		Certificate: string(certificatePEM),
		PrivateKey:  string(privateKeyPEM),
	})
	if err != nil {
		t.Fatalf("create imported certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, created.ID); !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("expected activation domain validation error, got %v", err)
	}
}

func TestImportedWildcardCertificateValidation(t *testing.T) {
	service := newTestService(t)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("*.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}

	if _, err := service.CreateCertificate(context.Background(), CertificateInput{
		Name:           "Wildcard",
		Type:           models.CertificateTypeImported,
		Domain:         "*.example.com",
		ValidateDomain: true,
		Certificate:    string(certificatePEM),
		PrivateKey:     string(privateKeyPEM),
	}); err != nil {
		t.Fatalf("validate wildcard certificate: %v", err)
	}
}

func TestLegacyConfigurationMigratesAtomically(t *testing.T) {
	service := newTestService(t)
	directory := t.TempDir()
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("legacy.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}
	certificatePath := filepath.Join(directory, "certificate.pem")
	privateKeyPath := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificatePath, certificatePEM, 0600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	if err := service.Initialize(context.Background(), &internal.TLSConfig{
		CertFile: certificatePath,
		KeyFile:  privateKeyPath,
	}); err != nil {
		t.Fatalf("migrate legacy certificate: %v", err)
	}

	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected migrated and fallback certificates, got %d", len(items))
	}
	var migrated *CertificateView
	for index := range items {
		if items[index].Type == models.CertificateTypeImported {
			migrated = &items[index]
		}
	}
	if migrated == nil || !migrated.Active || migrated.Domain != "legacy.example.com" {
		t.Fatalf("unexpected migrated certificate: %#v", migrated)
	}
	assertMigrationCount(t, service, legacyMigrationMarker, 1)
}

func TestLegacyGeneratedCertificateBecomesSystemDefault(t *testing.T) {
	service := newTestService(t)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificate()
	if err != nil {
		t.Fatalf("generate legacy system certificate: %v", err)
	}
	for name, data := range map[string]string{
		"tls_cert": string(certificatePEM),
		"tls_key":  string(privateKeyPEM),
	} {
		if err := service.DB.Create(&models.SystemSecrets{Name: name, Data: data}).Error; err != nil {
			t.Fatalf("store legacy TLS secret %q: %v", name, err)
		}
	}

	if err := service.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("migrate generated legacy certificate: %v", err)
	}
	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one preserved system default certificate, got %d", len(items))
	}
	if items[0].Type != models.CertificateTypeSystemDefault || !items[0].Active || items[0].Name != defaultCertificateName {
		t.Fatalf("unexpected preserved system default certificate: %#v", items[0])
	}
	var stored models.Certificate
	if err := service.DB.First(&stored, items[0].ID).Error; err != nil {
		t.Fatalf("load preserved system default certificate: %v", err)
	}
	if strings.TrimSpace(stored.CertificatePEM) != strings.TrimSpace(string(certificatePEM)) ||
		strings.TrimSpace(stored.PrivateKeyPEM) != strings.TrimSpace(string(privateKeyPEM)) {
		t.Fatal("legacy system default certificate material changed during migration")
	}
	var secretCount int64
	if err := service.DB.Model(&models.SystemSecrets{}).
		Where("name IN ?", []string{"tls_cert", "tls_key"}).
		Count(&secretCount).Error; err != nil {
		t.Fatalf("count legacy TLS secrets: %v", err)
	}
	if secretCount != 0 {
		t.Fatalf("expected legacy TLS secrets to be removed, found %d", secretCount)
	}
	assertMigrationCount(t, service, legacyMigrationMarker, 1)
}

func TestInvalidLegacyConfigurationDoesNotCommitMigration(t *testing.T) {
	service := newTestService(t)
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "certificate.pem")
	privateKeyPath := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificatePath, []byte("invalid"), 0600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, []byte("invalid"), 0600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	err := service.Initialize(context.Background(), &internal.TLSConfig{
		CertFile: certificatePath,
		KeyFile:  privateKeyPath,
	})
	if err == nil {
		t.Fatal("expected invalid legacy certificate to fail initialization")
	}
	assertMigrationCount(t, service, legacyMigrationMarker, 0)
	var count int64
	if err := service.DB.Model(&models.Certificate{}).Count(&count).Error; err != nil {
		t.Fatalf("count certificates: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected migration rollback, found %d certificates", count)
	}
}

func TestMigrationMarkerFailureRollsBackCertificateAndSettings(t *testing.T) {
	service := newTestService(t)
	trigger := `
		CREATE TRIGGER reject_certificate_migration
		BEFORE INSERT ON migrations
		WHEN NEW.name = 'migrate_legacy_tls_config_to_certificates_v1'
		BEGIN
			SELECT RAISE(ABORT, 'forced migration marker failure');
		END;
	`
	if err := service.DB.Exec(trigger).Error; err != nil {
		t.Fatalf("create migration trigger: %v", err)
	}
	if err := service.Initialize(context.Background(), nil); err == nil {
		t.Fatal("expected migration marker failure")
	}

	for name, model := range map[string]any{
		"certificates": &models.Certificate{},
		"settings":     &models.CertificateSettings{},
	} {
		var count int64
		if err := service.DB.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("expected %s rollback, found %d rows", name, count)
		}
	}
}

func TestConfiguredCertificateWinsOverLegacyPaths(t *testing.T) {
	service := newTestService(t)
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("configured.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	certificate := modelFromMaterial(
		"Configured",
		models.CertificateTypeImported,
		"configured.example.com",
		false,
		material,
		nil,
	)
	if err := service.DB.Create(&certificate).Error; err != nil {
		t.Fatalf("save certificate: %v", err)
	}
	if err := service.DB.Create(&models.CertificateSettings{ID: 1, ActiveCertificateID: certificate.ID}).Error; err != nil {
		t.Fatalf("save settings: %v", err)
	}

	if err := service.Initialize(context.Background(), &internal.TLSConfig{
		CertFile: "/does/not/exist",
		KeyFile:  "/also/missing",
	}); err != nil {
		t.Fatalf("configured certificate should win: %v", err)
	}
	assertRuntimeDomain(t, service.TLSConfig(), "configured.example.com")
	assertMigrationCount(t, service, legacyMigrationMarker, 1)
}

func TestRenewalWorkerRenewsLetsEncryptInsideWindow(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}

	issueCount := 0
	issueCertificate := func(_ context.Context, domain string, _ bool) ([]byte, []byte, error) {
		issueCount++
		return sylveCrypto.GenerateSelfSignedCertificateForDomain(domain)
	}
	service.issueCertificate = issueCertificate
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "ACME",
		Type:   models.CertificateTypeLetsEncrypt,
		Domain: "acme.example.com",
	})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if created.Renewable {
		t.Fatal("expected new Let's Encrypt certificate not to be due for renewal")
	}
	if _, err := service.ActivateCertificate(ctx, created.ID); err != nil {
		t.Fatalf("schedule Let's Encrypt certificate: %v", err)
	}
	service = restartTestService(t, service)
	service.issueCertificate = issueCertificate
	runtimeConfig := service.TLSConfig()
	before, err := runtimeConfig.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("get certificate before renewal: %v", err)
	}
	service.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	if err := service.DB.Model(&models.Certificate{}).
		Where("id = ?", created.ID).
		Update("not_after", service.currentTime().Add(29*24*time.Hour)).Error; err != nil {
		t.Fatalf("move certificate into renewal window: %v", err)
	}

	service.renewDueCertificates(ctx)
	if issueCount != 2 {
		t.Fatalf("expected create and renewal issuance, got %d calls", issueCount)
	}
	after, err := runtimeConfig.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("get certificate after renewal: %v", err)
	}
	if string(before.Leaf.Raw) == string(after.Leaf.Raw) {
		t.Fatal("expected active Let's Encrypt renewal to update the runtime certificate")
	}
}

func TestRenewCertificateRejectsLetsEncryptOutsideWindow(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	issueCount := 0
	service.issueCertificate = func(_ context.Context, domain string, _ bool) ([]byte, []byte, error) {
		issueCount++
		return sylveCrypto.GenerateSelfSignedCertificateForDomain(domain)
	}
	created, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Early Renewal",
		Type:   models.CertificateTypeLetsEncrypt,
		Domain: "early-renewal.example.com",
	})
	if err != nil {
		t.Fatalf("create Let's Encrypt certificate: %v", err)
	}
	if created.Renewable {
		t.Fatal("new Let's Encrypt certificate unexpectedly exposed renewal")
	}
	if _, err := service.RenewCertificate(ctx, created.ID); !errors.Is(err, ErrRenewalNotDue) {
		t.Fatalf("expected early renewal rejection, got %v", err)
	}
	if issueCount != 1 {
		t.Fatalf("early renewal contacted issuer; calls=%d", issueCount)
	}

	dueAt := now.Add(renewalWindow)
	if err := service.DB.Model(&models.Certificate{}).Where("id = ?", created.ID).Update("not_after", dueAt).Error; err != nil {
		t.Fatalf("move certificate into renewal window: %v", err)
	}
	items, err := service.ListCertificates(ctx)
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	for _, item := range items {
		if item.ID == created.ID && !item.Renewable {
			t.Fatal("certificate inside renewal window did not expose renewal")
		}
	}
	renewed, err := service.RenewCertificate(ctx, created.ID)
	if err != nil {
		t.Fatalf("renew eligible certificate: %v", err)
	}
	if issueCount != 2 {
		t.Fatalf("eligible renewal did not contact issuer; calls=%d", issueCount)
	}
	if renewed.Renewable {
		t.Fatal("newly renewed certificate remained immediately renewable")
	}
}

func TestCertificateViewDoesNotExposePEM(t *testing.T) {
	service := newTestService(t)
	if err := service.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal views: %v", err)
	}
	value := string(encoded)
	if strings.Contains(value, "BEGIN CERTIFICATE") || strings.Contains(value, "PRIVATE KEY") {
		t.Fatalf("certificate view leaked PEM data: %s", value)
	}
}

func TestExportCertificateArchive(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	items, err := service.ListCertificates(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list certificates: items=%d err=%v", len(items), err)
	}

	archive, err := service.ExportCertificateArchive(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("export certificate archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open certificate archive: %v", err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("expected two archive entries, got %d", len(reader.File))
	}

	contents := make(map[string][]byte, len(reader.File))
	expectedModes := map[string]fs.FileMode{
		"certificate.pem": 0o644,
		"private-key.pem": 0o600,
	}
	for _, entry := range reader.File {
		expectedMode, ok := expectedModes[entry.Name]
		if !ok {
			t.Fatalf("unexpected archive entry %q", entry.Name)
		}
		if entry.Mode().Perm() != expectedMode {
			t.Fatalf("entry %q mode=%o want=%o", entry.Name, entry.Mode().Perm(), expectedMode)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatalf("open archive entry %q: %v", entry.Name, err)
		}
		contents[entry.Name], err = io.ReadAll(file)
		closeErr := file.Close()
		if err != nil {
			t.Fatalf("read archive entry %q: %v", entry.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close archive entry %q: %v", entry.Name, closeErr)
		}
	}

	if _, err := tls.X509KeyPair(contents["certificate.pem"], contents["private-key.pem"]); err != nil {
		t.Fatalf("exported certificate and private key do not match: %v", err)
	}
	var stored models.Certificate
	if err := service.DB.First(&stored, items[0].ID).Error; err != nil {
		t.Fatalf("load stored certificate: %v", err)
	}
	if strings.TrimSpace(string(contents["certificate.pem"])) != strings.TrimSpace(stored.CertificatePEM) {
		t.Fatal("archive did not preserve the stored certificate chain")
	}
	if strings.TrimSpace(string(contents["private-key.pem"])) != strings.TrimSpace(stored.PrivateKeyPEM) {
		t.Fatal("archive did not preserve the stored private key")
	}
}

func TestExportCertificateArchiveRejectsUnavailableMaterial(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	pending := models.Certificate{
		Name:   "Pending Managed Certificate",
		Type:   models.CertificateTypeSylveManaged,
		Domain: "pending.example.com",
	}
	if err := service.DB.Create(&pending).Error; err != nil {
		t.Fatalf("create pending certificate: %v", err)
	}

	if _, err := service.ExportCertificateArchive(ctx, pending.ID); !errors.Is(err, ErrCertificateConflict) {
		t.Fatalf("expected unavailable material conflict, got %v", err)
	}
	if _, err := service.ExportCertificateArchive(ctx, pending.ID+1000); !errors.Is(err, ErrCertificateNotFound) {
		t.Fatalf("expected missing certificate error, got %v", err)
	}
}

func assertRuntimeDomain(t *testing.T, tlsConfig *tls.Config, domain string) {
	t.Helper()
	certificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("get runtime certificate: %v", err)
	}
	if certificate == nil || certificate.Leaf == nil {
		t.Fatal("runtime certificate has no parsed leaf")
	}
	if err := certificate.Leaf.VerifyHostname(domain); err != nil {
		t.Fatalf("runtime certificate does not match %s: %v", domain, err)
	}
}

func restartTestService(t *testing.T, service *Service) *Service {
	t.Helper()
	restarted := NewService(service.DB)
	if err := restarted.Initialize(context.Background(), nil); err != nil {
		t.Fatalf("restart certificate service: %v", err)
	}
	return restarted
}

func assertCertificateState(t *testing.T, service *Service, id uint, active, pending bool) {
	t.Helper()
	items, err := service.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("list certificates: %v", err)
	}
	for _, item := range items {
		if item.ID == id {
			if item.Active != active || item.Pending != pending {
				t.Fatalf("unexpected certificate state for %d: active=%t pending=%t", id, item.Active, item.Pending)
			}
			return
		}
	}
	t.Fatalf("certificate %d not found", id)
}

func assertMigrationCount(t *testing.T, service *Service, name string, expected int64) {
	t.Helper()
	var count int64
	if err := service.DB.Model(&models.Migrations{}).Where("name = ?", name).Count(&count).Error; err != nil {
		t.Fatalf("count migration markers: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d migration markers, got %d", expected, count)
	}
}
