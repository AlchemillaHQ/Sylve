// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"
	"github.com/digitalocean/go-libvirt"
	"github.com/klauspost/cpuid/v2"

	"gorm.io/gorm"
)

func (s *Service) ListVMs() ([]vmModels.VM, error) {
	var vms []vmModels.VM
	if err := s.DB.
		Preload("CPUPinning").
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("Networks.AddressObj").
		Preload("Networks.AddressObj.Entries").
		Preload("Networks.AddressObj.Resolutions").
		Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_vms: %w", err)
	}

	states, err := s.GetDomainStates()
	if err != nil {
		logger.L.Err(err).Msg("Error fetching domain states")
	}

	for _, vm := range vms {
		idx := slices.IndexFunc(states, func(s libvirtServiceInterfaces.DomainState) bool {
			return s.Domain == utils.IntToString(vm.VmID)
		})

		if idx == -1 {
			vm.State = 0
			continue
		}

		vm.State = states[idx].State
	}

	return vms, nil
}

func (s *Service) SimpleListVM() ([]libvirtServiceInterfaces.SimpleList, error) {
	type vmRow struct {
		ID   uint
		Name string
		VmID int
	}

	var vms []vmRow
	if err := s.DB.
		Model(&vmModels.VM{}).
		Select("id", "name", "vm_id").
		Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_vms: %w", err)
	}

	states, err := s.GetDomainStates()
	if err != nil {
		logger.L.Err(err).Msg("Error fetching domain states")
	}

	stateByVMID := make(map[string]libvirt.DomainState, len(states))
	for _, st := range states {
		stateByVMID[st.Domain] = st.State
	}

	list := make([]libvirtServiceInterfaces.SimpleList, 0, len(vms))
	const unknownState = 0

	for _, vm := range vms {
		vmidStr := utils.IntToString(vm.VmID)
		state, ok := stateByVMID[vmidStr]
		if !ok {
			state = unknownState
		}

		list = append(list, libvirtServiceInterfaces.SimpleList{
			ID:    vm.ID,
			Name:  vm.Name,
			VMID:  vm.VmID,
			State: state,
		})
	}

	return list, nil
}

func validateCPUPins(
	db *gorm.DB,
	req libvirtServiceInterfaces.CreateVMRequest,
	hostLogicalCores int,
	hostSocketCount int,
	hostLogicalPerSocket int,
) error {
	// 0) No pins => nothing to validate
	if len(req.CPUPinning) == 0 {
		return nil
	}

	// 1) Sanity checks
	if hostSocketCount <= 0 {
		return fmt.Errorf("invalid_host_socket_count")
	}

	if hostLogicalCores <= 0 {
		return fmt.Errorf("invalid_host_logical_cores")
	}

	vcpu := req.CPUSockets * req.CPUCores * req.CPUThreads
	if vcpu <= 0 {
		return fmt.Errorf("invalid_topology_vcpu_is_zero")
	}

	// 2) Socket index validation (now strict)
	seenSockets := make(map[int]struct{}, len(req.CPUPinning))
	for i, pin := range req.CPUPinning {
		if pin.Socket < 0 || pin.Socket >= hostSocketCount {
			return fmt.Errorf("socket_index_out_of_range: socket=%d max=%d", pin.Socket, hostSocketCount-1)
		}
		if _, dup := seenSockets[pin.Socket]; dup {
			return fmt.Errorf("duplicate_socket_in_request: socket=%d index=%d", pin.Socket, i)
		}
		seenSockets[pin.Socket] = struct{}{}
		if len(pin.Cores) == 0 {
			return fmt.Errorf("empty_core_list_for_socket: socket=%d", pin.Socket)
		}
	}

	// 3) Core range + duplicates + optional (core->socket) strictness
	seenCores := make(map[int]struct{}, 128)
	perSocketCounts := make(map[int]int, hostSocketCount)
	totalPinned := 0

	for _, pin := range req.CPUPinning {
		perSocketSeen := make(map[int]struct{}, len(pin.Cores))
		for j, c := range pin.Cores {
			if c < 0 || c >= hostLogicalCores {
				return fmt.Errorf("core_index_out_of_range: core=%d (max=%d) socket=%d pos=%d",
					c, hostLogicalCores-1, pin.Socket, j)
			}
			if _, dup := perSocketSeen[c]; dup {
				return fmt.Errorf("duplicate_core_within_socket: core=%d socket=%d", c, pin.Socket)
			}
			perSocketSeen[c] = struct{}{}

			if _, dup := seenCores[c]; dup {
				return fmt.Errorf("duplicate_core_across_sockets: core=%d", c)
			}
			seenCores[c] = struct{}{}
		}
		perSocketCounts[pin.Socket] += len(pin.Cores)
		totalPinned += len(pin.Cores)
	}

	// 4) Totals vs capacity
	if totalPinned > vcpu {
		return fmt.Errorf("cpu_pinning_exceeds_total_vcpus: pinned=%d vcpu=%d", totalPinned, vcpu)
	}

	if totalPinned > hostLogicalCores {
		return fmt.Errorf("cpu_pinning_exceeds_logical_cores: pinned=%d logical=%d", totalPinned, hostLogicalCores)
	}

	// 5) Optional strict per-socket cap
	if hostLogicalPerSocket > 0 {
		for sock, cnt := range perSocketCounts {
			if cnt > hostLogicalPerSocket {
				return fmt.Errorf("socket_capacity_exceeded: socket=%d pinned=%d cap=%d",
					sock, cnt, hostLogicalPerSocket)
			}
		}
	}

	// 6) Conflict check with other VMs’ pinning
	var vms []vmModels.VM
	if err := db.Preload("CPUPinning").Find(&vms).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vms: %w", err)
	}
	selfID := uint(0)
	if req.VMID != nil && *req.VMID > 0 {
		selfID = uint(*req.VMID)
	}

	occupied := make(map[int]uint, 512) // core -> VMID
	for _, vm := range vms {
		if selfID != 0 && vm.ID == selfID {
			continue
		}
		for _, p := range vm.CPUPinning {
			for _, c := range p.HostCPU {
				occupied[c] = vm.ID
			}
		}
	}

	for c := range seenCores {
		if owner, taken := occupied[c]; taken {
			return fmt.Errorf("core_conflict: core=%d already_pinned_by_vmid=%d", c, owner)
		}
	}

	return nil
}

