// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

func (s *Service) GetManualSwitches() ([]networkModels.ManualSwitch, error) {
	var switches []networkModels.ManualSwitch
	if err := s.DB.Find(&switches).Error; err != nil {
		return nil, err
	}
	return switches, nil
}

func (s *Service) CreateManualSwitch(name, bridge string) (*networkModels.ManualSwitch, error) {
	br, err := iface.Get(bridge)
	if err != nil {
		return nil, err
	}

	if br == nil {
		return nil, fmt.Errorf("bridge %s does not exist", bridge)
	}

	found := false
	for _, group := range br.Groups {
		if group == "bridge" {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("interface %s is not a bridge", bridge)
	}

	var count int64
	if err := s.DB.Model(&networkModels.ManualSwitch{}).
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		return nil, err
	}

	if count > 0 {
		return nil, fmt.Errorf("bridge_name_in_use")
	}

	sw := &networkModels.ManualSwitch{
		Name:   name,
		Bridge: bridge,
	}

	if err := s.DB.Create(sw).Error; err != nil {
		return nil, err
	}

	return sw, nil
}

func (s *Service) DeleteManualSwitch(id uint) error {
	var sw networkModels.ManualSwitch
	if err := s.DB.First(&sw, id).Error; err != nil {
		return err
	}

	if err := s.DB.Delete(&sw).Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) UpdateManualSwitch(id uint, name, bridge string) (*networkModels.ManualSwitch, error) {
	var count int64
	if err := s.DB.Model(&networkModels.ManualSwitch{}).
		Where("bridge = ? AND id != ?", bridge, id).
		Count(&count).Error; err != nil {
		return nil, err
	}

	if count > 0 {
		return nil, fmt.Errorf("bridge_in_use")
	}

	var oldSw networkModels.ManualSwitch
	if err := s.DB.First(&oldSw, id).Error; err != nil {
		return nil, err
	}

	br, err := iface.Get(bridge)
	if err != nil {
		return nil, err
	}

	if br == nil {
		return nil, fmt.Errorf("bridge %s does not exist", bridge)
	}

	found := false
	for _, group := range br.Groups {
		if group == "bridge" {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("interface %s is not a bridge", bridge)
	}

	oldSw.Name = name
	oldSw.Bridge = bridge

	if err := s.DB.Save(&oldSw).Error; err != nil {
		return nil, err
	}

	return &oldSw, nil
}
