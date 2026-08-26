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
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
	"gorm.io/gorm"
)

const vmPCIDevicesColumn = "pc_idevices"

func updateMemory(xml string, ram int) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	memory := doc.FindElement("//memory")
	if memory == nil {
		return "", fmt.Errorf("<memory> tag not found")
	}

	memory.SetText(fmt.Sprintf("%d", ram))
	memory.RemoveAttr("unit")
	memory.CreateAttr("unit", "B")

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}

	return out, nil
}

func removePinArgs(cmd *etree.Element) {
	for _, arg := range append([]*etree.Element{}, cmd.SelectElements("bhyve:arg")...) {
		if v := arg.SelectAttrValue("value", ""); v != "" {
			if strings.HasPrefix(v, "-p ") || strings.Contains(v, " -p ") {
				cmd.RemoveChild(arg)
			}
		}
	}
}

func (s *Service) updateCPU(xml string, cpuSockets, cpuCores, cpuThreads int, cpuPinning []vmModels.VMCPUPinning) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	vcpu := doc.FindElement("//vcpu")
	if vcpu == nil {
		return "", fmt.Errorf("<vcpu> tag not found")
	}

	vcpu.SetText(strconv.Itoa(cpuSockets * cpuCores * cpuThreads))

	cpu := doc.FindElement("//cpu")
	if cpu == nil {
		root := doc.Root()
		if root == nil {
			return "", fmt.Errorf("domain_xml_root_not_found")
		}
		cpu = root.CreateElement("cpu")
	}

	topology := cpu.FindElement("topology")
	if topology == nil {
		topology = cpu.CreateElement("topology")
	}

	topology.CreateAttr("sockets", strconv.Itoa(cpuSockets))
	topology.CreateAttr("cores", strconv.Itoa(cpuCores))
	topology.CreateAttr("threads", strconv.Itoa(cpuThreads))

	if len(cpuPinning) > 0 {
		bhyveCommandline := doc.FindElement("//commandline")
		if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
			root := doc.Root()
			if root.SelectAttr("xmlns:bhyve") == nil {
				root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
			}
			bhyveCommandline = root.CreateElement("bhyve:commandline")
		}

		removePinArgs(bhyveCommandline)

		pinStr := ""
		pinArr := s.GeneratePinArgs(cpuPinning)

		for i, pin := range pinArr {
			if i > 0 {
				pinStr += " "
			}

			pinStr += pin
		}

		if pinStr != "" {
			arg := bhyveCommandline.CreateElement("bhyve:arg")
			arg.CreateAttr("value", pinStr)
		}
	} else {
		if bhyveCommandline := doc.FindElement("//bhyve:commandline"); bhyveCommandline != nil {
			removePinArgs(bhyveCommandline)
			if len(bhyveCommandline.ChildElements()) == 0 {
				bhyveCommandline.Parent().RemoveChild(bhyveCommandline)
			}
		}
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}

	return out, nil
}

func (s *Service) updateRequestedCPUXML(
	xml string,
	req libvirtServiceInterfaces.ModifyCPURequest,
	cpuPinning []vmModels.VMCPUPinning,
) (string, error) {
	return s.updateCPU(xml, req.CPUSockets, req.CPUCores, req.CPUThreads, cpuPinning)
}

