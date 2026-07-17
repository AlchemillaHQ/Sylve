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
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/beevik/etree"
	goLibvirt "github.com/digitalocean/go-libvirt"
)

const (
	qgaTargetName          = "org.qemu.guest_agent.0"
	qgaControllerIndex     = 0
	qgaControllerPortCount = 16
	qgaDefaultPort         = 1
)

func qgaVirtioSerialController(slot int) libvirtServiceInterfaces.Controller {
	index := qgaControllerIndex
	return libvirtServiceInterfaces.Controller{
		Type:  "virtio-serial",
		Index: &index,
		Ports: qgaControllerPortCount,
		Address: &libvirtServiceInterfaces.Address{
			Type:     "pci",
			Domain:   "0x0000",
			Bus:      "0x00",
			Slot:     fmt.Sprintf("0x%02x", slot),
			Function: "0x0",
		},
	}
}

func qgaChannel(socketPath string) libvirtServiceInterfaces.Channel {
	return libvirtServiceInterfaces.Channel{
		Type: "unix",
		Source: libvirtServiceInterfaces.ChannelSource{
			Mode: "bind",
			Path: socketPath,
		},
		Target: libvirtServiceInterfaces.ChannelTarget{
			Type: "virtio",
			Name: qgaTargetName,
		},
		Address: libvirtServiceInterfaces.Address{
			Type:       "virtio-serial",
			Controller: strconv.Itoa(qgaControllerIndex),
			Bus:        "0",
			Port:       strconv.Itoa(qgaDefaultPort),
		},
	}
}

func updateQemuGuestAgentXML(domainXML, socketPath string, enabled bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		return "", fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return "", fmt.Errorf("invalid_domain_xml: root_missing")
	}

	legacySlot := -1
	for _, commandline := range doc.FindElements("//commandline") {
		if commandline.Space != "bhyve" {
			continue
		}

		for _, arg := range commandline.ChildElements() {
			slot, isQGA := legacyQGASlot(arg.SelectAttrValue("value", ""))
			if !isQGA {
				continue
			}
			if legacySlot < 0 {
				legacySlot = slot
			}
			commandline.RemoveChild(arg)
		}

		if len(commandline.ChildElements()) == 0 && commandline.Parent() != nil {
			commandline.Parent().RemoveChild(commandline)
		}
	}

	devices := root.FindElement("devices")
	if devices == nil {
		if !enabled {
			return doc.WriteToString()
		}
		devices = root.CreateElement("devices")
	}

	removedControllerIndices := make(map[string]bool)
	for _, channel := range devices.FindElements("channel") {
		if !isQGAChannelElement(channel) {
			continue
		}

		controllerIndex := strconv.Itoa(qgaControllerIndex)
		if address := channel.FindElement("address"); address != nil {
			controllerIndex = normalizeControllerIndex(address.SelectAttrValue("controller", controllerIndex))
		}
		removedControllerIndices[controllerIndex] = true
		devices.RemoveChild(channel)
	}

	if !enabled {
		removeUnusedVirtioSerialControllers(devices, removedControllerIndices)
		return doc.WriteToString()
	}

	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return "", fmt.Errorf("qga_socket_path_required")
	}

	preferredControllerIndex := qgaControllerIndex
	hasPreferredController := false
	for index := range removedControllerIndices {
		parsed, err := strconv.Atoi(index)
		if err != nil || parsed < 0 {
			continue
		}
		if !hasPreferredController || parsed < preferredControllerIndex {
			preferredControllerIndex = parsed
			hasPreferredController = true
		}
	}

	controller, controllerIndex, port := findVirtioSerialControllerWithCapacity(devices, preferredControllerIndex)
	if controller == nil {
		used := parseUsedIndicesFromDocument(doc)
		slot := legacySlot
		if slot < 0 || slot >= 32 || used[slot] {
			slot = firstFreePCIIndex(used)
		}
		if slot < 0 {
			return "", fmt.Errorf("no_free_indices_available_for_qemu_guest_agent")
		}
		controllerIndex = firstFreeVirtioSerialControllerIndex(devices)

		controller = devices.CreateElement("controller")
		controller.CreateAttr("type", "virtio-serial")
		controller.CreateAttr("index", strconv.Itoa(controllerIndex))
		controller.CreateAttr("ports", strconv.Itoa(qgaControllerPortCount))

		address := controller.CreateElement("address")
		address.CreateAttr("type", "pci")
		address.CreateAttr("domain", "0x0000")
		address.CreateAttr("bus", "0x00")
		address.CreateAttr("slot", fmt.Sprintf("0x%02x", slot))
		address.CreateAttr("function", "0x0")
		port = qgaDefaultPort
	}

	channel := devices.CreateElement("channel")
	channel.CreateAttr("type", "unix")

	source := channel.CreateElement("source")
	source.CreateAttr("mode", "bind")
	source.CreateAttr("path", socketPath)

	target := channel.CreateElement("target")
	target.CreateAttr("type", "virtio")
	target.CreateAttr("name", qgaTargetName)

	address := channel.CreateElement("address")
	address.CreateAttr("type", "virtio-serial")
	address.CreateAttr("controller", strconv.Itoa(controllerIndex))
	address.CreateAttr("bus", "0")
	address.CreateAttr("port", strconv.Itoa(port))

	return doc.WriteToString()
}

