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
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

// validateChapSecret keeps CHAP-MD5 secrets within the length accepted by the
// system iSCSI tools.
func validateChapSecret(secret, field string) error {
	l := len(secret)
	if l < 12 || l > 16 {
		return invalidRequest(fmt.Sprintf("%s_must_be_12_to_16_characters", field))
	}
	for i := 0; i < len(secret); i++ {
		if secret[i] < 0x20 || secret[i] > 0x7e {
			return invalidRequest(fmt.Sprintf("%s_contains_invalid_characters", field))
		}
	}
	return nil
}

func validateAuthMethod(authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret string) error {
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
		if tgtChapName == "" || tgtChapSecret == "" {
			return invalidRequest("tgt_chap_name_and_secret_required_for_mutual_chap")
		}
		if err := validateQuotedConfigValue(chapName, "chap_name", maxQuotedLength); err != nil {
			return err
		}
		if err := validateQuotedConfigValue(tgtChapName, "tgt_chap_name", maxQuotedLength); err != nil {
			return err
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		if err := validateChapSecret(tgtChapSecret, "tgt_chap_secret"); err != nil {
			return err
		}
		return nil
	default:
		return invalidRequest(fmt.Sprintf("invalid_auth_method: %s", authMethod))
	}
}

func (s *Service) GetInitiators() ([]iscsiModels.ISCSIInitiator, error) {
	var initiators []iscsiModels.ISCSIInitiator
	if err := s.DB.Find(&initiators).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_initiators: %w", err)
	}
	return initiators, nil
}

func (s *Service) CreateInitiator(nickname, targetAddress, targetName, initiatorName, authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	nickname = strings.TrimSpace(nickname)
	targetAddress = strings.TrimSpace(targetAddress)
	targetName = strings.TrimSpace(targetName)
	initiatorName = strings.TrimSpace(initiatorName)
	chapName = strings.TrimSpace(chapName)
	tgtChapName = strings.TrimSpace(tgtChapName)

	if nickname == "" {
		return invalidRequest("nickname_required")
	}
	if targetAddress == "" {
		return invalidRequest("target_address_required")
	}
	if targetName == "" {
		return invalidRequest("target_name_required")
	}
	if err := validateBareConfigToken(nickname, "nickname", maxNicknameLength); err != nil {
		return err
	}
	var err error
	targetAddress, err = normalizeInitiatorTargetAddress(targetAddress)
	if err != nil {
		return err
	}
	if err := validateBareConfigToken(targetName, "target_name", maxISCSINameLength); err != nil {
		return err
	}
	if initiatorName != "" {
		if err := validateBareConfigToken(initiatorName, "initiator_name", maxISCSINameLength); err != nil {
			return err
		}
	}

	var duplicateCount int64
	if err := s.DB.Model(&iscsiModels.ISCSIInitiator{}).Where("nickname = ?", nickname).Count(&duplicateCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_initiator_nickname: %w", err)
	}
	if duplicateCount > 0 {
		return resourceConflict("initiator_with_nickname_exists", nil)
	}

	authMethod = strings.TrimSpace(authMethod)
	if authMethod == "" {
		authMethod = "None"
	}
	if authMethod == "None" {
		chapName, chapSecret, tgtChapName, tgtChapSecret = "", "", "", ""
	} else if authMethod == "CHAP" {
		tgtChapName, tgtChapSecret = "", ""
	}

	if err := validateAuthMethod(authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret); err != nil {
		return err
	}

	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      nickname,
		TargetAddress: targetAddress,
		TargetName:    targetName,
		InitiatorName: initiatorName,
		AuthMethod:    authMethod,
		CHAPName:      chapName,
		CHAPSecret:    chapSecret,
		TgtCHAPName:   tgtChapName,
		TgtCHAPSecret: tgtChapSecret,
	}

	if err := s.DB.Create(&initiator).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return resourceConflict("initiator_with_nickname_exists", nil)
		}
		return fmt.Errorf("failed_to_create_initiator: %w", err)
	}

	return s.writeConfig(true)
}

