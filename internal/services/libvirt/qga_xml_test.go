// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"path/filepath"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/beevik/etree"
)

func TestCreateVmXMLProducesNativeQGAChannel(t *testing.T) {
	vmPath := t.TempDir()
	vm := vmModels.VM{
		RID:            100,
		CPUSockets:     1,
		CPUCores:       1,
		CPUThreads:     1,
		RAM:            512 * 1024 * 1024,
		TimeOffset:     vmModels.TimeOffsetUTC,
		QemuGuestAgent: true,
	}

	domainXML, err := (&Service{}).CreateVmXML(vm, vmPath)
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}
	if strings.Contains(domainXML, "virtio-console") || strings.Contains(domainXML, qgaTargetName+"=") {
		t.Fatalf("expected no legacy QGA bhyve argument, got: %s", domainXML)
	}

	doc := mustParseQGATestXML(t, domainXML)
	controller := findQGATestController(doc.Root().FindElement("devices"))
	if controller == nil {
		t.Fatalf("expected virtio-serial controller, got: %s", domainXML)
	}
	if controller.SelectAttrValue("index", "") != "0" || controller.SelectAttrValue("ports", "") != "16" {
		t.Fatalf("unexpected QGA controller attributes: %s", domainXML)
	}
	if address := controller.FindElement("address"); address == nil || address.SelectAttrValue("slot", "") != "0x0a" {
		t.Fatalf("expected QGA controller at PCI slot 10, got: %s", domainXML)
	}

	channel := findQGATestChannel(doc.Root().FindElement("devices"))
	if channel == nil {
		t.Fatalf("expected native QGA channel, got: %s", domainXML)
	}
	if source := channel.FindElement("source"); source == nil ||
		source.SelectAttrValue("mode", "") != "bind" ||
		source.SelectAttrValue("path", "") != filepath.Join(vmPath, "qga.sock") {
		t.Fatalf("unexpected QGA channel source: %s", domainXML)
	}
	if address := channel.FindElement("address"); address == nil ||
		address.SelectAttrValue("controller", "") != "0" ||
		address.SelectAttrValue("port", "") != "1" {
		t.Fatalf("unexpected QGA channel address: %s", domainXML)
	}
}

func TestUpdateQemuGuestAgentXMLMigratesLegacyArgument(t *testing.T) {
	domainXML := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices><controller type="usb" model="nec-xhci"/></devices><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-s 12:0,virtio-console,org.qemu.guest_agent.0=/old/qga.sock"/><bhyve:arg value="-s 13:0,ahci-hd,/disk.img"/></bhyve:commandline></domain>`

	updated, err := updateQemuGuestAgentXML(domainXML, "/vm/100/qga.sock", true)
	if err != nil {
		t.Fatalf("updateQemuGuestAgentXML returned error: %v", err)
	}
	if strings.Contains(updated, "virtio-console") || strings.Contains(updated, qgaTargetName+"=") {
		t.Fatalf("expected legacy QGA argument to be removed, got: %s", updated)
	}
	if !strings.Contains(updated, `value="-S"`) || !strings.Contains(updated, `value="-s 13:0,ahci-hd,/disk.img"`) {
		t.Fatalf("expected unrelated bhyve arguments to be preserved, got: %s", updated)
	}

	doc := mustParseQGATestXML(t, updated)
	controller := findQGATestController(doc.Root().FindElement("devices"))
	if controller == nil {
		t.Fatalf("expected virtio-serial controller, got: %s", updated)
	}
	if address := controller.FindElement("address"); address == nil || address.SelectAttrValue("slot", "") != "0x0c" {
		t.Fatalf("expected legacy PCI slot 12 to be preserved, got: %s", updated)
	}
	channel := findQGATestChannel(doc.Root().FindElement("devices"))
	if channel == nil {
		t.Fatalf("expected native QGA channel, got: %s", updated)
	}
	if source := channel.FindElement("source"); source == nil || source.SelectAttrValue("path", "") != "/vm/100/qga.sock" {
		t.Fatalf("expected native QGA channel with updated socket path, got: %s", updated)
	}
}

