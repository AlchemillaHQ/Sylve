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
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"

	"github.com/klauspost/cpuid/v2"
	"gorm.io/gorm"

	sdb "github.com/alchemillahq/sylve/internal/db"
)

func (s *Service) ListVMs() ([]vmModels.VM, error) {
	var vms []vmModels.VM
	if err := s.DB.Preload("Networks").Preload("Storages").Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_vms: %w", err)
	}

	for i := range vms {
		inactive, err := s.IsDomainInactive(vms[i].VmID)
		if err != nil {
			vms[i].State = "UNKNOWN"
		}

		if inactive {
			vms[i].State = "INACTIVE"
		} else {
			vms[i].State = "ACTIVE"
		}
	}

	return vms, nil
}

func (s *Service) SimpleListVM() ([]libvirtServiceInterfaces.SimpleList, error) {
	var vms []vmModels.VM
	var list []libvirtServiceInterfaces.SimpleList

	if err := s.DB.Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_vms: %w", err)
	}

	for _, vm := range vms {
		inactive, err := s.IsDomainInactive(vm.VmID)
		if err != nil {
			logger.L.Error().Err(err).Msg("ListVMs: failed to check domain state")
			return nil, fmt.Errorf("failed_to_check_domain_state: %w", err)
		}

		var state string

		if inactive {
			state = "INACTIVE"
		} else {
			state = "ACTIVE"
		}

		list = append(list, libvirtServiceInterfaces.SimpleList{
			VMID:  vm.VmID,
			Name:  vm.Name,
			State: state,
		})
	}

	return list, nil
}

