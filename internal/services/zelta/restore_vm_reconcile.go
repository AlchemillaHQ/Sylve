// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

func (s *Service) reconcileRestoredVMFromDatasetWithOptions(ctx context.Context, dataset string, restoreNetwork bool) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L.Error().
				Interface("panic", recovered).
				Str("dataset", dataset).
				Str("stack", string(debug.Stack())).
				Msg("panic_in_restore_vm_reconcile")
			err = fmt.Errorf("panic_in_restore_vm_reconcile: %v", recovered)
		}
	}()

	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return nil
	}

	kind, fallbackRID := inferRestoreDatasetKind(dataset)
	if kind != clusterModels.BackupJobModeVM || fallbackRID == 0 {
		return nil
	}

	restoredMeta, err := s.readLocalRestoredVMMetadata(ctx, dataset, fallbackRID)
	if err != nil {
		return err
	}
	restored := &restoredMeta.VM
	if restored == nil {
		return fmt.Errorf("restored_vm_metadata_not_found")
	}
	if restored.RID == 0 {
		restored.RID = fallbackRID
	}
	if restored.RID == 0 {
		return fmt.Errorf("restored_vm_rid_missing")
	}

	rid := restored.RID
	if strings.TrimSpace(restored.Name) == "" {
		restored.Name = fmt.Sprintf("vm-%d", rid)
	}
	if restored.CPUSockets < 1 {
		restored.CPUSockets = 1
	}
	if restored.CPUCores < 1 {
		restored.CPUCores = 1
	}
	if restored.CPUThreads < 1 {
		restored.CPUThreads = 1
	}
	if restored.ShutdownWaitTime < 1 {
		restored.ShutdownWaitTime = 10
	}
	if strings.TrimSpace(string(restored.TimeOffset)) == "" {
		restored.TimeOffset = vmModels.TimeOffsetUTC
	}

	restored.ID = 0
	restored.CreatedAt = restored.CreatedAt.UTC().AddDate(-1000, 0, 0)
	restored.UpdatedAt = restored.UpdatedAt.UTC().AddDate(-1000, 0, 0)
	restored.State = 0
	restored.StartedAt = nil
	restored.StoppedAt = nil
	restored.CPUPinning = []vmModels.VMCPUPinning{}
	restored.PCIDevices = []int{}

	requiresSwitchSync := false
	reconciledVMID := uint(0)

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		normalizedStorages, err := s.normalizeRestoredVMStorages(ctx, tx, rid, restored.Storages)
		if err != nil {
			return err
		}

		normalizedNetworks := []vmModels.Network{}
		if restoreNetwork {
			networks, switchSync, err := s.normalizeRestoredVMNetworks(tx, rid, restored.Networks)
			if err != nil {
				return err
			}
			normalizedNetworks = networks
			requiresSwitchSync = requiresSwitchSync || switchSync
		}

		baseVM := vmModels.VM{
			Name:                   strings.TrimSpace(restored.Name),
			Description:            restored.Description,
			RID:                    rid,
			CPUSockets:             restored.CPUSockets,
			CPUCores:               restored.CPUCores,
			CPUThreads:             restored.CPUThreads,
			RAM:                    restored.RAM,
			TPMEmulation:           restored.TPMEmulation,
			ShutdownWaitTime:       restored.ShutdownWaitTime,
			Serial:                 restored.Serial,
			VNCEnabled:             restored.VNCEnabled,
			VNCPort:                restored.VNCPort,
			VNCPassword:            restored.VNCPassword,
			VNCResolution:          restored.VNCResolution,
			VNCWait:                restored.VNCWait,
			StartAtBoot:            restored.StartAtBoot,
			StartOrder:             restored.StartOrder,
			WoL:                    restored.WoL,
			TimeOffset:             restored.TimeOffset,
			ACPI:                   restored.ACPI,
			APIC:                   restored.APIC,
			CloudInitData:          restored.CloudInitData,
			CloudInitMetaData:      restored.CloudInitMetaData,
			CloudInitNetworkConfig: restored.CloudInitNetworkConfig,
			IgnoreUMSR:             restored.IgnoreUMSR,
			QemuGuestAgent:         restored.QemuGuestAgent,
		}

		var existing vmModels.VM
		existingFound := true
		if err := tx.Where("rid = ?", rid).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				existingFound = false
			} else {
				return fmt.Errorf("failed_to_lookup_existing_vm_by_rid: %w", err)
			}
		}

		if existingFound {
			if err := tx.Model(&existing).Select(
				"Name",
				"Description",
				"CPUSockets",
				"CPUCores",
				"CPUThreads",
				"RAM",
				"TPMEmulation",
				"ShutdownWaitTime",
				"Serial",
				"VNCEnabled",
				"VNCPort",
				"VNCPassword",
				"VNCResolution",
				"VNCWait",
				"StartAtBoot",
				"StartOrder",
				"WoL",
				"TimeOffset",
				"ACPI",
				"APIC",
				"CloudInitData",
				"CloudInitMetaData",
				"CloudInitNetworkConfig",
				"IgnoreUMSR",
				"QemuGuestAgent",
			).Updates(&baseVM).Error; err != nil {
				return fmt.Errorf("failed_to_update_restored_vm_record: %w", err)
			}
			reconciledVMID = existing.ID
		} else {
			if err := tx.Create(&baseVM).Error; err != nil {
				return fmt.Errorf("failed_to_create_restored_vm_record: %w", err)
			}
			reconciledVMID = baseVM.ID
		}

		if reconciledVMID == 0 {
			return fmt.Errorf("restored_vm_id_missing")
		}

		if err := tx.Where("vm_id = ?", reconciledVMID).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_restored_vm_cpu_pinning: %w", err)
		}
		if err := tx.Where("vm_id = ?", reconciledVMID).Delete(&vmModels.Network{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_restored_vm_networks: %w", err)
		}
		if err := tx.Where("vm_id = ?", reconciledVMID).Delete(&vmModels.Storage{}).Error; err != nil {
			return fmt.Errorf("failed_to_delete_restored_vm_storages: %w", err)
		}

		for _, storage := range normalizedStorages {
			if storage.ID == 0 {
				continue
			}
			var conflictCount int64
			if err := tx.Model(&vmModels.Storage{}).
				Where("id = ? AND vm_id <> ?", storage.ID, reconciledVMID).
				Count(&conflictCount).Error; err != nil {
				return fmt.Errorf("failed_to_validate_restored_vm_storage_id: %w", err)
			}
			if conflictCount > 0 {
				return fmt.Errorf("restored_vm_storage_id_conflict: %d", storage.ID)
			}
		}

		for i := range normalizedStorages {
			normalizedStorages[i].VMID = reconciledVMID
		}
		if len(normalizedStorages) > 0 {
			if err := tx.Create(&normalizedStorages).Error; err != nil {
				return fmt.Errorf("failed_to_insert_restored_vm_storages: %w", err)
			}
		}

		for i := range normalizedNetworks {
			normalizedNetworks[i].ID = 0
			normalizedNetworks[i].VMID = reconciledVMID
		}
		if len(normalizedNetworks) > 0 {
			if err := tx.Create(&normalizedNetworks).Error; err != nil {
				return fmt.Errorf("failed_to_insert_restored_vm_networks: %w", err)
			}
		}

		if restoredMeta.SnapshotsPresent {
			if err := reconcileRestoredVMSnapshots(
				tx,
				reconciledVMID,
				rid,
				restoredMeta.Snapshots,
				normalizedStorages,
				dataset,
			); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if requiresSwitchSync && s.Network != nil {
		if err := s.Network.SyncStandardSwitches(nil, "sync"); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("rid", rid).
				Msg("failed_to_sync_standard_switches_after_vm_restore_reconcile")
		}
	}

	if s.VM != nil {
		if err := s.VM.RemoveLvVm(rid); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("rid", rid).
				Msg("failed_to_remove_existing_vm_domain_before_restore_reconcile")
		}

		cloudInitData := restored.CloudInitData
		cloudInitMeta := restored.CloudInitMetaData
		cloudInitNetwork := restored.CloudInitNetworkConfig
		hadCloudInit := strings.TrimSpace(cloudInitData) != "" ||
			strings.TrimSpace(cloudInitMeta) != "" ||
			strings.TrimSpace(cloudInitNetwork) != ""
		cloudInitCleared := false
		restoreCloudInit := func() error {
			if !cloudInitCleared {
				return nil
			}

			if err := s.DB.Model(&vmModels.VM{}).
				Where("id = ?", reconciledVMID).
				Updates(map[string]any{
					"cloud_init_data":           cloudInitData,
					"cloud_init_meta_data":      cloudInitMeta,
					"cloud_init_network_config": cloudInitNetwork,
				}).Error; err != nil {
				return err
			}

			cloudInitCleared = false
			return nil
		}
		defer func() {
			if !cloudInitCleared {
				return
			}
			if err := restoreCloudInit(); err != nil {
				logger.L.Warn().
					Err(err).
					Uint("rid", rid).
					Msg("failed_to_restore_vm_cloud_init_after_restore_reconcile_error")
			}
		}()

		if hadCloudInit {
			if err := s.DB.Model(&vmModels.VM{}).
				Where("id = ?", reconciledVMID).
				Updates(map[string]any{
					"cloud_init_data":           "",
					"cloud_init_meta_data":      "",
					"cloud_init_network_config": "",
				}).Error; err != nil {
				return fmt.Errorf("failed_to_temporarily_clear_vm_cloud_init_before_domain_rebuild: %w", err)
			}
			cloudInitCleared = true
		}

		if err := s.VM.CreateLvVm(int(reconciledVMID), ctx); err != nil {
			return fmt.Errorf("failed_to_rebuild_restored_vm_domain: %w", err)
		}

		if err := s.restoreVMRuntimeArtifactsFromDataset(ctx, dataset, rid); err != nil {
			return fmt.Errorf("failed_to_restore_vm_runtime_artifacts: %w", err)
		}

		if hadCloudInit {
			if err := restoreCloudInit(); err != nil {
				return fmt.Errorf("failed_to_restore_vm_cloud_init_after_domain_rebuild: %w", err)
			}

			if err := s.VM.SyncVMDisks(rid); err != nil {
				logger.L.Warn().
					Err(err).
					Uint("rid", rid).
					Msg("failed_to_recreate_vm_cloud_init_media_after_restore_reconcile")
			}
		}

		if err := s.VM.WriteVMJson(rid); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("rid", rid).
				Msg("failed_to_write_vm_json_after_restore_reconcile")
		}
	}

	logger.L.Info().
		Uint("rid", rid).
		Str("dataset", dataset).
		Msg("restored_vm_reconciled")

	return nil
}

