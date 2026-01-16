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
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
)

func (s *Service) CreateVMDisk(rid uint, storage vmModels.Storage, ctx context.Context) error {
	usable, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var target *gzfs.ZPool

	for _, pool := range usable {
		if pool.Name == storage.Pool {
			target = pool
			break
		}
	}

	if target == nil {
		return fmt.Errorf("pool_not_found: %s", storage.Pool)
	}

	if target.Free < uint64(storage.Size) {
		return fmt.Errorf("insufficient_space_in_pool: %s", storage.Pool)
	}

	var datasets []*gzfs.Dataset

	if storage.Type == vmModels.VMStorageTypeRaw || storage.Type == vmModels.VMStorageTypeZVol {
		switch storage.Type {
		case vmModels.VMStorageTypeRaw:
			datasets, err = s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeFilesystem,
				false,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, rid, storage.ID),
			)
		case vmModels.VMStorageTypeZVol:
			datasets, err = s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeVolume,
				false,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, rid, storage.ID),
			)
		}

		if err != nil {
			if !strings.Contains(err.Error(), "dataset does not exist") {
				return fmt.Errorf("failed_to_get_datasets: %w", err)
			}
		}
	}

	// var dataset *zfs.Dataset
	var dataset *gzfs.Dataset

	if len(datasets) == 0 {
		var recordSize string
		if storage.RecordSize != 0 {
			recordSize = strconv.Itoa(storage.RecordSize)
		} else {
			recordSize = "1M"
		}

		var volblocksize string
		if storage.VolBlockSize != 0 {
			volblocksize = strconv.Itoa(storage.VolBlockSize)
		} else {
			volblocksize = "16K"
		}

		props := map[string]string{
			"compression":    "zstd",
			"logbias":        "throughput",
			"primarycache":   "metadata",
			"secondarycache": "all",
		}

		switch storage.Type {
		case vmModels.VMStorageTypeRaw:
			props["atime"] = "off"

			dataset, err = s.GZFS.ZFS.CreateFilesystem(
				ctx,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, rid, storage.ID),
				utils.MergeMaps(props, map[string]string{
					"recordsize": recordSize,
				}),
			)
		case vmModels.VMStorageTypeZVol:
			dataset, err = s.GZFS.ZFS.CreateVolume(
				ctx,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, rid, storage.ID),
				uint64(storage.Size),
				utils.MergeMaps(props, map[string]string{
					"volblocksize": volblocksize,
					"volmode":      "dev",
				}),
			)
		}

		if err != nil {
			return fmt.Errorf("failed_to_create_dataset: %w", err)
		}
	} else {
		dataset = datasets[0]
	}

	if storage.Type == vmModels.VMStorageTypeRaw {
		imagePath := filepath.Join(dataset.Mountpoint, fmt.Sprintf("%d.img", storage.ID))
		if _, err := os.Stat(imagePath); err == nil {
			logger.L.Info().Msgf("Disk image %s already exists, skipping creation", imagePath)
			return nil
		}

		if err := utils.CreateOrTruncateFile(imagePath, storage.Size); err != nil {
			_ = dataset.Destroy(ctx, true, false)
			return fmt.Errorf("failed_to_create_or_truncate_image_file: %w", err)
		}
	}

	storageDataset := vmModels.VMStorageDataset{
		Pool: target.Name,
		Name: dataset.Name,
		GUID: dataset.GUID,
	}

	if err := s.DB.Create(&storageDataset).Error; err != nil {
		_ = dataset.Destroy(ctx, true, false)
		return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
	}

	storage.DatasetID = &storageDataset.ID

	if err := s.DB.Save(&storage).Error; err != nil {
		_ = dataset.Destroy(ctx, true, false)
		return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
	}

	return nil
}