func validateCreate(data libvirtServiceInterfaces.CreateVMRequest, db *gorm.DB) error {
	if data.Name == "" || !utils.IsValidVMName(data.Name) {
		return fmt.Errorf("invalid_vm_name")
	}

	if data.VMID == nil || *data.VMID <= 0 || *data.VMID > 9999 {
		return fmt.Errorf("invalid_vm_id")
	}

	if data.Description != "" && (len(data.Description) < 1 || len(data.Description) > 1024) {
		return fmt.Errorf("invalid_description")
	}

	if data.StorageType == "raw" && (data.StorageSize == nil || *data.StorageSize < 1024*1024*128) {
		return fmt.Errorf("disk_size_must_be_greater_than_128mb")
	}

	if (data.StorageType == "raw" || data.StorageType == "zvol") && (data.StorageDataset == "" || len(data.StorageDataset) < 1) {
		noun := "filesystem"
		if data.StorageType == "zvol" {
			noun = "volume"
		}
		return fmt.Errorf("no_%s_selected", noun)
	}

	if data.StorageType != "" && data.StorageEmulationType == "" {
		return fmt.Errorf("no_emulation_type_selected")
	}

	if data.StorageDataset != "" {
		count, err := sdb.Count(db, &vmModels.Storage{}, "dataset = ?", data.StorageDataset)
		if err != nil {
			return fmt.Errorf("failed_to_check_storage_dataset_usage: %w", err)
		}

		if count > 0 {
			if data.StorageType == "zvol" {
				return fmt.Errorf("storage_dataset_zvol_already_in_use")
			} else if data.StorageType == "raw" {
				return fmt.Errorf("storage_dataset_filesystem_already_in_use")
			}
		}

		datasets, err := zfs.Datasets("")

		if err != nil {
			return fmt.Errorf("failed_to_get_dataset: %w", err)
		}

		if datasets == nil {
			return fmt.Errorf("dataset_not_found")
		}

		var dataset *zfs.Dataset

		for _, d := range datasets {
			if d.GUID == data.StorageDataset {
				dataset = d
				break
			}
		}

		if dataset == nil {
			return fmt.Errorf("dataset_not_found")
		}

		if data.StorageType == "raw" && dataset.Type != zfs.DatasetFilesystem {
			return fmt.Errorf("invalid_dataset_type_for_raw_storage")
		}

		if data.StorageType == "zvol" && dataset.Type != zfs.DatasetVolume {
			return fmt.Errorf("invalid_dataset_type_for_zvol_storage")
		}

		if data.StorageType == "raw" {
			if dataset.Mountpoint == "" {
				return fmt.Errorf("raw_storage_dataset_must_have_mountpoint")
			}

			if data.StorageSize == nil {
				return fmt.Errorf("raw_storage_dataset_size_must_be_specified")
			}

			available, err := dataset.GetProperty("available")

			if err != nil {
				return fmt.Errorf("failed_to_get_dataset_properties: %w", err)
			}

			if available == "" {
				return fmt.Errorf("raw_storage_dataset_must_have_available_space")
			}

			avail := utils.HumanFormatToSize(available)

			if err != nil {
				return fmt.Errorf("failed_to_parse_available_space: %w", err)
			}

			if avail < *data.StorageSize {
				return fmt.Errorf("not_enough_space_in_raw_storage_dataset")
			}
		}
	}

	if data.StorageEmulationType != "" {
		if data.StorageEmulationType != "virtio-blk" && data.StorageEmulationType != "ahci-hd" && data.StorageEmulationType != "nvme" {
			return fmt.Errorf("invalid_storage_emulation_type")
		}
	}

	if data.SwitchID != nil && *data.SwitchID != 0 {
		var macId uint
		if data.MacId != nil {
			macId = *data.MacId
		} else {
			macId = 0
		}

		if macId != 0 {
			var macObj networkModels.Object
			if err := db.Preload("Entries").First(&macObj, macId).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return fmt.Errorf("mac_object_not_found: %d", macId)
				}
				return fmt.Errorf("failed_to_find_mac_object: %w", err)
			}

			if macObj.Type != "Mac" {
				return fmt.Errorf("invalid_mac_object_type: %s", macObj.Type)
			}

			if len(macObj.Entries) == 0 {
				return fmt.Errorf("mac_object_has_no_entries: %d", macId)
			}

			var otherNetworks []vmModels.Network
			if err := db.Where("mac_id = ? AND vm_id != ?", macId, data.VMID).
				Find(&otherNetworks).Error; err != nil {
				return fmt.Errorf("failed_to_find_other_networks_using_mac_object: %w", err)
			}

			if len(otherNetworks) > 0 {
				return fmt.Errorf("mac_object_already_in_use: %d", macId)
			}
		}

		if data.SwitchEmulationType == "" {
			return fmt.Errorf("no_switch_emulation_type_selected")
		}
	}

	if data.CPUSockets < 1 {
		return fmt.Errorf("sockets_must_be_greater_than_0")
	}

	if data.CPUCores < 1 {
		return fmt.Errorf("cores_must_be_greater_than_0")
	}

	if data.CPUThreads < 1 {
		return fmt.Errorf("threads_must_be_greater_than_0")
	}

	if data.RAM < 1024*1024*128 {
		return fmt.Errorf("memory_must_be_greater_than_128mb")
	}

	if data.VNCPort < 1 || data.VNCPort > 65535 {
		return fmt.Errorf("vnc_port_must_be_between_1_and_65535")
	} else {
		count, err := sdb.Count(db, &vmModels.VM{}, "vnc_port = ?", data.VNCPort)
		if err != nil {
			return fmt.Errorf("failed_to_check_vnc_port_usage: %w", err)
		}

		if count > 0 {
			return fmt.Errorf("vnc_port_already_in_use")
		} else {
			if utils.IsPortInUse(data.VNCPort) {
				return fmt.Errorf("vnc_port_already_in_use_by_another_service")
			}
		}
	}

	if data.VNCPassword != "" && len(data.VNCPassword) < 1 {
		return fmt.Errorf("vnc_password_required")
	}

	if data.VNCResolution == "" {
		return fmt.Errorf("no_vnc_resolution_selected")
	}

	if data.StartOrder < 0 {
		return fmt.Errorf("start_order_must_be_greater_than_or_equal_to_0")
	}

	if len(data.PCIDevices) > 0 {
		for _, pciID := range data.PCIDevices {
			count, err := sdb.Count(db, &models.PassedThroughIDs{}, "id = ?", pciID)
			if err != nil {
				return fmt.Errorf("passthrough_device_does_not_exist: %w", err)
			}
			if count == 0 {
				return fmt.Errorf("pci_device_not_found: %d", pciID)
			}

			var vms []vmModels.VM
			if err := db.Find(&vms).Error; err != nil {
				return fmt.Errorf("failed_to_fetch_vms")
			}

			for _, vm := range vms {
				for _, deviceId := range vm.PCIDevices {
					if deviceId == pciID {
						return fmt.Errorf("pci_device_already_in_use: %d", pciID)
					}
				}
			}
		}
	}

	if len(data.CPUPinning) > 0 {
		vcpu := data.CPUSockets * data.CPUCores * data.CPUThreads
		if len(data.CPUPinning) > vcpu {
			return fmt.Errorf("cpu_pinning_exceeds_total_vcpus: %d", vcpu)
		}

		if len(data.CPUPinning) > cpuid.CPU.LogicalCores {
			return fmt.Errorf("cpu_pinning_exceeds_logical_cores: %d", cpuid.CPU.LogicalCores)
		}

		var vms []vmModels.VM
		if err := db.Find(&vms).Error; err != nil {
			return fmt.Errorf("failed_to_fetch_vms: %w", err)
		}

		for _, vm := range vms {
			for _, cPin := range vm.CPUPinning {
				for _, nPin := range data.CPUPinning {
					if cPin == nPin {
						return fmt.Errorf("vcpu_already_pinned: %d", nPin)
					}
				}
			}
		}
	}

	if data.ISO != "" && strings.ToLower(data.ISO) != "none" {
		count, err := sdb.Count(db, &utilitiesModels.Downloads{}, "uuid = ?", data.ISO)

		if err != nil {
			return fmt.Errorf("failed_to_check_iso_usage: %w", err)
		}

		if count == 0 {
			return fmt.Errorf("iso_not_found: %s", data.ISO)
		}
	}

	return nil
}