func updateVNC(xml string, vncPort int, vncBind string, vncResolution string, vncPassword string, vncWait bool, vncEnabled bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("domain_xml_root_not_found")
	}

	devicesEl := root.FindElement("devices")
	if devicesEl == nil {
		devicesEl = root.CreateElement("devices")
	}

	for _, el := range devicesEl.FindElements("graphics") {
		if el.SelectAttrValue("type", "") == "vnc" {
			devicesEl.RemoveChild(el)
		}
	}
	for _, el := range devicesEl.FindElements("video") {
		devicesEl.RemoveChild(el)
	}

	if bhyveCL := doc.FindElement("//commandline"); bhyveCL != nil && bhyveCL.Space == "bhyve" {
		for _, arg := range bhyveCL.ChildElements() {
			if v := arg.SelectAttrValue("value", ""); v != "" && strings.Contains(v, "fbuf,tcp") {
				bhyveCL.RemoveChild(arg)
			}
		}
	}

	if vncEnabled {
		resolutionParts := strings.Split(vncResolution, "x")
		if len(resolutionParts) != 2 {
			return "", fmt.Errorf("invalid_vnc_resolution_format: %s", vncResolution)
		}

		width, err := strconv.Atoi(resolutionParts[0])
		if err != nil {
			return "", fmt.Errorf("invalid_vnc_resolution_width: %s", resolutionParts[0])
		}

		height, err := strconv.Atoi(resolutionParts[1])
		if err != nil {
			return "", fmt.Errorf("invalid_vnc_resolution_height: %s", resolutionParts[1])
		}

		waitAttr := ""
		if vncWait {
			waitAttr = "yes"
		}

		vncBindNormalized := NormalizeVNCBindAddress(vncBind)

		graphicsEl := devicesEl.CreateElement("graphics")
		graphicsEl.CreateAttr("type", "vnc")
		graphicsEl.CreateAttr("port", strconv.Itoa(vncPort))
		if vncPassword != "" {
			graphicsEl.CreateAttr("passwd", vncPassword)
		}
		if waitAttr != "" {
			graphicsEl.CreateAttr("wait", waitAttr)
		}

		listenEl := graphicsEl.CreateElement("listen")
		listenEl.CreateAttr("type", "address")
		listenEl.CreateAttr("address", vncBindNormalized)

		videoEl := devicesEl.CreateElement("video")
		modelEl := videoEl.CreateElement("model")
		modelEl.CreateAttr("type", "gop")
		modelEl.CreateAttr("heads", "1")
		modelEl.CreateAttr("primary", "yes")

		resEl := modelEl.CreateElement("resolution")
		resEl.CreateAttr("x", strconv.Itoa(width))
		resEl.CreateAttr("y", strconv.Itoa(height))
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}

	return out, nil
}

func updatePassthrough(xml string, pciDevices []int, passedThroughIds []models.PassedThroughIDs) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("domain_xml_root_not_found")
	}
	if len(pciDevices) > 0 && root.SelectAttr("xmlns:bhyve") == nil {
		root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
	}

	if len(pciDevices) > 0 {
		memBacking := doc.FindElement("//memoryBacking")
		if memBacking == nil {
			memBacking = root.CreateElement("memoryBacking")
		}
		if memBacking.FindElement("locked") == nil {
			memBacking.CreateElement("locked")
		}
	} else {
		if memBacking := doc.FindElement("//memoryBacking"); memBacking != nil {
			if locked := memBacking.FindElement("locked"); locked != nil {
				memBacking.RemoveChild(locked)
			}
			if len(memBacking.ChildElements()) == 0 && memBacking.Parent() != nil {
				memBacking.Parent().RemoveChild(memBacking)
			}
		}
	}

	bhyveCL := doc.FindElement("//bhyve:commandline")
	if bhyveCL == nil && len(pciDevices) > 0 {
		bhyveCL = root.CreateElement("bhyve:commandline")
	}

	if bhyveCL != nil {
		for _, arg := range bhyveCL.SelectElements("bhyve:arg") {
			if v := arg.SelectAttrValue("value", ""); strings.Contains(v, "passthru") {
				bhyveCL.RemoveChild(arg)
			}
		}
	}

	deviceIDByMappingID := make(map[int]string, len(passedThroughIds))
	for _, mapping := range passedThroughIds {
		deviceIDByMappingID[mapping.ID] = strings.TrimSpace(mapping.DeviceID)
	}
	seenMappings := make(map[int]struct{}, len(pciDevices))

	usedIndices := parseUsedIndicesFromDocument(doc)
	currentIndex := 10

	for _, devID := range pciDevices {
		if _, duplicate := seenMappings[devID]; duplicate {
			return "", fmt.Errorf("duplicate_passthrough_device: %d", devID)
		}
		seenMappings[devID] = struct{}{}

		pid, found := deviceIDByMappingID[devID]
		if !found {
			return "", fmt.Errorf("passthrough_device_not_found: %d", devID)
		}
		if pid == "" {
			return "", fmt.Errorf("passthrough_device_mapping_empty: %d", devID)
		}

		for currentIndex < 30 && usedIndices[currentIndex] {
			currentIndex++
		}
		if currentIndex >= 30 {
			return "", fmt.Errorf("no_free_passthrough_indices")
		}

		idx := currentIndex
		usedIndices[idx] = true
		currentIndex++
		arg := bhyveCL.CreateElement("bhyve:arg")
		arg.CreateAttr("value", fmt.Sprintf("-s %d:0,passthru,%s", idx, pid))
	}
	if bhyveCL != nil && len(bhyveCL.ChildElements()) == 0 && bhyveCL.Parent() != nil {
		bhyveCL.Parent().RemoveChild(bhyveCL)
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}
	return out, nil
}

