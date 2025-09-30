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

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/beevik/etree"
)

func (s *Service) ModifyWakeOnLan(vmId int, enabled bool) error {
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("vm_id = ?", vmId).
		Update("wo_l", enabled).Error
	return err
}

func (s *Service) ModifyBootOrder(vmId int, startAtBoot bool, bootOrder int) error {
	err := s.DB.
		Model(&vmModels.VM{}).
		Where("vm_id = ?", vmId).
		Updates(map[string]interface{}{
			"start_order":   bootOrder,
			"start_at_boot": startAtBoot,
		}).Error
	return err
}

func (s *Service) ModifyClock(vmId int, timeOffset string) error {
	if timeOffset != "utc" && timeOffset != "localtime" {
		return fmt.Errorf("invalid_time_offset: %s", timeOffset)
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", vmId)
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
		Where("vm_id = ?", vmId).
		Update("time_offset", timeOffset).Error; err != nil {
		return fmt.Errorf("failed_to_update_time_offset_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifySerial(vmId int, enabled bool) error {
	var pre vmModels.VM
	if err := s.DB.Model(&vmModels.VM{}).Where("vm_id = ?", vmId).First(&pre).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vm_from_db: %w", err)
	}

	if pre.Serial == enabled {
		return nil
	}

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("domain_state_not_shutoff: %d", vmId)
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

	master := "/dev/nmdm" + strconv.Itoa(vmId) + "A"

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
		sourceEl.CreateAttr("slave", "/dev/nmdm"+strconv.Itoa(vmId)+"B")
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
		Where("vm_id = ?", vmId).
		Update("serial", enabled).Error; err != nil {
		return fmt.Errorf("failed_to_update_serial_in_db: %w", err)
	}

	return nil
}
