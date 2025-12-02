// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	cpuid "github.com/klauspost/cpuid/v2"
)

func (s *Service) GetJIDByCTID(ctId uint) int {
	ctidHash := utils.HashIntToNLetters(int(ctId), 5)
	output, err := utils.RunCommand(
		"jls",
		"-j",
		fmt.Sprintf("%s", ctidHash),
		"jid",
	)

	if err != nil {
		return -1
	}

	jid, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return -1
	}

	return jid
}

func (s *Service) GetJailStats(ctId uint, jail *jailModels.Jail) (jailServiceInterfaces.State, error) {
	if jail == nil || jail.CTID == 0 {
		var err error
		jail, err = s.GetJailByCTID(ctId)
		if err != nil {
			return jailServiceInterfaces.State{}, err
		}
	}

	var state jailServiceInterfaces.State
	state.CTID = ctId

	jid := s.GetJIDByCTID(ctId)

	if jid < 0 {
		state.Memory = 0
		state.PCPU = 0.0
		state.State = "INACTIVE"
		return state, nil
	}

	cmd := exec.Command("ps", "-axo", "jid,pcpu,rss", "--libxo", "json")
	out, err := cmd.Output()
	if err != nil {
		return state, fmt.Errorf("failed to run ps: %w", err)
	}

	var psData struct {
		ProcessInformation struct {
			Process []struct {
				JailID     string `json:"jail-id"`
				PercentCPU string `json:"percent-cpu"`
				RSS        string `json:"rss"`
			} `json:"process"`
		} `json:"process-information"`
	}
	if err := json.Unmarshal(out, &psData); err != nil {
		return state, fmt.Errorf("failed to parse ps json: %w", err)
	}

	var totalCPU, totalRSS float64
	for _, p := range psData.ProcessInformation.Process {
		if p.JailID == fmt.Sprintf("%d", jid) {
			cpuVal, _ := strconv.ParseFloat(p.PercentCPU, 64)
			rssVal, _ := strconv.ParseFloat(p.RSS, 64)
			totalCPU += cpuVal
			totalRSS += rssVal
		}
	}

	var allowedCores float64
	if *jail.ResourceLimits {
		if len(jail.CPUSet) > 0 {
			allowedCores = float64(len(jail.CPUSet))
		} else if jail.Cores > 0 {
			allowedCores = float64(jail.Cores)
		}
	}

	if allowedCores == 0 {
		allowedCores = float64(cpuid.CPU.LogicalCores)
	}

	normalized := totalCPU / allowedCores
	if normalized > 100 {
		normalized = 100
	}

	state.PCPU = math.Round(normalized*100) / 100
	state.Memory = int64(totalRSS * 1024)
	state.State = "ACTIVE"

	return state, nil
}

func (s *Service) GetStates() ([]jailServiceInterfaces.State, error) {
	var states []jailServiceInterfaces.State

	jails, err := s.GetJails()
	if err != nil {
		return nil, fmt.Errorf("failed to load jails: %w", err)
	}

	for _, jail := range jails {
		gState, err := s.GetJailStats(jail.CTID, &jail)
		if err != nil {
			return nil, fmt.Errorf("failed to get jail stats: %w", err)
		}

		states = append(states, gState)
	}

	return states, nil
}

func (s *Service) GetStateByCtId(ctId uint) (jailServiceInterfaces.State, error) {
	var state jailServiceInterfaces.State

	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return state, fmt.Errorf("failed_to_get_jail: %w", err)
	}

	state, err = s.GetJailStats(ctId, jail)
	if err != nil {
		return state, fmt.Errorf("failed_to_get_jail_stats: %w", err)
	}

	return state, nil
}

func (s *Service) IsJailActive(ctId uint) (bool, error) {
	states, err := s.GetStates()
	if err != nil {
		return false, err
	}

	for _, state := range states {
		if state.CTID == ctId {
			return state.State == "ACTIVE", nil
		}
	}

	return false, nil
}