func (s *Service) CreateVM(data libvirtServiceInterfaces.CreateVMRequest) error {
	if err := validateCreate(data, s.DB); err != nil {
		logger.L.Debug().Err(err).Msg("create_vm: validation failed")
		return err
	}

	vncWait := false
	startAtBoot := false
	tpmEmulation := false

	if data.VNCWait != nil {
		vncWait = *data.VNCWait
	} else {
		vncWait = true
	}

	if data.StartAtBoot == nil {
		startAtBoot = true
	} else {
		startAtBoot = *data.StartAtBoot
	}

	if data.TPMEmulation != nil {
		tpmEmulation = *data.TPMEmulation
	} else {
		tpmEmulation = false
	}

	var macId uint
	if data.MacId != nil {
		macId = *data.MacId
	} else {
		macId = 0
	}

	var networks []vmModels.Network
	if data.SwitchID != nil && *data.SwitchID != 0 {
		var sw networkModels.StandardSwitch
		if err := s.DB.Where("id = ?", *data.SwitchID).First(&sw).Error; err != nil {
			return fmt.Errorf("failed_to_find_switch: %w", err)
		}

		if macId == 0 {
			base := fmt.Sprintf("%s-%s", data.Name, sw.Name)
			name := base

			for i := 0; ; i++ {
				if i > 0 {
					name = fmt.Sprintf("%s-%d", base, i)
				}
				var exists int64
				if err := s.DB.
					Model(&networkModels.Object{}).
					Where("name = ?", name).
					Limit(1).
					Count(&exists).Error; err != nil {
					return fmt.Errorf("failed_to_check_mac_object_exists: %w", err)
				}
				if exists == 0 {
					break
				}
			}

			macAddress := utils.GenerateRandomMAC()
			macObj := networkModels.Object{
				Type: "Mac",
				Name: name,
			}

			if err := s.DB.Create(&macObj).Error; err != nil {
				return fmt.Errorf("failed_to_create_mac_object: %w", err)
			}

			macEntry := networkModels.ObjectEntry{
				ObjectID: macObj.ID,
				Value:    macAddress,
			}

			if err := s.DB.Create(&macEntry).Error; err != nil {
				return fmt.Errorf("failed_to_create_mac_entry: %w", err)
			}

			macId = macObj.ID
		}

		networks = append(networks, vmModels.Network{
			MacID:     &macId,
			SwitchID:  uint(*data.SwitchID),
			Emulation: data.SwitchEmulationType,
		})
	}

	var storages []vmModels.Storage
	if data.StorageType != "" {
		if data.StorageSize != nil && data.StorageType == "zvol" {
			*data.StorageSize = 0
		}

		var name string

		if data.StorageType == "raw" {
			name = fmt.Sprintf("%d", *data.VMID)
		}

		storages = append(storages, vmModels.Storage{
			Name:      name,
			Type:      data.StorageType,
			Dataset:   data.StorageDataset,
			Size:      int64(*data.StorageSize),
			Emulation: data.StorageEmulationType,
		})
	}

	if strings.ToLower(data.ISO) == "none" {
		data.ISO = ""
	}

	if data.ISO != "" {
		storages = append(storages, vmModels.Storage{
			Type:      "iso",
			Dataset:   data.ISO,
			Size:      0,
			Emulation: "ahci-cd",
		})
	}

	vm := &vmModels.VM{
		Name:          data.Name,
		VmID:          *data.VMID,
		Description:   data.Description,
		CPUSockets:    data.CPUSockets,
		CPUCores:      data.CPUCores,
		CPUsThreads:   data.CPUThreads,
		CPUPinning:    data.CPUPinning,
		RAM:           data.RAM,
		VNCPort:       data.VNCPort,
		VNCPassword:   data.VNCPassword,
		VNCResolution: data.VNCResolution,
		VNCWait:       vncWait,
		StartAtBoot:   startAtBoot,
		TPMEmulation:  tpmEmulation,
		StartOrder:    data.StartOrder,
		PCIDevices:    data.PCIDevices,
		ISO:           data.ISO,
		Storages:      storages,
		Networks:      networks,
	}

	if err := s.DB.
		Session(&gorm.Session{FullSaveAssociations: true}).
		Create(vm).Error; err != nil {
		logger.L.Debug().Err(err).Msg("create_vm: failed to create vm with associations")
		return fmt.Errorf("failed_to_create_vm_with_associations: %w", err)
	}

	if err := s.CreateLvVm(int(vm.ID)); err != nil {
		if err := s.DB.Delete(vm).Error; err != nil {
			logger.L.Debug().Err(err).Msg("create_vm: failed to delete vm after creation failure")
			return fmt.Errorf("failed_to_delete_vm_after_creation_failure: %w", err)
		}

		for _, storage := range storages {
			if err := s.DB.Delete(&storage).Error; err != nil {
				logger.L.Debug().Err(err).Msg("create_vm: failed to delete storage after creation failure")
				return fmt.Errorf("failed_to_delete_storage_after_vm_creation_failure: %w", err)
			}
		}

		for _, network := range networks {
			if err := s.DB.Delete(&network).Error; err != nil {
				logger.L.Debug().Err(err).Msg("create_vm: failed to delete network after creation failure")
				return fmt.Errorf("failed_to_delete_network_after_vm_creation_failure: %w", err)
			}
		}

		logger.L.Debug().Err(err).Msg("create_vm: failed to create lv vm")
		return fmt.Errorf("failed_to_create_lv_vm: %w", err)
	}

	return nil
}