func legacyQGASlot(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "-s") ||
		!strings.Contains(value, "virtio-console") ||
		!strings.Contains(value, qgaTargetName+"=") {
		return -1, false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(value, "-s"))
	separator := strings.IndexAny(rest, ":,")
	if separator <= 0 {
		return -1, false
	}

	slot, err := strconv.Atoi(rest[:separator])
	if err != nil {
		return -1, false
	}
	return slot, true
}

func isQGAChannelElement(channel *etree.Element) bool {
	if channel == nil || channel.Tag != "channel" {
		return false
	}
	target := channel.FindElement("target")
	return target != nil && target.SelectAttrValue("name", "") == qgaTargetName
}

func qgaXMLNeedsUpdate(domainXML string, enabled bool) (bool, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		return false, fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	hasLegacyArgument := false
	for _, commandline := range doc.FindElements("//commandline") {
		if commandline.Space != "bhyve" {
			continue
		}
		for _, arg := range commandline.ChildElements() {
			if _, isQGA := legacyQGASlot(arg.SelectAttrValue("value", "")); isQGA {
				hasLegacyArgument = true
				break
			}
		}
	}

	hasNativeChannel := false
	if root := doc.Root(); root != nil {
		if devices := root.FindElement("devices"); devices != nil {
			for _, channel := range devices.FindElements("channel") {
				if isQGAChannelElement(channel) {
					hasNativeChannel = true
					break
				}
			}
		}
	}

	if enabled {
		return hasLegacyArgument || !hasNativeChannel, nil
	}
	return hasLegacyArgument || hasNativeChannel, nil
}

func qgaXMLSupportsNativeReboot(domainXML string) (bool, error) {
	needsUpdate, err := qgaXMLNeedsUpdate(domainXML, true)
	if err != nil {
		return false, err
	}
	if needsUpdate {
		return false, nil
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		return false, fmt.Errorf("failed_to_parse_xml: %w", err)
	}
	root := doc.Root()
	if root == nil {
		return false, fmt.Errorf("invalid_domain_xml: root_missing")
	}

	onReboot := root.FindElement("on_reboot")
	if onReboot == nil {
		return true, nil
	}
	switch strings.TrimSpace(onReboot.Text()) {
	case "restart", "rename-restart":
		return true, nil
	default:
		return false, nil
	}
}

func normalizeControllerIndex(index string) string {
	parsed, err := strconv.ParseInt(strings.TrimSpace(index), 0, 32)
	if err != nil {
		return strings.TrimSpace(index)
	}
	return strconv.FormatInt(parsed, 10)
}

func findVirtioSerialController(devices *etree.Element, index int) *etree.Element {
	wanted := strconv.Itoa(index)
	for _, controller := range devices.FindElements("controller") {
		if controller.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}
		if normalizeControllerIndex(controller.SelectAttrValue("index", "0")) == wanted {
			return controller
		}
	}
	return nil
}

func findVirtioSerialControllerWithCapacity(devices *etree.Element, preferredIndex int) (*etree.Element, int, int) {
	if controller := findVirtioSerialController(devices, preferredIndex); controller != nil {
		if port := firstFreeVirtioSerialPort(devices, preferredIndex, virtioSerialControllerPortCount(controller)); port >= 0 {
			return controller, preferredIndex, port
		}
	}

	for _, controller := range devices.FindElements("controller") {
		if controller.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}
		indexValue := normalizeControllerIndex(controller.SelectAttrValue("index", "0"))
		index, err := strconv.Atoi(indexValue)
		if err != nil || index < 0 || index == preferredIndex {
			continue
		}
		if port := firstFreeVirtioSerialPort(devices, index, virtioSerialControllerPortCount(controller)); port >= 0 {
			return controller, index, port
		}
	}

	return nil, -1, -1
}

func virtioSerialControllerPortCount(controller *etree.Element) int {
	if controller != nil {
		if count, err := strconv.Atoi(controller.SelectAttrValue("ports", "")); err == nil && count > 0 {
			return count
		}
	}
	return qgaControllerPortCount
}

