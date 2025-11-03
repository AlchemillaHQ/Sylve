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
	"os"
	"path/filepath"
	"strconv"

	"github.com/alchemillahq/sylve/internal/db/models"
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

	if storage.Type == vmModels.VMStorageTypeDiskImage {
		datasets, err = zfs.Filesystems(fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, vmId, storage.ID))
		if err != nil {
			return fmt.Errorf("failed_to_get_datasets: %w", err)
		}
	} else if storage.Type == vmModels.VMStorageTypeZVol {
		datasets, err = zfs.Volumes(fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, vmId, storage.ID))
		if err != nil {
			return fmt.Errorf("failed_to_get_datasets: %w", err)
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

		if storage.Type == vmModels.VMStorageTypeDiskImage {
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

	imagePath := filepath.Join(dataset.Mountpoint, fmt.Sprintf("%d.img", storage.ID))
	if _, err := os.Stat(imagePath); err == nil {
		logger.L.Info().Msgf("Disk image %s already exists, skipping creation", imagePath)
		return nil
	}

	if err := utils.CreateOrTruncateFile(imagePath, storage.Size); err != nil {
		_ = dataset.Destroy(zfs.DestroyRecursive)
		return fmt.Errorf("failed_to_create_or_truncate_image_file: %w", err)
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

	index, err := findLowestIndex(xml)
	if err != nil {
		return fmt.Errorf("failed_to_find_lowest_index: %w", err)
	}

	argValues := []string{}

	for _, storage := range storages {
		argCommon := fmt.Sprintf("-s %d:0,%s", index, storage.Emulation)
		var argValue string
		var diskValue string

		if storage.Type == vmModels.VMStorageTypeDiskImage {
			diskValue = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				storage.Pool,
				vmId,
				storage.ID,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			diskValue = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
				storage.Pool,
				vmId,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeInstallationMedia {
			diskValue, err = s.FindISOByUUID(storage.DownloadUUID, true)
			if err != nil {
				return fmt.Errorf("failed_to_get_iso_path_by_uuid: %w", err)
			}
		}

		argValue = fmt.Sprintf("%s,%s", argCommon, diskValue)
		argValues = append(argValues, argValue)

		index++
	}

	for _, val := range argValues {
		argElement := bhyveCommandline.CreateElement("arg")
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

func (s *Service) StorageDetach(vmId int, storageId int) error {
	return nil
}

func (s *Service) StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest) error {
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

	pool, err := s.System.GetValidPool(req.Pool)
	if err != nil {
		return fmt.Errorf("failed_to_validate_pool: %w", err)
	}

	if pool == nil || pool.Name == "" {
		return fmt.Errorf("invalid_pool: %s", req.Pool)
	}

	var recordSize, volBlockSize, bootOrder int
	var size int64

	if req.RecordSize != nil {
		recordSize = *req.RecordSize
	} else {
		recordSize = 0
	}

	if req.VolBlockSize != nil {
		volBlockSize = *req.VolBlockSize
	} else {
		volBlockSize = 0
	}

	if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	} else {
		bootOrder = 0
	}

	if req.Size != nil {
		size = *req.Size
	} else {
		size = 0
	}

	if recordSize == 0 ||
		volBlockSize == 0 ||
		size == 0 {
		return fmt.Errorf("record_size_vol_block_size_and_size_must_be_non_zero")
	}

	vmDirectory := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool.Name, req.VMID)

	if req.StorageType == libvirtServiceInterfaces.StorageTypeISO {
		_, err := s.FindISOByUUID(req.UUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}

		storage := vmModels.Storage{
			Type:         vmModels.VMStorageTypeInstallationMedia,
			DownloadUUID: req.UUID,
			Pool:         pool.Name,
			Size:         size,
			Emulation:    vmModels.VMStorageEmulationType(req.Emulation),
			RecordSize:   int(0),
			VolBlockSize: int(0),
			BootOrder:    bootOrder,
		}

		err = s.DB.Model(&vm).Association("Storages").Append(&storage)
		if err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		if req.Emulation != libvirtServiceInterfaces.VirtIOStorageEmulation &&
			req.Emulation != libvirtServiceInterfaces.AHCIHDStorageEmulation &&
			req.Emulation != libvirtServiceInterfaces.AHCICDStorageEmulation &&
			req.Emulation != libvirtServiceInterfaces.NVMEStorageEmulation {
			return fmt.Errorf("invalid_emulation_type_for_raw_storage: %s", req.Emulation)
		}

		datasets, err := zfs.Filesystems(vmDirectory)
		if err != nil || len(datasets) == 0 {
			return fmt.Errorf("failed_to_get_vm_dataset: %w", err)
		}

		for _, ds := range datasets {
			if ds.Name == vmDirectory {
				var existingStorage vmModels.Storage
				err = s.DB.First(&existingStorage, "boot_order = ? AND vm_id = ?", bootOrder, req.VMID).Error
				if err == nil && existingStorage.ID != 0 {
					return fmt.Errorf("boot_order_already_in_use: %d", bootOrder)
				}

				storage := vmModels.Storage{
					Type:         vmModels.VMStorageTypeDiskImage,
					DownloadUUID: "",
					Pool:         pool.Name,
					Size:         size,
					Emulation:    vmModels.VMStorageEmulationType(req.Emulation),
					RecordSize:   int(recordSize),
					VolBlockSize: int(volBlockSize),
					BootOrder:    bootOrder,
				}

				err = s.DB.Model(&vm).Association("Storages").Append(&storage)
				if err != nil {
					return fmt.Errorf("failed_to_create_storage_record: %w", err)
				}

				dataset, err := zfs.CreateFilesystem(
					fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", pool.Name, req.VMID, storage.ID),
					map[string]string{
						"compression":    "zstd",
						"logbias":        "throughput",
						"primarycache":   "metadata",
						"secondarycache": "all",
						"recordsize":     strconv.Itoa(recordSize),
					},
				)

				if err != nil {
					_ = s.DB.Delete(&storage).Error
					return fmt.Errorf("failed_to_create_raw_storage_dataset: %w", err)
				}

				exists, err := utils.IsFileInDirectory(
					filepath.Join(ds.Mountpoint, fmt.Sprintf("%s.raw", req.Name)),
					ds.Mountpoint)

				if err != nil {
					return fmt.Errorf("failed_to_check_if_file_exists: %w", err)
				}

				if exists {
					err = os.Rename(
						filepath.Join(ds.Mountpoint, fmt.Sprintf("%s.raw", req.Name)),
						filepath.Join(dataset.Mountpoint, fmt.Sprintf("%d.img", storage.ID)),
					)

					if err != nil {
						_ = dataset.Destroy(zfs.DestroyRecursive)
						_ = s.DB.Delete(&storage).Error
						return fmt.Errorf("failed_to_move_existing_raw_image: %w", err)
					}
				} else {
					err = utils.CreateOrTruncateFile(
						filepath.Join(dataset.Mountpoint, fmt.Sprintf("%d.img", storage.ID)),
						size,
					)

					if err != nil {
						_ = dataset.Destroy(zfs.DestroyRecursive)
						_ = s.DB.Delete(&storage).Error
						return fmt.Errorf("failed_to_create_or_truncate_raw_image: %w", err)
					}
				}

				diskDataset := vmModels.VMStorageDataset{
					Pool: pool.Name,
					Name: dataset.Name,
					GUID: dataset.GUID,
					VMID: uint(req.VMID),
				}

				if err := s.DB.Create(&diskDataset).Error; err != nil {
					_ = dataset.Destroy(zfs.DestroyRecursive)
					_ = s.DB.Delete(&storage).Error
					return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
				}

				storage.DatasetID = &diskDataset.ID

				if err := s.DB.Save(&storage).Error; err != nil {
					_ = dataset.Destroy(zfs.DestroyRecursive)
					_ = s.DB.Delete(&storage).Error
					_ = s.DB.Delete(&diskDataset).Error
					return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
				}
			}
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		var existingStorage vmModels.Storage
		err = s.DB.First(&existingStorage, "boot_order = ? AND vm_id = ?", bootOrder, req.VMID).Error
		if err == nil && existingStorage.ID != 0 {
			return fmt.Errorf("boot_order_already_in_use: %d", bootOrder)
		}

		storage := vmModels.Storage{
			Type:         vmModels.VMStorageTypeZVol,
			DownloadUUID: "",
			Pool:         pool.Name,
			Size:         size,
			Emulation:    vmModels.VMStorageEmulationType(req.Emulation),
			RecordSize:   int(recordSize),
			VolBlockSize: int(volBlockSize),
			BootOrder:    bootOrder,
		}

		err = s.DB.Model(&vm).Association("Storages").Append(&storage)
		if err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		dataset, err := zfs.CreateVolume(
			fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", pool.Name, req.VMID, storage.ID),
			uint64(size),
			map[string]string{
				"compression":    "zstd",
				"logbias":        "throughput",
				"primarycache":   "metadata",
				"secondarycache": "all",
				"volblocksize":   strconv.Itoa(volBlockSize),
			},
		)

		if err != nil {
			_ = s.DB.Delete(&storage).Error
			return fmt.Errorf("failed_to_create_zvol_storage_dataset: %w", err)
		}

		zvolDataset := vmModels.VMStorageDataset{
			Pool: pool.Name,
			Name: dataset.Name,
			GUID: dataset.GUID,
			VMID: uint(req.VMID),
		}

		if err := s.DB.Create(&zvolDataset).Error; err != nil {
			_ = dataset.Destroy(zfs.DestroyRecursive)
			_ = s.DB.Delete(&storage).Error
			return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
		}

		storage.DatasetID = &zvolDataset.ID

		if err := s.DB.Save(&storage).Error; err != nil {
			_ = dataset.Destroy(zfs.DestroyRecursive)
			_ = s.DB.Delete(&storage).Error
			_ = s.DB.Delete(&zvolDataset).Error
			return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
		}
	}

	return s.SyncVMDisks(req.VMID)
}

func (s *Service) CreateStorageParent(vmId int) error {
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_find_basic_settings: %w", err)
	}

	var created []*zfs.Dataset

	for _, pool := range basicSettings.Pools {
		if _, err := zfs.GetZpool(pool); err != nil {
			for _, ds := range created {
				_ = ds.Destroy(zfs.DestroyRecursive)
			}
			return fmt.Errorf("pool_not_found_%s: %w", pool, err)
		}

		target := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vmId)
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