const (
	minimumVMHardwareRAMBytes = 128 * 1024 * 1024
	minimumVNCWidth           = 640
	minimumVNCHeight          = 480
	maximumVNCWidth           = 3840
	maximumVNCHeight          = 2160
)

type vmHardwareMutationHooks struct {
	defineXML     func(string) error
	writeVMJSON   func(*gorm.DB, uint) error
	restoreXML    func(string) error
	restoreVMJSON func(uint) error
}

func (s *Service) normalizeVMHardwareMutationHooks(hooks vmHardwareMutationHooks) vmHardwareMutationHooks {
	if hooks.defineXML == nil {
		hooks.defineXML = func(xml string) error {
			_, err := s.conn().DomainDefineXML(xml)
			return err
		}
	}
	if hooks.writeVMJSON == nil {
		hooks.writeVMJSON = s.writeVMJsonWithDB
	}
	if hooks.restoreXML == nil {
		hooks.restoreXML = func(xml string) error {
			_, err := s.conn().DomainDefineXML(xml)
			return err
		}
	}
	if hooks.restoreVMJSON == nil {
		hooks.restoreVMJSON = s.WriteVMJson
	}
	return hooks
}

func (s *Service) applyVMHardwareMutation(
	rid uint,
	oldXML string,
	newXML string,
	updateDB func(*gorm.DB) error,
	hooks vmHardwareMutationHooks,
) error {
	hooks = s.normalizeVMHardwareMutationHooks(hooks)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := updateDB(tx); err != nil {
			return err
		}
		if err := hooks.defineXML(newXML); err != nil {
			return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
		}
		if err := hooks.writeVMJSON(tx, rid); err != nil {
			return fmt.Errorf("failed_to_write_vm_json_after_hardware_update: %w", err)
		}
		return nil
	})
	if err == nil {
		return nil
	}

	var restoreErr error
	if strings.TrimSpace(oldXML) != "" {
		if xmlErr := hooks.restoreXML(oldXML); xmlErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_domain_xml: %w", xmlErr))
		}
	}
	if jsonErr := hooks.restoreVMJSON(rid); jsonErr != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_vm_json: %w", jsonErr))
	}
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("hardware_reconciliation_failed: %w", restoreErr))
	}
	return err
}

func (s *Service) prepareVMHardwareMutation(rid uint) (vmModels.VM, error) {
	if s == nil || s.DB == nil {
		return vmModels.VM{}, fmt.Errorf("db_not_initialized")
	}
	if rid == 0 {
		return vmModels.VM{}, fmt.Errorf("invalid_rid")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vmModels.VM{}, fmt.Errorf("vm_not_found: %w", err)
		}
		return vmModels.VM{}, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}
	if err := s.requireVMMutationOwnership(rid); err != nil {
		return vmModels.VM{}, err
	}
	shutoff, err := s.IsDomainShutOff(rid)
	if err != nil {
		return vmModels.VM{}, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return vmModels.VM{}, fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}
	return vm, nil
}

func (s *Service) captureVMHardwareXML(rid uint) (string, error) {
	xml, err := s.GetVMXML(rid)
	if err != nil {
		return "", fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}
	return xml, nil
}

