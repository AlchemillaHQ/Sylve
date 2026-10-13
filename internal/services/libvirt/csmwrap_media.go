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

	"github.com/alchemillahq/sylve/internal/assets/csmwrap"
	"github.com/alchemillahq/sylve/internal/config"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/beevik/etree"
)

const csmwrapPCISlot = 9

func csmwrapMedia(vm vmModels.VM, used map[int]bool) (string, error) {
	if normalizeBootROMValue(vm.BootROM) != vmModels.VMBootROMUEFICSMWrap {
		return "", nil
	}
	if err := ValidateVMBootConfiguration(vm); err != nil {
		return "", err
	}
	if used[csmwrapPCISlot] {
		return "", fmt.Errorf("csmwrap_pci_slot_in_use: %d", csmwrapPCISlot)
	}
	base, err := config.GetDataPath()
	if err != nil {
		return "", err
	}
	path, err := csmwrap.Ensure(base)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(path, ",\r\n") {
		return "", fmt.Errorf("csmwrap_invalid_media_path")
	}
	used[csmwrapPCISlot] = true
	return fmt.Sprintf("-s %d:0,ahci-cd,%s,ro,bootindex=0", csmwrapPCISlot, path), nil
}

// Preserve existing PCI identities during edits, and give newly added native
// devices explicit addresses so libvirt cannot allocate the opaque helper slot.
func addressCSMWrapDocumentDevices(doc *etree.Document) error {
	loader := doc.FindElement("//os/loader")
	if loader == nil || !strings.Contains(loader.Text(), "_csmwrap_vars.fd") {
		return nil
	}
	for _, address := range doc.FindElements("//devices//address[@type='pci']") {
		slot, err := strconv.ParseInt(address.SelectAttrValue("slot", ""), 0, 32)
		if err == nil && slot == csmwrapPCISlot {
			return fmt.Errorf("csmwrap_pci_slot_in_use: %d", csmwrapPCISlot)
		}
	}
	used := parseUsedIndicesFromDocument(doc)
	used[csmwrapPCISlot] = true
	current := 2
	for _, device := range doc.FindElements("//devices/*") {
		switch device.Tag {
		case "controller":
			if device.SelectAttrValue("type", "") == "pci" {
				continue
			}
		case "interface", "filesystem", "video":
		default:
			continue
		}
		if device.FindElement("address") != nil {
			continue
		}
		slot, err := nextVMPCISlot(&current, used)
		if err != nil {
			return err
		}
		address := device.CreateElement("address")
		address.CreateAttr("type", "pci")
		address.CreateAttr("domain", "0x0000")
		address.CreateAttr("bus", "0x00")
		address.CreateAttr("slot", fmt.Sprintf("0x%02x", slot))
		address.CreateAttr("function", "0x0")
	}
	return nil
}
