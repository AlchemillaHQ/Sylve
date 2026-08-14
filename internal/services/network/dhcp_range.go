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
	"net/netip"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type normalizedDHCPRangeRequest struct {
	rangeType      string
	startIP        string
	endIP          string
	standardSwitch *uint
	manualSwitch   *uint
	expiry         uint
	raOnly         bool
	slaac          bool
}

func (s *Service) GetRanges() ([]networkModels.DHCPRange, error) {
	var ranges []networkModels.DHCPRange
	if err := s.DB.
		Preload("StandardSwitch").
		Preload("StandardSwitch.Ports").
		Preload("ManualSwitch").
		Find(&ranges).Error; err != nil {
		return nil, err
	}
	for i := range ranges {
		ensureStandardSwitchPortCollection(ranges[i].StandardSwitch)
	}
	return ranges, nil
}

func (s *Service) CreateRange(req *networkServiceInterfaces.CreateDHCPRangeRequest) (uint, error) {
	if req == nil {
		return 0, invalidDHCPRange("invalid_dhcp_range_request", nil)
	}

	normalized, err := normalizeDHCPRangeRequest(
		req.Type,
		req.StartIP,
		req.EndIP,
		req.StandardSwitch,
		req.ManualSwitch,
		req.Expiry,
		req.RAOnly,
		req.SLAAC,
	)
	if err != nil {
		return 0, err
	}

	var id uint
	err = s.applyDHCPMutation("create_dhcp_range", func(tx *gorm.DB) (bool, error) {
		if err := validateDHCPRangeSwitch(tx, normalized); err != nil {
			return false, err
		}
		if err := checkDHCPRangeConflict(tx, normalized, nil); err != nil {
			return false, err
		}

		newRange := networkModels.DHCPRange{
			Type:             normalized.rangeType,
			StartIP:          normalized.startIP,
			EndIP:            normalized.endIP,
			StandardSwitchID: copyUintPointer(normalized.standardSwitch),
			ManualSwitchID:   copyUintPointer(normalized.manualSwitch),
			Expiry:           normalized.expiry,
			RAOnly:           normalized.raOnly,
			SLAAC:            normalized.slaac,
		}
		if err := tx.Create(&newRange).Error; err != nil {
			return false, fmt.Errorf("create_dhcp_range: %w", err)
		}
		id = newRange.ID
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) ModifyRange(id uint, req *networkServiceInterfaces.ModifyDHCPRangeRequest) error {
	if id == 0 {
		return invalidDHCPRange("invalid_dhcp_range_id", nil)
	}
	if req == nil {
		return invalidDHCPRange("invalid_dhcp_range_request", nil)
	}

	normalized, err := normalizeDHCPRangeRequest(
		req.Type,
		req.StartIP,
		req.EndIP,
		req.StandardSwitch,
		req.ManualSwitch,
		req.Expiry,
		req.RAOnly,
		req.SLAAC,
	)
	if err != nil {
		return err
	}

	return s.applyDHCPMutation("modify_dhcp_range", func(tx *gorm.DB) (bool, error) {
		var current networkModels.DHCPRange
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, dhcpRangeNotFound("dhcp_range_not_found", err)
			}
			return false, fmt.Errorf("load_dhcp_range: %w", err)
		}
		if current.Type != normalized.rangeType {
			return false, conflictingDHCPRange("dhcp_range_type_immutable", nil)
		}
		if err := validateDHCPRangeSwitch(tx, normalized); err != nil {
			return false, err
		}
		if err := checkDHCPRangeConflict(tx, normalized, &id); err != nil {
			return false, err
		}
		if sameDHCPRange(&current, normalized) {
			return false, nil
		}

		updates := map[string]any{
			"start_ip":           normalized.startIP,
			"end_ip":             normalized.endIP,
			"standard_switch_id": nullableUint(normalized.standardSwitch),
			"manual_switch_id":   nullableUint(normalized.manualSwitch),
			"expiry":             normalized.expiry,
			"ra_only":            normalized.raOnly,
			"sla_ac":             normalized.slaac,
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return false, fmt.Errorf("update_dhcp_range: %w", err)
		}
		return true, nil
	})
}

