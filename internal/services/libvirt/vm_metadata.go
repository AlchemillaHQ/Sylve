// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

func (s *Service) RetireVMLocalMetadata(rid uint, cleanUpMacs bool) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("libvirt_service_not_initialized")
	}
	if rid == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}

	if _, err := s.ensureConnection(); err == nil {
		if err := s.RemoveLvVm(rid); err != nil {
			return fmt.Errorf("failed_to_remove_lv_vm: %w", err)
		}
	} else {
		warnings := make([]string, 0)
		logger.L.Warn().Uint("rid", rid).Err(err).Msg("libvirt_connection_not_available_during_vm_retire")
		s.forceRemoveVMRuntimeArtifacts(rid, &warnings)
		for _, warning := range warnings {
			logger.L.Warn().
				Uint("rid", rid).
				Str("warning", warning).
				Msg("retire_vm_runtime_warning")
		}
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var vmIDs []uint
		if err := tx.Model(&vmModels.VM{}).
			Where("rid = ?", rid).
			Pluck("id", &vmIDs).Error; err != nil {
			return fmt.Errorf("failed_to_lookup_vm_ids_for_retire: %w", err)
		}

		if err := tx.Where("rid = ?", rid).Delete(&vmModels.VMSnapshot{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_snapshots_for_retire: %w", err)
		}

		if len(vmIDs) == 0 {
			return nil
		}

		var macIDs []uint
		if err := tx.Model(&vmModels.Network{}).
			Where("vm_id IN ?", vmIDs).
			Where("mac_id IS NOT NULL").
			Pluck("mac_id", &macIDs).Error; err != nil {
			return fmt.Errorf("failed_to_collect_vm_mac_ids_for_retire: %w", err)
		}

		var datasetIDs []uint
		if err := tx.Model(&vmModels.Storage{}).
			Where("vm_id IN ?", vmIDs).
			Where("dataset_id IS NOT NULL").
			Pluck("dataset_id", &datasetIDs).Error; err != nil {
			return fmt.Errorf("failed_to_collect_vm_storage_dataset_ids_for_retire: %w", err)
		}

		if err := tx.Where("vm_id IN ?", vmIDs).Delete(&vmModels.VMStats{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_stats_for_retire: %w", err)
		}
		if err := tx.Where("vm_id IN ?", vmIDs).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_cpu_pinning_for_retire: %w", err)
		}
		if err := tx.Where("vm_id IN ?", vmIDs).Delete(&vmModels.Network{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_networks_for_retire: %w", err)
		}
		if err := tx.Where("vm_id IN ?", vmIDs).Delete(&vmModels.Storage{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_storages_for_retire: %w", err)
		}
		if err := tx.Where("id IN ?", vmIDs).Delete(&vmModels.VM{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_rows_for_retire: %w", err)
		}

		for _, datasetID := range uniqueUintValues(datasetIDs) {
			var refCount int64
			if err := tx.Model(&vmModels.Storage{}).
				Where("dataset_id = ?", datasetID).
				Count(&refCount).Error; err != nil {
				return fmt.Errorf("failed_to_count_storage_refs_for_dataset_%d: %w", datasetID, err)
			}
			if refCount > 0 {
				continue
			}
			if err := tx.Delete(&vmModels.VMStorageDataset{}, datasetID).Error; err != nil {
				return fmt.Errorf("failed_to_delete_vm_storage_dataset_%d: %w", datasetID, err)
			}
		}

		if cleanUpMacs {
			macIDs = uniqueUintValues(macIDs)
			if len(macIDs) > 0 {
				if err := tx.Where("object_id IN ?", macIDs).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed_to_delete_mac_object_entries_for_retire: %w", err)
				}
				if err := tx.Where("object_id IN ?", macIDs).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
					return fmt.Errorf("failed_to_delete_mac_object_resolutions_for_retire: %w", err)
				}
				if err := tx.Where("id IN ?", macIDs).Delete(&networkModels.Object{}).Error; err != nil {
					return fmt.Errorf("failed_to_delete_mac_objects_for_retire: %w", err)
				}
			}
		}

		return nil
	}); err != nil {
		return err
	}

	logger.L.Info().Uint("rid", rid).Msg("retired_vm_local_metadata")
	return nil
}
