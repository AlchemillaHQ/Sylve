// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/beevik/etree"
	"github.com/digitalocean/go-libvirt"
	"github.com/klauspost/cpuid/v2"
)

func nativeCPUPins(pins []vmModels.VMCPUPinning) []libvirtServiceInterfaces.VCPUPin {
	if len(pins) == 0 {
		return nil
	}
	socketCount := utils.GetSocketCount(cpuid.CPU.PhysicalCores, cpuid.CPU.ThreadsPerCore)
	if socketCount <= 0 {
		socketCount = 1
	}
	logicalPerSocket := utils.GetLogicalCores() / socketCount
	if logicalPerSocket <= 0 {
		logicalPerSocket = cpuid.CPU.LogicalCores
	}
	return buildVCPUPins(pins, logicalPerSocket)
}

func buildVCPUPins(pins []vmModels.VMCPUPinning, logicalPerSocket int) []libvirtServiceInterfaces.VCPUPin {
	var native []libvirtServiceInterfaces.VCPUPin
	for _, pin := range pins {
		for _, localCPU := range pin.HostCPU {
			native = append(native, libvirtServiceInterfaces.VCPUPin{
				VCPU:   len(native),
				CPUSet: strconv.Itoa(pin.HostSocket*logicalPerSocket + localCPU),
			})
		}
	}
	return native
}

func updateCPUPinningXML(domainXML string, pins []libvirtServiceInterfaces.VCPUPin) (string, bool, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		return "", false, fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	changed, err := setCPUPinningXML(doc, pins)
	if err != nil {
		return "", false, err
	}
	if !changed {
		return domainXML, false, nil
	}
	updated, err := doc.WriteToString()
	return updated, true, err
}

func setCPUPinningXML(doc *etree.Document, pins []libvirtServiceInterfaces.VCPUPin) (bool, error) {
	root := doc.Root()
	if root == nil {
		return false, fmt.Errorf("domain_xml_root_not_found")
	}
	changed, err := removeLegacyCPUPinArgs(doc)
	if err != nil {
		return false, err
	}
	if nativeCPUPinsMatch(root, pins) {
		return changed, nil
	}

	var tune *etree.Element
	for _, element := range root.FindElements("cputune") {
		for _, pin := range element.FindElements("vcpupin") {
			element.RemoveChild(pin)
		}
		if len(element.ChildElements()) == 0 && len(element.Attr) == 0 {
			root.RemoveChild(element)
		} else if tune == nil {
			tune = element
		}
	}
	if len(pins) > 0 && tune == nil {
		tune = root.CreateElement("cputune")
	}
	for _, pin := range pins {
		element := tune.CreateElement("vcpupin")
		element.CreateAttr("vcpu", strconv.Itoa(pin.VCPU))
		element.CreateAttr("cpuset", pin.CPUSet)
	}
	return true, nil
}

func nativeCPUPinsMatch(root *etree.Element, pins []libvirtServiceInterfaces.VCPUPin) bool {
	remaining := make(map[string]string, len(pins))
	for _, pin := range pins {
		remaining[strconv.Itoa(pin.VCPU)] = pin.CPUSet
	}
	for _, pin := range root.FindElements("cputune/vcpupin") {
		vcpu := pin.SelectAttrValue("vcpu", "")
		cpuset, found := remaining[vcpu]
		if !found || cpuset != pin.SelectAttrValue("cpuset", "") {
			return false
		}
		delete(remaining, vcpu)
	}
	return len(remaining) == 0
}

type cpuPinArgToken struct {
	start, end int
	value      string
}

func cpuPinArgTokens(value string) []cpuPinArgToken {
	var tokens []cpuPinArgToken
	for i := 0; i < len(value); {
		if strings.ContainsRune(" \t\r\n\v\f", rune(value[i])) {
			i++
			continue
		}
		start := i
		var quote byte
		for i < len(value) {
			c := value[i]
			if c == '\\' && quote != '\'' && i+1 < len(value) {
				i += 2
				continue
			}
			if quote != 0 {
				if c == quote {
					quote = 0
				}
			} else if c == '\'' || c == '"' {
				quote = c
			} else if strings.ContainsRune(" \t\r\n\v\f", rune(c)) {
				break
			}
			i++
		}
		tokens = append(tokens, cpuPinArgToken{start: start, end: i, value: value[start:i]})
	}
	return tokens
}

func bhyveArgConsumesNextOperand(value string) bool {
	if len(value) < 2 || value[0] != '-' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if strings.IndexByte("kfoGpcsmnlKUr", value[i]) >= 0 {
			return i == len(value)-1
		}
	}
	return false
}

