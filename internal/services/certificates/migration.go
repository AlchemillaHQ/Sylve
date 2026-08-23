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
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
	"gorm.io/gorm"
)

func (s *Service) migrateLegacyConfiguration(ctx context.Context, legacy *internal.TLSConfig) error {
	applied, err := migrationApplied(s.DB.WithContext(ctx), legacyMigrationMarker)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}

	active, err := activeCertificateFromDB(s.DB.WithContext(ctx))
	if err != nil {
		return err
	}
	if active != nil {
		if _, err := parseCertificateMaterial(active.CertificatePEM, active.PrivateKeyPEM); err != nil {
			return fmt.Errorf("configured public TLS certificate is invalid: %w", err)
		}
		return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := deleteLegacyCertificateSecrets(tx); err != nil {
				return err
			}
			return recordMigration(tx, legacyMigrationMarker)
		})
	}

	fileMaterial, hasLegacyFiles, err := readLegacyCertificate(legacy)
	if err != nil {
		return err
	}

	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		applied, err := migrationApplied(tx, legacyMigrationMarker)
		if err != nil || applied {
			return err
		}

		active, err := activeCertificateFromDB(tx)
		if err != nil {
			return err
		}
		if active != nil {
			if _, err := parseCertificateMaterial(active.CertificatePEM, active.PrivateKeyPEM); err != nil {
				return fmt.Errorf("configured public TLS certificate is invalid: %w", err)
			}
			if err := deleteLegacyCertificateSecrets(tx); err != nil {
				return err
			}
			return recordMigration(tx, legacyMigrationMarker)
		}
		var certificate models.Certificate
		if hasLegacyFiles {
			certificate, err = findOrCreateLegacyCertificate(tx, fileMaterial)
		} else {
			storedMaterial, hasStoredCertificate, loadErr := readLegacyCertificateSecrets(tx)
			if loadErr != nil {
				return loadErr
			}
			if hasStoredCertificate {
				certificate, err = findOrCreateLegacyDefaultCertificate(tx, storedMaterial)
			} else {
				certificate, _, err = ensureDefaultCertificate(tx)
			}
		}
		if err != nil {
			return err
		}
		if err := setActiveCertificate(tx, certificate.ID); err != nil {
			return err
		}
		if err := deleteLegacyCertificateSecrets(tx); err != nil {
			return err
		}
		return recordMigration(tx, legacyMigrationMarker)
	})
}

func (s *Service) ensureActiveCertificate(ctx context.Context) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		defaultCertificate, _, err := ensureDefaultCertificate(tx)
		if err != nil {
			return err
		}

		var settings models.CertificateSettings
		err = tx.First(&settings, 1).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&models.CertificateSettings{
				ID:                  1,
				ActiveCertificateID: defaultCertificate.ID,
			}).Error
		}
		if err != nil {
			return fmt.Errorf("load certificate settings: %w", err)
		}
		if settings.ActiveCertificateID == 0 {
			return tx.Model(&settings).Update("active_certificate_id", defaultCertificate.ID).Error
		}

		var active models.Certificate
		if err := tx.First(&active, settings.ActiveCertificateID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Model(&settings).Update("active_certificate_id", defaultCertificate.ID).Error
			}
			return fmt.Errorf("load active certificate: %w", err)
		}
		if _, err := parseCertificateMaterial(active.CertificatePEM, active.PrivateKeyPEM); err != nil {
			return fmt.Errorf("active public TLS certificate is invalid: %w", err)
		}
		return nil
	})
}

func readLegacyCertificate(legacy *internal.TLSConfig) (certificateMaterial, bool, error) {
	if legacy == nil {
		return certificateMaterial{}, false, nil
	}
	certificatePath := strings.TrimSpace(legacy.CertFile)
	privateKeyPath := strings.TrimSpace(legacy.KeyFile)
	if certificatePath == "" && privateKeyPath == "" {
		return certificateMaterial{}, false, nil
	}
	if certificatePath == "" || privateKeyPath == "" {
		return certificateMaterial{}, false, fmt.Errorf("legacy tlsConfig must contain both certFile and keyFile")
	}

	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return certificateMaterial{}, false, fmt.Errorf("read legacy TLS certificate: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return certificateMaterial{}, false, fmt.Errorf("read legacy TLS private key: %w", err)
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		return certificateMaterial{}, false, fmt.Errorf("validate legacy TLS certificate: %w", err)
	}
	return material, true, nil
}

