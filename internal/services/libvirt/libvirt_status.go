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
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"
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

func (s *Service) ApplyVMStatsRetention() error {
	var vmIDs []uint
	if err := s.DB.
		Model(&vmModels.VMStats{}).
		Select("DISTINCT vm_id").
		Pluck("vm_id", &vmIDs).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_ids_for_retention: %w", err)
	}

	now := time.Now()

	for _, vmID := range vmIDs {
		var stats []vmModels.VMStats
		if err := s.DB.
			Where("vm_id = ?", vmID).
			Order("created_at ASC").
			Find(&stats).Error; err != nil {
			return fmt.Errorf("failed_to_get_vm_stats_for_retention: %w", err)
		}

		if len(stats) == 0 {
			continue
		}

		isOff, err := s.IsDomainShutOffByID(vmID)
		if err != nil {
			logger.L.Error().Err(err).Uint("vm_id", vmID).Msg("failed_to_check_if_domain_is_shutoff_for_retention")
			continue
		}

		if !isOff {
			_, deleteIDs := db.ApplyGFS(now, stats)
			if len(deleteIDs) == 0 {
				continue
			}

			if err := s.DB.
				Where("id IN ?", deleteIDs).
				Delete(&vmModels.VMStats{}).Error; err != nil {
				return fmt.Errorf("failed_to_delete_old_vm_stats: %w", err)
			}
		}
	}

	if err := s.PruneOrphanedVMStats(); err != nil {
		return err
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

		// Prefer dommemstat
		var (
			rssKB   uint64
			availKB uint64
		)

		if stats, err := s.Conn.DomainMemoryStats(domain, 8, 0); err == nil {
			// fmt.Printf("dommemstat output: %+v\n", stats)
			for _, st := range stats {
				switch st.Tag {
				case 7: // VIR_DOMAIN_MEMORY_STAT_RSS
					rssKB = st.Val
				case 5: // VIR_DOMAIN_MEMORY_STAT_AVAILABLE
					availKB = st.Val
				}
			}
		}

		if availKB > 0 {
			maxMemMB = float64(availKB) / 1024
		}

		var usedMemMB float64
		var memUsagePercent float64

		if rssKB > 0 {
			usedMemMB = float64(rssKB) / 1024
			if maxMemMB > 0 {
				memUsagePercent = (usedMemMB / maxMemMB) * 100
			}
		} else {
			psOut, err := utils.RunCommand("/bin/ps", "--libxo", "json", "-aux")
			if err != nil {
				continue
			}

			var top struct {
				ProcessInformation systemServiceInterfaces.ProcessInformation `json:"process-information"`
			}
			if err := json.Unmarshal([]byte(psOut), &top); err != nil {
				continue
			}

			var rssFromPsKB uint64
			for _, proc := range top.ProcessInformation.Process {
				if strings.Contains(proc.Command, fmt.Sprintf("bhyve: %d", rid)) {
					rssFromPsKB, _ = strconv.ParseUint(proc.RSS, 10, 64)
					break
				}
			}

			usedMemMB = float64(rssFromPsKB) / 1024
			if maxMemMB > 0 {
				memUsagePercent = (usedMemMB / maxMemMB) * 100
			}
		}

		var vmDbId uint
		if err := s.DB.Model(&vmModels.VM{}).
			Where("rid = ?", rid).
			Select("id").
			First(&vmDbId).Error; err != nil {
			return fmt.Errorf("failed_to_get_actual_vm_id: %w", err)
		}

		memUsagePercent = math.Max(0, math.Min(100, memUsagePercent))
		cpuUsage = math.Max(0, math.Min(100, cpuUsage))

		vmStats := &vmModels.VMStats{
			VMID:        vmDbId,
			CPUUsage:    cpuUsage,
			MemoryUsage: memUsagePercent,
			MemoryUsed:  usedMemMB,
		}

		if err := s.DB.Save(vmStats).Error; err != nil {
			continue
		}
	}

	return s.ApplyVMStatsRetention()
}

func (s *Service) GetVMUsage(vmId int, step db.GFSStep) ([]vmModels.VMStats, error) {
	if vmId == 0 {
		return nil, fmt.Errorf("vm_not_found")
	}

	window, err := step.Window()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	from := now.Add(-window)

	var vmStats []vmModels.VMStats
	if err := s.DB.
		Where("vm_id = ? AND created_at >= ?", vmId, from).
		Order("created_at ASC").
		Find(&vmStats).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_usage: %w", err)
	}

	return vmStats, nil
}
