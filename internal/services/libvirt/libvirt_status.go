// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) PruneOrphanedVMStats() error {
	if err := s.DB.
		Where(
			"vm_id NOT IN (?)",
			s.DB.
				Model(&vmModels.VM{}).
				Select("id"),
		).
		Delete(&vmModels.VMStats{}).
		Error; err != nil {
		return fmt.Errorf("failed to prune orphaned VMStats: %w", err)
	}
	return nil
}

func (s *Service) StoreVMUsage() error {
	if s.crudMutex.TryLock() == false {
		return nil
	}

	defer s.crudMutex.Unlock()

	var rids []int
	if err := s.DB.Model(&vmModels.VM{}).Pluck("rid", &rids).Error; err != nil {
		return fmt.Errorf("failed_to_get_rids: %w", err)
	}

	if len(rids) == 0 {
		return nil
	}

	for _, rid := range rids {
		domain, err := s.Conn.DomainLookupByName(strconv.Itoa(rid))
		if err != nil {
			continue
		}

		_, _, _, vcpus, cpuTime1, err := s.Conn.DomainGetInfo(domain)
		if err != nil {
			continue
		}

		time.Sleep(1 * time.Second)

		_, rMaxMem, _, _, cpuTime2, err := s.Conn.DomainGetInfo(domain)
		if err != nil {
			return fmt.Errorf("failed_to_get_cpu_info_2: %w", err)
		}
		if vcpus == 0 || cpuTime2 <= cpuTime1 {
			continue
		}

		deltaCPU := cpuTime2 - cpuTime1
		cpuUsage := (float64(deltaCPU) / 1e9) / float64(vcpus) * 100
		maxMemMB := float64(rMaxMem) / 1024

		psOut, err := utils.RunCommand("ps", "--libxo", "json", "-aux")
		if err != nil {
			continue
		}

		var top struct {
			ProcessInformation systemServiceInterfaces.ProcessInformation `json:"process-information"`
		}
		if err := json.Unmarshal([]byte(psOut), &top); err != nil {
			continue
		}

		var rssKB uint64
		for _, proc := range top.ProcessInformation.Process {
			if strings.Contains(proc.Command, fmt.Sprintf("bhyve: %d", rid)) {
				rssKB, _ = strconv.ParseUint(proc.RSS, 10, 64)
				break
			}
		}
		usedMemMB := float64(rssKB) / 1024
		memUsagePercent := (usedMemMB / maxMemMB) * 100

		var vmDbId uint

		if err := s.DB.Model(&vmModels.VM{}).
			Where("rid = ?", rid).
			Select("id").
			First(&vmDbId).Error; err != nil {
			return fmt.Errorf("failed_to_get_actual_vm_id: %w", err)
		}

		vmStats := &vmModels.VMStats{
			VMID:        uint(vmDbId),
			CPUUsage:    cpuUsage,
			MemoryUsage: memUsagePercent,
			MemoryUsed:  usedMemMB,
		}

		if err := s.DB.Save(vmStats).Error; err != nil {
			continue
		}
	}

	var vmIdsToKeep []int
	if err := s.DB.Model(&vmModels.VMStats{}).
		Select("DISTINCT vm_id").
		Pluck("vm_id", &vmIdsToKeep).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_ids_to_keep: %w", err)
	}

	for _, vmId := range vmIdsToKeep {
		var vmStats []vmModels.VMStats
		if err := s.DB.Where("vm_id = ?", vmId).
			Order("id DESC").
			Limit(256).
			Find(&vmStats).Error; err != nil {
			return fmt.Errorf("failed_to_get_vm_stats: %w", err)
		}

		if len(vmStats) < 256 {
			continue
		}

		if err := s.DB.Where("vm_id = ? AND id < ?", vmId, vmStats[255].ID).
			Delete(&vmModels.VMStats{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_old_vm_stats: %w", err)
		}
	}

	if err := s.PruneOrphanedVMStats(); err != nil {
		return err
	}

	return nil
}

func (s *Service) GetVMUsage(rid int, limit int) ([]vmModels.VMStats, error) {
	var vmDbId uint
	if err := s.DB.Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Select("id").
		First(&vmDbId).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_actual_vm_id: %w", err)
	}

	if vmDbId == 0 {
		return nil, fmt.Errorf("vm_not_found")
	}

	var vmStats []vmModels.VMStats
	sub := s.DB.
		Model(&vmModels.VMStats{}).
		Where("vm_id = ?", vmDbId).
		Order("id DESC").
		Limit(limit)

	if err := s.DB.Table("(?) as sub", sub).
		Order("id ASC").
		Find(&vmStats).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_usage: %w", err)
	}

	if len(vmStats) == 0 {
		return vmStats, nil
	}

	return vmStats, nil
}