func (s *Service) normalizeRestoredVMStorages(
	ctx context.Context,
	tx *gorm.DB,
	rid uint,
	storages []vmModels.Storage,
) ([]vmModels.Storage, error) {
	out := make([]vmModels.Storage, 0, len(storages))

	for _, storage := range storages {
		cleaned := vmModels.Storage{
			ID:           storage.ID,
			Type:         storage.Type,
			Name:         strings.TrimSpace(storage.Name),
			DownloadUUID: strings.TrimSpace(storage.DownloadUUID),
			Pool:         strings.TrimSpace(storage.Pool),
			Size:         storage.Size,
			Emulation:    storage.Emulation,
			RecordSize:   storage.RecordSize,
			VolBlockSize: storage.VolBlockSize,
			BootOrder:    storage.BootOrder,
		}

		if cleaned.Type == vmModels.VMStorageTypeDiskImage {
			cleaned.DatasetID = nil
			out = append(out, cleaned)
			continue
		}

		if cleaned.Pool == "" {
			return nil, fmt.Errorf("restored_vm_storage_pool_missing_for_id_%d", cleaned.ID)
		}

		var datasetName string
		switch cleaned.Type {
		case vmModels.VMStorageTypeRaw:
			datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", cleaned.Pool, rid, cleaned.ID)
		case vmModels.VMStorageTypeZVol:
			datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", cleaned.Pool, rid, cleaned.ID)
		default:
			return nil, fmt.Errorf("unsupported_restored_vm_storage_type: %s", cleaned.Type)
		}

		exists, err := s.localDatasetExists(ctx, datasetName)
		if err != nil {
			return nil, fmt.Errorf("failed_to_check_restored_vm_storage_dataset: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("restored_vm_storage_dataset_not_found: %s", datasetName)
		}

		guid, err := s.runLocalZFSGet(ctx, "guid", datasetName)
		if err != nil {
			return nil, fmt.Errorf("failed_to_read_restored_vm_storage_dataset_guid: %w", err)
		}

		storageDataset := vmModels.VMStorageDataset{
			Pool: cleaned.Pool,
			Name: datasetName,
			GUID: strings.TrimSpace(guid),
		}

		var existing vmModels.VMStorageDataset
		if err := tx.Where("name = ?", datasetName).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed_to_lookup_restored_vm_storage_dataset: %w", err)
			}

			if err := tx.Create(&storageDataset).Error; err != nil {
				return nil, fmt.Errorf("failed_to_create_restored_vm_storage_dataset: %w", err)
			}
			cleaned.DatasetID = &storageDataset.ID
		} else {
			updates := map[string]any{
				"pool": storageDataset.Pool,
				"guid": storageDataset.GUID,
			}
			if err := tx.Model(&existing).Updates(updates).Error; err != nil {
				return nil, fmt.Errorf("failed_to_update_restored_vm_storage_dataset: %w", err)
			}
			cleaned.DatasetID = &existing.ID
		}

		out = append(out, cleaned)
	}

	return out, nil
}

