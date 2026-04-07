// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"fmt"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
)

func validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error {
	switch authMethod {
	case "None":
		return nil
	case "CHAP":
		if chapName == "" || chapSecret == "" {
			return fmt.Errorf("chap_name_and_secret_required_for_chap")
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		return nil
	case "MutualCHAP":
		if chapName == "" || chapSecret == "" {
			return fmt.Errorf("chap_name_and_secret_required_for_mutual_chap")
		}
		if mutualChapName == "" || mutualChapSecret == "" {
			return fmt.Errorf("mutual_chap_name_and_secret_required_for_mutual_chap")
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		if err := validateChapSecret(mutualChapSecret, "mutual_chap_secret"); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("invalid_auth_method: %s", authMethod)
	}
}

func (s *Service) GetTargets() ([]iscsiModels.ISCSITarget, error) {
	var targets []iscsiModels.ISCSITarget
	if err := s.DB.Preload("Portals").Preload("LUNs").Find(&targets).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_targets: %w", err)
	}
	return targets, nil
}

func (s *Service) CreateTarget(targetName, alias, authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error {
	if targetName == "" {
		return fmt.Errorf("target_name_required")
	}

	if authMethod == "" {
		authMethod = "None"
	}

	if err := validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret); err != nil {
		return err
	}

	if err := s.DB.Where("target_name = ?", targetName).First(&iscsiModels.ISCSITarget{}).Error; err == nil {
		return fmt.Errorf("target_with_name_exists")
	}

	target := iscsiModels.ISCSITarget{
		TargetName:       targetName,
		Alias:            alias,
		AuthMethod:       authMethod,
		CHAPName:         chapName,
		CHAPSecret:       chapSecret,
		MutualCHAPName:   mutualChapName,
		MutualCHAPSecret: mutualChapSecret,
	}

	if err := s.DB.Create(&target).Error; err != nil {
		return fmt.Errorf("failed_to_create_target: %w", err)
	}

	return nil
}

func (s *Service) UpdateTarget(id uint, targetName, alias, authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error {
	if targetName == "" {
		return fmt.Errorf("target_name_required")
	}

	if authMethod == "" {
		authMethod = "None"
	}

	if err := validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret); err != nil {
		return err
	}

	var target iscsiModels.ISCSITarget
	if err := s.DB.Where("id = ?", id).First(&target).Error; err != nil {
		return fmt.Errorf("target_not_found: %w", err)
	}

	if target.TargetName != targetName {
		if err := s.DB.Where("target_name = ? AND id != ?", targetName, id).First(&iscsiModels.ISCSITarget{}).Error; err == nil {
			return fmt.Errorf("target_with_name_exists")
		}
	}

	target.TargetName = targetName
	target.Alias = alias
	target.AuthMethod = authMethod
	target.CHAPName = chapName
	target.CHAPSecret = chapSecret
	target.MutualCHAPName = mutualChapName
	target.MutualCHAPSecret = mutualChapSecret

	if err := s.DB.Save(&target).Error; err != nil {
		return fmt.Errorf("failed_to_update_target: %w", err)
	}

	return s.WriteTargetConfig(true)
}

func (s *Service) DeleteTarget(id uint) error {
	var target iscsiModels.ISCSITarget
	if err := s.DB.Where("id = ?", id).First(&target).Error; err != nil {
		return fmt.Errorf("target_not_found: %w", err)
	}

	if err := s.DB.Where("target_id = ?", id).Delete(&iscsiModels.ISCSITargetPortal{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_target_portals: %w", err)
	}

	if err := s.DB.Where("target_id = ?", id).Delete(&iscsiModels.ISCSITargetLUN{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_target_luns: %w", err)
	}

	if err := s.DB.Delete(&target).Error; err != nil {
		return fmt.Errorf("failed_to_delete_target: %w", err)
	}

	return s.WriteTargetConfig(true)
}

func (s *Service) AddPortal(targetID uint, address string, port int) error {
	if address == "" {
		return fmt.Errorf("portal_address_required")
	}

	if err := s.DB.Where("id = ?", targetID).First(&iscsiModels.ISCSITarget{}).Error; err != nil {
		return fmt.Errorf("target_not_found: %w", err)
	}

	if port == 0 {
		port = 3260
	}

	portal := iscsiModels.ISCSITargetPortal{
		TargetID: targetID,
		Address:  address,
		Port:     port,
	}

	if err := s.DB.Create(&portal).Error; err != nil {
		return fmt.Errorf("failed_to_add_portal: %w", err)
	}

	return s.WriteTargetConfig(true)
}

func (s *Service) RemovePortal(id uint) error {
	var portal iscsiModels.ISCSITargetPortal
	if err := s.DB.Where("id = ?", id).First(&portal).Error; err != nil {
		return fmt.Errorf("portal_not_found: %w", err)
	}

	if err := s.DB.Delete(&portal).Error; err != nil {
		return fmt.Errorf("failed_to_remove_portal: %w", err)
	}

	return s.WriteTargetConfig(true)
}

func (s *Service) AddLUN(targetID uint, lunNumber int, zvol string) error {
	if zvol == "" {
		return fmt.Errorf("zvol_required")
	}

	if lunNumber < 0 {
		return fmt.Errorf("lun_number_must_be_non_negative")
	}

	if err := s.DB.Where("id = ?", targetID).First(&iscsiModels.ISCSITarget{}).Error; err != nil {
		return fmt.Errorf("target_not_found: %w", err)
	}

	var existing iscsiModels.ISCSITargetLUN
	if err := s.DB.Where("target_id = ? AND lun_number = ?", targetID, lunNumber).First(&existing).Error; err == nil {
		return fmt.Errorf("lun_number_already_in_use")
	}

	lun := iscsiModels.ISCSITargetLUN{
		TargetID:  targetID,
		LUNNumber: lunNumber,
		ZVol:      zvol,
	}

	if err := s.DB.Create(&lun).Error; err != nil {
		return fmt.Errorf("failed_to_add_lun: %w", err)
	}

	return s.WriteTargetConfig(true)
}

func (s *Service) RemoveLUN(id uint) error {
	var lun iscsiModels.ISCSITargetLUN
	if err := s.DB.Where("id = ?", id).First(&lun).Error; err != nil {
		return fmt.Errorf("lun_not_found: %w", err)
	}

	if err := s.DB.Delete(&lun).Error; err != nil {
		return fmt.Errorf("failed_to_remove_lun: %w", err)
	}

	return s.WriteTargetConfig(true)
}
