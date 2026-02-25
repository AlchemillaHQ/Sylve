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
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
)

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
		cpu = doc.CreateElement("cpu")
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

		for _, arg := range bhyveCommandline.ChildElements() {
			valueAttr := arg.SelectAttr("value")
			if valueAttr != nil {
				value := valueAttr.Value
				if value != "" && len(value) >= 2 && value[0:2] == "-p" {
					bhyveCommandline.RemoveChild(arg)
				}
			}
		}

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

func updateVNC(xml string, vncPort int, vncResolution string, vncPassword string, vncWait bool, vncEnabled bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	bhyveCommandline := doc.FindElement("//bhyve:commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		root := doc.Root()
		if root.SelectAttr("xmlns:bhyve") == nil {
			root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
		}
		bhyveCommandline = root.CreateElement("bhyve:commandline")
	}

	index := 0

	for _, arg := range bhyveCommandline.ChildElements() {
		valueAttr := arg.SelectAttr("value")
		if valueAttr != nil {
			value := valueAttr.Value
			if value != "" && strings.Contains(value, "fbuf,tcp") {
				start := strings.Index(value, "-s")
				end := strings.Index(value, ":")
				if start != -1 && end != -1 && end > start {
					indexStr := value[start+2 : end]
					if idx, err := strconv.Atoi(indexStr); err == nil {
						index = idx
					}
				}
				bhyveCommandline.RemoveChild(arg)
			}
		}
	}

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

	wait := ""

	if vncWait {
		wait = ",wait"
	}

	if index == 0 {
		index, err = findLowestIndex(xml)
		if err != nil {
			return "", fmt.Errorf("failed_to_find_lowest_index: %w", err)
		}
	}

	vnc := fmt.Sprintf("-s %d:0,fbuf,tcp=0.0.0.0:%d,w=%d,h=%d,password=%s%s", index, vncPort, width, height, vncPassword, wait)

	if vnc != "" && vncEnabled {
		arg := bhyveCommandline.CreateElement("bhyve:arg")
		arg.CreateAttr("value", vnc)
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}

	return out, nil
}

func updatePassthrough(xml string, pciDevices []string, passedThroughIds []models.PassedThroughIDs) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	root := doc.Root()
	if root.SelectAttr("xmlns:bhyve") == nil {
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
			parent := memBacking.Parent()
			parent.RemoveChild(memBacking)
		}
	}

	bhyveCL := doc.FindElement("//bhyve:commandline")
	if bhyveCL == nil {
		bhyveCL = root.CreateElement("bhyve:commandline")
	}

	for _, arg := range bhyveCL.SelectElements("bhyve:arg") {
		if v := arg.SelectAttrValue("value", ""); strings.Contains(v, "passthru") {
			bhyveCL.RemoveChild(arg)
		}
	}

	startIdx, err := findLowestIndex(xml)
	if err != nil {
		return "", fmt.Errorf("failed to find starting slot index: %w", err)
	}

	for i, devID := range pciDevices {
		pid := ""

		for _, ptID := range passedThroughIds {
			intDevId, err := strconv.Atoi(devID)
			if err != nil {
				return "", fmt.Errorf("failed to convert device ID to int: %w", err)
			}

			if ptID.ID == intDevId {
				pid = ptID.DeviceID
				break
			}
		}

		idx := startIdx + i
		arg := bhyveCL.CreateElement("bhyve:arg")
		arg.CreateAttr("value", fmt.Sprintf("-s %d:0,passthru,%s", idx, pid))
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}
	return out, nil
}