func (s *Service) validateCreate(data libvirtServiceInterfaces.CreateVMRequest) error {
	if data.Name == "" || !utils.IsValidVMName(data.Name) {
		return fmt.Errorf("invalid_vm_name")
	}

	if data.VMID == nil || *data.VMID <= 0 || *data.VMID > 9999 {
		return fmt.Errorf("invalid_vm_id")
	}

	var count int64
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("(vm_id = ? OR name = ?) AND id != ?", *data.VMID, data.Name, 0).
		Count(&count).Error

	if err != nil {
		return fmt.Errorf("failed_to_count_vms: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("vm_id_or_name_already_in_use")
	}

	if data.Description != "" && (len(data.Description) < 1 || len(data.Description) > 1024) {
		return fmt.Errorf("invalid_description")
	}

	if data.StorageType == "raw" && (data.StorageSize == nil || *data.StorageSize < 1024*1024*128) {
		return fmt.Errorf("disk_size_must_be_greater_than_128mb")
	}

	if (data.StorageType == "raw" || data.StorageType == "zvol") && (data.StoragePool == "") {
		noun := "filesystem"
		if data.StorageType == "zvol" {
			noun = "volume"
		}
		return fmt.Errorf("no_pool_selected_for_%s", noun)
	}

	if data.StorageType != "" && data.StorageEmulationType == "" {
		return fmt.Errorf("no_emulation_type_selected")
	}

	// var basicSettings models.BasicSettings
	// err = db.First(&basicSettings).Error

	if err != nil {
		return fmt.Errorf("unable_to_get_basic_settings")
	}

	if data.StorageType != libvirtServiceInterfaces.StorageTypeNone {
		usable, err := s.System.GetUsablePools()
		if err != nil {
			return fmt.Errorf("failed_to_get_usable_pools: %w", err)
		}

		var pool *zfs.Zpool
		for _, p := range usable {
			if p.Name == data.StoragePool {
				pool = p
				break
			}
		}

		size := uint64(0)
		if data.StorageSize != nil {
			size = *data.StorageSize
		}

		if size < internal.MinimumVMStorageSize {
			return fmt.Errorf("size_should_be_at_least_%d", internal.MinimumVMStorageSize)
		}

		if size > pool.Free {
			return fmt.Errorf("size_greater_than_Available")
		}
	}

	if data.SwitchName != "" && strings.ToLower(data.SwitchName) != "none" {
		var macId uint
		if data.MacId != nil {
			macId = *data.MacId
		} else {
			macId = 0
		}

		if macId != 0 {
			var macObj networkModels.Object
			if err := s.DB.Preload("Entries").First(&macObj, macId).Error; err != nil {
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
			if err := s.DB.Where("mac_id = ? AND vm_id != ?", macId, data.VMID).
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

	if data.CPUSockets < 1 || data.CPUCores < 1 || data.CPUThreads < 1 {
		return fmt.Errorf("cpu_sockets_cores_threads_must_be_greater_than_1")
	}

	if len(data.CPUPinning) > 0 {
		err := validateCPUPins(s.DB,
			data,
			utils.GetLogicalCores(),
			utils.GetSocketCount(cpuid.CPU.PhysicalCores,
				cpuid.CPU.ThreadsPerCore), 0)
		if err != nil {
			return err
		}
	}

	if data.RAM < 1024*1024*128 {
		return fmt.Errorf("memory_must_be_greater_than_128mb")
	}

	if data.VNCPort < 1 || data.VNCPort > 65535 {
		return fmt.Errorf("vnc_port_must_be_between_1_and_65535")
	} else {
		var exists bool
		err := s.DB.Model(&vmModels.VM{}).
			Select("1").
			Where("vnc_port = ?", data.VNCPort).
			Limit(1).
			Find(&exists).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			if utils.IsPortInUse(data.VNCPort) {
				return fmt.Errorf("vnc_port_already_in_use_by_another_service")
			}
		} else if err != nil {
			return fmt.Errorf("failed_to_check_vnc_port_usage: %w", err)
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
			var count int64
			err := s.DB.
				Model(&models.PassedThroughIDs{}).
				Where("id = ?", pciID).
				Count(&count).Error

			if err != nil {
				return fmt.Errorf("failed_to_check_passthrough_device: %w", err)
			}

			if count == 0 {
				return fmt.Errorf("passthrough_device_does_not_exist")
			}

			/*
				We don't validate if it's being used by another VM, multiple VMs can share a PCI device,
				they just cannot share it at the same time. Do the check at start of the VM, not here.
			*/
		}
	}

	if data.ISO != "" {
		var exists bool
		err := s.DB.Model(&utilitiesModels.Downloads{}).
			Select("1").
			Where("uuid = ?", data.ISO).
			Limit(1).
			Find(&exists).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("image_not_found: %s", data.ISO)
		}

		if err != nil {
			return fmt.Errorf("failed_to_check_iso_usage: %w", err)
		}
	}

	return nil
}

func (s *Service) GetVM(id int) (vmModels.VM, error) {
	var vm vmModels.VM
	err := s.DB.
		Preload("CPUPinning").
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("Networks.AddressObj").
		Where("id = ?", id).
		First(&vm).Error

	return vm, err
}

func (s *Service) GetVMByVmId(id int) (vmModels.VM, error) {
	var vm vmModels.VM
	err := s.DB.
		Preload("CPUPinning").
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("Networks.AddressObj").
		Where("vm_id = ?", id).
		First(&vm).Error

	return vm, err
}

func (s *Service) CreateVM(data libvirtServiceInterfaces.CreateVMRequest) error {
	if err := s.validateCreate(data); err != nil {
		logger.L.Debug().Err(err).Msg("CreateVM: validation failed")
		return err
	}

	vncWait := false
	startAtBoot := false
	tpmEmulation := false
	serial := false
	apic := true
	acpi := true

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

	if data.Serial != nil {
		serial = *data.Serial
	} else {
		serial = false
	}

	var macId uint
	if data.MacId != nil {
		macId = *data.MacId
	} else {
		macId = 0
	}

	if data.APIC != nil {
		apic = *data.APIC
	}

	if data.ACPI != nil {
		acpi = *data.ACPI
	}

	var networks []vmModels.Network
	if data.SwitchName != "" && strings.ToLower(data.SwitchName) != "none" {
		swType := ""

		var stdSwitch networkModels.StandardSwitch
		if err := s.DB.First(&stdSwitch, "name = ?", data.SwitchName).Error; err == nil {
			swType = "standard"
		}

		var manualSwitch networkModels.ManualSwitch
		if err := s.DB.First(&manualSwitch, "name = ?", data.SwitchName).Error; err == nil {
			swType = "manual"
		}

		if swType == "" {
			return fmt.Errorf("switch_not_found: %s", data.SwitchName)
		}

		var sw any

		switch swType {
		case "standard":
			sw = stdSwitch
		case "manual":
			sw = manualSwitch
		default:
			return fmt.Errorf("unknown_switch_type: %s", swType)
		}

		if macId == 0 {
			var base string

			switch v := sw.(type) {
			case networkModels.StandardSwitch:
				base = fmt.Sprintf("%s-%s", data.Name, v.Name)
			case networkModels.ManualSwitch:
				base = fmt.Sprintf("%s-%s", data.Name, v.Name)
			default:
				return fmt.Errorf("invalid switch type %T", v)
			}

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

		var switchId uint

		switch v := sw.(type) {
		case networkModels.StandardSwitch:
			switchId = v.ID
		case networkModels.ManualSwitch:
			switchId = v.ID
		default:
			return fmt.Errorf("invalid switch type %T", v)
		}

		networks = append(networks, vmModels.Network{
			MacID:      &macId,
			SwitchID:   switchId,
			SwitchType: swType,
			Emulation:  data.SwitchEmulationType,
		})
	}

	var storages []vmModels.Storage
	if data.StorageType != libvirtServiceInterfaces.StorageTypeNone {
		storages = append(storages, vmModels.Storage{
			Pool:      data.StoragePool,
			Type:      vmModels.VMStorageType(data.StorageType),
			Size:      int64(*data.StorageSize),
			Emulation: vmModels.VMStorageEmulationType(data.StorageEmulationType),
			BootOrder: 1,
		})
	}

	if data.ISO != "" && strings.ToLower(data.ISO) != "none" {
		storages = append(storages, vmModels.Storage{
			DownloadUUID: data.ISO,
			Type:         vmModels.VMStorageTypeInstallationMedia,
			Size:         0,
			Emulation:    "ahci-cd",
		})
	}

	vm := &vmModels.VM{
		Name:          data.Name,
		VmID:          *data.VMID,
		Description:   data.Description,
		CPUSockets:    data.CPUSockets,
		CPUCores:      data.CPUCores,
		CPUThreads:    data.CPUThreads,
		RAM:           data.RAM,
		Serial:        serial,
		VNCPort:       data.VNCPort,
		VNCPassword:   data.VNCPassword,
		VNCResolution: data.VNCResolution,
		VNCWait:       vncWait,
		StartAtBoot:   startAtBoot,
		TPMEmulation:  tpmEmulation,
		StartOrder:    data.StartOrder,
		PCIDevices:    data.PCIDevices,
		APIC:          apic,
		ACPI:          acpi,
		Storages:      storages,
		Networks:      networks,
		TimeOffset:    vmModels.TimeOffset(data.TimeOffset),
	}

	vm.CPUPinning = []vmModels.VMCPUPinning{}

	for _, p := range data.CPUPinning {
		vm.CPUPinning = append(vm.CPUPinning, vmModels.VMCPUPinning{
			HostSocket: p.Socket,
			HostCPU:    p.Cores,
		})
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

func (s *Service) RemoveVM(id uint, cleanUpMacs bool, deleteRawDisks bool, deleteVolumes bool) error {
	var vm vmModels.VM
	if err := s.DB.
		Preload("Stats").
		Preload("Networks").
		Preload("CPUPinning").
		Preload("Storages").
		Preload("Storages.Dataset").
		First(&vm, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("vm_not_found: %d", id)
		}
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	for _, storage := range vm.Storages {
		var datasets *[]zfs.Dataset
		var cSets []*zfs.Dataset
		var err error

		if storage.Type == vmModels.VMStorageTypeDiskImage {
			if deleteRawDisks {
				cSets, err = zfs.Filesystems(fmt.Sprintf(
					"%s/sylve/virtual-machines/%d/raw-%d",
					storage.Dataset.Pool,
					vm.VmID,
					storage.ID,
				))
			}
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			if deleteVolumes {
				cSets, err = zfs.Volumes(fmt.Sprintf(
					"%s/sylve/virtual-machines/%d/zvol-%d",
					storage.Dataset.Pool,
					vm.VmID,
					storage.ID,
				))
			}
		}

		if err != nil {
			if !strings.Contains(err.Error(), "dataset does not exist") {
				logger.L.Error().Err(err).Msg("RemoveVM: failed to get zfs datasets for storage removal")
			}
		}

		for _, ds := range cSets {
			datasets = &[]zfs.Dataset{}
			*datasets = append(*datasets, *ds)
		}

		if err := s.DB.Delete(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_delete_storage: %w", err)
		}

		if datasets != nil && len(*datasets) > 0 {
			for _, ds := range *datasets {
				err := ds.Destroy(zfs.DestroyRecursive)
				if err != nil {
					logger.L.Error().Err(err).Msgf("RemoveVM: failed to destroy dataset %s", ds.Name)
				}
			}
		}
	}

	err := s.RemoveLvVm(int(vm.VmID))
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

	for _, p := range vm.CPUPinning {
		if err := s.DB.Delete(&p).Error; err != nil {
			return fmt.Errorf("failed_to_delete_cpupinning: %w", err)
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
