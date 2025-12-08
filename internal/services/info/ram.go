// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/system/swapctl"

	ram "github.com/shirou/gopsutil/mem"
)

func (s *Service) GetRAMInfo() (infoServiceInterfaces.RAMInfo, error) {
	ramInfo, err := ram.VirtualMemory()
	if err != nil {
		return infoServiceInterfaces.RAMInfo{}, err
	}

	return infoServiceInterfaces.RAMInfo{
		Total:       ramInfo.Total,
		Free:        ramInfo.Free,
		UsedPercent: ramInfo.UsedPercent,
	}, nil
}

func (s *Service) GetSwapInfo() (infoServiceInterfaces.SwapInfo, error) {
	swapDevices, err := swapctl.GetSwapDevices()
	if len(swapDevices) == 0 {
		return infoServiceInterfaces.SwapInfo{
			Total:       0,
			Free:        0,
			UsedPercent: 0,
		}, nil
	}

	swapInfo, err := ram.SwapMemory()

	if err != nil {
		return infoServiceInterfaces.SwapInfo{
			Total:       0,
			Free:        0,
			UsedPercent: 0,
		}, err
	}

	return infoServiceInterfaces.SwapInfo{
		Total:       swapInfo.Total,
		Free:        swapInfo.Free,
		UsedPercent: swapInfo.UsedPercent,
	}, nil
}

func (s *Service) GetRAMUsageHistorical() ([]infoModels.RAM, error) {
	historicalData, err := db.GetAll[infoModels.RAM](s.DB)
	if err != nil {
		return nil, err
	}

	return historicalData, nil
}

func (s *Service) GetSwapUsageHistorical() ([]infoModels.Swap, error) {
	historicalData, err := db.GetAll[infoModels.Swap](s.DB)
	if err != nil {
		return nil, err
	}

	return historicalData, nil
}
