// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) GetRanges() ([]networkModels.DHCPRange, error) {
	var ranges []networkModels.DHCPRange
	if err := s.DB.
		Preload("StandardSwitch").
		Preload("ManualSwitch").
		Find(&ranges).Error; err != nil {
		return nil, err
	}
	return ranges, nil
}

func (s *Service) CreateRange(req *networkServiceInterfaces.CreateDHCPRangeRequest) error {
	if req.StandardSwitch == nil && req.ManualSwitch == nil {
		return fmt.Errorf("one_switch_required")
	}

	if req.StandardSwitch != nil && req.ManualSwitch != nil {
		return fmt.Errorf("maximum_one_switch_allowed")
	}

	expiry := uint(0)
	if req.Expiry != nil {
		expiry = *req.Expiry
	}

	raOnly := false
	if req.RAOnly != nil {
		raOnly = *req.RAOnly
	}

	slaac := false
	if req.SLAAC != nil {
		slaac = *req.SLAAC
	}

	if req.StandardSwitch != nil {
		var sw networkModels.StandardSwitch

		if err := s.DB.First(&sw, "id = ?", *req.StandardSwitch).Error; err != nil {
			return fmt.Errorf("invalid_standard_switch_id: %w", err)
		}
	}

	if req.ManualSwitch != nil {
		var sw networkModels.ManualSwitch

		if err := s.DB.First(&sw, "id = ?", *req.ManualSwitch).Error; err != nil {
			return fmt.Errorf("invalid_manual_switch_id: %w", err)
		}
	}

	if req.Type == "ipv4" && !utils.IsValidDHCPRange(req.StartIP, req.EndIP) {
		return fmt.Errorf("invalid_dhcp_range")
	}

	{
		var count int64
		q := s.DB.Model(&networkModels.DHCPRange{})
		if req.StandardSwitch != nil {
			q = q.Where("standard_switch_id = ? and type = ?", *req.StandardSwitch, req.Type)
		} else {
			q = q.Where("manual_switch_id = ? and type = ?", *req.ManualSwitch, req.Type)
		}
		if err := q.Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("switch_already_has_dhcp_range")
		}
	}

	newRange := &networkModels.DHCPRange{
		Type:             req.Type,
		StartIP:          req.StartIP,
		EndIP:            req.EndIP,
		StandardSwitchID: req.StandardSwitch,
		ManualSwitchID:   req.ManualSwitch,
		Expiry:           expiry,
		RAOnly:           raOnly,
		SLAAC:            slaac,
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(newRange).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if err := s.WriteDHCPConfig(); err != nil {
		return fmt.Errorf("failed_to_apply_new_dhcp_range: %w", err)
	}

	return nil
}

func (s *Service) ModifyRange(req *networkServiceInterfaces.ModifyDHCPRangeRequest) error {
	if req.StandardSwitch == nil && req.ManualSwitch == nil {
		return fmt.Errorf("one_switch_required")
	}

	if req.StandardSwitch != nil && req.ManualSwitch != nil {
		return fmt.Errorf("maximum_one_switch_allowed")
	}

	expiry := uint(0)
	if req.Expiry != nil {
		expiry = *req.Expiry
	}

	raOnly := false
	if req.RAOnly != nil {
		raOnly = *req.RAOnly
	}

	slaac := false
	if req.SLAAC != nil {
		slaac = *req.SLAAC
	}

	if req.StandardSwitch != nil {
		var sw networkModels.StandardSwitch
		if err := s.DB.First(&sw, "id = ?", *req.StandardSwitch).Error; err != nil {
			return fmt.Errorf("invalid_standard_switch_id: %w", err)
		}
	} else {
		var sw networkModels.ManualSwitch
		if err := s.DB.First(&sw, "id = ?", *req.ManualSwitch).Error; err != nil {
			return fmt.Errorf("invalid_manual_switch_id: %w", err)
		}
	}

	if req.Type == "ipv4" && !utils.IsValidDHCPRange(req.StartIP, req.EndIP) {
		return fmt.Errorf("invalid_dhcp_range")
	}

	current := &networkModels.DHCPRange{}
	if err := s.DB.First(current, "id = ?", req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("invalid_dhcp_range_id: %w", err)
		}
		return err
	}

	{
		var count int64
		q := s.DB.Model(&networkModels.DHCPRange{}).Where("id != ?", req.ID)
		if req.StandardSwitch != nil {
			q = q.Where("standard_switch_id = ? and type = ?", *req.StandardSwitch, req.Type)
		} else {
			q = q.Where("manual_switch_id = ? and type = ?", *req.ManualSwitch, req.Type)
		}
		if err := q.Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("switch_already_has_dhcp_range")
		}
	}

	current.StartIP = req.StartIP
	current.EndIP = req.EndIP
	current.StandardSwitchID = req.StandardSwitch
	current.ManualSwitchID = req.ManualSwitch
	current.Expiry = expiry
	current.RAOnly = raOnly
	current.SLAAC = slaac

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(current).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if err := s.WriteDHCPConfig(); err != nil {
		return fmt.Errorf("failed_to_apply_modified_dhcp_range: %w", err)
	}

	return nil
}

func (s *Service) DeleteRange(id uint) error {
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var rng networkModels.DHCPRange
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&rng, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("invalid_dhcp_range_id: %w", err)
			}
			return err
		}

		if err := tx.Where("dhcp_range_id = ?", id).
			Delete(&networkModels.DHCPStaticLease{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_associated_leases: %w", err)
		}

		if err := tx.Delete(&rng).Error; err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	if err := s.WriteDHCPConfig(); err != nil {
		return fmt.Errorf("failed_to_apply_dhcp_range_deletion: %w", err)
	}

	return nil
}
