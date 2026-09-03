// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	cpuid "github.com/klauspost/cpuid/v2"
	"github.com/shirou/gopsutil/mem"
	"gorm.io/gorm"
)

const (
	jailHardwareManagedStart = "### Start Sylve-Managed Hardware ###"
	jailHardwareManagedEnd   = "### End Sylve-Managed Hardware ###"
	jailUserManagedHookStart = "### Start User-Managed Hook ###"
	jailHardwareMiB          = int64(1024 * 1024)
	jailHardwareHostReserve  = uint64(1024 * 1024 * 1024)
)

var (
	jailLegacyMemoryLimitPattern = regexp.MustCompile(`^jail:[^:]+:memoryuse:deny=[0-9]+M$`)
	jailCPUListPattern           = regexp.MustCompile(`^[0-9]+(?:[-,][0-9]+)*$`)
)

type jailHardwareOps interface {
	IsJailRunning(ctID uint) (bool, error)
	HostMemoryTotal() (uint64, error)
	HostLogicalCores() int
	ApplyMemory(jailHash string, memoryMiB int64) error
	RemoveMemory(jailHash string) error
	ApplyCPU(jailHash, cpuList string) error
}

type hostJailHardwareOps struct {
	service *Service
}

func (o hostJailHardwareOps) IsJailRunning(ctID uint) (bool, error) {
	return o.service.IsJailRunning(ctID)
}

func (hostJailHardwareOps) HostMemoryTotal() (uint64, error) {
	usage, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return usage.Total, nil
}

func (hostJailHardwareOps) HostLogicalCores() int {
	return cpuid.CPU.LogicalCores
}

func (o hostJailHardwareOps) ApplyMemory(jailHash string, memoryMiB int64) error {
	if err := o.RemoveMemory(jailHash); err != nil {
		return err
	}
	if _, err := utils.RunCommand(
		"/usr/bin/rctl",
		"-a",
		fmt.Sprintf("jail:%s:memoryuse:deny=%dM", jailHash, memoryMiB),
	); err != nil {
		return fmt.Errorf("failed_to_apply_memory_limit: %w", err)
	}
	return nil
}

func (hostJailHardwareOps) RemoveMemory(jailHash string) error {
	_, err := utils.RunCommand("/usr/bin/rctl", "-r", fmt.Sprintf("jail:%s:memoryuse", jailHash))
	if err == nil {
		return nil
	}

	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no matching") ||
		strings.Contains(lower, "no such") {
		return nil
	}
	return fmt.Errorf("failed_to_remove_memory_limit: %w", err)
}

func (hostJailHardwareOps) ApplyCPU(jailHash, cpuList string) error {
	if _, err := utils.RunCommand("/bin/cpuset", "-l", cpuList, "-j", jailHash); err != nil {
		return fmt.Errorf("failed_to_apply_cpu_set: %w", err)
	}
	return nil
}

func (s *Service) jailHardwareOps() jailHardwareOps {
	if s.hardwareOps != nil {
		return s.hardwareOps
	}
	return hostJailHardwareOps{service: s}
}

type jailHardwareState struct {
	resourceLimits bool
	memory         int64
	cores          int
	cpuSet         []int
}

type jailHardwareMutation struct {
	memory         *int64
	cores          *int64
	resourceLimits *bool
}

func jailHardwareStateFromModel(jail *jailModels.Jail) jailHardwareState {
	enabled := jail.ResourceLimits != nil && *jail.ResourceLimits
	return jailHardwareState{
		resourceLimits: enabled,
		memory:         int64(jail.Memory),
		cores:          jail.Cores,
		cpuSet:         append([]int(nil), jail.CPUSet...),
	}
}

func (state jailHardwareState) result(ctID uint) jailServiceInterfaces.JailHardwareResult {
	cpuSet := append([]int(nil), state.cpuSet...)
	if cpuSet == nil {
		cpuSet = []int{}
	}
	return jailServiceInterfaces.JailHardwareResult{
		CTID:           ctID,
		ResourceLimits: state.resourceLimits,
		Memory:         state.memory,
		Cores:          state.cores,
		CPUSet:         cpuSet,
	}
}