func (s *Service) DeleteRange(id uint) error {
	if id == 0 {
		return invalidDHCPRange("invalid_dhcp_range_id", nil)
	}

	return s.applyDHCPMutation("delete_dhcp_range", func(tx *gorm.DB) (bool, error) {
		var current networkModels.DHCPRange
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, dhcpRangeNotFound("dhcp_range_not_found", err)
			}
			return false, fmt.Errorf("load_dhcp_range: %w", err)
		}

		if err := tx.Where("dhcp_range_id = ?", id).Delete(&networkModels.DHCPStaticLease{}).Error; err != nil {
			return false, fmt.Errorf("delete_dhcp_range_leases: %w", err)
		}
		if err := tx.Delete(&current).Error; err != nil {
			return false, fmt.Errorf("delete_dhcp_range: %w", err)
		}
		return true, nil
	})
}

func normalizeDHCPRangeRequest(
	rangeType string,
	startIP string,
	endIP string,
	standardSwitch *uint,
	manualSwitch *uint,
	expiry *uint,
	raOnly *bool,
	slaac *bool,
) (*normalizedDHCPRangeRequest, error) {
	if rangeType != "ipv4" && rangeType != "ipv6" {
		return nil, invalidDHCPRange("invalid_dhcp_range_request", nil)
	}
	if standardSwitch == nil && manualSwitch == nil {
		return nil, invalidDHCPRange("dhcp_range_switch_required", nil)
	}
	if standardSwitch != nil && manualSwitch != nil {
		return nil, invalidDHCPRange("dhcp_range_multiple_switches", nil)
	}
	if standardSwitch != nil && *standardSwitch == 0 {
		return nil, invalidDHCPRange("invalid_dhcp_standard_switch_id", nil)
	}
	if manualSwitch != nil && *manualSwitch == 0 {
		return nil, invalidDHCPRange("invalid_dhcp_manual_switch_id", nil)
	}
	if expiry == nil || uint64(*expiry) > MaxDHCPRangeExpirySeconds {
		return nil, invalidDHCPRange("invalid_dhcp_range_expiry", nil)
	}

	normalized := &normalizedDHCPRangeRequest{
		rangeType:      rangeType,
		standardSwitch: copyUintPointer(standardSwitch),
		manualSwitch:   copyUintPointer(manualSwitch),
		expiry:         *expiry,
	}
	if raOnly != nil {
		normalized.raOnly = *raOnly
	}
	if slaac != nil {
		normalized.slaac = *slaac
	}

	startIP = strings.TrimSpace(startIP)
	endIP = strings.TrimSpace(endIP)
	if rangeType == "ipv4" {
		if normalized.raOnly || normalized.slaac {
			return nil, invalidDHCPRange("dhcp_ipv4_ra_flags_not_allowed", nil)
		}
		start, startErr := netip.ParseAddr(startIP)
		end, endErr := netip.ParseAddr(endIP)
		if startErr != nil || endErr != nil || !start.Is4() || !end.Is4() || start.Compare(end) >= 0 {
			return nil, invalidDHCPRange("invalid_dhcp_ipv4_range", errors.Join(startErr, endErr))
		}
		normalized.startIP = start.String()
		normalized.endIP = end.String()
		return normalized, nil
	}

	if startIP == "" && endIP == "" {
		return normalized, nil
	}
	if startIP == "" || endIP == "" {
		return nil, invalidDHCPRange("invalid_dhcp_ipv6_range", nil)
	}
	start, startErr := netip.ParseAddr(startIP)
	end, endErr := netip.ParseAddr(endIP)
	if startErr != nil || endErr != nil || !start.Is6() || !end.Is6() || start.Is4In6() || end.Is4In6() || start.Zone() != "" || end.Zone() != "" || start.Compare(end) >= 0 {
		return nil, invalidDHCPRange("invalid_dhcp_ipv6_range", errors.Join(startErr, endErr))
	}
	normalized.startIP = start.String()
	normalized.endIP = end.String()
	return normalized, nil
}

