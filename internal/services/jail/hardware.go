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
	"os"
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

	postStart, err := s.GetHookScriptPath(ctId, "post-start")
	if err != nil {
		return err
	}

	content, err := os.ReadFile(postStart)
	if err != nil {
		return fmt.Errorf("failed to read post-start hook script: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.Contains(t, "rctl -a") && strings.Contains(t, "memoryuse") {
			lines[i] = fmt.Sprintf("rctl -a jail:%s:memoryuse:deny=%dM", utils.HashIntToNLetters(int(ctId), 5), mb)
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, fmt.Sprintf("rctl -a jail:%s:memoryuse:deny=%dM", utils.HashIntToNLetters(int(ctId), 5), mb))
	}

	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(postStart, []byte(newContent), 0755); err != nil {
		return fmt.Errorf("failed to write updated post-start hook script: %w", err)
	}

	// Live update
	_, err = utils.RunCommand("/usr/bin/rctl", "-a", fmt.Sprintf("jail:%s:memoryuse:deny=%dM", utils.HashIntToNLetters(int(ctId), 5), mb))
	if err != nil {
		return fmt.Errorf("failed to apply memory limit with rctl: %w", err)
	}

	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return err
	}

	jail.Memory = int(memoryBytes)
	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail memory in database: %w", err)
	}

	err = s.WriteJailJSON(ctId)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write jail JSON after memory update")
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

	sort.Slice(all, func(i, j int) bool { return all[i].Count < all[j].Count })
	selected := make([]int, 0, cores)

	for i := 0; i < int(cores) && i < len(all); i++ {
		selected = append(selected, all[i].Core)
	}
	if len(selected) == 0 {
		return fmt.Errorf("no CPU cores selected")
	}

	coreListStr := strings.Trim(strings.Replace(fmt.Sprint(selected), " ", ",", -1), "[]")
	ctIdHash := utils.HashIntToNLetters(int(ctId), 5)

	postStart, err := s.GetHookScriptPath(ctId, "post-start")
	if err != nil {
		return err
	}

	content, err := os.ReadFile(postStart)
	if err != nil {
		return fmt.Errorf("failed to read post-start hook script: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	newLine := fmt.Sprintf("cpuset -l %s -j %s", coreListStr, ctIdHash)

	found := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "cpuset -l ") {
			lines[i] = newLine
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, newLine)
	}

	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(postStart, []byte(newContent), 0755); err != nil {
		return fmt.Errorf("failed to write updated post-start hook script: %w", err)
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

	if _, err := utils.RunCommand("/bin/cpuset", "-l", coreListStr, "-j", ctIdHash); err != nil {
		if strings.Contains(err.Error(), "not found") {
			logger.L.Warn().Msgf("jail %s not running, skipping live CPU set", ctIdHash)
		} else {
			return fmt.Errorf("failed to apply CPU set with cpuset: %w", err)
		}
	}

	err = s.WriteJailJSON(ctId)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write jail JSON after CPU update")
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

	postStart, err := s.GetHookScriptPath(ctId, "post-start")
	if err != nil {
		return err
	}

	content, err := os.ReadFile(postStart)
	if err != nil {
		return fmt.Errorf("failed to read post-start hook script: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		isRctl := strings.Contains(t, "rctl -a")
		isCpuset := strings.Contains(t, "cpuset -l")
		if isRctl || isCpuset {
			continue
		}
		filtered = append(filtered, line)
	}

	newContent := strings.Join(filtered, "\n")
	if err := os.WriteFile(postStart, []byte(newContent), 0755); err != nil {
		return fmt.Errorf("failed to write updated post-start hook script: %w", err)
	}

	_, err = utils.RunCommand("/usr/bin/rctl", "-r", fmt.Sprintf("jail:%s", ctIdHash))
	if err != nil {
		logger.L.Warn().Err(err).Msgf("Failed to remove rctl rules for jail %d", ctId)
	}

	numLogical := cpuid.CPU.LogicalCores
	allRange := ""
	if numLogical > 1 {
		allRange = fmt.Sprintf("0-%d", numLogical-1)
	}
	_, err = utils.RunCommand("/bin/cpuset", "-l", allRange, "-j", ctIdHash)
	if err != nil {
		logger.L.Warn().Err(err).Msgf("Failed to reset cpuset for jail %d", ctId)
	}

	val := false
	jail.ResourceLimits = &val
	jail.Memory = 0
	jail.Cores = 0
	jail.CPUSet = []int{}

	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail resource limits in database: %w", err)
	}

	err = s.WriteJailJSON(ctId)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write jail JSON after memory update")
	}

	return nil
}