func jailHardwareManagedBlock(cpuConfig, memoryConfig string) string {
	commands := make([]string, 0, 2)
	if strings.TrimSpace(memoryConfig) != "" {
		commands = append(commands, strings.TrimSpace(memoryConfig))
	}
	if strings.TrimSpace(cpuConfig) != "" {
		commands = append(commands, strings.TrimSpace(cpuConfig))
	}
	if len(commands) == 0 {
		return ""
	}

	return jailHardwareManagedStart + "\n" + strings.Join(commands, "\n") + "\n" + jailHardwareManagedEnd
}

func isLegacyJailMemoryCommand(line, jailHash string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 3 || filepath.Base(fields[0]) != "rctl" || fields[1] != "-a" {
		return false
	}
	return jailLegacyMemoryLimitPattern.MatchString(fields[2]) &&
		strings.HasPrefix(fields[2], "jail:"+jailHash+":memoryuse:deny=")
}

func isLegacyJailCPUCommand(line, jailHash string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) == 5 && filepath.Base(fields[0]) == "cpuset" && fields[1] == "-l" &&
		jailCPUListPattern.MatchString(fields[2]) && fields[3] == "-j" && fields[4] == jailHash
}

func reconcileJailHardwareHook(
	content string,
	jailHash string,
	desired jailHardwareState,
) (string, error) {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	inManagedBlock := false

	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case jailHardwareManagedStart:
			if inManagedBlock {
				return "", fmt.Errorf("jail_hardware_hook_conflict: nested managed hardware block")
			}
			inManagedBlock = true
			continue
		case jailHardwareManagedEnd:
			if !inManagedBlock {
				return "", fmt.Errorf("jail_hardware_hook_conflict: unmatched managed hardware marker")
			}
			inManagedBlock = false
			continue
		}
		if inManagedBlock || isLegacyJailMemoryCommand(line, jailHash) || isLegacyJailCPUCommand(line, jailHash) {
			continue
		}
		filtered = append(filtered, line)
	}
	if inManagedBlock {
		return "", fmt.Errorf("jail_hardware_hook_conflict: unterminated managed hardware block")
	}

	cleaned := strings.TrimRight(strings.Join(filtered, "\n"), "\n")
	if strings.TrimSpace(cleaned) == "" {
		cleaned = "#!/bin/sh"
	} else if !strings.HasPrefix(strings.TrimLeft(cleaned, "\r\n"), "#!") {
		cleaned = "#!/bin/sh\n" + strings.TrimLeft(cleaned, "\r\n")
	}

	block := ""
	if desired.resourceLimits {
		cpuList := jailHardwareCPUList(desired.cpuSet)
		block = jailHardwareManagedBlock(
			fmt.Sprintf("cpuset -l %s -j %s", cpuList, jailHash),
			fmt.Sprintf("rctl -a jail:%s:memoryuse:deny=%dM", jailHash, desired.memory/jailHardwareMiB),
		)
	}
	if block == "" {
		return cleaned + "\n", nil
	}

	cleanedLines := strings.Split(cleaned, "\n")
	insertAt := len(cleanedLines)
	for index, line := range cleanedLines {
		if strings.TrimSpace(line) == jailUserManagedHookStart {
			insertAt = index
			break
		}
	}
	result := make([]string, 0, len(cleanedLines)+5)
	result = append(result, cleanedLines[:insertAt]...)
	if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) != "" {
		result = append(result, "")
	}
	result = append(result, strings.Split(block, "\n")...)
	if insertAt < len(cleanedLines) && strings.TrimSpace(cleanedLines[insertAt]) != "" {
		result = append(result, "")
	}
	result = append(result, cleanedLines[insertAt:]...)
	return strings.TrimRight(strings.Join(result, "\n"), "\n") + "\n", nil
}

