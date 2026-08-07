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
	"regexp"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

var (
	manualSwitchIfaceGet    = iface.Get
	manualSwitchNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func (s *Service) GetManualSwitches() ([]networkModels.ManualSwitch, error) {
	switches := make([]networkModels.ManualSwitch, 0)
	if err := s.DB.Find(&switches).Error; err != nil {
		return nil, err
	}
	return switches, nil
}

func (s *Service) CreateManualSwitch(name, bridge string) (*networkModels.ManualSwitch, error) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	name, bridge, err := normalizeManualSwitch(name, bridge)
	if err != nil {
		return nil, err
	}
	if err := s.checkManualSwitchConflicts(0, name, bridge); err != nil {
		return nil, err
	}
	if err := validateManualSwitchBridge(bridge); err != nil {
		return nil, err
	}

	sw := &networkModels.ManualSwitch{
		Name:   name,
		Bridge: bridge,
	}

	if err := s.DB.Create(sw).Error; err != nil {
		if isManualSwitchDuplicateError(err) {
			if conflictErr := s.checkManualSwitchConflicts(0, name, bridge); conflictErr != nil {
				return nil, conflictErr
			}
			return nil, manualSwitchConflict("manual_switch_conflict", err)
		}
		return nil, fmt.Errorf("create manual switch: %w", err)
	}

	return sw, nil
}

func (s *Service) DeleteManualSwitch(id uint) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var sw networkModels.ManualSwitch
	if err := s.DB.First(&sw, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return manualSwitchNotFound(err)
		}
		return fmt.Errorf("load manual switch: %w", err)
	}

	if err := s.checkManualSwitchUsage(id); err != nil {
		return err
	}

	if err := s.DB.Delete(&sw).Error; err != nil {
		return fmt.Errorf("delete manual switch: %w", err)
	}

	return nil
}

func (s *Service) UpdateManualSwitch(id uint, name, bridge string) (*networkModels.ManualSwitch, error) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var oldSw networkModels.ManualSwitch
	if err := s.DB.First(&oldSw, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, manualSwitchNotFound(err)
		}
		return nil, fmt.Errorf("load manual switch: %w", err)
	}

	name, bridge, err := normalizeManualSwitch(name, bridge)
	if err != nil {
		return nil, err
	}
	if err := s.checkManualSwitchUsage(id); err != nil {
		return nil, err
	}
	if err := s.checkManualSwitchConflicts(id, name, bridge); err != nil {
		return nil, err
	}
	if err := validateManualSwitchBridge(bridge); err != nil {
		return nil, err
	}

	oldSw.Name = name
	oldSw.Bridge = bridge

	if err := s.DB.Save(&oldSw).Error; err != nil {
		if isManualSwitchDuplicateError(err) {
			if conflictErr := s.checkManualSwitchConflicts(id, name, bridge); conflictErr != nil {
				return nil, conflictErr
			}
			return nil, manualSwitchConflict("manual_switch_conflict", err)
		}
		return nil, fmt.Errorf("update manual switch: %w", err)
	}

	return &oldSw, nil
}

func normalizeManualSwitch(name, bridge string) (string, string, error) {
	name = strings.TrimSpace(name)
	bridge = strings.TrimSpace(bridge)

	if name == "" || len(name) > MaxManualSwitchNameBytes || !manualSwitchNamePattern.MatchString(name) {
		return "", "", invalidManualSwitch("invalid_manual_switch_name", nil)
	}
	if bridge == "" || len(bridge) > MaxManualSwitchBridgeBytes {
		return "", "", invalidManualSwitch("invalid_manual_switch_bridge", nil)
	}

	return name, bridge, nil
}

func validateManualSwitchBridge(bridge string) error {
	br, err := manualSwitchIfaceGet(bridge)
	if err != nil {
		if isInterfaceMissingError(err) {
			return invalidManualSwitch("manual_switch_bridge_not_found", err)
		}
		return fmt.Errorf("inspect manual switch bridge %q: %w", bridge, err)
	}
	if br == nil {
		return invalidManualSwitch("manual_switch_bridge_not_found", nil)
	}

	for _, group := range br.Groups {
		if group == "bridge" {
			return nil
		}
	}
	return invalidManualSwitch("manual_switch_interface_not_bridge", nil)
}

func (s *Service) checkManualSwitchConflicts(id uint, name, bridge string) error {
	manualByName := s.DB.Model(&networkModels.ManualSwitch{}).Where("name = ?", name)
	manualByBridge := s.DB.Model(&networkModels.ManualSwitch{}).Where("bridge = ?", bridge)
	if id != 0 {
		manualByName = manualByName.Where("id <> ?", id)
		manualByBridge = manualByBridge.Where("id <> ?", id)
	}

	var count int64
	if err := manualByName.Count(&count).Error; err != nil {
		return fmt.Errorf("check manual switch name conflict: %w", err)
	}
	if count > 0 {
		return manualSwitchConflict("manual_switch_name_conflict", nil)
	}
	if err := s.DB.Model(&networkModels.StandardSwitch{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return fmt.Errorf("check standard switch name conflict: %w", err)
	}
	if count > 0 {
		return manualSwitchConflict("manual_switch_name_conflict", nil)
	}
	if err := manualByBridge.Count(&count).Error; err != nil {
		return fmt.Errorf("check manual switch bridge conflict: %w", err)
	}
	if count > 0 {
		return manualSwitchConflict("manual_switch_bridge_conflict", nil)
	}
	if err := s.DB.Model(&networkModels.StandardSwitch{}).Where("bridge_name = ?", bridge).Count(&count).Error; err != nil {
		return fmt.Errorf("check standard switch bridge conflict: %w", err)
	}
	if count > 0 {
		return manualSwitchConflict("manual_switch_bridge_conflict", nil)
	}

	return nil
}

func (s *Service) checkManualSwitchUsage(id uint) error {
	checks := []struct {
		query *gorm.DB
		code  string
		label string
	}{
		{
			query: s.DB.Model(&vmModels.Network{}).Where("switch_id = ? AND switch_type = ?", id, "manual"),
			code:  "manual_switch_in_use_by_vm",
			label: "VM",
		},
		{
			query: s.DB.Model(&jailModels.Network{}).Where("switch_id = ? AND switch_type = ?", id, "manual"),
			code:  "manual_switch_in_use_by_jail",
			label: "jail",
		},
		{
			query: s.DB.Table("dhcp_manual_switches").Where("manual_switch_id = ?", id),
			code:  "manual_switch_in_use_by_dhcp_config",
			label: "DHCP config",
		},
		{
			query: s.DB.Model(&networkModels.DHCPRange{}).Where("manual_switch_id = ?", id),
			code:  "manual_switch_in_use_by_dhcp_range",
			label: "DHCP range",
		},
	}

	for _, check := range checks {
		var count int64
		if err := check.query.Count(&count).Error; err != nil {
			return fmt.Errorf("check manual switch %s usage: %w", check.label, err)
		}
		if count > 0 {
			return manualSwitchInUse(check.code)
		}
	}

	return nil
}

func isManualSwitchDuplicateError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}