func removeLegacyCPUPinArgs(doc *etree.Document) (bool, error) {
	changed := false
	for _, commandline := range doc.FindElements("//commandline") {
		if commandline.Space != "bhyve" {
			continue
		}
		args := commandline.SelectElements("bhyve:arg")
		for i := 0; i < len(args); i++ {
			arg := args[i]
			value := arg.SelectAttrValue("value", "")
			if value == "--" {
				break
			}
			if !strings.HasPrefix(value, "-p") {
				if bhyveArgConsumesNextOperand(value) {
					i++
				}
				continue
			}
			tokens := cpuPinArgTokens(value)
			var spans []cpuPinArgToken
			var operand *etree.Element
			for j := 0; j < len(tokens); j++ {
				token := tokens[j]
				mapping := ""
				if token.value == "-p" {
					j++
					if j < len(tokens) {
						mapping = tokens[j].value
					} else if i+1 < len(args) {
						i++
						operand = args[i]
						mapping = strings.TrimSpace(operand.SelectAttrValue("value", ""))
					} else {
						return false, fmt.Errorf("invalid_legacy_cpu_pin: missing_mapping")
					}
				} else if strings.HasPrefix(token.value, "-p") {
					mapping = token.value[2:]
				} else {
					continue
				}
				vcpuRaw, cpuRaw, found := strings.Cut(mapping, ":")
				vcpu, vcpuErr := strconv.Atoi(vcpuRaw)
				cpu, cpuErr := strconv.Atoi(cpuRaw)
				if !found || vcpuErr != nil || cpuErr != nil || vcpu < 0 || cpu < 0 {
					return false, fmt.Errorf("invalid_legacy_cpu_pin: %s", mapping)
				}
				spans = append(spans, token)
				if token.value == "-p" && j < len(tokens) {
					spans = append(spans, tokens[j])
				}
			}
			if operand != nil {
				commandline.RemoveChild(operand)
			}
			for j := len(spans) - 1; j >= 0; j-- {
				span := spans[j]
				value = value[:span.start] + value[span.end:]
			}
			value = strings.TrimSpace(value)
			if value == "" {
				commandline.RemoveChild(arg)
			} else {
				arg.CreateAttr("value", value)
			}
			changed = true
		}
		if len(args) > 0 && len(commandline.ChildElements()) == 0 {
			commandline.Parent().RemoveChild(commandline)
		}
	}
	return changed, nil
}

type cpuPinningConnection interface {
	DomainLookupByName(string) (libvirt.Domain, error)
	DomainGetState(libvirt.Domain, uint32) (int32, int32, error)
	DomainGetXMLDesc(libvirt.Domain, libvirt.DomainXMLFlags) (string, error)
	DomainDefineXML(string) (libvirt.Domain, error)
}

func (s *Service) MigrateCPUPinningToNativeFormat() error {
	conn, err := s.ensureConnection()
	if err != nil {
		return err
	}
	return s.migrateCPUPinningToNativeFormat(conn)
}

func (s *Service) migrateCPUPinningToNativeFormat(conn cpuPinningConnection) error {
	unlock := s.lockVMOptionMutation()
	defer unlock()

	var vms []vmModels.VM
	if err := s.DB.Preload("CPUPinning").Find(&vms).Error; err != nil {
		return fmt.Errorf("failed_to_list_vms_for_cpu_pinning_migration: %w", err)
	}
	for _, vm := range vms {
		if len(vm.CPUPinning) == 0 {
			continue
		}
		domain, err := conn.DomainLookupByName(strconv.Itoa(int(vm.RID)))
		if err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("cpu_pinning_migration: failed to lookup domain")
			continue
		}
		ready, err := ensureNativeCPUPinningXML(conn, domain, vm.CPUPinning)
		if err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("cpu_pinning_migration: failed to ensure native XML")
			continue
		}
		if !ready {
			continue
		}

		migration := models.Migrations{Name: fmt.Sprintf("cpu_pinning_native_xml_format_1_%d", vm.RID)}
		result := s.DB.Where("name = ?", migration.Name).FirstOrCreate(&migration)
		if result.Error != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(result.Error).Msg("cpu_pinning_migration: failed to record migration")
			continue
		}
		if result.RowsAffected > 0 {
			logger.L.Info().Uint("rid", vm.RID).Msg("cpu_pinning_migration: verified native XML format")
		}
	}
	return nil
}

func (s *Service) ensureCPUPinningNativeXML(conn cpuPinningConnection, domain libvirt.Domain, rid uint) error {
	var vm vmModels.VM
	if err := s.DB.Select("id", "rid").Preload("CPUPinning").Where("rid = ?", rid).First(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_cpu_pinning: %w", err)
	}
	if len(vm.CPUPinning) == 0 {
		return nil
	}
	ready, err := ensureNativeCPUPinningXML(conn, domain, vm.CPUPinning)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("domain_not_shutoff_for_cpu_pinning_migration")
	}
	return nil
}

func ensureNativeCPUPinningXML(conn cpuPinningConnection, domain libvirt.Domain, pins []vmModels.VMCPUPinning) (bool, error) {
	state, _, err := conn.DomainGetState(domain, 0)
	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}
	if state != int32(libvirt.DomainShutoff) {
		return false, nil
	}
	domainXML, err := conn.DomainGetXMLDesc(domain, libvirt.DomainXMLInactive|libvirt.DomainXMLSecure)
	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}
	updatedXML, changed, err := updateCPUPinningXML(domainXML, nativeCPUPins(pins))
	if err != nil {
		return false, err
	}
	if changed {
		if _, err := conn.DomainDefineXML(updatedXML); err != nil {
			return false, fmt.Errorf("failed_to_define_domain_with_native_cpu_pinning: %w", err)
		}
	}
	return true, nil
}