func reconcileJailHardwareConfig(configContent, postStartPath string, hookHasBody bool) (string, error) {
	managedLine := fmt.Sprintf("exec.poststart += \"%s\";", postStartPath)
	lines := strings.Split(configContent, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == managedLine {
			continue
		}
		filtered = append(filtered, line)
	}
	result := strings.Join(filtered, "\n")
	if !hookHasBody {
		return result, nil
	}

	lastCurly := strings.LastIndex(result, "}")
	if lastCurly < 0 {
		return "", fmt.Errorf("jail_hardware_config_conflict: invalid jail config")
	}
	line := "\texec.poststart += \"" + postStartPath + "\";\n"
	return result[:lastCurly] + line + result[lastCurly:], nil
}

func jailHardwareCPUList(cpuSet []int) string {
	parts := make([]string, 0, len(cpuSet))
	for _, core := range cpuSet {
		parts = append(parts, fmt.Sprintf("%d", core))
	}
	return strings.Join(parts, ",")
}

func jailHardwareAllCPUList(logicalCores int) string {
	if logicalCores <= 1 {
		return "0"
	}
	return fmt.Sprintf("0-%d", logicalCores-1)
}

func normalizeJailHardwareMemory(memoryBytes int64, hostTotal uint64) (int64, error) {
	if memoryBytes <= 0 {
		return 0, fmt.Errorf("invalid_memory")
	}
	if memoryBytes < jailHardwareMiB {
		return 0, fmt.Errorf("memory_limit_too_low")
	}

	memoryMiB := ((memoryBytes - 1) / jailHardwareMiB) + 1
	maxInt64 := int64(^uint64(0) >> 1)
	if memoryMiB > maxInt64/jailHardwareMiB {
		return 0, fmt.Errorf("invalid_memory")
	}
	normalized := memoryMiB * jailHardwareMiB
	if uint64(normalized) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid_memory")
	}
	if hostTotal <= jailHardwareHostReserve || uint64(normalized) > hostTotal-jailHardwareHostReserve {
		return 0, fmt.Errorf("memory_limit_exceeds_host_capacity")
	}
	return normalized, nil
}

func validatePersistedJailHardwareMemory(memoryBytes int64) error {
	if memoryBytes < jailHardwareMiB || memoryBytes%jailHardwareMiB != 0 ||
		uint64(memoryBytes) > uint64(^uint(0)>>1) {
		return fmt.Errorf("jail_hardware_state_invalid")
	}
	return nil
}

func validateJailHardwareCPUState(state jailHardwareState, logicalCores int) error {
	if !state.resourceLimits {
		return nil
	}
	if state.cores < 1 || state.cores > logicalCores || len(state.cpuSet) != state.cores {
		return fmt.Errorf("jail_hardware_state_invalid")
	}
	seen := make(map[int]struct{}, len(state.cpuSet))
	for _, core := range state.cpuSet {
		if core < 0 || core >= logicalCores {
			return fmt.Errorf("jail_hardware_state_invalid")
		}
		if _, exists := seen[core]; exists {
			return fmt.Errorf("jail_hardware_state_invalid")
		}
		seen[core] = struct{}{}
	}
	return nil
}

func (s *Service) selectJailHardwareCPUSet(ctID uint, cores, logicalCores int) ([]int, error) {
	if cores < 1 || cores > logicalCores {
		return nil, fmt.Errorf("invalid_cores")
	}

	var jails []jailModels.Jail
	if err := s.DB.Select("ct_id", "cpu_set").Find(&jails).Error; err != nil {
		return nil, fmt.Errorf("failed_to_fetch_current_jails: %w", err)
	}
	usage := make([]int, logicalCores)
	for _, current := range jails {
		if current.CTID == ctID {
			continue
		}
		for _, core := range current.CPUSet {
			if core >= 0 && core < logicalCores {
				usage[core]++
			}
		}
	}

	available := make([]int, logicalCores)
	for core := range available {
		available[core] = core
	}
	sort.Slice(available, func(i, j int) bool {
		if usage[available[i]] == usage[available[j]] {
			return available[i] < available[j]
		}
		return usage[available[i]] < usage[available[j]]
	})
	selected := append([]int(nil), available[:cores]...)
	sort.Ints(selected)
	return selected, nil
}

