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
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
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

func updateCPU(xml string, cpuSockets, cpuCores, cpuThreads int, cpuPinning []int) (string, error) {
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

		for i, cpu := range cpuPinning {
			if i > 0 {
				pinStr += " "
			}

			pinStr += fmt.Sprintf("-p %d:%d", i, cpu)
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

func removeMemoryBacking(xml string) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	if mem := doc.FindElement("//memoryBacking"); mem != nil {
		parent := mem.Parent()
		parent.RemoveChild(mem)
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}
	return out, nil
}

func cleanPassthrough(xml string) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", fmt.Errorf("failed to parse XML: %w", err)
	}

	if bhyveCL := doc.FindElement("//bhyve:commandline"); bhyveCL != nil {
		for _, arg := range bhyveCL.SelectElements("bhyve:arg") {
			if v := arg.SelectAttrValue("value", ""); strings.Contains(v, "passthru") {
				bhyveCL.RemoveChild(arg)
			}
		}
	}

	out, err := doc.WriteToString()
	if err != nil {
		return "", fmt.Errorf("failed to serialize XML: %w", err)
	}
	return out, nil
}

func (s *Service) ModifyCPU(
	vmId int,
	cpuSockets int,
	cpuCores int,
	cpuThreads int,
	cpuPinning []int,
) error {
	vm, err := s.GetVmByVmId(vmId)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.VmID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.VmID)
	}

	if vm.CPUCores == cpuCores &&
		vm.CPUSockets == cpuSockets &&
		vm.CPUsThreads == cpuThreads &&
		len(vm.CPUPinning) == len(cpuPinning) {
		for i, cpu := range vm.CPUPinning {
			if i >= len(cpuPinning) || cpu != cpuPinning[i] {
				return fmt.Errorf("no_changes_detected: %d", vmId)
			}
		}
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	domainXML, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	xml := string(domainXML)
	updatedXML := xml

	vm.CPUCores = cpuCores
	vm.CPUSockets = cpuSockets
	vm.CPUsThreads = cpuThreads
	vm.CPUPinning = cpuPinning

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_cpu_in_db: %w", err)
	}

	updatedXML, err = updateCPU(xml, cpuSockets, cpuCores, cpuThreads, cpuPinning)

	if err != nil {
		return fmt.Errorf("failed_to_update_cpu_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

func (s *Service) ModifyRAM(vmId int, ram int) error {
	vm, err := s.GetVmByVmId(vmId)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.VmID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.VmID)
	}

	if vm.RAM == ram {
		return fmt.Errorf("no_changes_detected: %d", vmId)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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

	return nil
}

func (s *Service) ModifyVNC(vmId int, vncEnabled bool, vncPort int, vncResolution string, vncPassword string, vncWait bool) error {
	vm, err := s.GetVmByVmId(vmId)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.VmID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.VmID)
	}

	if vm.VNCPort == vncPort &&
		vm.VNCResolution == vncResolution &&
		vm.VNCPassword == vncPassword &&
		vm.VNCWait == vncWait &&
		vm.VNCEnabled == vncEnabled {
		return fmt.Errorf("no_changes_detected: %d", vmId)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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
	vm.VNCPort = vncPort
	vm.VNCResolution = vncResolution
	vm.VNCPassword = vncPassword
	vm.VNCWait = vncWait

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_update_vm_vnc_in_db: %w", err)
	}

	updatedXML, err = updateVNC(xml, vncPort, vncResolution, vncPassword, vncWait, vncEnabled)
	if err != nil {
		return fmt.Errorf("failed_to_update_vnc_in_xml: %w", err)
	}

	if err := s.Conn.DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.Conn.DomainDefineXML(updatedXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

func (s *Service) ModifyPassthrough(vmId int, pciDevices []int) error {
	vm, err := s.GetVmByVmId(vmId)

	if err != nil {
		return err
	}

	shutoff, err := s.IsDomainShutOff(vm.VmID)

	if err != nil {
		return err
	}

	if !shutoff {
		return fmt.Errorf("domain_not_shutoff: %d", vm.VmID)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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

	return nil
}

func findLowestIndex(xml string) (int, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return -1, fmt.Errorf("failed to parse XML: %w", err)
	}
	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
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
				colonIndex := strings.Index(indexPart, ":")
				if colonIndex > 0 {
					indexStr := indexPart[0:colonIndex] // "10"
					if index, err := strconv.Atoi(indexStr); err == nil {
						usedIndices[index] = true
					}
				}
			}
		}
	}

	for i := 10; i < 30; i++ {
		if !usedIndices[i] {
			return i, nil
		}
	}

	return -1, fmt.Errorf("all indices 10-29 are in use")
}
