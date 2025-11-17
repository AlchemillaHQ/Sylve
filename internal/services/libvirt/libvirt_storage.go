// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"
	"github.com/beevik/etree"
)

func (s *Service) CreateVMDisk(vmId int, storage vmModels.Storage) error {
	usable, err := s.System.GetUsablePools()
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var target *zfs.Zpool

	for _, pool := range usable {
		if pool.Name == storage.Pool {
			target = pool
			break
		}
	}

	if target.Free < uint64(storage.Size) {
		return fmt.Errorf("insufficient_space_in_pool: %s", storage.Pool)
	}

	if target == nil {
		return fmt.Errorf("pool_not_found: %s", storage.Pool)
	}

	var datasets []*zfs.Dataset

	if storage.Type == vmModels.VMStorageTypeRaw || storage.Type == vmModels.VMStorageTypeZVol {
		if storage.Type == vmModels.VMStorageTypeRaw {
			datasets, err = zfs.Filesystems(fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, vmId, storage.ID))
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			datasets, err = zfs.Volumes(fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, vmId, storage.ID))
		}

		if err != nil {
			if !strings.Contains(err.Error(), "dataset does not exist") {
				return fmt.Errorf("failed_to_get_datasets: %w", err)
			}
		}
	}

	var dataset *zfs.Dataset

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

		if storage.Type == vmModels.VMStorageTypeRaw {
			dataset, err = zfs.CreateFilesystem(
				fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, vmId, storage.ID),
				utils.MergeMaps(props, map[string]string{
					"recordsize": recordSize,
				}),
			)
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			dataset, err = zfs.CreateVolume(
				fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, vmId, storage.ID),
				uint64(storage.Size),
				utils.MergeMaps(props, map[string]string{
					"volblocksize": volblocksize,
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
			_ = dataset.Destroy(zfs.DestroyRecursive)
			return fmt.Errorf("failed_to_create_or_truncate_image_file: %w", err)
		}
	}

	storageDataset := vmModels.VMStorageDataset{
		Pool: target.Name,
		Name: dataset.Name,
		GUID: dataset.GUID,
		VMID: uint(vmId),
	}

	if err := s.DB.Create(&storageDataset).Error; err != nil {
		_ = dataset.Destroy(zfs.DestroyRecursive)
		return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
	}

	storage.DatasetID = &storageDataset.ID

	if err := s.DB.Save(&storage).Error; err != nil {
		_ = dataset.Destroy(zfs.DestroyRecursive)
		return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
	}

	return nil
}

func (s *Service) RemoveLibvirtDisks(vmId int) error {
	return nil
}

func (s *Service) SyncVMDisks(vmId int) error {
	off, err := s.IsDomainShutOff(vmId)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", vmId)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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

	vm, err := s.GetVMByVmId(vmId)
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
			diskValue = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				storage.Pool,
				vmId,
				storage.ID,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			diskValue = fmt.Sprintf("/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d",
				storage.Pool,
				vmId,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeDiskImage {
			diskValue, err = s.FindISOByUUID(storage.DownloadUUID, true)
			if err != nil {
				return fmt.Errorf("failed_to_get_iso_path_by_uuid: %w", err)
			}
		}

		argValue = fmt.Sprintf("%s,%s", argCommon, diskValue)
		argValues = append(argValues, argValue)
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

func (s *Service) RemoveStorageXML(vmId int, storage vmModels.Storage) error {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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
			vmId,
			storage.ID,
			storage.ID,
		)
	} else if storage.Type == vmModels.VMStorageTypeZVol {
		filePath = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
			storage.Pool,
			vmId,
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
	off, err := s.IsDomainShutOff(req.VMID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.VMID)
	}

	vm, err := s.GetVMByVmId(req.VMID)
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

	if err := s.RemoveStorageXML(req.VMID, storage); err != nil {
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

func (s *Service) StorageImport(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM) error {
	var storage vmModels.Storage

	storage.Name = req.Name
	storage.VMID = vm.ID
	storage.Pool = req.Pool
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

		if err := s.CreateVMDisk(vm.VmID, storage); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		datasetPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.VmID,
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
		datasets, err := zfs.Volumes(req.Pool)
		if err != nil {
			return fmt.Errorf("failed_to_get_zvols_in_pool: %w", err)
		}

		var found *zfs.Dataset
		for _, ds := range datasets {
			if ds.GUID == req.Dataset {
				found = ds
				break
			}
		}

		if found == nil {
			return fmt.Errorf("zvol_dataset_not_found_in_pool: %s", req.Dataset)
		}

		var storage vmModels.Storage
		storage.Type = vmModels.VMStorageTypeZVol
		storage.Size = int64(found.Volsize)

		if err := s.DB.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		if err := s.CreateVMDisk(vm.VmID, storage); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		snapshotName := fmt.Sprintf("import-snap-%d-%d", vm.VmID, storage.ID)
		snapshot, err := found.Snapshot(snapshotName, false)
		if err != nil {
			return fmt.Errorf("failed_to_create_snapshot_of_imported_zvol: %w", err)
		}

		targetDatasetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
			req.Pool,
			vm.VmID,
			storage.ID,
		)

		targetDatasets, err := zfs.Volumes(targetDatasetPath)
		if err != nil {
			return fmt.Errorf("failed_to_get_target_zvols: %w", err)
		}

		if len(targetDatasets) == 0 {
			return fmt.Errorf("target_zvol_dataset_not_found: %s", targetDatasetPath)
		}

		targetDataset := targetDatasets[0]
		err = snapshot.SendSnapshotToDataset(targetDataset, true)
		if err != nil {
			return fmt.Errorf("failed_to_send_snapshot_to_dataset: %w", err)
		}

		if err := snapshot.Destroy(zfs.DestroyRecursive); err != nil {
			logger.L.Warn().Err(err).Msg("failed_to_destroy_import_snapshot")
		}
	}

	return s.SyncVMDisks(vm.VmID)
}

