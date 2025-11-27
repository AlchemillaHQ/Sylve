// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"sort"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	cpuid "github.com/klauspost/cpuid/v2"
)

func (s *Service) UpdateMemory(ctId uint, memoryBytes int64) error {
	if memoryBytes < 0 {
		return fmt.Errorf("invalid memory value: %d", memoryBytes)
	}

	const MiB = int64(1024 * 1024)
	mb := (memoryBytes + MiB - 1) / MiB
	if mb < 1 {
		return fmt.Errorf("memory must be at least 1MB, got: %dMB", mb)
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg) == "" {
		return fmt.Errorf("jail config not found for CTID: %d", ctId)
	}

	ctIdHash := utils.HashIntToNLetters(int(ctId), 5)
	prefix := fmt.Sprintf(`exec.poststart += "rctl -a jail:%s:memoryuse:deny=`, ctIdHash)

	lines := strings.Split(cfg, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = fmt.Sprintf(`	exec.poststart += "rctl -a jail:%s:memoryuse:deny=%dM";`, ctIdHash, mb)
			found = true
			break
		}
	}

	var newCfg string
	if found {
		newCfg = strings.Join(lines, "\n")
	} else {
		toAppend := fmt.Sprintf("\texec.poststart += \"rctl -a jail:%s:memoryuse:deny=%dM\";\n", ctIdHash, mb)
		newCfg, err = s.AppendToConfig(ctId, cfg, toAppend)
		if err != nil {
			return fmt.Errorf("failed to append memory limit to config: %w", err)
		}
	}

	if err := s.SaveJailConfig(ctId, newCfg); err != nil {
		return fmt.Errorf("failed to save jail config: %w", err)
	}

	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return err
	}

	jail.Memory = int(memoryBytes)
	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail memory in database: %w", err)
	}

	if _, err := utils.RunCommand("rctl", "-a", fmt.Sprintf("jail:%s:memoryuse:deny=%dM", ctIdHash, mb)); err != nil {
		return fmt.Errorf("failed to apply memory limit with rctl: %w", err)
	}

	return nil
}

func (s *Service) UpdateCPU(ctId uint, cores int64) error {
	if cores <= 0 {
		return fmt.Errorf("invalid cores value: %d (must be >= 1)", cores)
	}

	numLogical := int64(cpuid.CPU.LogicalCores)
	if cores > numLogical {
		return fmt.Errorf("requested cores (%d) exceed logical cores available (%d)", cores, numLogical)
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg) == "" {
		return fmt.Errorf("jail config not found for CTID: %d", ctId)
	}

	var currentJails []jailModels.Jail
	if err := s.DB.Find(&currentJails).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_current_jails: %w", err)
	}

	// Track usage of each core across all jails
	coreUsage := map[int]int{}
	for _, j := range currentJails {
		if j.CTID == ctId {
			continue
		}
		for _, c := range j.CPUSet {
			coreUsage[c]++
		}
	}

	type coreCount struct {
		Core  int
		Count int
	}

	all := make([]coreCount, 0, cpuid.CPU.LogicalCores)
	for i := 0; i < cpuid.CPU.LogicalCores; i++ {
		all = append(all, coreCount{Core: i, Count: coreUsage[i]})
	}

	// Sort by least used cores first
	sort.Slice(all, func(i, j int) bool { return all[i].Count < all[j].Count })

	// Pick the least used cores
	selected := make([]int, 0, cores)
	for i := 0; i < int(cores) && i < len(all); i++ {
		selected = append(selected, all[i].Core)
	}
	if len(selected) == 0 {
		return fmt.Errorf("no CPU cores selected")
	}

	coreListStr := strings.Trim(strings.Replace(fmt.Sprint(selected), " ", ",", -1), "[]")
	ctIdHash := utils.HashIntToNLetters(int(ctId), 5)
	wantLine := fmt.Sprintf(`	exec.created += "cpuset -l %s -j %s";`, coreListStr, ctIdHash)

	// Look for an existing cpuset line
	lines := strings.Split(cfg, "\n")
	found := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, `exec.created += "cpuset -l`) && strings.HasSuffix(t, fmt.Sprintf(`-j %s";`, ctIdHash)) {
			lines[i] = wantLine
			found = true
			break
		}
	}

	var newCfg string
	if found {
		// Replace in-place
		newCfg = strings.Join(lines, "\n")
	} else {
		// Append neatly before closing curly
		toAppend := fmt.Sprintf("\texec.created += \"cpuset -l %s -j %s\";\n", coreListStr, ctIdHash)
		newCfg, err = s.AppendToConfig(ctId, cfg, toAppend)
		if err != nil {
			return fmt.Errorf("failed to append CPU set to config: %w", err)
		}
	}

	if err := s.SaveJailConfig(ctId, newCfg); err != nil {
		return fmt.Errorf("failed to save jail config: %w", err)
	}

	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return err
	}

	jail.Cores = int(cores)
	jail.CPUSet = selected

	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail CPU in database: %w", err)
	}

	if _, err := utils.RunCommand("cpuset", "-l", coreListStr, "-j", ctIdHash); err != nil {
		if strings.Contains(err.Error(), "not found") {
			logger.L.Warn().Msgf("jail %s not running, skipping live CPU set", ctIdHash)
		} else {
			return fmt.Errorf("failed to apply CPU set with cpuset: %w", err)
		}
	}

	return nil
}

