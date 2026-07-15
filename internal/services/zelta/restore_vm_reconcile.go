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
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"gorm.io/gorm"
)

func normalizeRestoredVMBootROM(value vmModels.VMBootROM) vmModels.VMBootROM {
	switch strings.TrimSpace(strings.ToLower(string(value))) {
	case string(vmModels.VMBootROMNone):
		return vmModels.VMBootROMNone
	case string(vmModels.VMBootROMUBoot):
		return vmModels.VMBootROMUBoot
	case "":
		if runtime.GOARCH == "arm64" {
			return vmModels.VMBootROMUBoot
		}
		return vmModels.VMBootROMUEFI
	default:
		return vmModels.VMBootROMUEFI
	}
}

func (s *Service) reconcileRestoredVMFromDatasetWithOptions(ctx context.Context, dataset string, restoreNetwork bool) error {
	return s.reconcileRestoredVMFromDataset(ctx, dataset, "", restoreNetwork, false, false)
}

// reconcileRestoredVMFromDatasetForOwnerRecovery rebuilds a registration that
// was retired on the active owner. Its caller has already proven the owner
// lease, so the restored storage rows are not a user topology change.
func (s *Service) reconcileRestoredVMFromDatasetForOwnerRecovery(ctx context.Context, dataset string) error {
	return s.reconcileRestoredVMFromDataset(ctx, dataset, "", true, false, true)
}

func (s *Service) reconcileRestoredVMFromDatasetAsNew(
	ctx context.Context,
	dataset, sourcePrimaryRoot string,
	restoreNetwork bool,
) error {
	return s.reconcileRestoredVMFromDataset(ctx, dataset, sourcePrimaryRoot, restoreNetwork, true, false)
}