func (s *Service) ModifyCPU(rid uint, req libvirtServiceInterfaces.ModifyCPURequest) error {
	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return err
	}

	status, err := s.IsDomainShutOff(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_domain_shutoff_status: %w", err)
	}

	if !status {
		return fmt.Errorf("domain_not_shutoff: %d", vm.RID)
	}

	err = s.ValidateCPUPins(uint(vm.RID), req.CPUPinning, 0)
	if err != nil {
		return fmt.Errorf("failed_to_validate_cpu_pins: %w", err)
	}

	vm.CPUCores = req.CPUCores
	vm.CPUSockets = req.CPUSockets
	vm.CPUThreads = req.CPUThreads

	// Normalize the incoming pins (optional: sort for stable equality checks)
	newPins := make([]vmModels.VMCPUPinning, 0, len(req.CPUPinning))
	for _, p := range req.CPUPinning {
		cores := append([]int(nil), p.Cores...)
		sort.Ints(cores)
		newPins = append(newPins, vmModels.VMCPUPinning{
			VMID:       vm.ID,
			HostSocket: p.Socket,
			HostCPU:    cores,
		})
	}
	sort.Slice(newPins, func(i, j int) bool {
		if newPins[i].HostSocket != newPins[j].HostSocket {
			return newPins[i].HostSocket < newPins[j].HostSocket
		}
		// secondary sort for deterministic order
		if len(newPins[i].HostCPU) != len(newPins[j].HostCPU) {
			return len(newPins[i].HostCPU) < len(newPins[j].HostCPU)
		}
		for k := range newPins[i].HostCPU {
			if newPins[i].HostCPU[k] != newPins[j].HostCPU[k] {
				return newPins[i].HostCPU[k] < newPins[j].HostCPU[k]
			}
		}
		return false
	})

	// Load existing pinning to check for no-op updates
	if err := s.DB.Preload("CPUPinning").First(&vm, vm.ID).Error; err != nil {
		return fmt.Errorf("failed_to_load_vm_pinning: %w", err)
	}

	// Build a normalized copy for comparison
	oldPins := make([]vmModels.VMCPUPinning, 0, len(vm.CPUPinning))
	for _, p := range vm.CPUPinning {
		cores := append([]int(nil), p.HostCPU...)
		sort.Ints(cores)
		oldPins = append(oldPins, vmModels.VMCPUPinning{
			VMID:       vm.ID,
			HostSocket: p.HostSocket,
			HostCPU:    cores,
		})
	}
	sort.Slice(oldPins, func(i, j int) bool {
		if oldPins[i].HostSocket != oldPins[j].HostSocket {
			return oldPins[i].HostSocket < oldPins[j].HostSocket
		}
		if len(oldPins[i].HostCPU) != len(oldPins[j].HostCPU) {
			return len(oldPins[i].HostCPU) < len(oldPins[j].HostCPU)
		}
		for k := range oldPins[i].HostCPU {
			if oldPins[i].HostCPU[k] != oldPins[j].HostCPU[k] {
				return oldPins[i].HostCPU[k] < oldPins[j].HostCPU[k]
			}
		}
		return false
	})

	// Quick no-op guard (prevents unnecessary writes)
	if reflect.DeepEqual(oldPins, newPins) &&
		vm.CPUSockets == req.CPUSockets &&
		vm.CPUCores == req.CPUCores &&
		vm.CPUThreads == req.CPUThreads {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// Update basic CPU topology
	if err := tx.Model(&vm).Updates(map[string]any{
		"cpu_sockets": req.CPUSockets,
		"cpu_cores":   req.CPUCores,
		"cpu_threads": req.CPUThreads,
	}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_update_vm_cpu: %w", err)
	}

	// Replace pinning in one shot:
	// This clears existing rows (for this VM) and inserts `newPins`, setting VMID automatically.
	// NOTE: Association.Replace requires that newPins have no zero primary keys (they don't) and the FK is declared.
	if err := tx.Model(&vm).Association("CPUPinning").Replace(newPins); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_replace_cpu_pinning: %w", err)
	}

	err = tx.Commit().Error
	if err != nil {
		return fmt.Errorf("failed_to_commit_cpu_update_transaction: %w", err)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	domainXML, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	xml := string(domainXML)
	updatedXML, err := s.updateCPU(xml, vm.CPUSockets, vm.CPUCores, vm.CPUThreads, vm.CPUPinning)
	if err != nil {
		return fmt.Errorf("failed_to_update_cpu_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after CPU update")
	}

	return nil
}

