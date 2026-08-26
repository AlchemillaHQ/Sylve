// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/crypto"
	"gorm.io/gorm"
)

const (
	clusterCertificateSecret = "cluster_tls_cert"
	clusterPrivateKeySecret  = "cluster_tls_key"
	clusterCertificateMarker = "generate_internal_cluster_tls_certificate_v1"
	clusterCertificateName   = "sylve-cluster.internal"
)

func (s *Service) GetClusterTLSConfig() (*tls.Config, error) {
	var certificatePEM, privateKeyPEM string

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var markerCount int64
		if err := tx.Model(&models.Migrations{}).
			Where("name = ?", clusterCertificateMarker).
			Count(&markerCount).Error; err != nil {
			return fmt.Errorf("check cluster TLS initialization: %w", err)
		}

		storedCertificatePEM, err := secretData(tx, clusterCertificateSecret)
		if err != nil {
			return err
		}
		certificatePEM = storedCertificatePEM
		storedPrivateKeyPEM, err := secretData(tx, clusterPrivateKeySecret)
		if err != nil {
			return err
		}
		privateKeyPEM = storedPrivateKeyPEM

		if markerCount > 0 {
			if certificatePEM == "" || privateKeyPEM == "" {
				return fmt.Errorf("cluster TLS certificate storage is incomplete")
			}
			if _, err := loadClusterKeyPair(certificatePEM, privateKeyPEM); err != nil {
				return fmt.Errorf("load cluster TLS certificate: %w", err)
			}
			return nil
		}

		if certificatePEM == "" || privateKeyPEM == "" {
			certPEM, keyPEM, err := crypto.GenerateClusterCertificate()
			if err != nil {
				return fmt.Errorf("generate cluster TLS certificate: %w", err)
			}
			certificatePEM = string(certPEM)
			privateKeyPEM = string(keyPEM)
		} else if _, err := loadClusterKeyPair(certificatePEM, privateKeyPEM); err != nil {
			certPEM, keyPEM, generateErr := crypto.GenerateClusterCertificate()
			if generateErr != nil {
				return fmt.Errorf("replace invalid cluster TLS certificate: %w", generateErr)
			}
			certificatePEM = string(certPEM)
			privateKeyPEM = string(keyPEM)
		}

		if err := upsertSecretData(tx, clusterCertificateSecret, certificatePEM); err != nil {
			return err
		}
		if err := upsertSecretData(tx, clusterPrivateKeySecret, privateKeyPEM); err != nil {
			return err
		}
		if err := tx.Create(&models.Migrations{Name: clusterCertificateMarker}).Error; err != nil {
			return fmt.Errorf("record cluster TLS initialization: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	certificate, err := loadClusterKeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load cluster TLS certificate: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func loadClusterKeyPair(certificatePEM, privateKeyPEM string) (tls.Certificate, error) {
	certificate, err := tls.X509KeyPair([]byte(certificatePEM), []byte(privateKeyPEM))
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, fmt.Errorf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse certificate: %w", err)
	}
	if !leaf.NotAfter.After(leaf.NotBefore) {
		return tls.Certificate{}, fmt.Errorf("certificate validity period is invalid")
	}
	serverAuth := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
			serverAuth = true
			break
		}
	}
	if !serverAuth {
		return tls.Certificate{}, fmt.Errorf("certificate is not valid for TLS server authentication")
	}
	if err := leaf.VerifyHostname(clusterCertificateName); err != nil {
		return tls.Certificate{}, fmt.Errorf("certificate identity is invalid: %w", err)
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func secretData(tx *gorm.DB, name string) (string, error) {
	var secret models.SystemSecrets
	err := tx.Where("name = ?", name).First(&secret).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load cluster TLS secret %q: %w", name, err)
	}
	return secret.Data, nil
}

func upsertSecretData(tx *gorm.DB, name, data string) error {
	var secret models.SystemSecrets
	err := tx.Where("name = ?", name).First(&secret).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := tx.Create(&models.SystemSecrets{Name: name, Data: data}).Error; err != nil {
			return fmt.Errorf("create cluster TLS secret %q: %w", name, err)
		}
	case err != nil:
		return fmt.Errorf("load cluster TLS secret %q: %w", name, err)
	default:
		if err := tx.Model(&secret).Update("data", data).Error; err != nil {
			return fmt.Errorf("update cluster TLS secret %q: %w", name, err)
		}
	}
	return nil
}