func readLegacyCertificateSecrets(tx *gorm.DB) (certificateMaterial, bool, error) {
	var secrets []models.SystemSecrets
	if err := tx.Where("name IN ?", []string{"tls_cert", "tls_key"}).Find(&secrets).Error; err != nil {
		return certificateMaterial{}, false, fmt.Errorf("load legacy TLS certificate storage: %w", err)
	}

	values := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		values[secret.Name] = secret.Data
	}
	certificatePEM := strings.TrimSpace(values["tls_cert"])
	privateKeyPEM := strings.TrimSpace(values["tls_key"])
	if certificatePEM == "" && privateKeyPEM == "" {
		return certificateMaterial{}, false, nil
	}
	if certificatePEM == "" || privateKeyPEM == "" {
		return certificateMaterial{}, false, fmt.Errorf("legacy TLS certificate storage is incomplete")
	}
	material, err := parseCertificateMaterial(certificatePEM, privateKeyPEM)
	if err != nil {
		return certificateMaterial{}, false, fmt.Errorf("validate stored legacy TLS certificate: %w", err)
	}
	return material, true, nil
}

func deleteLegacyCertificateSecrets(tx *gorm.DB) error {
	if err := tx.Where("name IN ?", []string{"tls_cert", "tls_key"}).Delete(&models.SystemSecrets{}).Error; err != nil {
		return fmt.Errorf("remove legacy TLS certificate storage: %w", err)
	}
	return nil
}

func findOrCreateLegacyDefaultCertificate(tx *gorm.DB, material certificateMaterial) (models.Certificate, error) {
	var certificate models.Certificate
	err := tx.Where("type = ?", models.CertificateTypeSystemDefault).First(&certificate).Error
	if err == nil {
		certificate.Domain = domainFromCertificate(material.leaf)
		certificate.Staging = false
		certificate.DynamicDNSEntryID = nil
		applyMaterial(&certificate, material, nil)
		if err := tx.Save(&certificate).Error; err != nil {
			return models.Certificate{}, fmt.Errorf("preserve legacy system default certificate: %w", err)
		}
		return certificate, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Certificate{}, fmt.Errorf("load system default certificate: %w", err)
	}

	name, err := uniqueCertificateName(tx, defaultCertificateName)
	if err != nil {
		return models.Certificate{}, err
	}
	certificate = modelFromMaterial(
		name,
		models.CertificateTypeSystemDefault,
		domainFromCertificate(material.leaf),
		false,
		material,
		nil,
	)
	if err := tx.Create(&certificate).Error; err != nil {
		return models.Certificate{}, fmt.Errorf("save legacy system default certificate: %w", err)
	}
	return certificate, nil
}

func findOrCreateLegacyCertificate(tx *gorm.DB, material certificateMaterial) (models.Certificate, error) {
	var existing models.Certificate
	err := tx.Where("certificate_pem = ? AND private_key_pem = ?", material.certificatePEM, material.privateKeyPEM).
		First(&existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Certificate{}, fmt.Errorf("find migrated TLS certificate: %w", err)
	}

	name, err := uniqueCertificateName(tx, "Migrated TLS Certificate")
	if err != nil {
		return models.Certificate{}, err
	}
	certificate := modelFromMaterial(
		name,
		models.CertificateTypeImported,
		domainFromCertificate(material.leaf),
		false,
		material,
		nil,
	)
	if err := tx.Create(&certificate).Error; err != nil {
		return models.Certificate{}, fmt.Errorf("save migrated TLS certificate: %w", err)
	}
	return certificate, nil
}

