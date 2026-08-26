// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
)

func TestGetClusterTLSConfigGeneratesOnce(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemSecrets{})
	service := &Service{DB: db}

	first, err := service.GetClusterTLSConfig()
	if err != nil {
		t.Fatalf("generate cluster TLS config: %v", err)
	}
	second, err := service.GetClusterTLSConfig()
	if err != nil {
		t.Fatalf("reload cluster TLS config: %v", err)
	}
	if len(first.Certificates) != 1 || len(second.Certificates) != 1 {
		t.Fatal("expected one cluster certificate")
	}
	if first.Certificates[0].Leaf == nil {
		t.Fatal("expected parsed cluster certificate leaf")
	}
	if err := first.Certificates[0].Leaf.VerifyHostname(clusterCertificateName); err != nil {
		t.Fatalf("cluster certificate identity: %v", err)
	}
	if !bytes.Equal(first.Certificates[0].Certificate[0], second.Certificates[0].Certificate[0]) {
		t.Fatal("cluster certificate changed after initialization")
	}

	var markerCount int64
	if err := db.Model(&models.Migrations{}).
		Where("name = ?", clusterCertificateMarker).
		Count(&markerCount).Error; err != nil {
		t.Fatalf("count initialization marker: %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("expected one initialization marker, got %d", markerCount)
	}
	for _, name := range []string{clusterCertificateSecret, clusterPrivateKeySecret} {
		var count int64
		if err := db.Model(&models.SystemSecrets{}).Where("name = ?", name).Count(&count).Error; err != nil {
			t.Fatalf("count secret %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("expected secret %s", name)
		}
	}
}

func TestGetClusterTLSConfigAllowsExpiredCertificate(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemSecrets{})
	certificatePEM, privateKeyPEM := expiredClusterCertificate(t)
	if err := db.Create(&models.Migrations{Name: clusterCertificateMarker}).Error; err != nil {
		t.Fatalf("create initialization marker: %v", err)
	}
	for name, data := range map[string]string{
		clusterCertificateSecret: string(certificatePEM),
		clusterPrivateKeySecret:  string(privateKeyPEM),
	} {
		if err := db.Create(&models.SystemSecrets{Name: name, Data: data}).Error; err != nil {
			t.Fatalf("create secret %s: %v", name, err)
		}
	}

	config, err := (&Service{DB: db}).GetClusterTLSConfig()
	if err != nil {
		t.Fatalf("load expired cluster certificate: %v", err)
	}
	if len(config.Certificates) != 1 || config.Certificates[0].Leaf == nil {
		t.Fatal("expected parsed expired cluster certificate")
	}
}

func TestGetClusterTLSConfigRejectsWrongIdentityAfterInitialization(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemSecrets{})
	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("wrong.example.com")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}
	if err := db.Create(&models.Migrations{Name: clusterCertificateMarker}).Error; err != nil {
		t.Fatalf("create initialization marker: %v", err)
	}
	for name, data := range map[string]string{
		clusterCertificateSecret: string(certificatePEM),
		clusterPrivateKeySecret:  string(privateKeyPEM),
	} {
		if err := db.Create(&models.SystemSecrets{Name: name, Data: data}).Error; err != nil {
			t.Fatalf("create secret %s: %v", name, err)
		}
	}

	service := &Service{DB: db}
	if _, err := service.GetClusterTLSConfig(); err == nil {
		t.Fatal("expected wrong cluster certificate identity to fail")
	}
}

func TestGetClusterTLSConfigRejectsIncompleteInitializedStorage(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemSecrets{})
	if err := db.Create(&models.Migrations{Name: clusterCertificateMarker}).Error; err != nil {
		t.Fatalf("create initialization marker: %v", err)
	}

	service := &Service{DB: db}
	if _, err := service.GetClusterTLSConfig(); err == nil {
		t.Fatal("expected incomplete initialized cluster TLS storage to fail")
	}
}

func expiredClusterCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Sylve Internal Cluster Certificate"}},
		NotBefore:             now.Add(-2 * time.Hour),
		NotAfter:              now.Add(-time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{clusterCertificateName},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
}
