// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"gorm.io/gorm"
)

var (
	ErrCloudInitTemplateInvalid  = errors.New("invalid_cloud_init_template")
	ErrCloudInitTemplateNotFound = errors.New("cloud_init_template_not_found")
	ErrCloudInitTemplateConflict = errors.New("cloud_init_template_conflict")
)

func normalizeCloudInitTemplateRequest(
	req utilitiesServiceInterfaces.CloudInitTemplateRequest,
) (utilitiesServiceInterfaces.CloudInitTemplateRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 255 {
		return req, ErrCloudInitTemplateInvalid
	}
	if strings.TrimSpace(req.User) == "" || strings.TrimSpace(req.Meta) == "" {
		return req, ErrCloudInitTemplateInvalid
	}
	if req.NetworkConfig == nil {
		return req, ErrCloudInitTemplateInvalid
	}
	return req, nil
}

func isCloudInitTemplateUniqueError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key")
}

func cloudInitTemplateNameAvailable(
	tx *gorm.DB,
	name string,
	excludeID uint,
) error {
	var templates []utilitiesModels.CloudInitTemplate
	if err := tx.Select("id", "name").Find(&templates).Error; err != nil {
		return fmt.Errorf("check cloud-init template name: %w", err)
	}
	for _, template := range templates {
		if template.ID != excludeID && strings.EqualFold(strings.TrimSpace(template.Name), name) {
			return ErrCloudInitTemplateConflict
		}
	}
	return nil
}

func (s *Service) ListTemplates() ([]utilitiesModels.CloudInitTemplate, error) {
	templates := make([]utilitiesModels.CloudInitTemplate, 0)
	if err := s.DB.Order("name COLLATE NOCASE ASC, id ASC").Find(&templates).Error; err != nil {
		return nil, fmt.Errorf("list cloud-init templates: %w", err)
	}
	return templates, nil
}

func (s *Service) AddTemplate(
	req utilitiesServiceInterfaces.CloudInitTemplateRequest,
) (*utilitiesModels.CloudInitTemplate, error) {
	normalized, err := normalizeCloudInitTemplateRequest(req)
	if err != nil {
		return nil, err
	}

	s.cloudInitTemplateMu.Lock()
	defer s.cloudInitTemplateMu.Unlock()

	if err := cloudInitTemplateNameAvailable(s.DB, normalized.Name, 0); err != nil {
		return nil, err
	}

	template := utilitiesModels.CloudInitTemplate{
		Name:          normalized.Name,
		User:          normalized.User,
		Meta:          normalized.Meta,
		NetworkConfig: *normalized.NetworkConfig,
	}
	if err := s.DB.Create(&template).Error; err != nil {
		if isCloudInitTemplateUniqueError(err) {
			return nil, ErrCloudInitTemplateConflict
		}
		return nil, fmt.Errorf("create cloud-init template: %w", err)
	}

	return &template, nil
}

func (s *Service) EditTemplate(
	id uint,
	req utilitiesServiceInterfaces.CloudInitTemplateRequest,
) (*utilitiesModels.CloudInitTemplate, error) {
	if id == 0 {
		return nil, ErrCloudInitTemplateInvalid
	}
	normalized, err := normalizeCloudInitTemplateRequest(req)
	if err != nil {
		return nil, err
	}

	s.cloudInitTemplateMu.Lock()
	defer s.cloudInitTemplateMu.Unlock()

	var updated utilitiesModels.CloudInitTemplate
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var existing utilitiesModels.CloudInitTemplate
		if err := tx.First(&existing, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCloudInitTemplateNotFound
			}
			return fmt.Errorf("find cloud-init template: %w", err)
		}
		if err := cloudInitTemplateNameAvailable(tx, normalized.Name, id); err != nil {
			return err
		}

		result := tx.Model(&utilitiesModels.CloudInitTemplate{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"name":           normalized.Name,
				"user":           normalized.User,
				"meta":           normalized.Meta,
				"network_config": *normalized.NetworkConfig,
			})
		if result.Error != nil {
			if isCloudInitTemplateUniqueError(result.Error) {
				return ErrCloudInitTemplateConflict
			}
			return fmt.Errorf("replace cloud-init template: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrCloudInitTemplateNotFound
		}
		if err := tx.First(&updated, id).Error; err != nil {
			return fmt.Errorf("load replaced cloud-init template: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &updated, nil
}

func (s *Service) DeleteTemplate(
	id uint,
) (utilitiesServiceInterfaces.CloudInitTemplateIdentity, error) {
	identity := utilitiesServiceInterfaces.CloudInitTemplateIdentity{ID: id}
	if id == 0 {
		return identity, ErrCloudInitTemplateInvalid
	}

	s.cloudInitTemplateMu.Lock()
	defer s.cloudInitTemplateMu.Unlock()

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var template utilitiesModels.CloudInitTemplate
		if err := tx.First(&template, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCloudInitTemplateNotFound
			}
			return fmt.Errorf("find cloud-init template: %w", err)
		}
		identity.Name = template.Name

		result := tx.Where("id = ?", id).Delete(&utilitiesModels.CloudInitTemplate{})
		if result.Error != nil {
			return fmt.Errorf("delete cloud-init template: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrCloudInitTemplateNotFound
		}
		return nil
	})
	if err != nil {
		return identity, err
	}

	return identity, nil
}