func TestUpdateQemuGuestAgentXMLAvoidsNativeAndRawPCISlots(t *testing.T) {
	domainXML := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices><controller type="usb"><address type="pci" domain="0x0000" bus="0x00" slot="0x0a" function="0x0"/></controller></devices><bhyve:commandline><bhyve:arg value="-s 11:0,ahci-hd,/disk.img"/></bhyve:commandline></domain>`

	updated, err := updateQemuGuestAgentXML(domainXML, "/vm/100/qga.sock", true)
	if err != nil {
		t.Fatalf("updateQemuGuestAgentXML returned error: %v", err)
	}

	doc := mustParseQGATestXML(t, updated)
	controller := findQGATestController(doc.Root().FindElement("devices"))
	if controller == nil {
		t.Fatalf("expected virtio-serial controller, got: %s", updated)
	}
	if address := controller.FindElement("address"); address == nil || address.SelectAttrValue("slot", "") != "0x0c" {
		t.Fatalf("expected first unused PCI slot 12, got: %s", updated)
	}
}

func TestUpdateQemuGuestAgentXMLDisablesNativeChannel(t *testing.T) {
	domainXML := `<domain type="bhyve"><devices><controller type="usb"/><controller type="virtio-serial" index="0" ports="16"><address type="pci" slot="0x0a"/></controller><channel type="unix"><source mode="bind" path="/vm/100/qga.sock"/><target type="virtio" name="org.qemu.guest_agent.0"/><address type="virtio-serial" controller="0" bus="0" port="1"/></channel></devices></domain>`

	updated, err := updateQemuGuestAgentXML(domainXML, "", false)
	if err != nil {
		t.Fatalf("updateQemuGuestAgentXML returned error: %v", err)
	}
	if strings.Contains(updated, qgaTargetName) || strings.Contains(updated, `type="virtio-serial"`) {
		t.Fatalf("expected QGA channel and unused controller to be removed, got: %s", updated)
	}
	if !strings.Contains(updated, `type="usb"`) {
		t.Fatalf("expected unrelated controller to be preserved, got: %s", updated)
	}
}

func TestUpdateQemuGuestAgentXMLPreservesSharedController(t *testing.T) {
	domainXML := `<domain type="bhyve"><devices><controller type="virtio-serial" index="0" ports="16"><address type="pci" slot="0x0a"/></controller><channel type="unix"><source mode="bind" path="/vm/100/qga.sock"/><target type="virtio" name="org.qemu.guest_agent.0"/><address type="virtio-serial" controller="0" bus="0" port="1"/></channel><channel type="unix"><source mode="bind" path="/vm/100/console.sock"/><target type="virtio" name="org.example.console"/><address type="virtio-serial" controller="0" bus="0" port="2"/></channel></devices></domain>`

	updated, err := updateQemuGuestAgentXML(domainXML, "", false)
	if err != nil {
		t.Fatalf("updateQemuGuestAgentXML returned error: %v", err)
	}
	if strings.Contains(updated, qgaTargetName) {
		t.Fatalf("expected QGA channel to be removed, got: %s", updated)
	}
	if !strings.Contains(updated, `name="org.example.console"`) || !strings.Contains(updated, `type="virtio-serial"`) {
		t.Fatalf("expected shared controller and unrelated channel to be preserved, got: %s", updated)
	}
}

func TestUpdateQemuGuestAgentXMLCreatesControllerWhenExistingOneIsFull(t *testing.T) {
	domainXML := `<domain type="bhyve"><devices><controller type="virtio-serial" index="0" ports="1"><address type="pci" slot="0x0a"/></controller></devices></domain>`

	updated, err := updateQemuGuestAgentXML(domainXML, "/vm/100/qga.sock", true)
	if err != nil {
		t.Fatalf("updateQemuGuestAgentXML returned error: %v", err)
	}

	doc := mustParseQGATestXML(t, updated)
	devices := doc.Root().FindElement("devices")
	channel := findQGATestChannel(devices)
	if channel == nil {
		t.Fatalf("expected native QGA channel, got: %s", updated)
	}
	if address := channel.FindElement("address"); address == nil || address.SelectAttrValue("controller", "") != "1" {
		t.Fatalf("expected QGA channel on new controller index 1, got: %s", updated)
	}
	controller := findVirtioSerialController(devices, 1)
	if controller == nil {
		t.Fatalf("expected new virtio-serial controller, got: %s", updated)
	}
	if address := controller.FindElement("address"); address == nil || address.SelectAttrValue("slot", "") != "0x0b" {
		t.Fatalf("expected new controller at PCI slot 11, got: %s", updated)
	}
}

func TestParseUsedIndicesFromDocumentIncludesNativePCIAddresses(t *testing.T) {
	doc := mustParseQGATestXML(t, `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices><controller><address type="pci" slot="0x0c"/></controller></devices><bhyve:commandline><bhyve:arg value="-s 13:0,ahci-hd,/disk.img"/></bhyve:commandline></domain>`)

	used := parseUsedIndicesFromDocument(doc)
	if !used[12] || !used[13] {
		t.Fatalf("expected native slot 12 and raw slot 13 to be marked used, got: %#v", used)
	}
}

func TestQGAXMLNeedsUpdate(t *testing.T) {
	legacy := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices/><bhyve:commandline><bhyve:arg value="-s 10,virtio-console,org.qemu.guest_agent.0=/vm/qga.sock"/></bhyve:commandline></domain>`
	native := `<domain><devices><channel type="unix"><source mode="bind" path="/vm/qga.sock"/><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	disabled := `<domain><devices/></domain>`

	tests := []struct {
		name    string
		xml     string
		enabled bool
		want    bool
	}{
		{name: "migrate legacy enabled", xml: legacy, enabled: true, want: true},
		{name: "keep native enabled", xml: native, enabled: true, want: false},
		{name: "remove native disabled", xml: native, enabled: false, want: true},
		{name: "keep absent disabled", xml: disabled, enabled: false, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := qgaXMLNeedsUpdate(test.xml, test.enabled)
			if err != nil {
				t.Fatalf("qgaXMLNeedsUpdate returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("qgaXMLNeedsUpdate = %t, want %t", got, test.want)
			}
		})
	}
}

func TestQGAXMLSupportsNativeReboot(t *testing.T) {
	native := `<domain><devices><channel type="unix"><source mode="bind" path="/vm/qga.sock"/><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	nativeRestart := `<domain><on_reboot>restart</on_reboot><devices><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	nativeRestartRename := `<domain><on_reboot>rename-restart</on_reboot><devices><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	nativeDestroy := `<domain><on_reboot>destroy</on_reboot><devices><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	nativePreserve := `<domain><on_reboot>preserve</on_reboot><devices><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`
	legacy := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices/><bhyve:commandline><bhyve:arg value="-s 10,virtio-console,org.qemu.guest_agent.0=/vm/qga.sock"/></bhyve:commandline></domain>`
	partial := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices><bhyve:commandline><bhyve:arg value="-s 10,virtio-console,org.qemu.guest_agent.0=/vm/qga.sock"/></bhyve:commandline></domain>`

	tests := []struct {
		name string
		xml  string
		want bool
	}{
		{name: "native default restart", xml: native, want: true},
		{name: "native explicit restart", xml: nativeRestart, want: true},
		{name: "native restart rename", xml: nativeRestartRename, want: true},
		{name: "native destroy", xml: nativeDestroy, want: false},
		{name: "native preserve", xml: nativePreserve, want: false},
		{name: "legacy", xml: legacy, want: false},
		{name: "partial migration", xml: partial, want: false},
		{name: "no agent", xml: `<domain><devices/></domain>`, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := qgaXMLSupportsNativeReboot(test.xml)
			if err != nil {
				t.Fatalf("qgaXMLSupportsNativeReboot returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("qgaXMLSupportsNativeReboot = %t, want %t", got, test.want)
			}
		})
	}

	if _, err := qgaXMLSupportsNativeReboot(`<domain>`); err == nil {
		t.Fatal("expected malformed domain XML to fail")
	}
}

func mustParseQGATestXML(t *testing.T, value string) *etree.Document {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(value); err != nil {
		t.Fatalf("failed to parse test XML: %v", err)
	}
	return doc
}

func findQGATestController(devices *etree.Element) *etree.Element {
	if devices == nil {
		return nil
	}
	for _, controller := range devices.FindElements("controller") {
		if controller.SelectAttrValue("type", "") == "virtio-serial" {
			return controller
		}
	}
	return nil
}

func findQGATestChannel(devices *etree.Element) *etree.Element {
	if devices == nil {
		return nil
	}
	for _, channel := range devices.FindElements("channel") {
		if isQGAChannelElement(channel) {
			return channel
		}
	}
	return nil
}