func validateDHCPRangeSwitch(tx *gorm.DB, req *normalizedDHCPRangeRequest) error {
	var config networkModels.DHCPConfig
	if err := tx.First(&config).Error; err != nil {
		return fmt.Errorf("load_dhcp_config: %w", err)
	}

	var count int64
	if req.standardSwitch != nil {
		var sw networkModels.StandardSwitch
		if err := tx.First(&sw, "id = ?", *req.standardSwitch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return dhcpRangeNotFound("dhcp_standard_switch_not_found", err)
			}
			return fmt.Errorf("load_dhcp_standard_switch: %w", err)
		}
		if err := tx.Table("dhcp_standard_switches").
			Where("dhcp_config_id = ? AND standard_switch_id = ?", config.ID, *req.standardSwitch).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check_dhcp_standard_switch_membership: %w", err)
		}
	} else {
		var sw networkModels.ManualSwitch
		if err := tx.First(&sw, "id = ?", *req.manualSwitch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return dhcpRangeNotFound("dhcp_manual_switch_not_found", err)
			}
			return fmt.Errorf("load_dhcp_manual_switch: %w", err)
		}
		if err := tx.Table("dhcp_manual_switches").
			Where("dhcp_config_id = ? AND manual_switch_id = ?", config.ID, *req.manualSwitch).
			Count(&count).Error; err != nil {
			return fmt.Errorf("check_dhcp_manual_switch_membership: %w", err)
		}
	}
	if count == 0 {
		return conflictingDHCPRange("dhcp_switch_not_enabled", nil)
	}
	return nil
}

func checkDHCPRangeConflict(tx *gorm.DB, req *normalizedDHCPRangeRequest, excludeID *uint) error {
	query := tx.Model(&networkModels.DHCPRange{}).Where("type = ?", req.rangeType)
	if excludeID != nil {
		query = query.Where("id <> ?", *excludeID)
	}
	if req.standardSwitch != nil {
		query = query.Where("standard_switch_id = ?", *req.standardSwitch)
	} else {
		query = query.Where("manual_switch_id = ?", *req.manualSwitch)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("check_dhcp_range_conflict: %w", err)
	}
	if count > 0 {
		return conflictingDHCPRange("dhcp_switch_family_range_exists", nil)
	}
	return nil
}

func sameDHCPRange(current *networkModels.DHCPRange, requested *normalizedDHCPRangeRequest) bool {
	return current.Type == requested.rangeType &&
		current.StartIP == requested.startIP &&
		current.EndIP == requested.endIP &&
		sameOptionalUint(current.StandardSwitchID, requested.standardSwitch) &&
		sameOptionalUint(current.ManualSwitchID, requested.manualSwitch) &&
		current.Expiry == requested.expiry &&
		current.RAOnly == requested.raOnly &&
		current.SLAAC == requested.slaac
}

func sameOptionalUint(left *uint, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func copyUintPointer(value *uint) *uint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func nullableUint(value *uint) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Service) applyDHCPMutation(operation string, mutate func(*gorm.DB) (bool, error)) error {
	// DHCP state references switches and network objects and is rendered into
	// the singleton config, so use the same lock order as switch/config changes.
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	s.dhcpRuntimeMutex.Lock()
	defer s.dhcpRuntimeMutex.Unlock()

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("%s_begin_transaction: %w", operation, tx.Error)
	}
	transactionOpen := true
	rollback := func() {
		if !transactionOpen {
			return
		}
		transactionOpen = false
		if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
			logger.L.Error().Err(rollbackErr).Str("operation", operation).Msg("dhcp_transaction_rollback_failed")
		}
	}
	defer rollback()

	changed, err := mutate(tx)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	candidate, err := renderDHCPConfig(tx)
	if err != nil {
		return fmt.Errorf("%s_render_config: %w", operation, err)
	}
	snapshot, err := s.snapshotDHCPConfigFile()
	if err != nil {
		return fmt.Errorf("%s_snapshot_config: %w", operation, err)
	}
	if err := s.writeDHCPConfigFile(candidate); err != nil {
		rollback()
		if restoreErr := s.restoreDHCPConfigFile(snapshot); restoreErr != nil {
			logger.L.Error().Err(restoreErr).Str("operation", operation).Msg("dhcp_config_file_restore_failed")
		}
		return fmt.Errorf("%s_write_config: %w", operation, err)
	}
	if err := s.restartDNSMasq(); err != nil {
		rollback()
		s.restoreDHCPRuntimeAfterFailure(snapshot, operation+"_restart_failed")
		return fmt.Errorf("%s_restart_dnsmasq: %w", operation, err)
	}

	if err := tx.Commit().Error; err != nil {
		transactionOpen = false
		_ = tx.Rollback().Error
		s.restoreDHCPRuntimeAfterFailure(snapshot, operation+"_commit_failed")
		return fmt.Errorf("%s_commit_transaction: %w", operation, err)
	}
	transactionOpen = false
	return nil
}