func (s *Service) StoreJailUsage() error {
	if !s.crudMutex.TryLock() {
		return nil
	}
	defer s.crudMutex.Unlock()

	var jails []jailModels.Jail

	if err := s.DB.Select("id, ct_id, memory").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed_to_load_jails: %w", err)
	}

	jDBIDs := make([]uint, 0, len(jails))

	if len(jails) == 0 {
		return s.PruneOrphanedJailStats(jDBIDs)
	}

	states, err := s.GetStates()
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_states: %w", err)
	}

	type sInfo struct {
		CPUPercent   float64
		MemBytesUsed int64
		Active       bool
	}

	stateByCTID := make(map[uint]sInfo, len(states))
	for _, st := range states {
		stateByCTID[st.CTID] = sInfo{
			CPUPercent:   st.PCPU,
			MemBytesUsed: st.Memory,
			Active:       st.State == "ACTIVE",
		}
	}

	for _, j := range jails {
		live, ok := stateByCTID[j.CTID]
		if !ok || !live.Active {
			continue
		}

		cpuPct := live.CPUPercent

		var memPct float64
		if j.Memory > 0 {
			memPct = float64(math.Round((float64(live.MemBytesUsed) / float64(j.Memory)) * 100.0))
			if memPct < 0 {
				memPct = 0
			} else if memPct > 100 {
				memPct = 100
			}
		} else {
			sysRAM, err := utils.GetSystemMemoryBytes()
			if err != nil {
				return fmt.Errorf("failed to get system memory: %w", err)
			}

			memPct = math.Round((float64(live.MemBytesUsed)/float64(sysRAM))*10000.0) / 100.0
		}

		stat := &jailModels.JailStats{
			JailID:      (j.ID),
			CPUUsage:    cpuPct,
			MemoryUsage: memPct,
		}

		if err := s.DB.Save(stat).Error; err != nil {
			continue
		}
	}

	for _, j := range jails {
		jDBIDs = append(jDBIDs, j.ID)
	}

	for _, dbID := range jDBIDs {
		var stats []jailModels.JailStats
		if err := s.DB.
			Where("jid = ?", dbID).
			Order("id DESC").
			Limit(256).
			Find(&stats).Error; err != nil {
			return fmt.Errorf("failed_to_get_jail_stats: %w", err)
		}

		if len(stats) < 256 {
			continue
		}

		cutoff := stats[len(stats)-1].ID
		if err := s.DB.
			Where("jid = ? AND id < ?", dbID, cutoff).
			Delete(&jailModels.JailStats{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_old_jail_stats: %w", err)
		}
	}

	if err := s.PruneOrphanedJailStats(jDBIDs); err != nil {
		return err
	}

	return nil
}

func (s *Service) PruneOrphanedJailStats(validJailIds []uint) error {
	if len(validJailIds) == 0 {
		return s.DB.Where("1 = 1").Delete(&jailModels.JailStats{}).Error
	}

	valid := make([]int, len(validJailIds))
	for i, id := range validJailIds {
		valid[i] = int(id)
	}

	if err := s.DB.
		Where("jid NOT IN ?", valid).
		Delete(&jailModels.JailStats{}).Error; err != nil {
		return fmt.Errorf("failed_to_prune_orphaned_jail_stats: %w", err)
	}
	return nil
}

func (s *Service) GetJailUsage(ctId uint, limit int) ([]jailModels.JailStats, error) {
	var jailId uint
	if err := s.DB.Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Select("id").
		First(&jailId).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_actual_jail_id: %w", err)
	}

	if jailId == 0 {
		return nil, fmt.Errorf("jail_not_found")
	}

	var jailStats []jailModels.JailStats
	sub := s.DB.
		Model(&jailModels.JailStats{}).
		Where("jid = ?", jailId).
		Order("id DESC").
		Limit(limit)

	if err := s.DB.Table("(?) as sub", sub).
		Order("id ASC").
		Find(&jailStats).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_jail_usage: %w", err)
	}

	return jailStats, nil
}
