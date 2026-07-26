// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"slices"
	"strconv"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	golibvirt "github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

// GetManagedVMCounts returns running and total Sylve-managed virtual machines.
func (s *Service) GetManagedVMCounts() (int, int, error) {
	var rids []uint
	if err := s.DB.Model(&vmModels.VM{}).Pluck("rid", &rids).Error; err != nil {
		return 0, 0, err
	}

	if len(rids) == 0 {
		return 0, len(rids), nil
	}
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, len(rids), nil
		}
		return 0, len(rids), err
	}
	if !slices.Contains(settings.Services, models.Virtualization) {
		return 0, len(rids), nil
	}

	states, err := s.GetDomainStates()
	if err != nil {
		return 0, len(rids), err
	}

	return countRunningManagedVMs(rids, states), len(rids), nil
}

func countRunningManagedVMs(rids []uint, states []libvirtServiceInterfaces.DomainState) int {
	managed := make(map[string]struct{}, len(rids))
	for _, rid := range rids {
		managed[strconv.FormatUint(uint64(rid), 10)] = struct{}{}
	}

	running := 0
	for _, state := range states {
		if _, ok := managed[state.Domain]; ok && state.State == golibvirt.DomainRunning {
			running++
		}
	}
	return running
}
