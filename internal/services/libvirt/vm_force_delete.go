// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) ForceRemoveVM(rid uint, cleanUpMacs bool, ctx context.Context) ([]string, error) {
	if rid == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	warnings := make([]string, 0)

	if s.Conn != nil {
		if err := s.RemoveLvVm(rid); err != nil {
			appendForceRemoveWarning(&warnings, rid, "failed_to_remove_vm_domain", err)
		}
	} else {
		appendForceRemoveWarning(&warnings, rid, "libvirt_connection_not_available", nil)
	}

	s.forceRemoveVMRuntimeArtifacts(rid, &warnings)
	s.forceRemoveVMZFSDatasets(ctx, rid, &warnings)
	s.forceRemoveVMDBRecords(rid, cleanUpMacs, &warnings)

	return warnings, nil
}

func appendForceRemoveWarning(warnings *[]string, rid uint, code string, err error) {
	message := code
	if err != nil {
		message = fmt.Sprintf("%s: %v", code, err)
		logger.L.Warn().Uint("rid", rid).Err(err).Msg(code)
	} else {
		logger.L.Warn().Uint("rid", rid).Msg(code)
	}
	*warnings = append(*warnings, message)
}

func (s *Service) forceRemoveVMRuntimeArtifacts(rid uint, warnings *[]string) {
	stopTPMErr := s.StopTPM(rid)
	if stopTPMErr != nil {
		lower := strings.ToLower(stopTPMErr.Error())
		if !strings.Contains(lower, "vm_not_found") && !strings.Contains(lower, "tpm_socket_not_found") {
			appendForceRemoveWarning(warnings, rid, "failed_to_stop_tpm_runtime", stopTPMErr)
		}
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		appendForceRemoveWarning(warnings, rid, "failed_to_get_vm_runtime_dir", err)
		return
	}

	vmPath := filepath.Join(vmDir, strconv.Itoa(int(rid)))
	if _, statErr := os.Stat(vmPath); statErr == nil {
		if removeErr := os.RemoveAll(vmPath); removeErr != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_remove_vm_runtime_dir", removeErr)
		}
	}
}

func (s *Service) forceRemoveVMZFSDatasets(ctx context.Context, rid uint, warnings *[]string) {
	output, err := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-o", "name", "-t", "filesystem,volume")
	if err != nil {
		appendForceRemoveWarning(warnings, rid, "failed_to_list_zfs_datasets_for_force_vm_delete", err)
		return
	}

	seen := make(map[string]struct{})
	datasets := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		dataset := strings.TrimSpace(line)
		if dataset == "" {
			continue
		}
		if !datasetBelongsToVMRID(dataset, rid) {
			continue
		}
		if _, exists := seen[dataset]; exists {
			continue
		}
		seen[dataset] = struct{}{}
		datasets = append(datasets, dataset)
	}

	sort.Slice(datasets, func(i, j int) bool {
		leftDepth := strings.Count(datasets[i], "/")
		rightDepth := strings.Count(datasets[j], "/")
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return datasets[i] > datasets[j]
	})

	for _, dataset := range datasets {
		destroyOut, destroyErr := utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", dataset)
		if destroyErr != nil {
			lower := strings.ToLower(strings.TrimSpace(destroyOut + " " + destroyErr.Error()))
			if strings.Contains(lower, "dataset does not exist") {
				continue
			}
			appendForceRemoveWarning(
				warnings,
				rid,
				fmt.Sprintf("failed_to_destroy_vm_dataset_%s", dataset),
				fmt.Errorf("%w (%s)", destroyErr, strings.TrimSpace(destroyOut)),
			)
		}
	}
}

func datasetBelongsToVMRID(dataset string, rid uint) bool {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return false
	}

	idx := strings.Index(dataset, "/sylve/virtual-machines/")
	if idx < 0 {
		return false
	}

	rest := dataset[idx+len("/sylve/virtual-machines/"):]
	if rest == "" {
		return false
	}

	segment := rest
	if slash := strings.Index(segment, "/"); slash >= 0 {
		segment = segment[:slash]
	}
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return false
	}

	cutAt := len(segment)
	for _, delimiter := range []string{".", "_"} {
		if pos := strings.Index(segment, delimiter); pos > 0 && pos < cutAt {
			cutAt = pos
		}
	}
	segment = strings.TrimSpace(segment[:cutAt])
	if segment == "" {
		return false
	}

	parsedRID, err := strconv.ParseUint(segment, 10, 64)
	if err != nil {
		return false
	}

	return uint(parsedRID) == rid
}

