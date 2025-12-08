// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"

	cpuid "github.com/klauspost/cpuid/v2"
	"github.com/shirou/gopsutil/cpu"
)

func (s *Service) GetCPUInfo(usageOnly bool) (infoServiceInterfaces.CPUInfo, error) {
	percentages, err := cpu.Percent(time.Second, false)
	if err != nil {
		return infoServiceInterfaces.CPUInfo{}, err
	}

	used := float64(0)
	if len(percentages) > 0 {
		used = percentages[0]
	}

	if usageOnly {
		return infoServiceInterfaces.CPUInfo{
			Usage: used,
		}, nil
	}

	physicalCores := int16(cpuid.CPU.PhysicalCores)
	threadsPerCore := int16(cpuid.CPU.ThreadsPerCore)

	logicalCores := int16(utils.GetLogicalCores())
	if logicalCores == 0 {
		logicalCores = int16(cpuid.CPU.LogicalCores)
	}

	sockets := (logicalCores) / (physicalCores * threadsPerCore)

	return infoServiceInterfaces.CPUInfo{
		Name:           cpuid.CPU.BrandName,
		Sockets:        sockets,
		PhysicalCores:  int16(cpuid.CPU.PhysicalCores),
		ThreadsPerCore: int16(cpuid.CPU.ThreadsPerCore),
		LogicalCores:   logicalCores,
		Family:         int16(cpuid.CPU.Family),
		Model:          int16(cpuid.CPU.Model),
		Features:       cpuid.CPU.FeatureSet(),
		CacheLine:      int16(cpuid.CPU.CacheLine),
		Cache: struct {
			L1D int16 `json:"l1d"`
			L1I int16 `json:"l1i"`
			L2  int16 `json:"l2"`
			L3  int16 `json:"l3"`
		}{
			L1D: int16(cpuid.CPU.Cache.L1D),
			L1I: int16(cpuid.CPU.Cache.L1I),
			L2:  int16(cpuid.CPU.Cache.L2),
			L3:  int16(cpuid.CPU.Cache.L3),
		},
		Frequency: int64(cpuid.CPU.Hz),
		Usage:     used,
	}, nil
}

func (s *Service) GetCPUUsageHistorical() ([]infoModels.CPU, error) {
	historicalData, err := db.GetAll[infoModels.CPU](s.DB)
	if err != nil {
		return nil, err
	}

	return historicalData, nil
}
