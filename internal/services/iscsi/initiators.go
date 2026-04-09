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

// validateChapSecret enforces RFC 3720 11.1.1? CHAP-MD5 secrets must be 12–16 bytes.
// I don't kknow if this right tbh, but it's better than allowing arbitrary length secrets which may cause issues with the kernel or ctld.
func validateChapSecret(secret, field string) error {
	l := len(secret)
	if l < 12 || l > 16 {
		return fmt.Errorf("%s_must_be_12_to_16_characters", field)
	}
	return nil
}

func validateAuthMethod(authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret string) error {
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
		if tgtChapName == "" || tgtChapSecret == "" {
			return fmt.Errorf("tgt_chap_name_and_secret_required_for_mutual_chap")
		}
		if err := validateChapSecret(chapSecret, "chap_secret"); err != nil {
			return err
		}
		if err := validateChapSecret(tgtChapSecret, "tgt_chap_secret"); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("invalid_auth_method: %s", authMethod)
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
	if nickname == "" {
		return fmt.Errorf("nickname_required")
	}
	if targetAddress == "" {
		return fmt.Errorf("target_address_required")
	}
	if targetName == "" {
		return fmt.Errorf("target_name_required")
	}

	if err := s.DB.Where("nickname = ?", nickname).First(&iscsiModels.ISCSIInitiator{}).Error; err == nil {
		return fmt.Errorf("initiator_with_nickname_exists")
	}

	if authMethod == "" {
		authMethod = "None"
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
		return fmt.Errorf("failed_to_create_initiator: %w", err)
	}

	return s.WriteConfig(true)
}

func (s *Service) UpdateInitiator(id uint, nickname, targetAddress, targetName, initiatorName, authMethod, chapName, chapSecret, tgtChapName, tgtChapSecret string) error {
	if nickname == "" {
		return fmt.Errorf("nickname_required")
	}
	if targetAddress == "" {
		return fmt.Errorf("target_address_required")
	}
	if targetName == "" {
		return fmt.Errorf("target_name_required")
	}

	var initiator iscsiModels.ISCSIInitiator
	if err := s.DB.Where("id = ?", id).First(&initiator).Error; err != nil {
		return fmt.Errorf("initiator_not_found: %w", err)
	}

	if initiator.Nickname != nickname {
		if err := s.DB.Where("nickname = ? AND id != ?", nickname, id).First(&iscsiModels.ISCSIInitiator{}).Error; err == nil {
			return fmt.Errorf("initiator_with_nickname_exists")
		}
	}

	if authMethod == "" {
		authMethod = "None"
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
		return fmt.Errorf("failed_to_update_initiator: %w", err)
	}

	return s.WriteConfig(true)
}

func (s *Service) DeleteInitiator(id uint) error {
	var initiator iscsiModels.ISCSIInitiator
	if err := s.DB.Where("id = ?", id).First(&initiator).Error; err != nil {
		return fmt.Errorf("initiator_not_found: %w", err)
	}

	if err := s.DB.Delete(&initiator).Error; err != nil {
		return fmt.Errorf("failed_to_delete_initiator: %w", err)
	}

	return s.WriteConfig(true)
}