func (s *Service) normalizeRestoredVMNetworks(
	tx *gorm.DB,
	rid uint,
	networks []vmModels.Network,
) ([]vmModels.Network, bool, error) {
	if len(networks) == 0 {
		return []vmModels.Network{}, false, nil
	}

	jailLike := make([]jailModels.Network, 0, len(networks))
	emulationByName := make(map[string]string)

	for idx, network := range networks {
		name := fmt.Sprintf("restored-vm-%d-network-%d", rid, idx+1)
		emulation := strings.TrimSpace(network.Emulation)
		if emulation == "" {
			emulation = "virtio"
		}
		emulationByName[name] = emulation

		jailLike = append(jailLike, jailModels.Network{
			Name:           name,
			SwitchID:       network.SwitchID,
			SwitchType:     network.SwitchType,
			StandardSwitch: network.StandardSwitch,
			ManualSwitch:   network.ManualSwitch,
			MacID:          network.MacID,
			MacAddressObj:  network.AddressObj,
		})
	}

	normalized, requiresSwitchSync, err := s.normalizeRestoredJailNetworks(tx, rid, 0, jailLike)
	if err != nil {
		return nil, false, err
	}

	out := make([]vmModels.Network, 0, len(normalized))
	for _, net := range normalized {
		emulation := emulationByName[net.Name]
		if emulation == "" {
			emulation = "virtio"
		}

		out = append(out, vmModels.Network{
			SwitchID:   net.SwitchID,
			SwitchType: net.SwitchType,
			MacID:      net.MacID,
			Emulation:  emulation,
		})
	}

	return out, requiresSwitchSync, nil
}

