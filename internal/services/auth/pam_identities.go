// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

func (s *Service) getOrCreatePAMIdentity(username string) (models.PAMIdentity, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return models.PAMIdentity{}, fmt.Errorf("username_required")
	}

	var identity models.PAMIdentity
	if err := s.DB.Where("username = ?", username).First(&identity).Error; err == nil {
		return identity, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.PAMIdentity{}, fmt.Errorf("failed_to_lookup_pam_identity: %w", err)
	}

	identity = models.PAMIdentity{
		Username: username,
	}
	if err := s.DB.Create(&identity).Error; err != nil {
		if err := s.DB.Where("username = ?", username).First(&identity).Error; err == nil {
			return identity, nil
		}
		return models.PAMIdentity{}, fmt.Errorf("failed_to_create_pam_identity: %w", err)
	}

	return identity, nil
}