func (s *Service) UpdateResourceLimits(ctId uint, enabled bool) error {
	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return err
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg) == "" {
		return fmt.Errorf("jail config not found for CTID: %d", ctId)
	}

	ctIdHash := utils.HashIntToNLetters(int(ctId), 5)

	if enabled {
		const oneGiB = int64(1024 * 1024 * 1024)

		// Use refactored append-safe methods
		if err := s.UpdateMemory(ctId, oneGiB); err != nil {
			return fmt.Errorf("failed to set default memory limit: %w", err)
		}
		if err := s.UpdateCPU(ctId, 1); err != nil {
			return fmt.Errorf("failed to set default cpu limit: %w", err)
		}

		val := true
		jail.ResourceLimits = &val
		jail.Memory = int(oneGiB)
		jail.Cores = 1

		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail resource limits in database: %w", err)
		}
		return nil
	}

	// --- Disable path ---
	lines := strings.Split(cfg, "\n")
	memPrefix := fmt.Sprintf(`exec.poststart += "rctl -a jail:%s:memoryuse:deny=`, ctIdHash)
	cpuCreatedSuffix := fmt.Sprintf(`-j %s";`, ctIdHash)

	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		isMem := strings.HasPrefix(t, memPrefix)
		isCPU := strings.HasPrefix(t, `exec.created += "cpuset -l`) && strings.HasSuffix(t, cpuCreatedSuffix)
		if isMem || isCPU {
			continue // remove both memory + CPU limit lines
		}
		filtered = append(filtered, line)
	}

	newCfg := strings.Join(filtered, "\n")
	if err := s.SaveJailConfig(ctId, newCfg); err != nil {
		return fmt.Errorf("failed to save jail config: %w", err)
	}

	// Remove live rctl rule
	if _, err := utils.RunCommand("rctl", "-r", fmt.Sprintf("jail:%s", ctIdHash)); err != nil {
		logger.L.Warn().Err(err).Msgf("failed to remove rctl rules for jail %s", ctIdHash)
	}

	// Reset CPU affinity to all cores
	numLogical := cpuid.CPU.LogicalCores
	allRange := "0"
	if numLogical > 1 {
		allRange = fmt.Sprintf("0-%d", numLogical-1)
	}
	if _, err := utils.RunCommand("cpuset", "-l", allRange, "-j", ctIdHash); err != nil {
		logger.L.Warn().Err(err).Msgf("failed to reset cpuset for jail %s", ctIdHash)
	}

	// Reset DB fields
	val := false
	jail.ResourceLimits = &val
	jail.Memory = 0
	jail.Cores = 0
	jail.CPUSet = []int{}

	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail resource limits in database: %w", err)
	}

	return nil
}
