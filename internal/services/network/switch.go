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
	networkModels "sylve/internal/db/models/network"
)

func (s *Service) GetBridgeNameByID(id uint) (string, error) {
	var standardSwitches []networkModels.StandardSwitch
	if err := s.DB.
		Preload("Ports").
		Preload("AddressObj.Entries").
		Preload("Address6Obj.Entries").
		Find(&standardSwitches).Error; err != nil {
		return "", err
	}

	for _, sw := range standardSwitches {
		if sw.ID == int(id) {
			return sw.BridgeName, nil
		}
	}

	return "", fmt.Errorf("switch/bridge with ID %d not found", id)
}
