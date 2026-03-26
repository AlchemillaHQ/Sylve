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

	"github.com/alchemillahq/gzfs"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/klauspost/cpuid/v2"
	"github.com/shirou/gopsutil/cpu"

	"gorm.io/gorm"
)

var _ infoServiceInterfaces.InfoServiceInterface = (*Service)(nil)

type Service struct {
	DB          *gorm.DB
	TelemetryDB *gorm.DB
	GZFS        *gzfs.Client
}

func NewInfoService(db *gorm.DB, telemetryDB *gorm.DB, gzfs *gzfs.Client) infoServiceInterfaces.InfoServiceInterface {
	if telemetryDB == nil {
		panic("info service requires a non-nil telemetry database")
	}

	return &Service{
		DB:          db,
		TelemetryDB: telemetryDB,
		GZFS:        gzfs,
	}
}

func (s *Service) telemetryDB() *gorm.DB {
	if s.TelemetryDB == nil {
		panic("info service telemetry database is nil")
	}

	return s.TelemetryDB
}

func (s *Service) cpuDB() *gorm.DB { return s.telemetryDB() }

func (s *Service) ramDB() *gorm.DB { return s.telemetryDB() }

func (s *Service) swapDB() *gorm.DB { return s.telemetryDB() }

func (s *Service) networkDB() *gorm.DB { return s.telemetryDB() }

func (s *Service) auditDB() *gorm.DB { return s.telemetryDB() }

func (s *Service) GetNodeInfo() (infoServiceInterfaces.NodeInfo, error) {
	nodeInfo := infoServiceInterfaces.NodeInfo{}

	hostname, err := utils.GetSystemHostname()
	if err != nil {
		return nodeInfo, err
	}

	nodeInfo.Hostname = hostname

	nodeInfo.LogicalCores = int16(cpuid.CPU.LogicalCores)
	if nodeInfo.LogicalCores <= 0 {
		nodeInfo.LogicalCores = int16(1)
	}

	if perc, err := cpu.Percent(time.Second, false); err == nil && len(perc) > 0 {
		nodeInfo.CPUUsage = perc[0]
	} else {
		nodeInfo.CPUUsage = 0.0
	}

	ramInfo, err := s.GetRAMInfo()
	if err != nil {
		nodeInfo.RAMTotal = 0
		nodeInfo.RAMUsage = 0.0
	} else {
		nodeInfo.RAMTotal = ramInfo.Total
		nodeInfo.RAMUsage = ramInfo.UsedPercent
	}

	disksUsage, err := s.GetDisksUsage()
	if err != nil {
		nodeInfo.DiskTotal = uint64(0)
		nodeInfo.DiskUsage = (0)
	} else {
		nodeInfo.DiskTotal = uint64(disksUsage.Total)
		nodeInfo.DiskUsage = (disksUsage.Usage)
	}

	var resourceIds []uint

	err = s.DB.Raw(`SELECT ct_id AS id FROM jails UNION ALL SELECT rid AS id FROM vms`).Scan(&resourceIds).Error
	if err != nil {
		nodeInfo.Guests = []uint{}
	} else {
		nodeInfo.Guests = resourceIds
	}

	return nodeInfo, nil
}
