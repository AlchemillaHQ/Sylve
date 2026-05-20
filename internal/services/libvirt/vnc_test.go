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

func TestUpdateVNC_MigratesOldFormatToNative(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,fbuf,tcp=127.0.0.1:5900,w=640,h=480,password=oldpass"/></bhyve:commandline></domain>`

	updated, err := updateVNC(xml, 5901, "192.0.2.10", "800x600", "newpass", false, true)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Contains(updated, "fbuf,tcp") {
		t.Fatalf("expected old fbuf arg to be removed, got: %s", updated)
	}

	if !strings.Contains(updated, `<graphics type="vnc"`) {
		t.Fatalf("expected native graphics element, got: %s", updated)
	}

	if !strings.Contains(updated, `port="5901"`) {
		t.Fatalf("expected VNC port in graphics element, got: %s", updated)
	}

	if !strings.Contains(updated, `address="192.0.2.10"`) {
		t.Fatalf("expected VNC bind address in listen element, got: %s", updated)
	}

	if !strings.Contains(updated, `passwd="newpass"`) {
		t.Fatalf("expected VNC password in graphics element, got: %s", updated)
	}

	if !strings.Contains(updated, `<video>`) {
		t.Fatalf("expected video element, got: %s", updated)
	}

	if !strings.Contains(updated, `<model type="gop"`) {
		t.Fatalf("expected gop video model, got: %s", updated)
	}

	if !strings.Contains(updated, `<resolution x="800" y="600"`) {
		t.Fatalf("expected video resolution, got: %s", updated)
	}
}

func TestUpdateVNC_UpdatesAlreadyNativeFormat(t *testing.T) {
	xml := `<domain type="bhyve"><devices><graphics type="vnc" port="5900" passwd="oldpass"><listen type="address" address="127.0.0.1"/></graphics><video><model type="gop" heads="1" primary="yes"><resolution x="640" y="480"/></model></video></devices></domain>`

	updated, err := updateVNC(xml, 5902, "192.0.2.30", "1024x768", "newpass", true, true)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Count(updated, `<graphics type="vnc"`) != 1 {
		t.Fatalf("expected exactly one graphics element, got: %s", updated)
	}

	if strings.Count(updated, `<video>`) != 1 {
		t.Fatalf("expected exactly one video element, got: %s", updated)
	}

	if !strings.Contains(updated, `port="5902"`) {
		t.Fatalf("expected updated VNC port, got: %s", updated)
	}

	if !strings.Contains(updated, `address="192.0.2.30"`) {
		t.Fatalf("expected updated VNC bind address, got: %s", updated)
	}

	if !strings.Contains(updated, `passwd="newpass"`) {
		t.Fatalf("expected updated VNC password, got: %s", updated)
	}

	if !strings.Contains(updated, `wait="yes"`) {
		t.Fatalf("expected wait attribute, got: %s", updated)
	}

	if !strings.Contains(updated, `<resolution x="1024" y="768"`) {
		t.Fatalf("expected updated video resolution, got: %s", updated)
	}
}

func TestUpdateVNC_RemovesVNCWhenDisabled_OldFormat(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,fbuf,tcp=127.0.0.1:5900,w=640,h=480,password=oldpass"/></bhyve:commandline></domain>`

	updated, err := updateVNC(xml, 0, "127.0.0.1", "bad-resolution", "newpass", false, false)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Contains(updated, "fbuf,tcp") {
		t.Fatalf("expected old fbuf arg to be removed, got: %s", updated)
	}

	if strings.Contains(updated, `<graphics type="vnc"`) {
		t.Fatalf("expected no graphics element when VNC disabled, got: %s", updated)
	}
}