func (s *Service) RemoveVM(id uint, cleanUpMacs bool) error {
	var vm vmModels.VM
	if err := s.DB.Preload("Stats").Preload("Networks").Preload("Storages").First(&vm, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("vm_not_found: %d", id)
		}
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	filesystems, err := zfs.Filesystems("")

	if err != nil {
		return fmt.Errorf("failed_to_get_filesystems: %w", err)
	}

	for _, storage := range vm.Storages {
		if storage.Type == "raw" {
			var dataset *zfs.Dataset
			for _, fs := range filesystems {
				if fs.GUID == storage.Dataset {
					dataset = fs
					break
				}
			}

			if dataset == nil {
				return fmt.Errorf("dataset_not_found")
			}

			if dataset.Mountpoint == "" {
				return fmt.Errorf("raw_storage_dataset_must_have_mountpoint")
			}

			datasetPath := filepath.Join(dataset.Mountpoint)
			err := utils.RemoveDirContents(datasetPath)

			if err != nil {
				return fmt.Errorf("failed_to_remove_raw_storage_files: %w", err)
			}
		}

		if err := s.DB.Delete(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_delete_storage: %w", err)
		}
	}

	err = s.RemoveLvVm(int(vm.VmID))
	if err != nil {
		return fmt.Errorf("failed_to_remove_lv_vm: %w", err)
	}

	var usedMACS []uint

	for _, network := range vm.Networks {
		if network.MacID != nil {
			usedMACS = append(usedMACS, *network.MacID)
		}

		if err := s.DB.Delete(&network).Error; err != nil {
			return fmt.Errorf("failed_to_delete_network: %w", err)
		}
	}

	for _, stat := range vm.Stats {
		if err := s.DB.Delete(&stat).Error; err != nil {
			return fmt.Errorf("failed_to_delete_vm_stat: %w", err)
		}
	}

	if err := s.DB.Delete(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_delete_vm: %w", err)
	}

	if cleanUpMacs {
		tx := s.DB.Begin()

		if len(usedMACS) > 0 {
			if err := tx.Where("object_id IN ?", usedMACS).
				Delete(&networkModels.ObjectEntry{}).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_object_entries: %w", err)
			}

			if err := tx.Where("object_id IN ?", usedMACS).
				Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_object_resolutions: %w", err)
			}

			if err := tx.Delete(&networkModels.Object{}, usedMACS).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("failed_to_delete_objects: %w", err)
			}

			if err := tx.Commit().Error; err != nil {
				return fmt.Errorf("failed_to_commit_cleanup: %w", err)
			}
		}
	}

	return nil
}

func (s *Service) PerformAction(id uint, action string) error {
	var vm vmModels.VM

	if err := s.DB.First(&vm, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("vm_not_found: %d", id)
		}
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	err := s.LvVMAction(vm, action)
	if err != nil {
		return fmt.Errorf("failed_to_perform_action: %w", err)
	}

	return nil
}

func (s *Service) UpdateDescription(id uint, description string) error {
	var vm vmModels.VM
	if err := s.DB.First(&vm, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("vm_not_found: %d", id)
		}
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	if len(description) < 1 || len(description) > 1024 {
		return fmt.Errorf("invalid_description")
	}

	vm.Description = description

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_description: %w", err)
	}

	return nil
}