func (s *Service) SyncVMDisks(rid uint) error {
	off, err := s.IsDomainShutOff(rid)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		root := doc.Root()
		if root.SelectAttr("xmlns:bhyve") == nil {
			root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
		}
		bhyveCommandline = root.CreateElement("bhyve:commandline")
	}

	for _, arg := range bhyveCommandline.ChildElements() {
		valAttr := arg.SelectAttr("value")
		if valAttr == nil {
			continue
		}

		val := valAttr.Value

		if val == "" {
			continue
		}

		emulations := []string{
			string(libvirtServiceInterfaces.AHCICDStorageEmulation),
			string(libvirtServiceInterfaces.AHCIHDStorageEmulation),
			string(libvirtServiceInterfaces.NVMEStorageEmulation),
			string(libvirtServiceInterfaces.VirtIOStorageEmulation),
		}

		if utils.PartialStringInSlice(val, emulations) {
			bhyveCommandline.RemoveChild(arg)
		}
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	var storages []vmModels.Storage
	if err := s.DB.
		Where("vm_id = ?", vm.ID).
		Order("boot_order ASC").
		Find(&storages).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_storages: %w", err)
	}

	argValues := []string{}

	used := parseUsedIndicesFromElement(bhyveCommandline)
	currentIndex := 10

	for _, storage := range storages {
		for currentIndex < 30 && used[currentIndex] {
			currentIndex++
		}

		if currentIndex >= 30 {
			return fmt.Errorf("no free indices available")
		}

		index := currentIndex
		used[index] = true
		currentIndex++

		argCommon := fmt.Sprintf("-s %d:0,%s", index, storage.Emulation)
		var argValue string
		var diskValue string

		if storage.Type == vmModels.VMStorageTypeRaw {
			diskValue = fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				storage.Pool,
				rid,
				storage.ID,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			diskValue = fmt.Sprintf("/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d",
				storage.Pool,
				rid,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeDiskImage {
			diskValue, err = s.FindISOByUUID(storage.DownloadUUID, true)
			if err != nil {
				return fmt.Errorf("failed_to_get_iso_path_by_uuid: %w", err)
			}

			diskValue = fmt.Sprintf("%s,ro", diskValue)
		}

		argValue = fmt.Sprintf("%s,%s", argCommon, diskValue)
		argValues = append(argValues, argValue)
	}

	err = s.CreateCloudInitISO(vm)
	if err != nil {
		logger.L.Error().Err(err).Msg("vm: sync_vm_disks: failed_to_create_cloud_init_iso")
	}

	if vm.CloudInitData != "" {
		cloudInitISOPath, err := s.GetCloudInitISOPath(vm.RID)
		if err != nil {
			logger.L.Warn().Err(err).Msg("vm: sync_vm_disks: failed_to_get_cloud_init_iso_path")
		} else if cloudInitISOPath != "" {
			for currentIndex < 30 && used[currentIndex] {
				currentIndex++
			}

			if currentIndex >= 30 {
				return fmt.Errorf("no_free_indices_available_for_cloud_init_iso")
			}

			used[currentIndex] = true

			argValue := fmt.Sprintf("-s %d:0,ahci-cd,%s,ro", currentIndex, cloudInitISOPath)
			argValues = append(argValues, argValue)
		}
	}

	for _, val := range argValues {
		argElement := bhyveCommandline.CreateElement("bhyve:arg")
		argElement.CreateAttr("value", val)
	}

	newXML, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed to serialize XML: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(newXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

func (s *Service) RemoveStorageXML(rid uint, storage vmModels.Storage) error {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		root := doc.Root()
		if root.SelectAttr("xmlns:bhyve") == nil {
			root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
		}
		bhyveCommandline = root.CreateElement("bhyve:commandline")
	}

	var filePath string

	if storage.Type == vmModels.VMStorageTypeDiskImage &&
		storage.DownloadUUID != "" {
		filePath, err = s.FindISOByUUID(storage.DownloadUUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}
	} else if storage.Type == vmModels.VMStorageTypeRaw {
		filePath = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			rid,
			storage.ID,
			storage.ID,
		)
	} else if storage.Type == vmModels.VMStorageTypeZVol {
		filePath = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
			storage.Pool,
			rid,
			storage.ID,
		)
	}

	if filePath == "" {
		return fmt.Errorf("unable_to_determine_storage_path")
	}

	for _, arg := range bhyveCommandline.ChildElements() {
		valAttr := arg.SelectAttr("value")
		if valAttr == nil {
			continue
		}

		val := valAttr.Value
		if val == "" {
			continue
		}

		if (storage.Type == vmModels.VMStorageTypeDiskImage ||
			storage.Type == vmModels.VMStorageTypeRaw ||
			storage.Type == vmModels.VMStorageTypeZVol) &&
			strings.Contains(val, filePath) {
			bhyveCommandline.RemoveChild(arg)
		}
	}

	out, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed_to_serialize_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(out); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

func (s *Service) StorageDetach(req libvirtServiceInterfaces.StorageDetachRequest) error {
	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	var storage vmModels.Storage
	if err := s.DB.
		Preload("Dataset").
		First(&storage, "id = ? AND vm_id = ?", req.StorageId, vm.ID).
		Error; err != nil {
		return fmt.Errorf("failed_to_find_storage_record: %w", err)
	}

	if err := s.RemoveStorageXML(req.RID, storage); err != nil {
		logger.L.Error().Err(err).Msg("vm: storage_detach: failed_to_remove_storage_xml")
	}

	if storage.DatasetID != nil {
		var dataset vmModels.VMStorageDataset
		if err := s.DB.First(&dataset, "id = ?", *storage.DatasetID).Error; err != nil {
			return fmt.Errorf("failed_to_find_storage_dataset_record: %w", err)
		}

		if err := s.DB.Delete(&dataset).Error; err != nil {
			return fmt.Errorf("failed_to_delete_storage_dataset_record: %w", err)
		}
	}

	if err := s.DB.Delete(&storage).Error; err != nil {
		return fmt.Errorf("failed_to_delete_storage_record: %w", err)
	}

	return nil
}

func (s *Service) GetNextBootOrderIndex(vmId int) (int, error) {
	var maxBootOrder sql.NullInt64
	err := s.DB.
		Model(&vmModels.Storage{}).
		Where("vm_id = ?", vmId).
		Select("MAX(boot_order)").
		Scan(&maxBootOrder).Error
	if err != nil {
		return 0, fmt.Errorf("failed_to_get_max_boot_order: %w", err)
	}

	if maxBootOrder.Valid {
		return int(maxBootOrder.Int64) + 1, nil
	}

	return 0, nil
}

func (s *Service) ValidateBootOrderIndex(vmId int, bootOrder int) (bool, error) {
	var count int64
	err := s.DB.
		Model(&vmModels.Storage{}).
		Where("vm_id = ? AND boot_order = ?", vmId, bootOrder).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
	}

	return count == 0, nil
}

