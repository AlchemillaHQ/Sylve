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
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func TestUpdateVNC_UsesConfiguredBindAddress(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,fbuf,tcp=127.0.0.1:5900,w=640,h=480,password=oldpass"/></bhyve:commandline></domain>`

	updated, err := updateVNC(xml, 5901, "192.0.2.10", "800x600", "newpass", false, true)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if !strings.Contains(updated, "tcp=192.0.2.10:5901") {
		t.Fatalf("expected updated VNC bind+port in XML, got: %s", updated)
	}
}

func TestUpdateVNC_RemovesFramebufferArgWhenDisabled(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,fbuf,tcp=127.0.0.1:5900,w=640,h=480,password=oldpass"/></bhyve:commandline></domain>`

	updated, err := updateVNC(xml, 0, "127.0.0.1", "bad-resolution", "newpass", false, false)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Contains(updated, "fbuf,tcp=") {
		t.Fatalf("expected VNC arg to be removed when disabled, got: %s", updated)
	}
}

func TestCreateVmXML_OmitsVNCArgWhenDisabled(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:        "vm-no-vnc",
		RID:         100,
		CPUSockets:  1,
		CPUCores:    1,
		CPUThreads:  1,
		RAM:         1024 * 1024 * 512,
		VNCEnabled:  false,
		VNCPort:     0,
		VNCBind:     "127.0.0.1",
		VNCPassword: "password",
		// Intentionally invalid to ensure disabled VNC skips resolution parsing.
		VNCResolution: "invalid-resolution",
		TimeOffset:    vmModels.TimeOffsetUTC,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if strings.Contains(xml, "fbuf,tcp=") {
		t.Fatalf("expected no VNC framebuffer argument when disabled, got: %s", xml)
	}
}

func TestCreateVmXML_UsesConfiguredVNCBindAddress(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:          "vm-vnc-bind",
		RID:           101,
		CPUSockets:    1,
		CPUCores:      1,
		CPUThreads:    1,
		RAM:           1024 * 1024 * 512,
		VNCEnabled:    true,
		VNCPort:       5902,
		VNCBind:       "198.51.100.20",
		VNCPassword:   "password",
		VNCResolution: "640x480",
		TimeOffset:    vmModels.TimeOffsetUTC,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if !strings.Contains(xml, "tcp=198.51.100.20:5902") {
		t.Fatalf("expected configured VNC bind address in XML, got: %s", xml)
	}
}

func TestCreateVmXML_PrependsExtraBhyveOptionsBeforeGeneratedArgs(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:              "vm-extra-bhyve",
		RID:               102,
		CPUSockets:        1,
		CPUCores:          1,
		CPUThreads:        1,
		RAM:               1024 * 1024 * 512,
		VNCEnabled:        false,
		TimeOffset:        vmModels.TimeOffsetUTC,
		IgnoreUMSR:        true,
		ExtraBhyveOptions: []string{"-S", " -u \n\n -A "},
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	idxS := strings.Index(xml, `value="-S"`)
	idxU := strings.Index(xml, `value="-u"`)
	idxA := strings.Index(xml, `value="-A"`)
	idxW := strings.Index(xml, `value="-w"`)

	if idxS == -1 || idxU == -1 || idxA == -1 || idxW == -1 {
		t.Fatalf("expected all arguments in XML, got: %s", xml)
	}

	if !(idxS < idxU && idxU < idxA && idxA < idxW) {
		t.Fatalf("expected custom args in-order before generated args, got: %s", xml)
	}
}

func TestCreateVmXML_UsesUEFIBootROMByDefault(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:       "vm-bootrom-uefi",
		RID:        103,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		RAM:        1024 * 1024 * 512,
		VNCEnabled: false,
		TimeOffset: vmModels.TimeOffsetUTC,
		BootROM:    vmModels.VMBootROMUEFI,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if !strings.Contains(xml, uefiFirmwarePath) {
		t.Fatalf("expected UEFI firmware loader path in XML, got: %s", xml)
	}

	if !strings.Contains(xml, fmt.Sprintf("%d_vars.fd", vm.RID)) {
		t.Fatalf("expected UEFI vars path in XML, got: %s", xml)
	}
}

func TestCreateVmXML_OmitsLoaderWhenBootROMNone(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:       "vm-bootrom-none",
		RID:        105,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		RAM:        1024 * 1024 * 512,
		VNCEnabled: false,
		TimeOffset: vmModels.TimeOffsetUTC,
		BootROM:    vmModels.VMBootROMNone,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if strings.Contains(xml, "<loader") {
		t.Fatalf("expected no loader element when boot ROM is none, got: %s", xml)
	}
}

func TestNormalizeVNCBindAddressForDial_NormalizesUnspecified(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty default", in: "", want: "127.0.0.1"},
		{name: "ipv4 unspecified", in: "0.0.0.0", want: "127.0.0.1"},
		{name: "ipv6 unspecified", in: "::", want: "::1"},
		{name: "custom ipv4", in: "192.0.2.15", want: "192.0.2.15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeVNCBindAddressForDial(tt.in); got != tt.want {
				t.Fatalf("NormalizeVNCBindAddressForDial(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