func (s *Service) NormalizeRestoredJailHardware(data *jailModels.Jail) error {
	if data == nil || data.CTID == 0 {
		return fmt.Errorf("restored_jail_hardware_state_invalid")
	}

	enabled := data.ResourceLimits == nil || *data.ResourceLimits
	data.ResourceLimits = &enabled
	if !enabled {
		data.Cores = 0
		data.CPUSet = []int{}
		data.Memory = 0
		return nil
	}
	if data.Cores < 0 {
		return fmt.Errorf("restored_jail_hardware_state_invalid")
	}
	if data.Cores == 0 {
		data.CPUSet = []int{}
		return nil
	}

	logicalCores := s.jailHardwareOps().HostLogicalCores()
	cpuSet, err := s.selectJailHardwareCPUSet(data.CTID, data.Cores, logicalCores)
	if err != nil {
		return fmt.Errorf(
			"restored_jail_cpu_capacity_insufficient: requested=%d available=%d: %w",
			data.Cores,
			logicalCores,
			err,
		)
	}
	data.CPUSet = cpuSet
	return nil
}

func loadJailForHardware(db *gorm.DB, ctID uint) (*jailModels.Jail, error) {
	var jail jailModels.Jail
	err := db.Preload("Storages").Where("ct_id = ?", ctID).First(&jail).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("jail_not_found: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_load_jail: %w", err)
	}
	return &jail, nil
}

func (s *Service) ensureJailHardwareMutationAllowedLocked(ctID uint) (*jailModels.Jail, error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("jail_service_not_initialized")
	}
	allowed, err := s.canMutateProtectedJail(ctID)
	if err != nil {
		return nil, fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("replication_lease_not_owned")
	}
	restoring, err := s.jailRestoreInProgress(ctID)
	if err != nil {
		return nil, fmt.Errorf("restore_fence_check_failed: %w", err)
	}
	if restoring {
		return nil, fmt.Errorf("restore_in_progress")
	}
	return loadJailForHardware(s.DB, ctID)
}

func (s *Service) captureJailHardwareFiles(
	jail *jailModels.Jail,
	configPath string,
	postStartPath string,
) ([]jailFileSnapshot, error) {
	paths := []string{configPath, postStartPath}
	mountPoint, err := s.resolveJailRoot(context.Background(), jail)
	if err != nil {
		return nil, err
	}
	paths = append(paths, filepath.Join(mountPoint, ".sylve", "jail.json"))

	return captureJailFiles(paths)
}

func updateJailHardwareColumns(db *gorm.DB, ctID uint, state jailHardwareState) error {
	enabled := state.resourceLimits
	updates := jailModels.Jail{
		ResourceLimits: &enabled,
		Memory:         int(state.memory),
		Cores:          state.cores,
		CPUSet:         append([]int{}, state.cpuSet...),
	}
	result := db.Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctID).
		Select("ResourceLimits", "Memory", "Cores", "CPUSet").
		Updates(&updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("jail_not_found")
	}
	return nil
}

func (s *Service) compensateJailHardwareMutation(
	ctID uint,
	jailHash string,
	prior jailHardwareState,
	logicalCores int,
	running bool,
	memoryAttempted bool,
	cpuAttempted bool,
	databaseUpdated bool,
	snapshots []jailFileSnapshot,
) error {
	var compensationErr error
	ops := s.jailHardwareOps()
	if running && cpuAttempted {
		cpuList := jailHardwareAllCPUList(logicalCores)
		if prior.resourceLimits {
			cpuList = jailHardwareCPUList(prior.cpuSet)
		}
		if err := ops.ApplyCPU(jailHash, cpuList); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("failed_to_restore_cpu_set: %w", err))
		}
	}
	if running && memoryAttempted {
		var err error
		if prior.resourceLimits {
			err = ops.ApplyMemory(jailHash, prior.memory/jailHardwareMiB)
		} else {
			err = ops.RemoveMemory(jailHash)
		}
		if err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("failed_to_restore_memory_limit: %w", err))
		}
	}
	if databaseUpdated {
		if err := updateJailHardwareColumns(s.DB, ctID, prior); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("failed_to_restore_jail_hardware_database: %w", err))
		}
	}
	return errors.Join(compensationErr, restoreJailFiles(snapshots))
}