func (s *Service) UpdateInitiator(id uint, nickname, targetAddress, targetName, initiatorName, authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	nickname = strings.TrimSpace(nickname)
	targetAddress = strings.TrimSpace(targetAddress)
	targetName = strings.TrimSpace(targetName)
	initiatorName = strings.TrimSpace(initiatorName)
	chapName = strings.TrimSpace(chapName)
	tgtChapName = strings.TrimSpace(tgtChapName)

	if nickname == "" {
		return invalidRequest("nickname_required")
	}
	if targetAddress == "" {
		return invalidRequest("target_address_required")
	}
	if targetName == "" {
		return invalidRequest("target_name_required")
	}
	if err := validateBareConfigToken(nickname, "nickname", maxNicknameLength); err != nil {
		return err
	}
	var err error
	targetAddress, err = normalizeInitiatorTargetAddress(targetAddress)
	if err != nil {
		return err
	}
	if err := validateBareConfigToken(targetName, "target_name", maxISCSINameLength); err != nil {
		return err
	}
	if initiatorName != "" {
		if err := validateBareConfigToken(initiatorName, "initiator_name", maxISCSINameLength); err != nil {
			return err
		}
	}

	var initiator iscsiModels.ISCSIInitiator
	if err := s.DB.Where("id = ?", id).First(&initiator).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("initiator_not_found", err)
		}
		return fmt.Errorf("failed_to_get_initiator: %w", err)
	}

	if initiator.Nickname != nickname {
		var duplicateCount int64
		if err := s.DB.Model(&iscsiModels.ISCSIInitiator{}).Where("nickname = ? AND id != ?", nickname, id).Count(&duplicateCount).Error; err != nil {
			return fmt.Errorf("failed_to_check_initiator_nickname: %w", err)
		}
		if duplicateCount > 0 {
			return resourceConflict("initiator_with_nickname_exists", nil)
		}
	}

	authMethod = strings.TrimSpace(authMethod)
	if authMethod == "" {
		authMethod = "None"
	}
	if chapSecret == "" && (initiator.AuthMethod == "CHAP" || initiator.AuthMethod == "MutualCHAP") {
		chapSecret = initiator.CHAPSecret
	}
	if tgtChapSecret == "" && initiator.AuthMethod == "MutualCHAP" {
		tgtChapSecret = initiator.TgtCHAPSecret
	}
	if authMethod == "None" {
		chapName, chapSecret, tgtChapName, tgtChapSecret = "", "", "", ""
	} else if authMethod == "CHAP" {
		tgtChapName, tgtChapSecret = "", ""
	}

	if err := validateAuthMethod(authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret); err != nil {
		return err
	}

	initiator.Nickname = nickname
	initiator.TargetAddress = targetAddress
	initiator.TargetName = targetName
	initiator.InitiatorName = initiatorName
	initiator.AuthMethod = authMethod
	initiator.CHAPName = chapName
	initiator.CHAPSecret = chapSecret
	initiator.TgtCHAPName = tgtChapName
	initiator.TgtCHAPSecret = tgtChapSecret

	if err := s.DB.Save(&initiator).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return resourceConflict("initiator_with_nickname_exists", nil)
		}
		return fmt.Errorf("failed_to_update_initiator: %w", err)
	}

	return s.writeConfig(true)
}

func (s *Service) DeleteInitiator(id uint) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var initiator iscsiModels.ISCSIInitiator
	if err := s.DB.Where("id = ?", id).First(&initiator).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("initiator_not_found", err)
		}
		return fmt.Errorf("failed_to_get_initiator: %w", err)
	}

	if err := s.DB.Delete(&initiator).Error; err != nil {
		return fmt.Errorf("failed_to_delete_initiator: %w", err)
	}

	return s.writeConfig(true)
}

func (s *Service) ConnectInitiator(id uint) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	var initiator iscsiModels.ISCSIInitiator
	if err := s.DB.Where("id = ?", id).First(&initiator).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return resourceNotFound("initiator_not_found", err)
		}
		return fmt.Errorf("failed_to_get_initiator: %w", err)
	}

	if _, err := utils.RunCommandAllowExitCode("/usr/bin/iscsictl", []int{0}, "-An", initiator.Nickname); err != nil {
		logger.L.Error().Err(err).Uint("initiator_id", id).Msg("failed to connect iSCSI initiator")
		return applyFailed("failed_to_connect_initiator", err)
	}

	return nil
}