func (s *Service) forceRemoveVMDBRecords(rid uint, cleanUpMacs bool, warnings *[]string) {
	var vmIDs []uint
	if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", rid).Pluck("id", &vmIDs).Error; err != nil {
		appendForceRemoveWarning(warnings, rid, "failed_to_lookup_vm_ids_for_force_delete", err)
		return
	}

	var macIDs []uint
	var datasetIDs []uint
	if len(vmIDs) > 0 {
		if err := s.DB.Model(&vmModels.Network{}).
			Where("vm_id IN ?", vmIDs).
			Where("mac_id IS NOT NULL").
			Pluck("mac_id", &macIDs).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_collect_vm_mac_ids_for_force_delete", err)
		}

		if err := s.DB.Model(&vmModels.Storage{}).
			Where("vm_id IN ?", vmIDs).
			Where("dataset_id IS NOT NULL").
			Pluck("dataset_id", &datasetIDs).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_collect_vm_storage_dataset_ids_for_force_delete", err)
		}

		if err := s.DB.Where("vm_id IN ?", vmIDs).Delete(&vmModels.VMStats{}).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_delete_vm_stats_for_force_delete", err)
		}
		if err := s.DB.Where("vm_id IN ?", vmIDs).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_delete_vm_cpu_pinning_for_force_delete", err)
		}
		if err := s.DB.Where("vm_id IN ?", vmIDs).Delete(&vmModels.Network{}).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_delete_vm_networks_for_force_delete", err)
		}
		if err := s.DB.Where("vm_id IN ?", vmIDs).Delete(&vmModels.Storage{}).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_delete_vm_storages_for_force_delete", err)
		}
		if err := s.DB.Where("id IN ?", vmIDs).Delete(&vmModels.VM{}).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, "failed_to_delete_vm_rows_for_force_delete", err)
		}
	}

	cleanupDatasetIDs := uniqueUintValues(datasetIDs)
	patternDatasetIDs := make([]uint, 0)
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("name LIKE ? OR name LIKE ? OR name LIKE ? OR name LIKE ?",
			fmt.Sprintf("%%/sylve/virtual-machines/%d", rid),
			fmt.Sprintf("%%/sylve/virtual-machines/%d/%%", rid),
			fmt.Sprintf("%%/sylve/virtual-machines/%d.%%", rid),
			fmt.Sprintf("%%/sylve/virtual-machines/%d_%%", rid)).
		Pluck("id", &patternDatasetIDs).Error; err != nil {
		appendForceRemoveWarning(warnings, rid, "failed_to_lookup_vm_storage_dataset_rows_by_name", err)
	} else {
		cleanupDatasetIDs = uniqueUintValues(append(cleanupDatasetIDs, patternDatasetIDs...))
	}

	for _, datasetID := range cleanupDatasetIDs {
		var refCount int64
		if err := s.DB.Model(&vmModels.Storage{}).Where("dataset_id = ?", datasetID).Count(&refCount).Error; err != nil {
			appendForceRemoveWarning(warnings, rid, fmt.Sprintf("failed_to_count_storage_refs_for_dataset_%d", datasetID), err)
			continue
		}
		if refCount > 0 {
			continue
		}
		if err := s.DB.Delete(&vmModels.VMStorageDataset{}, datasetID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			appendForceRemoveWarning(warnings, rid, fmt.Sprintf("failed_to_delete_vm_storage_dataset_%d", datasetID), err)
		}
	}

	if cleanUpMacs {
		macIDs = uniqueUintValues(macIDs)
		if len(macIDs) > 0 {
			if err := s.DB.Where("object_id IN ?", macIDs).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
				appendForceRemoveWarning(warnings, rid, "failed_to_delete_mac_object_entries_for_force_delete", err)
			}
			if err := s.DB.Where("object_id IN ?", macIDs).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				appendForceRemoveWarning(warnings, rid, "failed_to_delete_mac_object_resolutions_for_force_delete", err)
			}
			if err := s.DB.Where("id IN ?", macIDs).Delete(&networkModels.Object{}).Error; err != nil {
				appendForceRemoveWarning(warnings, rid, "failed_to_delete_mac_objects_for_force_delete", err)
			}
		}
	}
}

func uniqueUintValues(values []uint) []uint {
	if len(values) == 0 {
		return []uint{}
	}

	seen := make(map[uint]struct{}, len(values))
	out := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