func TestUpdateVNC_RemovesVNCWhenDisabled_NativeFormat(t *testing.T) {
	xml := `<domain type="bhyve"><devices><graphics type="vnc" port="5900" passwd="oldpass"><listen type="address" address="127.0.0.1"/></graphics><video><model type="gop" heads="1" primary="yes"><resolution x="640" y="480"/></model></video></devices></domain>`

	updated, err := updateVNC(xml, 0, "127.0.0.1", "bad-resolution", "newpass", false, false)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Contains(updated, `<graphics type="vnc"`) {
		t.Fatalf("expected no graphics element when VNC disabled, got: %s", updated)
	}

	if strings.Contains(updated, `<video>`) {
		t.Fatalf("expected no video element when VNC disabled, got: %s", updated)
	}
}

func TestCreateVmXML_OmitsVNCWhenDisabled(t *testing.T) {
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

	if strings.Contains(xml, "<graphics") {
		t.Fatalf("expected no graphics element when VNC disabled, got: %s", xml)
	}
}

func TestCreateVmXML_ProducesNativeVNCFormat(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:          "vm-vnc-native",
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

	if strings.Contains(xml, "fbuf,tcp") {
		t.Fatalf("expected no fbuf arg, got native format: %s", xml)
	}

	if !strings.Contains(xml, `<graphics type="vnc"`) {
		t.Fatalf("expected native graphics element, got: %s", xml)
	}

	if !strings.Contains(xml, `port="5902"`) {
		t.Fatalf("expected configured VNC port in graphics element, got: %s", xml)
	}

	if !strings.Contains(xml, `address="198.51.100.20"`) {
		t.Fatalf("expected configured VNC bind address in listen element, got: %s", xml)
	}

	if !strings.Contains(xml, `passwd="password"`) {
		t.Fatalf("expected VNC password in graphics element, got: %s", xml)
	}

	if !strings.Contains(xml, `<video>`) {
		t.Fatalf("expected video element, got: %s", xml)
	}

	if !strings.Contains(xml, `<resolution x="640" y="480"`) {
		t.Fatalf("expected video resolution, got: %s", xml)
	}
}

func TestCreateVmXML_OmitsPasswdAttrWhenPasswordEmpty(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:          "vm-vnc-empty-pass",
		RID:           110,
		CPUSockets:    1,
		CPUCores:      1,
		CPUThreads:    1,
		RAM:           1024 * 1024 * 512,
		VNCEnabled:    true,
		VNCPort:       5905,
		VNCBind:       "127.0.0.1",
		VNCPassword:   "",
		VNCResolution: "800x600",
		TimeOffset:    vmModels.TimeOffsetUTC,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if strings.Contains(xml, `passwd=`) {
		t.Fatalf("expected no passwd attribute when password empty, got: %s", xml)
	}

	if !strings.Contains(xml, `<graphics type="vnc"`) {
		t.Fatalf("expected graphics element despite empty password, got: %s", xml)
	}
}

func TestUpdateVNC_PreservesNonVNCBhyveArgs(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices/><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-u"/><bhyve:arg value="-s 10:0,fbuf,tcp=127.0.0.1:5900,w=640,h=480,password=oldpass"/><bhyve:arg value="-w"/></bhyve:commandline></domain>`

	updated, err := updateVNC(xml, 5901, "192.0.2.10", "800x600", "newpass", false, true)
	if err != nil {
		t.Fatalf("updateVNC returned error: %v", err)
	}

	if strings.Contains(updated, "fbuf,tcp") {
		t.Fatalf("expected fbuf arg to be removed, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-S"`) {
		t.Fatalf("expected non-VNC arg -S to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-u"`) {
		t.Fatalf("expected non-VNC arg -u to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-w"`) {
		t.Fatalf("expected non-VNC arg -w to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `<graphics type="vnc"`) {
		t.Fatalf("expected native graphics element after migration, got: %s", updated)
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

	if idxS == -1 || idxU == -1 || idxA == -1 {
		t.Fatalf("expected all extra bhyve arguments in XML, got: %s", xml)
	}

	if !(idxS < idxU && idxU < idxA) {
		t.Fatalf("expected custom args in-order before generated args, got: %s", xml)
	}

	if !strings.Contains(xml, `<msrs unknown="ignore"`) {
		t.Fatalf("expected msrs element in features for ignoreUMSR, got: %s", xml)
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