func ensureDefaultCertificate(tx *gorm.DB) (models.Certificate, certificateMaterial, error) {
	var certificate models.Certificate
	err := tx.Where("type = ?", models.CertificateTypeSystemDefault).First(&certificate).Error
	if err == nil {
		material, parseErr := parseCertificateMaterial(certificate.CertificatePEM, certificate.PrivateKeyPEM)
		if parseErr == nil {
			return certificate, material, nil
		}
		certificatePEM, privateKeyPEM, generateErr := sylveCrypto.GenerateSelfSignedCertificate()
		if generateErr != nil {
			return models.Certificate{}, certificateMaterial{}, fmt.Errorf("regenerate system default certificate: %w", generateErr)
		}
		material, parseErr = parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
		if parseErr != nil {
			return models.Certificate{}, certificateMaterial{}, parseErr
		}
		applyMaterial(&certificate, material, nil)
		if err := tx.Save(&certificate).Error; err != nil {
			return models.Certificate{}, certificateMaterial{}, fmt.Errorf("save regenerated system default certificate: %w", err)
		}
		return certificate, material, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Certificate{}, certificateMaterial{}, fmt.Errorf("load system default certificate: %w", err)
	}

	certificatePEM, privateKeyPEM, err := sylveCrypto.GenerateSelfSignedCertificate()
	if err != nil {
		return models.Certificate{}, certificateMaterial{}, fmt.Errorf("generate system default certificate: %w", err)
	}
	material, err := parseCertificateMaterial(string(certificatePEM), string(privateKeyPEM))
	if err != nil {
		return models.Certificate{}, certificateMaterial{}, err
	}
	name, err := uniqueCertificateName(tx, defaultCertificateName)
	if err != nil {
		return models.Certificate{}, certificateMaterial{}, err
	}
	certificate = modelFromMaterial(
		name,
		models.CertificateTypeSystemDefault,
		defaultCertificateDomain,
		false,
		material,
		nil,
	)
	if err := tx.Create(&certificate).Error; err != nil {
		return models.Certificate{}, certificateMaterial{}, fmt.Errorf("save system default certificate: %w", err)
	}
	return certificate, material, nil
}

func activeCertificateFromDB(db *gorm.DB) (*models.Certificate, error) {
	var settings models.CertificateSettings
	if err := db.First(&settings, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load certificate settings: %w", err)
	}
	if settings.ActiveCertificateID == 0 {
		return nil, nil
	}

	var certificate models.Certificate
	if err := db.First(&certificate, settings.ActiveCertificateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load configured public TLS certificate: %w", err)
	}
	return &certificate, nil
}

func setActiveCertificate(tx *gorm.DB, certificateID uint) error {
	var settings models.CertificateSettings
	err := tx.First(&settings, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := tx.Create(&models.CertificateSettings{ID: 1, ActiveCertificateID: certificateID}).Error; err != nil {
			return fmt.Errorf("create certificate settings: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load certificate settings: %w", err)
	}
	if err := tx.Model(&settings).Update("active_certificate_id", certificateID).Error; err != nil {
		return fmt.Errorf("update active certificate: %w", err)
	}
	return nil
}

func migrationApplied(db *gorm.DB, name string) (bool, error) {
	var count int64
	if err := db.Model(&models.Migrations{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check migration %q: %w", name, err)
	}
	return count > 0, nil
}

func recordMigration(tx *gorm.DB, name string) error {
	applied, err := migrationApplied(tx, name)
	if err != nil || applied {
		return err
	}
	if err := tx.Create(&models.Migrations{Name: name}).Error; err != nil {
		return fmt.Errorf("record migration %q: %w", name, err)
	}
	return nil
}

func uniqueCertificateName(tx *gorm.DB, base string) (string, error) {
	for suffix := 0; suffix < 1000; suffix++ {
		name := base
		if suffix > 0 {
			name = fmt.Sprintf("%s (%d)", base, suffix+1)
		}
		var count int64
		if err := tx.Model(&models.Certificate{}).Where("name = ?", name).Count(&count).Error; err != nil {
			return "", fmt.Errorf("check certificate name: %w", err)
		}
		if count == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: no available certificate name", ErrCertificateConflict)
}
