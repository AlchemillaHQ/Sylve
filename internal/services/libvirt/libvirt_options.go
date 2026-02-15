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
	"strconv"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/beevik/etree"
)

func (s *Service) ModifyWakeOnLan(rid uint, enabled bool) error {
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("wo_l", enabled).Error
	return err
}

func (s *Service) ModifyBootOrder(rid uint, startAtBoot bool, bootOrder int) error {
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Updates(map[string]interface{}{
			"start_order":   bootOrder,
			"start_at_boot": startAtBoot,
		}).Error
	return err
}

func (s *Service) ModifyClock(rid uint, timeOffset string) error {
	if timeOffset != "utc" && timeOffset != "localtime" {
		return fmt.Errorf("invalid_time_offset: %s", timeOffset)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("invalid_domain_xml: root_missing")
	}

	clockEl := doc.FindElement("//clock")
	if clockEl == nil {
		clockEl = root.CreateElement("clock")
	}

	attr := clockEl.SelectAttr("offset")
	if attr == nil {
		clockEl.CreateAttr("offset", timeOffset)
	} else {
		attr.Value = timeOffset
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

	if err := s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("time_offset", timeOffset).Error; err != nil {
		return fmt.Errorf("failed_to_update_time_offset_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifySerial(rid uint, enabled bool) error {
	var pre vmModels.VM
	if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", rid).First(&pre).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vm_from_db: %w", err)
	}

	if pre.Serial == enabled {
		return nil
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("invalid_domain_xml: root_missing")
	}

	master := "/dev/nmdm" + strconv.Itoa(int(rid)) + "A"

	// remove any existing <serial>/<console> for this nmdm pair
	devicesEl := doc.FindElement("//devices")
	if devicesEl != nil {
		children := append([]*etree.Element{}, devicesEl.ChildElements()...)
		for _, el := range children {
			if el.Tag != "serial" && el.Tag != "console" {
				continue
			}
			if src := el.FindElement("source"); src != nil {
				if a := src.SelectAttr("master"); a != nil && a.Value == master {
					devicesEl.RemoveChild(el)
				}
			}
		}
	}

	if enabled {
		if devicesEl == nil {
			devicesEl = etree.NewElement("devices")
			root.AddChild(devicesEl)
		}
		serialEl := etree.NewElement("serial")
		serialEl.CreateAttr("type", "nmdm")

		sourceEl := etree.NewElement("source")
		sourceEl.CreateAttr("master", master)
		sourceEl.CreateAttr("slave", "/dev/nmdm"+strconv.Itoa(int(rid))+"B")
		serialEl.AddChild(sourceEl)

		devicesEl.AddChild(serialEl)
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

	if err := s.DB.Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("serial", enabled).Error; err != nil {
		return fmt.Errorf("failed_to_update_serial_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyShutdownWaitTime(rid uint, waitTime int) error {
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("shutdown_wait_time", waitTime).Error
	return err
}

func (s *Service) ModifyCloudInitData(rid uint, data string, metadata string, networkConfig string) error {
	if data == "" && metadata != "" || data != "" && metadata == "" {
		return fmt.Errorf("both_data_and_metadata_must_be_provided")
	}

	if data != "" && metadata != "" {
		if utils.IsValidYAML(data) == false || utils.IsValidYAML(metadata) == false {
			return fmt.Errorf("invalid_yaml_in_cloud_init_data_or_metadata")
		}
	}

	if networkConfig != "" {
		if utils.IsValidYAML(networkConfig) == false {
			return fmt.Errorf("invalid_yaml_in_cloud_init_network_config")
		}
	}

	err := s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Updates(map[string]interface{}{
			"cloud_init_data":           data,
			"cloud_init_meta_data":      metadata,
			"cloud_init_network_config": networkConfig,
		}).Error

	if err != nil {
		return fmt.Errorf("failed_to_update_cloud_init_data_in_db: %w", err)
	}

	return s.SyncVMDisks(rid)
}

func (s *Service) ModifyIgnoreUMSRs(rid uint, ignore bool) error {
	var vm vmModels.VM
	if err := s.DB.Where("rid = ?", rid).First(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vm_from_db: %w", err)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("invalid_domain_xml: root_missing")
	}

	bhyveCmdEl := doc.FindElement("//bhyve:commandline")
	if bhyveCmdEl == nil {
		bhyveCmdEl = root.CreateElement("bhyve:commandline")
	}

	for {
		found := false
		children := bhyveCmdEl.ChildElements()
		for _, el := range children {
			if el.Tag == "bhyve:arg" || el.Tag == "arg" {
				if a := el.SelectAttr("value"); a != nil && a.Value == "-w" {
					bhyveCmdEl.RemoveChild(el)
					found = true
					break
				}
			}
		}
		if !found {
			break
		}
	}

	if ignore {
		argEl := etree.NewElement("bhyve:arg")
		argEl.CreateAttr("value", "-w")
		bhyveCmdEl.AddChild(argEl)
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

	err = s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("ignore_umsr", ignore).Error
	return err
}

func (s *Service) ModifyQemuGuestAgent(rid uint, enabled bool) error {
	var vm vmModels.VM
	if err := s.DB.Where("rid = ?", rid).First(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vm_from_db: %w", err)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("invalid_domain_xml: root_missing")
	}

	bhyveCmdEl := doc.FindElement("//bhyve:commandline")
	if bhyveCmdEl == nil {
		bhyveCmdEl = root.CreateElement("bhyve:commandline")
	}

	for {
		found := false
		children := bhyveCmdEl.ChildElements()
		for _, el := range children {
			if el.Tag == "bhyve:arg" || el.Tag == "arg" {
				if a := el.SelectAttr("value"); a != nil && strings.Contains(a.Value, "org.qemu.guest_agent.0=") {
					bhyveCmdEl.RemoveChild(el)
					found = true
					break
				}
			}
		}
		if !found {
			break
		}
	}

	if enabled {
		dataPath, err := s.GetVMConfigDirectory(vm.RID)
		if err != nil {
			return fmt.Errorf("failed_to_get_vm_data_path: %w", err)
		}

		used := parseUsedIndicesFromElement(bhyveCmdEl)
		index := 10
		for index < 30 && used[index] {
			index++
		}
		if index >= 30 {
			return fmt.Errorf("no_free_indices_available_for_qemu_guest_agent")
		}

		qgaArg := fmt.Sprintf("-s %d,virtio-console,org.qemu.guest_agent.0=%s",
			index,
			filepath.Join(dataPath, "qga.sock"),
		)

		argEl := etree.NewElement("bhyve:arg")
		argEl.CreateAttr("value", qgaArg)
		bhyveCmdEl.AddChild(argEl)
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

	err = s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("qemu_guest_agent", enabled).Error
	if err != nil {
		return fmt.Errorf("failed_to_update_qemu_guest_agent_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyTPMEmulation(rid uint, enabled bool) error {
	var vm vmModels.VM
	if err := s.DB.Where("rid = ?", rid).First(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vm_from_db: %w", err)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	xml, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("invalid_domain_xml: root_missing")
	}

	bhyveCmdEl := doc.FindElement("//bhyve:commandline")
	if bhyveCmdEl == nil {
		bhyveCmdEl = root.CreateElement("bhyve:commandline")
	}

	for {
		found := false
		children := bhyveCmdEl.ChildElements()
		for _, el := range children {
			if el.Tag == "bhyve:arg" || el.Tag == "arg" {
				if a := el.SelectAttr("value"); a != nil && len(a.Value) >= 5 && a.Value[:5] == "-ltpm" {
					bhyveCmdEl.RemoveChild(el)
					found = true
					break
				}
			}
		}

		if !found {
			break
		}
	}

	if enabled {
		dataPath, err := s.GetVMConfigDirectory(vm.RID)
		if err != nil {
			return fmt.Errorf("failed_to_get_vm_data_path: %w", err)
		}

		tpmArg := fmt.Sprintf("-ltpm,swtpm,%s", filepath.Join(dataPath, fmt.Sprintf("%d_tpm.socket", vm.RID)))

		argEl := etree.NewElement("bhyve:arg")
		argEl.CreateAttr("value", tpmArg)
		bhyveCmdEl.AddChild(argEl)
	} else {
		err := s.StopTPM(vm.RID)
		if err != nil {
			if !strings.Contains(err.Error(), "tpm_socket_not_found") {
				logger.L.Err(err).Msg("Failed to stop TPM")
			}
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

	err = s.DB.
		Model(&vmModels.VM{}).
		Where("rid = ?", rid).
		Update("tpm_emulation", enabled).Error
	if err != nil {
		return fmt.Errorf("failed_to_update_tpm_emulation_in_db: %w", err)
	}

	return nil
}