func (s *Service) StorageImport(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM, ctx context.Context) error {
	var storage vmModels.Storage

	storage.Name = req.Name
	storage.VMID = vm.ID

	if req.Pool == "" || strings.TrimSpace(req.Pool) == "" {
		storage.Pool = ""
	} else {
		storage.Pool = req.Pool
	}

	storage.Emulation = vmModels.VMStorageEmulationType(req.Emulation)
	storage.BootOrder = *req.BootOrder

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		exists, err := utils.FileExists(req.RawPath)
		if err != nil {
			return fmt.Errorf("failed_to_check_raw_path_exists: %w", err)
		}

		if !exists {
			return fmt.Errorf("raw_path_does_not_exist: %s", req.RawPath)
		}

		info, err := os.Stat(req.RawPath)
		if err != nil {
			return fmt.Errorf("failed_to_stat_raw_path: %w", err)
		}

		storage.Type = vmModels.VMStorageTypeRaw
		storage.Size = info.Size()

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		if err := s.CreateVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		datasetPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.RID,
			storage.ID,
			storage.ID,
		)

		err = os.Remove(datasetPath)
		if err != nil {
			logger.L.Warn().Err(err).Msg("failed_to_remove_created_image_file")
		}

		if err := utils.CopyFile(req.RawPath, datasetPath); err != nil {
			return fmt.Errorf("failed_to_copy_raw_file_to_dataset: %w", err)
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		datasets, err := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeVolume,
			true,
			req.Pool,
		)

		if err != nil {
			return fmt.Errorf("failed_to_get_zvols_in_pool: %w", err)
		}

		var found *gzfs.Dataset
		for _, ds := range datasets {
			if ds.GUID == req.Dataset {
				found = ds
				break
			}
		}

		if found == nil {
			return fmt.Errorf("zvol_dataset_not_found_in_pool: %s", req.Dataset)
		}

		var sourcePool string
		parts := strings.SplitN(found.Name, "/", 2)
		if len(parts) > 0 {
			sourcePool = parts[0]
		}

		var volSize int64
		volSizeProp, ok := found.Properties["volsize"]
		if ok {
			volSize, err = strconv.ParseInt(volSizeProp.Value, 10, 64)
			if err != nil {
				return fmt.Errorf("failed_to_parse_volsize: %w", err)
			}
		} else {
			return fmt.Errorf("volsize_property_not_found_in_zvol_dataset")
		}

		if volSize <= 0 {
			return fmt.Errorf("invalid_volsize: %d", volSize)
		}

		storage.Size = volSize
		storage.Type = vmModels.VMStorageTypeZVol

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		if sourcePool == req.Pool {
			targetDatasetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
				req.Pool,
				vm.RID,
				storage.ID,
			)

			dataset, err := found.Rename(ctx, targetDatasetPath, false)
			if err != nil || dataset == nil {
				_ = s.DB.Delete(&storage).Error
				return fmt.Errorf("failed_to_rename_zvol_dataset: %w", err)
			}

			storageDataset := vmModels.VMStorageDataset{
				Pool: req.Pool,
				Name: dataset.Name,
				GUID: dataset.GUID,
			}

			if err := s.DB.Create(&storageDataset).Error; err != nil {
				return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
			}

			storage.DatasetID = &storageDataset.ID

			if err := s.DB.Save(&storage).Error; err != nil {
				return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
			}
		} else {
			if err := s.CreateVMDisk(vm.RID, storage, ctx); err != nil {
				return fmt.Errorf("failed_to_create_vm_disk: %w", err)
			}

			snapshotName := fmt.Sprintf("import-snap-%d-%d", vm.RID, storage.ID)
			snapshot, err := found.Snapshot(ctx, snapshotName, false)
			if err != nil {
				return fmt.Errorf("failed_to_create_snapshot_of_imported_zvol: %w", err)
			}

			targetDatasetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
				req.Pool,
				vm.RID,
				storage.ID,
			)

			targetDatasets, err := s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeVolume,
				false,
				targetDatasetPath,
			)

			if err != nil {
				return fmt.Errorf("failed_to_get_target_zvols: %w", err)
			}

			if len(targetDatasets) == 0 {
				return fmt.Errorf("target_zvol_dataset_not_found: %s", targetDatasetPath)
			}

			targetDataset := targetDatasets[0]
			_, err = snapshot.SendToDataset(ctx, targetDataset.Name, true)
			if err != nil {
				return fmt.Errorf("failed_to_send_snapshot_to_dataset: %w", err)
			}

			if err := snapshot.Destroy(ctx, true, false); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_destroy_import_snapshot")
			}
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeDiskImage {
		imagePath, err := s.FindISOByUUID(req.UUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}

		info, err := os.Stat(imagePath)
		if err != nil {
			return fmt.Errorf("failed_to_stat_iso_path: %w", err)
		}

		storage.Type = vmModels.VMStorageTypeDiskImage
		storage.Size = info.Size()
		storage.DownloadUUID = req.UUID

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
	}

	return s.SyncVMDisks(vm.RID)
}

