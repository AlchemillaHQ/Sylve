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
	info := infoServiceInterfaces.CPUInfo{
		Usage: 0,
	}

	if perc, err := cpu.Percent(time.Second, false); err == nil && len(perc) > 0 {
		info.Usage = perc[0]
	}

	if usageOnly {
		return info, nil
	}

	logical := int16(utils.GetLogicalCores())
	if logical <= 0 {
		logical = int16(cpuid.CPU.LogicalCores)
	}
	if logical <= 0 {
		logical = 1
	}

	physical := int16(cpuid.CPU.PhysicalCores)
	if physical <= 0 {
		physical = logical
	}

	threadsPerCore := int16(cpuid.CPU.ThreadsPerCore)
	if threadsPerCore <= 0 {
		threadsPerCore = 1
	}

	sockets := int16(1)
	if physical*threadsPerCore > 0 {
		if v := logical / (physical * threadsPerCore); v > 0 {
			sockets = v
		}
	}

	cache := struct {
		L1D int16 `json:"l1d"`
		L1I int16 `json:"l1i"`
		L2  int16 `json:"l2"`
		L3  int16 `json:"l3"`
	}{
		L1D: int16(cpuid.CPU.Cache.L1D),
		L1I: int16(cpuid.CPU.Cache.L1I),
		L2:  int16(cpuid.CPU.Cache.L2),
		L3:  int16(cpuid.CPU.Cache.L3),
	}

	freq := int64(cpuid.CPU.Hz)
	if freq < 0 {
		freq = 0
	}

	info.Name = cpuid.CPU.BrandName
	info.Sockets = sockets
	info.PhysicalCores = physical
	info.ThreadsPerCore = threadsPerCore
	info.LogicalCores = logical
	info.Family = int16(cpuid.CPU.Family)
	info.Model = int16(cpuid.CPU.Model)
	info.Features = cpuid.CPU.FeatureSet()
	info.CacheLine = int16(cpuid.CPU.CacheLine)
	info.Cache = cache
	info.Frequency = freq

	return info, nil
}

func (s *Service) GetCPUUsageHistorical() ([]infoModels.CPU, error) {
	historicalData, err := db.GetAll[infoModels.CPU](s.DB)
	if err != nil {
		return nil, err
	}

	return historicalData, nil
}