func (s *Service) ModifyRAM(rid uint, ram int) error {
	vm, err := s.GetVMByRID(rid)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.RID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.RID)
	}

	if vm.RAM == ram {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	domainXML, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	xml := string(domainXML)
	updatedXML := xml

	vm.RAM = ram
	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_ram_in_db: %w", err)
	}

	updatedXML, err = updateMemory(xml, ram)
	if err != nil {
		return fmt.Errorf("failed_to_update_memory_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after memory update")
	}

	return nil
}

func (s *Service) ModifyVNC(rid uint, req libvirtServiceInterfaces.ModifyVNCRequest) error {
	vm, err := s.GetVMByRID(rid)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.RID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.RID)
	}

	vncWait := false

	if req.VNCWait != nil {
		vncWait = *req.VNCWait
	}

	vncEnabled := false

	if req.VNCEnabled != nil {
		vncEnabled = *req.VNCEnabled
	}

	if vm.VNCPort == req.VNCPort &&
		vm.VNCResolution == req.VNCResolution &&
		vm.VNCPassword == req.VNCPassword &&
		vm.VNCWait == vncWait &&
		vm.VNCEnabled == vncEnabled {
		return fmt.Errorf("no_changes_detected: %d", rid)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	domainXML, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	xml := string(domainXML)
	updatedXML := xml

	vm.VNCEnabled = vncEnabled
	vm.VNCPort = req.VNCPort
	vm.VNCResolution = req.VNCResolution
	vm.VNCPassword = req.VNCPassword
	vm.VNCWait = vncWait

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_vnc_in_db: %w", err)
	}

	updatedXML, err = updateVNC(xml, req.VNCPort, req.VNCResolution, req.VNCPassword, vncWait, vncEnabled)
	if err != nil {
		return fmt.Errorf("failed_to_update_vnc_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after VNC update")
	}

	return nil
}

func (s *Service) ModifyPassthrough(rid uint, pciDevices []int) error {
	vm, err := s.GetVMByRID(rid)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.RID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.RID)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	domainXML, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	xml := string(domainXML)
	updatedXML := xml

	vm.PCIDevices = pciDevices

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_pci_devices_in_db: %w", err)
	}

	strSlice := utils.IntSliceToStrSlice(pciDevices)

	var passedThroughIds []models.PassedThroughIDs
	if err := s.DB.Find(&passedThroughIds).Error; err != nil {
		return fmt.Errorf("failed_to_get_passed_through_ids: %w", err)
	}

	updatedXML, err = updatePassthrough(xml, strSlice, passedThroughIds)
	if err != nil {
		return fmt.Errorf("failed_to_update_passthrough_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after passthrough update")
	}

	return nil
}

func findLowestIndex(xml string) (int, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return -1, fmt.Errorf("failed to parse XML: %w", err)
	}
	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		fmt.Println("i1", 10)
		return 10, nil
	}

	usedIndices := make(map[int]bool)
	for _, arg := range bhyveCommandline.ChildElements() {
		valueAttr := arg.SelectAttr("value")
		if valueAttr == nil {
			continue
		}

		value := valueAttr.Value
		if len(value) >= 2 && value[0:2] == "-s" {
			parts := strings.Fields(value)
			if len(parts) >= 2 {
				indexPart := parts[1]
				sepIndex := strings.IndexAny(indexPart, ":,")
				if sepIndex > 0 {
					indexStr := indexPart[0:sepIndex] // "10"
					if index, err := strconv.Atoi(indexStr); err == nil {
						usedIndices[index] = true
					}
				}
			}
		}
	}

	fmt.Println(usedIndices)

	for i := 10; i < 30; i++ {
		if !usedIndices[i] {
			fmt.Println("i2", i)
			return i, nil
		}
	}

	return -1, fmt.Errorf("all indices 10-29 are in use")
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