func (s *Service) StorageNew(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM, ctx context.Context) error {
	var storage vmModels.Storage

	storage.Name = req.Name
	storage.VMID = vm.ID
	storage.Pool = req.Pool
	storage.Emulation = vmModels.VMStorageEmulationType(req.Emulation)
	storage.Size = *req.Size
	storage.BootOrder = *req.BootOrder

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		storage.Type = vmModels.VMStorageTypeRaw

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		if err := s.CreateVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		diskPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.RID,
			storage.ID,
			storage.ID,
		)

		exists, err := utils.FileExists(diskPath)
		if err != nil {
			return fmt.Errorf("failed_to_check_created_disk_path_exists: %w", err)
		}

		if !exists {
			return fmt.Errorf("created_disk_path_does_not_exist_after_creation: %s", diskPath)
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		storage.Type = vmModels.VMStorageTypeZVol

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		if err := s.CreateVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
	}

	return s.SyncVMDisks(vm.RID)
}

func (s *Service) StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) error {
	if req.Name == "" ||
		strings.TrimSpace(req.Name) == "" ||
		len(req.Name) == 0 ||
		len(req.Name) > 128 {
		return fmt.Errorf("invalid_storage_name")
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	var bootOrder int
	if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	} else {
		nextIndex, err := s.GetNextBootOrderIndex(int(vm.ID))
		if err != nil {
			return fmt.Errorf("failed_to_get_next_boot_order_index: %w", err)
		}
		bootOrder = nextIndex
	}

	valid, err := s.ValidateBootOrderIndex(int(vm.ID), bootOrder)
	if err != nil {
		return fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
	}

	if !valid {
		return fmt.Errorf("boot_order_index_already_in_use: %d", bootOrder)
	}

	if req.StorageType != libvirtServiceInterfaces.StorageTypeDiskImage {
		err = s.CreateStorageParent(vm.RID, req.Pool, ctx)
		if err != nil {
			return fmt.Errorf("failed_to_create_storage_parent: %w", err)
		}
	}

	req.BootOrder = &bootOrder

	switch req.AttachType {
	case libvirtServiceInterfaces.StorageAttachTypeImport:
		return s.StorageImport(req, vm, ctx)
	case libvirtServiceInterfaces.StorageAttachTypeNew:
		return s.StorageNew(req, vm, ctx)
	}

	return fmt.Errorf("invalid_storage_attach_type: %s", req.AttachType)
}