type restoredVMMetadata struct {
	VM               vmModels.VM
	Snapshots        []vmModels.VMSnapshot
	SnapshotsPresent bool
}

func reconcileRestoredVMSnapshots(
	tx *gorm.DB,
	vmID uint,
	rid uint,
	snapshots []vmModels.VMSnapshot,
	storages []vmModels.Storage,
	destinationDataset string,
) error {
	if tx == nil || vmID == 0 || rid == 0 {
		return nil
	}

	roots := inferRestoredVMRootDatasets(rid, storages, destinationDataset)

	if err := tx.Where("vm_id = ?", vmID).Delete(&vmModels.VMSnapshot{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_restored_vm_snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return nil
	}

	ordered := append([]vmModels.VMSnapshot(nil), snapshots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})

	oldToNewIDs := make(map[uint]uint, len(ordered))
	insertedByName := make(map[string]uint, len(ordered))
	var latestInsertedID uint

	for _, snapshot := range ordered {
		snapshotName := strings.TrimSpace(snapshot.SnapshotName)
		if snapshotName == "" {
			continue
		}
		if _, exists := insertedByName[snapshotName]; exists {
			continue
		}

		name := strings.TrimSpace(snapshot.Name)
		if name == "" {
			name = snapshotName
		}

		record := vmModels.VMSnapshot{
			VMID:         vmID,
			RID:          rid,
			Name:         name,
			Description:  strings.TrimSpace(snapshot.Description),
			SnapshotName: snapshotName,
			RootDatasets: append([]string(nil), roots...),
		}
		if !snapshot.CreatedAt.IsZero() {
			record.CreatedAt = snapshot.CreatedAt
		}
		if !snapshot.UpdatedAt.IsZero() {
			record.UpdatedAt = snapshot.UpdatedAt
		}

		parentID := uint(0)
		if snapshot.ParentSnapshotID != nil && *snapshot.ParentSnapshotID > 0 {
			if mapped, ok := oldToNewIDs[*snapshot.ParentSnapshotID]; ok {
				parentID = mapped
			}
		}
		if parentID == 0 && latestInsertedID > 0 {
			parentID = latestInsertedID
		}
		if parentID > 0 {
			record.ParentSnapshotID = &parentID
		}

		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("failed_to_insert_restored_vm_snapshot: %w", err)
		}

		if snapshot.ID > 0 {
			oldToNewIDs[snapshot.ID] = record.ID
		}
		insertedByName[snapshotName] = record.ID
		latestInsertedID = record.ID
	}

	return nil
}