func validateCPUHardwareRequest(req libvirtServiceInterfaces.ModifyCPURequest) error {
	if req.CPUSockets <= 0 || req.CPUCores <= 0 || req.CPUThreads <= 0 {
		return fmt.Errorf("cpu_topology_must_be_positive")
	}

	maxInt := int(^uint(0) >> 1)
	if req.CPUSockets > maxInt/req.CPUCores {
		return fmt.Errorf("cpu_topology_overflow")
	}
	socketsAndCores := req.CPUSockets * req.CPUCores
	if socketsAndCores > maxInt/req.CPUThreads {
		return fmt.Errorf("cpu_topology_overflow")
	}
	vcpuCount := socketsAndCores * req.CPUThreads

	pinnedCount := 0
	for _, pin := range req.CPUPinning {
		pinnedCount += len(pin.Cores)
	}
	if pinnedCount > vcpuCount {
		return fmt.Errorf(
			"cpu_pinning_exceeds_vcpu_count: pinned=%d vcpus=%d",
			pinnedCount,
			vcpuCount,
		)
	}
	return nil
}

func sortVMHardwareCPUPins(pins []vmModels.VMCPUPinning) {
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].HostSocket != pins[j].HostSocket {
			return pins[i].HostSocket < pins[j].HostSocket
		}
		if len(pins[i].HostCPU) != len(pins[j].HostCPU) {
			return len(pins[i].HostCPU) < len(pins[j].HostCPU)
		}
		for k := range pins[i].HostCPU {
			if pins[i].HostCPU[k] != pins[j].HostCPU[k] {
				return pins[i].HostCPU[k] < pins[j].HostCPU[k]
			}
		}
		return false
	})
}

func normalizeRequestedVMHardwareCPUPins(
	vmID uint,
	pins []libvirtServiceInterfaces.CPUPinning,
) []vmModels.VMCPUPinning {
	normalized := make([]vmModels.VMCPUPinning, 0, len(pins))
	for _, pin := range pins {
		cores := append([]int(nil), pin.Cores...)
		sort.Ints(cores)
		normalized = append(normalized, vmModels.VMCPUPinning{
			VMID:       vmID,
			HostSocket: pin.Socket,
			HostCPU:    cores,
		})
	}
	sortVMHardwareCPUPins(normalized)
	return normalized
}

func normalizeStoredVMHardwareCPUPins(vmID uint, pins []vmModels.VMCPUPinning) []vmModels.VMCPUPinning {
	normalized := make([]vmModels.VMCPUPinning, 0, len(pins))
	for _, pin := range pins {
		cores := append([]int(nil), pin.HostCPU...)
		sort.Ints(cores)
		normalized = append(normalized, vmModels.VMCPUPinning{
			VMID:       vmID,
			HostSocket: pin.HostSocket,
			HostCPU:    cores,
		})
	}
	sortVMHardwareCPUPins(normalized)
	return normalized
}

func validateVNCResolution(resolution string) error {
	widthRaw, heightRaw, found := strings.Cut(strings.TrimSpace(resolution), "x")
	if !found {
		return fmt.Errorf("invalid_vnc_resolution_format")
	}
	width, err := strconv.Atoi(widthRaw)
	if err != nil {
		return fmt.Errorf("invalid_vnc_resolution_width: %s", widthRaw)
	}
	height, err := strconv.Atoi(heightRaw)
	if err != nil {
		return fmt.Errorf("invalid_vnc_resolution_height: %s", heightRaw)
	}
	if width < minimumVNCWidth || width > maximumVNCWidth ||
		height < minimumVNCHeight || height > maximumVNCHeight {
		return fmt.Errorf("vnc_resolution_out_of_range: %dx%d", width, height)
	}
	return nil
}