func firstFreeVirtioSerialControllerIndex(devices *etree.Element) int {
	used := make(map[int]bool)
	for _, controller := range devices.FindElements("controller") {
		if controller.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}
		indexValue := normalizeControllerIndex(controller.SelectAttrValue("index", "0"))
		if index, err := strconv.Atoi(indexValue); err == nil && index >= 0 {
			used[index] = true
		}
	}

	for index := 0; ; index++ {
		if !used[index] {
			return index
		}
	}
}

func removeUnusedVirtioSerialControllers(devices *etree.Element, candidates map[string]bool) {
	for _, controller := range devices.FindElements("controller") {
		if controller.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}

		index := normalizeControllerIndex(controller.SelectAttrValue("index", "0"))
		if !candidates[index] || virtioSerialControllerInUse(devices, index) {
			continue
		}
		devices.RemoveChild(controller)
	}
}

func virtioSerialControllerInUse(devices *etree.Element, controllerIndex string) bool {
	for _, device := range devices.ChildElements() {
		if device.Tag == "controller" {
			continue
		}
		address := device.FindElement("address")
		if address == nil || address.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}
		if normalizeControllerIndex(address.SelectAttrValue("controller", "0")) == controllerIndex {
			return true
		}
	}
	return false
}

func firstFreePCIIndex(used map[int]bool) int {
	for index := 10; index < 30; index++ {
		if !used[index] {
			return index
		}
	}
	return -1
}

func firstFreeVirtioSerialPort(devices *etree.Element, controllerIndex, portCount int) int {
	used := make(map[int]bool)
	wanted := strconv.Itoa(controllerIndex)

	for _, channel := range devices.FindElements("channel") {
		address := channel.FindElement("address")
		if address == nil || address.SelectAttrValue("type", "") != "virtio-serial" {
			continue
		}
		if normalizeControllerIndex(address.SelectAttrValue("controller", "0")) != wanted {
			continue
		}
		if port, err := strconv.Atoi(address.SelectAttrValue("port", "")); err == nil {
			used[port] = true
		}
	}

	for port := qgaDefaultPort; port < portCount; port++ {
		if !used[port] {
			return port
		}
	}
	return -1
}

func (s *Service) ensureQemuGuestAgentNativeXML(domain goLibvirt.Domain, vm vmModels.VM) (bool, error) {
	domainXML, err := s.conn().DomainGetXMLDesc(domain, 0)
	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	needsUpdate, err := qgaXMLNeedsUpdate(domainXML, vm.QemuGuestAgent)
	if err != nil {
		return false, err
	}
	if !needsUpdate {
		return false, nil
	}

	socketPath := ""
	if vm.QemuGuestAgent {
		dataPath, err := s.GetVMConfigDirectory(vm.RID)
		if err != nil {
			return false, fmt.Errorf("failed_to_get_vm_data_path: %w", err)
		}
		socketPath = filepath.Join(dataPath, "qga.sock")
	}

	updatedXML, err := updateQemuGuestAgentXML(domainXML, socketPath, vm.QemuGuestAgent)
	if err != nil {
		return false, err
	}
	if _, err := s.conn().DomainDefineXML(updatedXML); err != nil {
		return false, fmt.Errorf("failed_to_define_domain_with_native_qga: %w", err)
	}

	return true, nil
}

func (s *Service) MigrateQemuGuestAgentToNativeFormat() error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	vms, err := s.ListVMs()
	if err != nil {
		return fmt.Errorf("failed_to_list_vms_for_qga_migration: %w", err)
	}

	for _, vm := range vms {
		migrationName := fmt.Sprintf("qga_native_xml_format_1_%d", vm.RID)

		var count int64
		if err := s.DB.Table("migrations").Where("name = ?", migrationName).Count(&count).Error; err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("qga_migration: failed to check per-VM migration record")
			continue
		}
		if count > 0 {
			continue
		}

		domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(vm.RID)))
		if err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("qga_migration: failed to lookup domain")
			continue
		}

		state, _, err := s.conn().DomainGetState(domain, 0)
		if err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("qga_migration: failed to get domain state")
			continue
		}
		if state != int32(goLibvirt.DomainShutoff) {
			continue
		}

		migrated, err := s.ensureQemuGuestAgentNativeXML(domain, vm)
		if err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("qga_migration: failed to reconcile QGA XML")
			continue
		}

		if err := s.DB.Table("migrations").Create(map[string]any{"name": migrationName}).Error; err != nil {
			logger.L.Warn().Uint("rid", vm.RID).Err(err).Msg("qga_migration: failed to record per-VM migration")
			continue
		}

		if migrated {
			logger.L.Info().Uint("rid", vm.RID).Msg("qga_migration: migrated to native XML format")
		}
	}

	return nil
}