func inferRestoredVMRootDatasets(rid uint, storages []vmModels.Storage, destinationDataset string) []string {
	roots := make([]string, 0, len(storages)+1)
	seen := make(map[string]struct{}, len(storages)+1)
	addRoot := func(root string) {
		root = normalizeRestoreDestinationDataset(root)
		if root == "" {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}

	for _, storage := range storages {
		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			pool = strings.TrimSpace(storage.Dataset.Pool)
		}
		if pool == "" {
			datasetName := normalizeDatasetPath(storage.Dataset.Name)
			if idx := strings.Index(datasetName, "/"); idx > 0 {
				pool = strings.TrimSpace(datasetName[:idx])
			}
		}
		if pool == "" {
			continue
		}

		addRoot(fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, rid))
	}

	destinationDataset = normalizeRestoreDestinationDataset(destinationDataset)
	if destinationDataset != "" {
		root := vmDatasetRoot(destinationDataset)
		if root == "" {
			root = destinationDataset
		}
		addRoot(root)
	}

	sort.Strings(roots)
	return roots
}

func (s *Service) readLocalRestoredVMMetadata(
	ctx context.Context,
	dataset string,
	fallbackRID uint,
) (*restoredVMMetadata, error) {
	candidates := vmMetadataCandidateDatasets(dataset)
	for _, candidate := range candidates {
		metaRaw, err := s.readLocalDatasetMetadataFile(ctx, candidate, ".sylve/vm.json")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(metaRaw) == "" {
			continue
		}

		type vmMetadataPayload struct {
			vmModels.VM
			Snapshots []vmModels.VMSnapshot `json:"snapshots"`
		}

		payload := vmMetadataPayload{}
		if err := json.Unmarshal([]byte(metaRaw), &payload); err != nil {
			return nil, fmt.Errorf("invalid_restored_vm_metadata_json: %w", err)
		}

		snapshotsPresent := false
		var rawMap map[string]json.RawMessage
		if err := json.Unmarshal([]byte(metaRaw), &rawMap); err == nil {
			_, snapshotsPresent = rawMap["snapshots"]
		}

		if payload.RID == 0 {
			payload.RID = fallbackRID
		}
		return &restoredVMMetadata{
			VM:               payload.VM,
			Snapshots:        payload.Snapshots,
			SnapshotsPresent: snapshotsPresent,
		}, nil
	}

	return nil, nil
}

