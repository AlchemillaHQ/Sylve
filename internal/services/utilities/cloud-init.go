package utilities

import (
	"errors"
	"fmt"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"gorm.io/gorm"
)

func (s *Service) ListTemplates() ([]utilitiesModels.CloudInitTemplate, error) {
	var templates []utilitiesModels.CloudInitTemplate
	err := s.DB.Find(&templates).Error
	if err != nil {
		return nil, err
	}

	return templates, nil
}

func (s *Service) AddTemplate(req utilitiesServiceInterfaces.AddTemplateRequest) error {
	template := utilitiesModels.CloudInitTemplate{
		Name: req.Name,
		User: req.User,
		Meta: req.Meta,
	}

	if err := s.DB.Create(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("template_name_taken: %s", req.Name)
		}
		return err
	}

	return nil
}

func (s *Service) EditTemplate(req utilitiesServiceInterfaces.EditTemplateRequest) error {
	var template utilitiesModels.CloudInitTemplate
	if err := s.DB.First(&template, req.ID).Error; err != nil {
		return err
	}

	updates := map[string]any{}

	if req.Name != "" && req.Name != template.Name {
		updates["name"] = req.Name
	}

	if req.User != "" {
		updates["user"] = req.User
	}

	if req.Meta != "" {
		updates["meta"] = req.Meta
	}

	if len(updates) == 0 {
		return nil
	}

	if err := s.DB.Model(&template).Updates(updates).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("template_name_taken: %s", req.Name)
		}
		return err
	}

	return nil
}

func (s *Service) DeleteTemplate(id uint) error {
	err := s.DB.Delete(&utilitiesModels.CloudInitTemplate{}, id).Error
	if err != nil {
		return err
	}

	return nil
}