func (s *Service) StorageNew(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM) error {
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

		if err := s.CreateVMDisk(vm.VmID, storage); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		diskPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.VmID,
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

		if err := s.CreateVMDisk(vm.VmID, storage); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
	}

	return s.SyncVMDisks(vm.VmID)
}

func (s *Service) StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest) error {
	if req.Name == "" ||
		strings.TrimSpace(req.Name) == "" ||
		len(req.Name) == 0 ||
		len(req.Name) > 128 {
		return fmt.Errorf("invalid_storage_name")
	}

	vm, err := s.GetVMByVmId(req.VMID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	off, err := s.IsDomainShutOff(req.VMID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.VMID)
	}

	var bootOrder int
	if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	} else {
		nextIndex, err := s.GetNextBootOrderIndex(vm.VmID)
		if err != nil {
			return fmt.Errorf("failed_to_get_next_boot_order_index: %w", err)
		}
		bootOrder = nextIndex
	}

	valid, err := s.ValidateBootOrderIndex(vm.VmID, bootOrder)
	if err != nil {
		return fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
	}

	if !valid {
		return fmt.Errorf("boot_order_index_already_in_use: %d", bootOrder)
	}

	err = s.CreateStorageParent(vm.VmID, req.Pool)
	if err != nil {
		return fmt.Errorf("failed_to_create_storage_parent: %w", err)
	}

	req.BootOrder = &bootOrder

	if req.AttachType == libvirtServiceInterfaces.StorageAttachTypeImport {
		return s.StorageImport(req, vm)
	} else if req.AttachType == libvirtServiceInterfaces.StorageAttachTypeNew {
		return s.StorageNew(req, vm)
	}

	return fmt.Errorf("invalid_storage_attach_type: %s", req.AttachType)
}

func (s *Service) CreateStorageParent(vmId int, poolName string) error {
	pools, err := s.System.GetUsablePools()
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var created []*zfs.Dataset

	for _, pool := range pools {
		if poolName != "" && pool.Name != poolName {
			continue
		}

		target := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool.Name, vmId)
		datasets, _ := zfs.Filesystems(target)

		if len(datasets) > 0 {
			continue
		}

		props := map[string]string{
			"compression":    "zstd",
			"logbias":        "throughput",
			"primarycache":   "metadata",
			"secondarycache": "all",
		}

		ds, err := zfs.CreateFilesystem(target, props)
		if err != nil {
			for _, createdDS := range created {
				_ = createdDS.Destroy(zfs.DestroyRecursive)
			}

			return fmt.Errorf("failed_to_create_%s: %w", target, err)
		}

		created = append(created, ds)
	}

	return nil
}