func (s *Service) UpdateMemory(
	ctID uint,
	memoryBytes int64,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.updateJailHardware(ctID, jailHardwareMutation{memory: &memoryBytes})
}

func (s *Service) UpdateCPU(
	ctID uint,
	cores int64,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.updateJailHardware(ctID, jailHardwareMutation{cores: &cores})
}

func (s *Service) UpdateResourceLimits(
	ctID uint,
	enabled bool,
) (jailServiceInterfaces.JailHardwareResult, error) {
	return s.updateJailHardware(ctID, jailHardwareMutation{resourceLimits: &enabled})
}

func (s *Service) updateJailHardware(
	ctID uint,
	mutation jailHardwareMutation,
) (jailServiceInterfaces.JailHardwareResult, error) {
	if s == nil {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("jail_service_not_initialized")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailHardwareMutationAllowedLocked(ctID)
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}
	prior := jailHardwareStateFromModel(jail)
	desired := jailHardwareState{
		resourceLimits: prior.resourceLimits,
		memory:         prior.memory,
		cores:          prior.cores,
		cpuSet:         append([]int(nil), prior.cpuSet...),
	}
	if mutation.resourceLimits == nil && !prior.resourceLimits {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("resource_limits_disabled")
	}

	ops := s.jailHardwareOps()
	logicalCores := ops.HostLogicalCores()
	if logicalCores < 1 {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("host_cpu_unavailable")
	}
	var hostMemory uint64
	needsHostMemory := mutation.memory != nil ||
		(mutation.resourceLimits != nil && *mutation.resourceLimits && !prior.resourceLimits)
	if needsHostMemory {
		hostMemory, err = ops.HostMemoryTotal()
		if err != nil {
			return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("failed_to_get_host_memory: %w", err)
		}
	}

	if mutation.resourceLimits != nil {
		desired.resourceLimits = *mutation.resourceLimits
		if !desired.resourceLimits {
			desired.memory = 0
			desired.cores = 0
			desired.cpuSet = []int{}
		} else if !prior.resourceLimits {
			desired.memory, err = normalizeJailHardwareMemory(1024*1024*1024, hostMemory)
			if err != nil {
				return jailServiceInterfaces.JailHardwareResult{}, err
			}
			desired.cores = 1
			desired.cpuSet, err = s.selectJailHardwareCPUSet(ctID, 1, logicalCores)
			if err != nil {
				return jailServiceInterfaces.JailHardwareResult{}, err
			}
		}
	}

	if desired.resourceLimits {
		if mutation.memory != nil {
			desired.memory, err = normalizeJailHardwareMemory(*mutation.memory, hostMemory)
			if err != nil {
				return jailServiceInterfaces.JailHardwareResult{}, err
			}
		} else {
			if err := validatePersistedJailHardwareMemory(desired.memory); err != nil {
				return jailServiceInterfaces.JailHardwareResult{}, err
			}
		}
		if mutation.cores != nil {
			if *mutation.cores < 1 || *mutation.cores > int64(logicalCores) {
				return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("invalid_cores")
			}
			desired.cores = int(*mutation.cores)
			desired.cpuSet, err = s.selectJailHardwareCPUSet(ctID, desired.cores, logicalCores)
			if err != nil {
				return jailServiceInterfaces.JailHardwareResult{}, err
			}
		}
		if err := validateJailHardwareCPUState(desired, logicalCores); err != nil {
			return jailServiceInterfaces.JailHardwareResult{}, err
		}
	}

	running, err := ops.IsJailRunning(ctID)
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("failed_to_get_jail_state: %w", err)
	}
	jailHash := s.GetCTIDHash(ctID)
	postStartPath, err := s.GetHookScriptPath(ctID, "post-start")
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}
	hookContent, err := os.ReadFile(postStartPath)
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("failed_to_read_post_start_hook: %w", err)
	}
	newHook, err := reconcileJailHardwareHook(string(hookContent), jailHash, desired)
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}
	configContent, err := s.GetJailConfig(ctID)
	if errors.Is(err, os.ErrNotExist) {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("jail_config_not_found: %w", err)
	}
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}
	if strings.TrimSpace(configContent) == "" {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("jail_config_not_found")
	}
	newConfig, err := reconcileJailHardwareConfig(configContent, postStartPath, s.hasHookBody(newHook))
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fmt.Errorf("failed_to_get_jails_path: %w", err)
	}
	configPath := filepath.Join(jailsPath, fmt.Sprintf("%d", ctID), fmt.Sprintf("%d.conf", ctID))
	snapshots, err := s.captureJailHardwareFiles(jail, configPath, postStartPath)
	if err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, err
	}

	fail := func(primary error, memoryAttempted, cpuAttempted, databaseUpdated bool) error {
		return errors.Join(
			primary,
			s.compensateJailHardwareMutation(
				ctID,
				jailHash,
				prior,
				logicalCores,
				running,
				memoryAttempted,
				cpuAttempted,
				databaseUpdated,
				snapshots,
			),
		)
	}

	if err := utils.AtomicWriteFile(postStartPath, []byte(newHook), 0o755); err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fail(
			fmt.Errorf("failed_to_write_post_start_hook: %w", err), false, false, false,
		)
	}
	if err := utils.AtomicWriteFile(configPath, []byte(newConfig), 0o644); err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fail(
			fmt.Errorf("failed_to_write_jail_config: %w", err), false, false, false,
		)
	}
	if err := updateJailHardwareColumns(s.DB, ctID, desired); err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fail(
			fmt.Errorf("failed_to_update_jail_hardware_database: %w", err), false, false, false,
		)
	}
	if err := s.WriteJailJSON(ctID); err != nil {
		return jailServiceInterfaces.JailHardwareResult{}, fail(
			fmt.Errorf("failed_to_sync_jail_metadata: %w", err), false, false, true,
		)
	}

	memoryChanged := prior.resourceLimits != desired.resourceLimits || prior.memory != desired.memory
	cpuChanged := prior.resourceLimits != desired.resourceLimits ||
		prior.cores != desired.cores || jailHardwareCPUList(prior.cpuSet) != jailHardwareCPUList(desired.cpuSet)
	memoryAttempted := false
	cpuAttempted := false
	if running && memoryChanged {
		memoryAttempted = true
		if desired.resourceLimits {
			err = ops.ApplyMemory(jailHash, desired.memory/jailHardwareMiB)
		} else {
			err = ops.RemoveMemory(jailHash)
		}
		if err != nil {
			primary := err
			if stillRunning, stateErr := ops.IsJailRunning(ctID); stateErr == nil && !stillRunning {
				primary = fmt.Errorf("jail_runtime_state_changed: %w", err)
			}
			return jailServiceInterfaces.JailHardwareResult{}, fail(primary, memoryAttempted, cpuAttempted, true)
		}
	}
	if running && cpuChanged {
		cpuAttempted = true
		cpuList := jailHardwareAllCPUList(logicalCores)
		if desired.resourceLimits {
			cpuList = jailHardwareCPUList(desired.cpuSet)
		}
		if err := ops.ApplyCPU(jailHash, cpuList); err != nil {
			primary := err
			if stillRunning, stateErr := ops.IsJailRunning(ctID); stateErr == nil && !stillRunning {
				primary = fmt.Errorf("jail_runtime_state_changed: %w", err)
			}
			return jailServiceInterfaces.JailHardwareResult{}, fail(primary, memoryAttempted, cpuAttempted, true)
		}
	}

	return desired.result(ctID), nil
}