func normalizePassthroughDeviceIDs(pciDevices []int) ([]int, error) {
	normalized := make([]int, 0, len(pciDevices))
	seen := make(map[int]struct{}, len(pciDevices))
	for _, id := range pciDevices {
		if id <= 0 {
			return nil, fmt.Errorf("invalid_passthrough_device_id: %d", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate_passthrough_device: %d", id)
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

func equalIntLists(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) ModifyCPU(rid uint, req libvirtServiceInterfaces.ModifyCPURequest) error {
	if err := validateCPUHardwareRequest(req); err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	vm, err := s.prepareVMHardwareMutation(rid)
	if err != nil {
		return err
	}
	if err := s.ValidateCPUPins(rid, req.CPUPinning, 0); err != nil {
		return fmt.Errorf("failed_to_validate_cpu_pins: %w", err)
	}

	newPins := normalizeRequestedVMHardwareCPUPins(vm.ID, req.CPUPinning)
	oldPins := normalizeStoredVMHardwareCPUPins(vm.ID, vm.CPUPinning)
	if reflect.DeepEqual(oldPins, newPins) &&
		vm.CPUSockets == req.CPUSockets &&
		vm.CPUCores == req.CPUCores &&
		vm.CPUThreads == req.CPUThreads {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := s.updateRequestedCPUXML(oldXML, req, newPins)
	if err != nil {
		return fmt.Errorf("failed_to_update_cpu_in_xml: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Updates(map[string]any{
			"cpu_sockets": req.CPUSockets,
			"cpu_cores":   req.CPUCores,
			"cpu_threads": req.CPUThreads,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_update_vm_cpu: %w", err)
		}
		if err := tx.Where("vm_id = ?", vm.ID).Delete(&vmModels.VMCPUPinning{}).Error; err != nil {
			return fmt.Errorf("failed_to_clear_cpu_pinning: %w", err)
		}
		if len(newPins) > 0 {
			if err := tx.Create(&newPins).Error; err != nil {
				return fmt.Errorf("failed_to_replace_cpu_pinning: %w", err)
			}
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyRAM(rid uint, ram int) error {
	if ram < minimumVMHardwareRAMBytes {
		return fmt.Errorf("memory_must_be_at_least_128mb")
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	vm, err := s.prepareVMHardwareMutation(rid)
	if err != nil {
		return err
	}
	if vm.RAM == ram {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updateMemory(oldXML, ram)
	if err != nil {
		return fmt.Errorf("failed_to_update_memory_in_xml: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Update("ram", ram).Error; err != nil {
			return fmt.Errorf("failed_to_update_vm_ram_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyVNC(rid uint, req libvirtServiceInterfaces.ModifyVNCRequest) error {
	if req.VNCEnabled == nil {
		return fmt.Errorf("vnc_enabled_required")
	}
	if req.VNCWait == nil {
		return fmt.Errorf("vnc_wait_required")
	}
	vncEnabled := *req.VNCEnabled
	vncWait := *req.VNCWait
	vncBind := NormalizeVNCBindAddress(req.VNCBind)
	if err := ValidateVNCBindAddress(vncBind); err != nil {
		return err
	}
	vncResolution := strings.TrimSpace(req.VNCResolution)
	if err := validateVNCResolution(vncResolution); err != nil {
		return err
	}
	if strings.Contains(req.VNCPassword, ",") {
		return fmt.Errorf("vnc_password_cannot_contain_commas")
	}
	vncPort := req.VNCPort
	if !vncEnabled {
		vncPort = 0
	} else if vncPort < 1 || vncPort > 65535 {
		return fmt.Errorf("vnc_port_must_be_between_1_and_65535")
	}

	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	vm, err := s.prepareVMHardwareMutation(rid)
	if err != nil {
		return err
	}
	if vm.VNCPort == vncPort &&
		NormalizeVNCBindAddress(vm.VNCBind) == vncBind &&
		vm.VNCResolution == vncResolution &&
		vm.VNCPassword == req.VNCPassword &&
		vm.VNCWait == vncWait &&
		vm.VNCEnabled == vncEnabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	if vncEnabled {
		var count int64
		if err := s.DB.Model(&vmModels.VM{}).
			Where("vnc_enabled = ? AND vnc_port = ? AND rid != ?", true, vncPort, rid).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_vnc_port_usage: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("vnc_port_already_in_use_by_another_vm")
		}
		if utils.IsTCPPortInUse(vncPort) {
			return fmt.Errorf("vnc_port_already_in_use_by_another_service")
		}
	}

	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updateVNC(
		oldXML,
		vncPort,
		vncBind,
		vncResolution,
		req.VNCPassword,
		vncWait,
		vncEnabled,
	)
	if err != nil {
		return fmt.Errorf("failed_to_update_vnc_in_xml: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).Updates(map[string]any{
			"vnc_enabled":    vncEnabled,
			"vnc_port":       vncPort,
			"vnc_bind":       vncBind,
			"vnc_resolution": vncResolution,
			"vnc_password":   req.VNCPassword,
			"vnc_wait":       vncWait,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_update_vm_vnc_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func (s *Service) ModifyPassthrough(rid uint, pciDevices []int) error {
	normalizedDevices, err := normalizePassthroughDeviceIDs(pciDevices)
	if err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	vm, err := s.prepareVMHardwareMutation(rid)
	if err != nil {
		return err
	}
	var passedThroughIDs []models.PassedThroughIDs
	if len(normalizedDevices) > 0 {
		if err := s.DB.Where("id IN ?", normalizedDevices).Find(&passedThroughIDs).Error; err != nil {
			return fmt.Errorf("failed_to_get_passed_through_ids: %w", err)
		}
		found := make(map[int]struct{}, len(passedThroughIDs))
		for _, mapping := range passedThroughIDs {
			found[mapping.ID] = struct{}{}
		}
		for _, id := range normalizedDevices {
			if _, ok := found[id]; !ok {
				return fmt.Errorf("passthrough_device_not_found: %d", id)
			}
		}
	}
	if equalIntLists(vm.PCIDevices, normalizedDevices) {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	oldXML, err := s.captureVMHardwareXML(rid)
	if err != nil {
		return err
	}
	newXML, err := updatePassthrough(oldXML, normalizedDevices, passedThroughIDs)
	if err != nil {
		return fmt.Errorf("failed_to_update_passthrough_in_xml: %w", err)
	}

	return s.applyVMHardwareMutation(rid, oldXML, newXML, func(tx *gorm.DB) error {
		if err := tx.Model(&vm).
			Select("PCIDevices").
			Updates(vmModels.VM{PCIDevices: normalizedDevices}).Error; err != nil {
			return fmt.Errorf("failed_to_update_vm_pci_devices_in_db: %w", err)
		}
		return nil
	}, vmHardwareMutationHooks{})
}

func parseUsedIndicesFromElement(bhyveCommandline *etree.Element) map[int]bool {
	used := make(map[int]bool)
	if bhyveCommandline == nil {
		return used
	}

	for _, arg := range bhyveCommandline.ChildElements() {
		valueAttr := arg.SelectAttr("value")
		if valueAttr == nil {
			continue
		}
		value := strings.TrimSpace(valueAttr.Value)
		if value == "" {
			continue
		}

		// handle "-s 10:0,...", "-s10:0,...", and "-s 10,virtio-console,..."
		if strings.HasPrefix(value, "-s") {
			rest := strings.TrimPrefix(value, "-s")
			rest = strings.TrimSpace(rest)
			sep := strings.IndexAny(rest, ":,")
			if sep > 0 {
				if idx, err := strconv.Atoi(rest[:sep]); err == nil {
					used[idx] = true
				}
			}
		}
	}

	return used
}

func parseUsedIndicesFromDocument(doc *etree.Document) map[int]bool {
	used := make(map[int]bool)
	if doc == nil {
		return used
	}

	for _, commandline := range doc.FindElements("//commandline") {
		if commandline.Space != "bhyve" {
			continue
		}
		for index := range parseUsedIndicesFromElement(commandline) {
			used[index] = true
		}
	}

	for _, address := range doc.FindElements("//address") {
		if address.SelectAttrValue("type", "") != "pci" {
			continue
		}
		slot := strings.TrimSpace(address.SelectAttrValue("slot", ""))
		if slot == "" {
			continue
		}
		index, err := strconv.ParseInt(slot, 0, 32)
		if err != nil {
			continue
		}
		used[int(index)] = true
	}

	return used
}
