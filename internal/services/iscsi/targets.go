// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"errors"
	"fmt"
	"strings"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"gorm.io/gorm"
)

func validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error {
	switch authMethod {
	case "None":
		return nil
	case "CHAP":
		if chapName == "" || chapSecret == "" {
			return invalidRequest("chap_name_and_secret_required_for_chap")
		}
		if err := validateQuotedConfigValue(chapName, "chap_name", maxQuotedLength); err != nil {
			return err
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		return nil
	case "MutualCHAP":
		if chapName == "" || chapSecret == "" {
			return invalidRequest("chap_name_and_secret_required_for_mutual_chap")
		}
		if mutualChapName == "" || mutualChapSecret == "" {
			return invalidRequest("mutual_chap_name_and_secret_required_for_mutual_chap")
		}
		if err := validateQuotedConfigValue(chapName, "chap_name", maxQuotedLength); err != nil {
			return err
		}
		if err := validateQuotedConfigValue(mutualChapName, "mutual_chap_name", maxQuotedLength); err != nil {
			return err
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		if err := validateChapSecret(mutualChapSecret, "mutual_chap_secret"); err != nil {
			return err
		}
		return nil
	default:
		return invalidRequest(fmt.Sprintf("invalid_auth_method: %s", authMethod))
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
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	targetName = strings.TrimSpace(targetName)
	alias = strings.TrimSpace(alias)
	chapName = strings.TrimSpace(chapName)
	mutualChapName = strings.TrimSpace(mutualChapName)

	if targetName == "" {
		return invalidRequest("target_name_required")
	}
	if err := validateBareConfigToken(targetName, "target_name", maxISCSINameLength); err != nil {
		return err
	}
	if err := validateQuotedConfigValue(alias, "alias", maxQuotedLength); err != nil {
		return err
	}

	authMethod = strings.TrimSpace(authMethod)
	if authMethod == "" {
		authMethod = "None"
	}
	if authMethod == "None" {
		chapName, chapSecret, mutualChapName, mutualChapSecret = "", "", "", ""
	} else if authMethod == "CHAP" {
		mutualChapName, mutualChapSecret = "", ""
	}

	if err := validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret); err != nil {
		return err
	}

	var duplicateCount int64
	if err := s.DB.Model(&iscsiModels.ISCSITarget{}).Where("target_name = ?", targetName).Count(&duplicateCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_target_name: %w", err)
	}
	if duplicateCount > 0 {
		return resourceConflict("target_with_name_exists", nil)
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
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return resourceConflict("target_with_name_exists", nil)
		}
		return fmt.Errorf("failed_to_create_target: %w", err)
	}

	return s.writeTargetConfig(true)
}

func (s *Service) UpdateTarget(id uint, targetName, alias, authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	targetName = strings.TrimSpace(targetName)
	alias = strings.TrimSpace(alias)
	chapName = strings.TrimSpace(chapName)
	mutualChapName = strings.TrimSpace(mutualChapName)

	if targetName == "" {
		return invalidRequest("target_name_required")
	}
	if err := validateBareConfigToken(targetName, "target_name", maxISCSINameLength); err != nil {
		return err
	}
	if err := validateQuotedConfigValue(alias, "alias", maxQuotedLength); err != nil {
		return err
	}

	authMethod = strings.TrimSpace(authMethod)
	if authMethod == "" {
		authMethod = "None"
	}

	var target iscsiModels.ISCSITarget
	if err := s.DB.Where("id = ?", id).First(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("target_not_found", err)
		}
		return fmt.Errorf("failed_to_get_target: %w", err)
	}

	if target.TargetName != targetName {
		var duplicateCount int64
		if err := s.DB.Model(&iscsiModels.ISCSITarget{}).Where("target_name = ? AND id != ?", targetName, id).Count(&duplicateCount).Error; err != nil {
			return fmt.Errorf("failed_to_check_target_name: %w", err)
		}
		if duplicateCount > 0 {
			return resourceConflict("target_with_name_exists", nil)
		}
	}

	if chapSecret == "" && (target.AuthMethod == "CHAP" || target.AuthMethod == "MutualCHAP") {
		chapSecret = target.CHAPSecret
	}
	if mutualChapSecret == "" && target.AuthMethod == "MutualCHAP" {
		mutualChapSecret = target.MutualCHAPSecret
	}
	if authMethod == "None" {
		chapName, chapSecret, mutualChapName, mutualChapSecret = "", "", "", ""
	} else if authMethod == "CHAP" {
		mutualChapName, mutualChapSecret = "", ""
	}

	if err := validateTargetAuthMethod(authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret); err != nil {
		return err
	}

	target.TargetName = targetName
	target.Alias = alias
	target.AuthMethod = authMethod
	target.CHAPName = chapName
	target.CHAPSecret = chapSecret
	target.MutualCHAPName = mutualChapName
	target.MutualCHAPSecret = mutualChapSecret

	if err := s.DB.Save(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return resourceConflict("target_with_name_exists", nil)
		}
		return fmt.Errorf("failed_to_update_target: %w", err)
	}

	return s.writeTargetConfig(true)
}