func (s *Service) reconcileRestoredVMFromDataset(
	ctx context.Context,
	dataset, sourcePrimaryRoot string,
	restoreNetwork bool,
	strictAsNew bool,
	recoverOwnerRegistration bool,
) (err error) {
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
	if restoredMeta == nil {
		return fmt.Errorf("restored_vm_metadata_not_found")
	}
	restored := &restoredMeta.VM
	sourceRID := restored.RID
	if sourceRID == 0 {
		sourceRID = fallbackRID
	}
	restored.RID = fallbackRID
	if restored.RID == 0 {
		return fmt.Errorf("restored_vm_rid_missing")
	}
	if strictAsNew {
		rewriteRestoredVMMetadataIdentity(restoredMeta, fallbackRID)
		rebaseRestoredVMMetadataRoot(restoredMeta, sourcePrimaryRoot, dataset)
		if err := s.writeVMMetadataToDataset(ctx, dataset, restoredMeta); err != nil {
			return fmt.Errorf("failed_to_rewrite_restored_vm_metadata_identity: %w", err)
		}
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
	restored.BootROM = normalizeRestoredVMBootROM(restored.BootROM)

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

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if strictAsNew {
			var jailCount int64
			if err := tx.Model(&jailModels.Jail{}).Where("ct_id = ?", rid).Count(&jailCount).Error; err != nil {
				return fmt.Errorf("failed_to_lookup_existing_jail_by_ctid: %w", err)
			}
			if jailCount > 0 {
				return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=jail", rid)
			}
		}

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
			VNCBind:                libvirt.NormalizeVNCBindAddress(restored.VNCBind),
			VNCPort:                restored.VNCPort,
			VNCPassword:            restored.VNCPassword,
			VNCResolution:          restored.VNCResolution,
			VNCWait:                restored.VNCWait,
			StartAtBoot:            restored.StartAtBoot,
			StartOrder:             restored.StartOrder,
			WoL:                    restored.WoL,
			TimeOffset:             restored.TimeOffset,
			BootROM:                restored.BootROM,
			ACPI:                   restored.ACPI,
			APIC:                   restored.APIC,
			CloudInitData:          restored.CloudInitData,
			CloudInitMetaData:      restored.CloudInitMetaData,
			CloudInitNetworkConfig: restored.CloudInitNetworkConfig,
			ExtraBhyveOptions:      append([]string(nil), restored.ExtraBhyveOptions...),
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
			if strictAsNew {
				return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=vm", rid)
			}
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
				"VNCBind",
				"VNCPort",
				"VNCPassword",
				"VNCResolution",
				"VNCWait",
				"StartAtBoot",
				"StartOrder",
				"WoL",
				"TimeOffset",
				"BootROM",
				"ACPI",
				"APIC",
				"CloudInitData",
				"CloudInitMetaData",
				"CloudInitNetworkConfig",
				"ExtraBhyveOptions",
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
		storageTx := tx
		if recoverOwnerRegistration {
			storageTx = tx.Session(&gorm.Session{SkipHooks: true})
		}
		if err := storageTx.Where("vm_id = ?", reconciledVMID).Delete(&vmModels.Storage{}).Error; err != nil {
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
			if err := storageTx.Create(&normalizedStorages).Error; err != nil {
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
		if !strictAsNew {
			if err := s.VM.RemoveLvVm(rid); err != nil {
				logger.L.Warn().
					Err(err).
					Uint("rid", rid).
					Msg("failed_to_remove_existing_vm_domain_before_restore_reconcile")
			}
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

		if err := s.restoreVMRuntimeArtifactsFromDataset(ctx, dataset, sourceRID, rid); err != nil {
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
			if strictAsNew {
				return fmt.Errorf("failed_to_write_restored_vm_metadata: %w", err)
			}
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

func rewriteVMDatasetGuestID(dataset string, rid uint) string {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" || rid == 0 {
		return dataset
	}

	parts := strings.Split(dataset, "/")
	for idx := 0; idx+1 < len(parts); idx++ {
		if parts[idx] != "virtual-machines" || extractDatasetGuestID(parts[idx+1]) == 0 {
			continue
		}
		parts[idx+1] = strconv.FormatUint(uint64(rid), 10)
	}

	return normalizeDatasetPath(strings.Join(parts, "/"))
}

func rewriteRestoredVMMetadataIdentity(meta *restoredVMMetadata, rid uint) {
	if meta == nil || rid == 0 {
		return
	}

	meta.VM.RID = rid
	for idx := range meta.VM.Storages {
		storage := &meta.VM.Storages[idx]
		storage.Dataset.Name = rewriteVMDatasetGuestID(storage.Dataset.Name, rid)
		if storage.Dataset.Name != "" {
			pool := strings.Split(storage.Dataset.Name, "/")[0]
			if strings.TrimSpace(pool) != "" {
				storage.Pool = pool
				storage.Dataset.Pool = pool
			}
		}
	}
	for idx := range meta.Snapshots {
		snapshot := &meta.Snapshots[idx]
		snapshot.RID = rid
		snapshot.VMID = 0
		for rootIdx := range snapshot.RootDatasets {
			snapshot.RootDatasets[rootIdx] = rewriteVMDatasetGuestID(
				snapshot.RootDatasets[rootIdx],
				rid,
			)
		}
	}
}

func rebaseRestoredVMMetadataRoot(
	meta *restoredVMMetadata,
	sourceRoot, destinationRoot string,
) {
	if meta == nil || meta.VM.RID == 0 {
		return
	}

	sourceRoot = rewriteVMDatasetGuestID(vmDatasetRoot(sourceRoot), meta.VM.RID)
	destinationRoot = vmDatasetRoot(destinationRoot)
	if destinationRoot == "" {
		return
	}
	sourcePool := vmRootPool(sourceRoot)
	destinationPool := vmRootPool(destinationRoot)

	withinSource := func(dataset string) bool {
		dataset = normalizeDatasetPath(dataset)
		return sourceRoot != "" && (dataset == sourceRoot || strings.HasPrefix(dataset, sourceRoot+"/"))
	}
	matchesMetadata := false
	for _, storage := range meta.VM.Storages {
		if withinSource(storage.Dataset.Name) ||
			(storage.Type != vmModels.VMStorageTypeDiskImage &&
				strings.TrimSpace(storage.Dataset.Name) == "" &&
				sourcePool != "" &&
				strings.TrimSpace(storage.Pool) == sourcePool) {
			matchesMetadata = true
			break
		}
	}
	if !matchesMetadata {
		for _, snapshot := range meta.Snapshots {
			for _, root := range snapshot.RootDatasets {
				if withinSource(root) {
					matchesMetadata = true
					break
				}
			}
		}
	}
	if !matchesMetadata {
		if sourcePool != "" {
			return
		}
		candidateRoots := make(map[string]struct{})
		addCandidateRoot := func(root string) {
			root = vmDatasetRoot(root)
			if root != "" {
				candidateRoots[root] = struct{}{}
			}
		}
		for _, storage := range meta.VM.Storages {
			if storage.Type == vmModels.VMStorageTypeDiskImage {
				continue
			}
			if strings.TrimSpace(storage.Dataset.Name) != "" {
				addCandidateRoot(storage.Dataset.Name)
				continue
			}
			if pool := strings.TrimSpace(storage.Pool); pool != "" {
				addCandidateRoot(fmt.Sprintf(
					"%s/sylve/virtual-machines/%d",
					pool,
					meta.VM.RID,
				))
			}
		}
		for _, snapshot := range meta.Snapshots {
			for _, root := range snapshot.RootDatasets {
				addCandidateRoot(root)
			}
		}
		if len(candidateRoots) != 1 {
			return
		}
		for root := range candidateRoots {
			sourceRoot = root
		}
		sourcePool = vmRootPool(sourceRoot)
	}
	if sourceRoot == "" || sourceRoot == destinationRoot {
		return
	}

	rebase := func(dataset string) string {
		dataset = normalizeDatasetPath(dataset)
		if dataset == sourceRoot {
			return destinationRoot
		}
		if strings.HasPrefix(dataset, sourceRoot+"/") {
			return destinationRoot + strings.TrimPrefix(dataset, sourceRoot)
		}
		return dataset
	}

	for idx := range meta.VM.Storages {
		storage := &meta.VM.Storages[idx]
		if strings.TrimSpace(storage.Dataset.Name) == "" {
			if storage.Type != vmModels.VMStorageTypeDiskImage &&
				sourcePool != "" &&
				destinationPool != "" &&
				strings.TrimSpace(storage.Pool) == sourcePool {
				storage.Pool = destinationPool
				storage.Dataset.Pool = destinationPool
			}
			continue
		}
		storage.Dataset.Name = rebase(storage.Dataset.Name)
		if storage.Dataset.Name != "" {
			pool := strings.Split(storage.Dataset.Name, "/")[0]
			storage.Pool = pool
			storage.Dataset.Pool = pool
		}
	}
	for idx := range meta.Snapshots {
		for rootIdx := range meta.Snapshots[idx].RootDatasets {
			meta.Snapshots[idx].RootDatasets[rootIdx] = rebase(
				meta.Snapshots[idx].RootDatasets[rootIdx],
			)
		}
	}
}

func vmRootPool(root string) string {
	parts := strings.Split(vmDatasetRoot(root), "/")
	if len(parts) != 4 || parts[1] != "sylve" || parts[2] != "virtual-machines" {
		return ""
	}
	if extractDatasetGuestID(parts[3]) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *Service) normalizeRestoredVMStorages(
	ctx context.Context,
	tx *gorm.DB,
	rid uint,
	storages []vmModels.Storage,
) ([]vmModels.Storage, error) {
	out := make([]vmModels.Storage, 0, len(storages))

	for _, storage := range storages {
		originalID := storage.ID
		cleaned := vmModels.Storage{
			ID:           0,
			Type:         storage.Type,
			Name:         strings.TrimSpace(storage.Name),
			DownloadUUID: strings.TrimSpace(storage.DownloadUUID),
			Pool:         strings.TrimSpace(storage.Pool),
			Enable:       storage.Enable,
			Size:         storage.Size,
			Emulation:    storage.Emulation,
			RecordSize:   storage.RecordSize,
			VolBlockSize: storage.VolBlockSize,
			BootOrder:    storage.BootOrder,
		}

		if cleaned.Type == vmModels.VMStorageTypeDiskImage {
			// Downloaded media remains replicated metadata, not a restored ZFS
			// topology edge. Discard stale pool/dataset hints from older vm.json.
			cleaned.Pool = ""
			cleaned.DatasetID = nil
			cleaned.Dataset = vmModels.VMStorageDataset{}
			out = append(out, cleaned)
			continue
		}

		if cleaned.Pool == "" {
			return nil, fmt.Errorf("restored_vm_storage_pool_missing_for_id_%d", originalID)
		}

		var datasetName string
		if strings.TrimSpace(storage.Dataset.Name) != "" {
			datasetName = strings.TrimSpace(storage.Dataset.Name)
		} else {
			switch cleaned.Type {
			case vmModels.VMStorageTypeRaw:
				datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", cleaned.Pool, rid, originalID)
			case vmModels.VMStorageTypeZVol:
				datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", cleaned.Pool, rid, originalID)
			case vmModels.VMStorageTypeFilesystem:
				logger.L.Warn().
					Uint("rid", rid).
					Uint("storage_id", originalID).
					Msg("skipping_restored_vm_filesystem_storage_without_dataset")
				continue
			default:
				return nil, fmt.Errorf("unsupported_restored_vm_storage_type: %s", cleaned.Type)
			}
		}

		exists, err := s.localDatasetExists(ctx, datasetName)
		if err != nil {
			return nil, fmt.Errorf("failed_to_check_restored_vm_storage_dataset: %w", err)
		}
		if !exists {
			if cleaned.Type == vmModels.VMStorageTypeFilesystem {
				logger.L.Warn().
					Uint("rid", rid).
					Str("dataset", datasetName).
					Msg("skipping_restored_vm_filesystem_share_dataset_not_present_on_target")
				continue
			}
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

	type networkSettings struct {
		emulation string
		enabled   bool
	}
	jailLike := make([]jailModels.Network, 0, len(networks))
	settingsByName := make(map[string]networkSettings)

	for idx, network := range networks {
		name := fmt.Sprintf("restored-vm-%d-network-%d", rid, idx+1)
		emulation := strings.TrimSpace(network.Emulation)
		if emulation == "" {
			emulation = "virtio"
		}
		settingsByName[name] = networkSettings{emulation: emulation, enabled: network.Enable}

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
		settings := settingsByName[net.Name]
		if settings.emulation == "" {
			// The jail-network helper may suffix its transient name to avoid a
			// collision. VM networks do not persist that name, so recover the
			// original settings from the generated-name prefix.
			for originalName, candidate := range settingsByName {
				if strings.HasPrefix(net.Name, originalName+"-") {
					settings = candidate
					break
				}
			}
		}
		emulation := settings.emulation
		if emulation == "" {
			emulation = "virtio"
		}

		out = append(out, vmModels.Network{
			SwitchID:   net.SwitchID,
			SwitchType: net.SwitchType,
			MacID:      net.MacID,
			Emulation:  emulation,
			Enable:     settings.enabled,
		})
	}

	return out, requiresSwitchSync, nil
}

type restoredVMMetadata struct {
	VM        vmModels.VM
	Snapshots []vmModels.VMSnapshot
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

		if !isBackupSnapshotShortName(snapshotName) {
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

		if payload.RID == 0 {
			payload.RID = fallbackRID
		}
		return &restoredVMMetadata{
			VM:        payload.VM,
			Snapshots: payload.Snapshots,
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

type vmRuntimeArtifactName struct {
	Source      string
	Destination string
}

func vmRuntimeArtifactNames(sourceRID, destinationRID uint) []vmRuntimeArtifactName {
	if destinationRID == 0 {
		return nil
	}
	if sourceRID == 0 {
		sourceRID = destinationRID
	}

	source := strconv.FormatUint(uint64(sourceRID), 10)
	destination := strconv.FormatUint(uint64(destinationRID), 10)
	return []vmRuntimeArtifactName{
		{Source: source + "_vars.fd", Destination: destination + "_vars.fd"},
		{Source: source + "_tpm.log", Destination: destination + "_tpm.log"},
		{Source: source + "_tpm.state", Destination: destination + "_tpm.state"},
	}
}

func (s *Service) restoreVMRuntimeArtifactsFromDataset(
	ctx context.Context,
	dataset string,
	sourceRID, destinationRID uint,
) error {
	if destinationRID == 0 {
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

	configDir := filepath.Join(vmDir, strconv.Itoa(int(destinationRID)))
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_vm_config_dir: %w", err)
	}

	artifactNames := vmRuntimeArtifactNames(sourceRID, destinationRID)

	copied := make([]string, 0, len(artifactNames))
	for _, artifactName := range artifactNames {
		relativePath := filepath.Join(".sylve", artifactName.Source)
		artifactCopied := false

		for _, candidate := range candidates {
			data, found, readErr := s.readLocalDatasetMetadataBytes(ctx, candidate, relativePath)
			if readErr != nil {
				return fmt.Errorf("failed_to_read_vm_artifact_%s_from_%s: %w", artifactName.Source, candidate, readErr)
			}
			if !found {
				continue
			}

			dstPath := filepath.Join(configDir, artifactName.Destination)
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return fmt.Errorf("failed_to_write_vm_artifact_%s: %w", artifactName.Destination, err)
			}

			artifactCopied = true
			copied = append(copied, artifactName.Destination)
			break
		}

		if !artifactCopied {
			logger.L.Debug().
				Uint("rid", destinationRID).
				Str("artifact", artifactName.Source).
				Msg("restored_vm_artifact_not_found_in_dataset_metadata")
		}
	}

	if len(copied) > 0 {
		logger.L.Info().
			Uint("rid", destinationRID).
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
