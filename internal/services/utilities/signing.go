// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/crypto"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

const downloadSigningSecretName = "DownloadSigningSecret"

func (s *Service) getCachedDownloadSigningSecret() string {
	if s == nil {
		return ""
	}

	s.signingSecretMu.RLock()
	defer s.signingSecretMu.RUnlock()
	return strings.TrimSpace(s.downloadSignSecret)
}

func (s *Service) setCachedDownloadSigningSecret(secret string) {
	if s == nil {
		return
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return
	}

	s.signingSecretMu.Lock()
	s.downloadSignSecret = secret
	s.signingSecretMu.Unlock()
}

func (s *Service) getOrCreateDownloadSigningSecret() (string, error) {
	if s == nil || s.DB == nil {
		return "", fmt.Errorf("db_unavailable")
	}

	if cached := s.getCachedDownloadSigningSecret(); cached != "" {
		return cached, nil
	}

	var secret models.SystemSecrets
	err := s.DB.Where("name = ?", downloadSigningSecretName).First(&secret).Error
	if err == nil {
		if strings.TrimSpace(secret.Data) != "" {
			s.setCachedDownloadSigningSecret(secret.Data)
			return strings.TrimSpace(secret.Data), nil
		}

		fresh := utils.GenerateRandomString(64)
		if updateErr := s.DB.Model(&secret).Update("data", fresh).Error; updateErr != nil {
			return "", fmt.Errorf("download_signing_secret_update_failed: %w", updateErr)
		}
		s.setCachedDownloadSigningSecret(fresh)
		return fresh, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("download_signing_secret_lookup_failed: %w", err)
	}

	fresh := utils.GenerateRandomString(64)
	created := models.SystemSecrets{
		Name: downloadSigningSecretName,
		Data: fresh,
	}
	if createErr := s.DB.Create(&created).Error; createErr != nil {
		// Handle concurrent creation on startup races.
		if lookupErr := s.DB.Where("name = ?", downloadSigningSecretName).First(&secret).Error; lookupErr == nil && strings.TrimSpace(secret.Data) != "" {
			s.setCachedDownloadSigningSecret(secret.Data)
			return strings.TrimSpace(secret.Data), nil
		}
		return "", fmt.Errorf("download_signing_secret_create_failed: %w", createErr)
	}

	s.setCachedDownloadSigningSecret(fresh)
	return fresh, nil
}

func (s *Service) BuildDownloadSignature(input string, expires int64) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || expires <= 0 {
		return "", fmt.Errorf("invalid_signature_input")
	}

	secret, err := s.getOrCreateDownloadSigningSecret()
	if err != nil {
		return "", err
	}

	return crypto.GenerateSignature(input, expires, []byte(secret)), nil
}

func (s *Service) ValidateDownloadSignature(input string, expires int64, provided string) (bool, error) {
	expected, err := s.BuildDownloadSignature(input, expires)
	if err != nil {
		return false, err
	}

	provided = strings.TrimSpace(provided)
	if len(provided) != len(expected) {
		return false, nil
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1, nil
}