func (s *Service) DeleteTarget(id uint) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var target iscsiModels.ISCSITarget
	if err := s.DB.Where("id = ?", id).First(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("target_not_found", err)
		}
		return fmt.Errorf("failed_to_get_target: %w", err)
	}
	if err := s.ensureTargetHasNoSessions(target.TargetName); err != nil {
		return err
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("target_id = ?", id).Delete(&iscsiModels.ISCSITargetPortal{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_target_portals: %w", err)
		}
		if err := tx.Where("target_id = ?", id).Delete(&iscsiModels.ISCSITargetLUN{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_target_luns: %w", err)
		}
		if err := tx.Delete(&target).Error; err != nil {
			return fmt.Errorf("failed_to_delete_target: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	return s.writeTargetConfig(true)
}

func (s *Service) AddPortal(targetID uint, address string, port int) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	address = strings.TrimSpace(address)
	if address == "" {
		return invalidRequest("portal_address_required")
	}
	if port == 0 {
		port = 3260
	}
	if port < 1 || port > 65535 {
		return invalidRequest("portal_port_must_be_between_1_and_65535")
	}
	var err error
	address, err = normalizePortalAddress(address)
	if err != nil {
		return err
	}

	if err := s.DB.Where("id = ?", targetID).First(&iscsiModels.ISCSITarget{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("target_not_found", err)
		}
		return fmt.Errorf("failed_to_get_target: %w", err)
	}

	var duplicateCount int64
	if err := s.DB.Model(&iscsiModels.ISCSITargetPortal{}).
		Where("target_id = ? AND address = ? AND port = ?", targetID, address, port).
		Count(&duplicateCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_target_portal: %w", err)
	}
	if duplicateCount > 0 {
		return resourceConflict("portal_already_exists", nil)
	}

	portal := iscsiModels.ISCSITargetPortal{
		TargetID: targetID,
		Address:  address,
		Port:     port,
	}

	if err := s.DB.Create(&portal).Error; err != nil {
		return fmt.Errorf("failed_to_add_portal: %w", err)
	}

	return s.writeTargetConfig(true)
}

func (s *Service) RemovePortal(targetID, id uint) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var portal iscsiModels.ISCSITargetPortal
	if err := s.DB.Where("id = ? AND target_id = ?", id, targetID).First(&portal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("portal_not_found", err)
		}
		return fmt.Errorf("failed_to_get_portal: %w", err)
	}

	if err := s.DB.Delete(&portal).Error; err != nil {
		return fmt.Errorf("failed_to_remove_portal: %w", err)
	}

	return s.writeTargetConfig(true)
}

func (s *Service) AddLUN(targetID uint, lunNumber int, zvol string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	zvol = strings.TrimSpace(zvol)
	if err := validateZVol(zvol); err != nil {
		return err
	}

	if lunNumber < 0 {
		return invalidRequest("lun_number_must_be_non_negative")
	}

	if err := s.DB.Where("id = ?", targetID).First(&iscsiModels.ISCSITarget{}).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("target_not_found", err)
		}
		return fmt.Errorf("failed_to_get_target: %w", err)
	}

	var duplicateCount int64
	if err := s.DB.Model(&iscsiModels.ISCSITargetLUN{}).
		Where("target_id = ? AND lun_number = ?", targetID, lunNumber).
		Count(&duplicateCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_lun_number: %w", err)
	}
	if duplicateCount > 0 {
		return resourceConflict("lun_number_already_in_use", nil)
	}

	if err := s.DB.Model(&iscsiModels.ISCSITargetLUN{}).
		Where("z_vol = ?", zvol).
		Count(&duplicateCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_lun_zvol: %w", err)
	}
	if duplicateCount > 0 {
		return resourceConflict("zvol_already_in_use", nil)
	}

	lun := iscsiModels.ISCSITargetLUN{
		TargetID:  targetID,
		LUNNumber: lunNumber,
		ZVol:      zvol,
	}

	if err := s.DB.Create(&lun).Error; err != nil {
		return fmt.Errorf("failed_to_add_lun: %w", err)
	}

	return s.writeTargetConfig(true)
}

func (s *Service) RemoveLUN(targetID, id uint) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var lun iscsiModels.ISCSITargetLUN
	if err := s.DB.Where("id = ? AND target_id = ?", id, targetID).First(&lun).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("lun_not_found", err)
		}
		return fmt.Errorf("failed_to_get_lun: %w", err)
	}

	if err := s.DB.Delete(&lun).Error; err != nil {
		return fmt.Errorf("failed_to_remove_lun: %w", err)
	}

	return s.writeTargetConfig(true)
}