func (s *Service) StorageUpdate(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) error {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 {
		return fmt.Errorf("invalid_storage_name")
	}

	var current vmModels.Storage
	if err := s.DB.
		Preload("Dataset").
		First(&current, "id = ?", req.ID).Error; err != nil {
		return fmt.Errorf("failed_to_find_storage_record: %w", err)
	}

	var vm vmModels.VM
	if err := s.DB.First(&vm, "id = ?", current.VMID).Error; err != nil {
		return fmt.Errorf("failed_to_find_vm_record: %w", err)
	}

	off, err := s.IsDomainShutOff(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", vm.RID)
	}

	if req.BootOrder != nil && *req.BootOrder != current.BootOrder {
		var count int64
		if err := s.DB.
			Model(&vmModels.Storage{}).
			Where("vm_id = ? AND boot_order = ? AND id != ?", current.VMID, *req.BootOrder, current.ID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
		}

		if count > 0 {
			return fmt.Errorf("boot_order_index_already_in_use: %d", *req.BootOrder)
		}

		current.BootOrder = *req.BootOrder
	}

	if req.Size != 0 && req.Size != current.Size {
		if req.Size < current.Size {
			return fmt.Errorf("shrinking_storage_not_supported")
		}

		growBy := req.Size - current.Size

		if current.Pool != "" && growBy > 0 {
			pool, err := s.GZFS.Zpool.Get(ctx, current.Pool)
			if err != nil || pool == nil {
				return err
			}

			if pool.Free < uint64(growBy) {
				return fmt.Errorf("insufficient_space_in_pool: %s", current.Pool)
			}
		}

		switch current.Type {
		case vmModels.VMStorageTypeRaw:
			imagePath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				current.Pool,
				vm.RID,
				current.ID,
				current.ID,
			)

			if err := utils.CreateOrResizeFile(imagePath, req.Size); err != nil {
				return fmt.Errorf("failed_to_resize_raw_image_file: %w", err)
			}

		case vmModels.VMStorageTypeZVol:
			dsList, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, current.Dataset.Name)
			if err != nil {
				return fmt.Errorf("failed_to_get_zvol_dataset: %w", err)
			}

			if len(dsList) == 0 {
				return fmt.Errorf("zvol_dataset_not_found: %s", current.Dataset.Name)
			}

			ds := dsList[0]
			var volSize uint64

			volSizeProp, ok := ds.Properties["volsize"]
			if ok {
				volSize = gzfs.ParseSize(volSizeProp.Value)
			}

			volSize = gzfs.ParseSize(volSizeProp.Value)
			newSize := uint64(req.Size)

			if newSize < volSize {
				return fmt.Errorf("new_size_must_be_greater_than_or_equal_to_current_volsize")
			}

			err = ds.SetProperties(ctx, "volsize", fmt.Sprintf("%d", newSize))
			if err != nil {
				return fmt.Errorf("failed_to_set_zvol_volsize: %w", err)
			}

		case vmModels.VMStorageTypeDiskImage:
			return fmt.Errorf("size_edit_not_supported_for_disk_image_storage")
		default:
			return fmt.Errorf("size_edit_not_supported_for_storage_type: %s", current.Type)
		}

		current.Size = req.Size
	}

	current.Name = req.Name
	current.Emulation = vmModels.VMStorageEmulationType(req.Emulation)

	if err := s.DB.Save(&current).Error; err != nil {
		return fmt.Errorf("failed_to_update_storage_record: %w", err)
	}

	if err := s.SyncVMDisks(vm.RID); err != nil {
		return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
	}

	return nil
}

func (s *Service) CreateStorageParent(rid uint, poolName string, ctx context.Context) error {
	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var created []*gzfs.Dataset

	for _, pool := range pools {
		if poolName != "" && pool.Name != poolName {
			continue
		}

		target := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool.Name, rid)
		datasets, _ := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeFilesystem,
			false,
			target,
		)

		if len(datasets) > 0 {
			continue
		}

		props := map[string]string{
			"compression":    "zstd",
			"logbias":        "throughput",
			"primarycache":   "metadata",
			"secondarycache": "all",
		}

		ds, err := s.GZFS.ZFS.CreateFilesystem(ctx, target, props)
		if err != nil {
			for _, createdDS := range created {
				_ = createdDS.Destroy(ctx, true, false)
			}

			return fmt.Errorf("failed_to_create_%s: %w", target, err)
		}

		created = append(created, ds)
	}

	return nil
}