func (s *Service) readLocalDatasetMetadataFile(ctx context.Context, dataset string, relativeMetaPath string) (string, error) {
	raw, found, err := s.readLocalDatasetMetadataBytes(ctx, dataset, relativeMetaPath)
	if err != nil || !found {
		return "", err
	}

	return strings.TrimSpace(string(raw)), nil
}

func (s *Service) restoreVMRuntimeArtifactsFromDataset(ctx context.Context, dataset string, rid uint) error {
	if rid == 0 {
		return nil
	}

	candidates := vmMetadataCandidateDatasets(dataset)
	if len(candidates) == 0 {
		return nil
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_vms_path: %w", err)
	}

	configDir := filepath.Join(vmDir, strconv.Itoa(int(rid)))
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_vm_config_dir: %w", err)
	}

	artifactNames := []string{
		fmt.Sprintf("%d_vars.fd", rid),
		fmt.Sprintf("%d_tpm.log", rid),
		fmt.Sprintf("%d_tpm.state", rid),
	}

	copied := make([]string, 0, len(artifactNames))
	for _, artifactName := range artifactNames {
		relativePath := filepath.Join(".sylve", artifactName)
		artifactCopied := false

		for _, candidate := range candidates {
			data, found, readErr := s.readLocalDatasetMetadataBytes(ctx, candidate, relativePath)
			if readErr != nil {
				return fmt.Errorf("failed_to_read_vm_artifact_%s_from_%s: %w", artifactName, candidate, readErr)
			}
			if !found {
				continue
			}

			dstPath := filepath.Join(configDir, artifactName)
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return fmt.Errorf("failed_to_write_vm_artifact_%s: %w", artifactName, err)
			}

			artifactCopied = true
			copied = append(copied, artifactName)
			break
		}

		if !artifactCopied {
			logger.L.Debug().
				Uint("rid", rid).
				Str("artifact", artifactName).
				Msg("restored_vm_artifact_not_found_in_dataset_metadata")
		}
	}

	if len(copied) > 0 {
		logger.L.Info().
			Uint("rid", rid).
			Strs("artifacts", copied).
			Msg("restored_vm_runtime_artifacts")
	}

	return nil
}

func (s *Service) readLocalDatasetMetadataBytes(
	ctx context.Context,
	dataset string,
	relativeMetaPath string,
) ([]byte, bool, error) {
	mountedOut, err := s.runLocalZFSGet(ctx, "mounted", dataset)
	if err != nil {
		return nil, false, fmt.Errorf("failed_to_read_dataset_mounted_property: %w", err)
	}
	mountpointOut, err := s.runLocalZFSGet(ctx, "mountpoint", dataset)
	if err != nil {
		return nil, false, fmt.Errorf("failed_to_read_dataset_mountpoint_property: %w", err)
	}

	mounted := strings.EqualFold(strings.TrimSpace(mountedOut), "yes")
	mountPoint := strings.TrimSpace(mountpointOut)
	if mountPoint == "" || mountPoint == "-" || mountPoint == "none" || mountPoint == "legacy" {
		return nil, false, nil
	}

	if !mounted {
		if err := s.mountLocalDataset(ctx, dataset); err != nil {
			return nil, false, fmt.Errorf("failed_to_mount_restored_dataset: %w", err)
		}
	}

	metaPath := filepath.Join(strings.TrimSuffix(mountPoint, "/"), strings.TrimLeft(relativeMetaPath, "/"))
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed_to_read_restored_dataset_metadata_file: %w", err)
	}

	return raw, true, nil
}
