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

func TestCreateVmXML(t *testing.T) {
	tests := []struct {
		name    string
		vm      vmModels.VM // only the fields under test; CPU, RAM and time offset come from the base
		want    []string
		inOrder []string // must appear in this order
		notWant []string
	}{
		{
			name: "omits vnc when disabled",
			// an invalid resolution proves disabled VNC skips resolution parsing
			vm:      vmModels.VM{RID: 100, VNCBind: "127.0.0.1", VNCPassword: "password", VNCResolution: "invalid-resolution"},
			notWant: []string{"fbuf,tcp=", "<graphics"},
		},
		{
			name: "native vnc format",
			vm: vmModels.VM{
				RID: 101, VNCEnabled: true, VNCPort: 5902, VNCBind: "198.51.100.20", VNCPassword: "password", VNCResolution: "640x480",
			},
			want: []string{
				`<graphics type="vnc"`, `port="5902"`, `address="198.51.100.20"`, `passwd="password"`,
				`<video>`, `<resolution x="640" y="480"`,
			},
			notWant: []string{"fbuf,tcp"},
		},
		{
			name:    "omits passwd attribute when password empty",
			vm:      vmModels.VM{RID: 110, VNCEnabled: true, VNCPort: 5905, VNCBind: "127.0.0.1", VNCResolution: "800x600"},
			want:    []string{`<graphics type="vnc"`},
			notWant: []string{`passwd=`},
		},
		{
			name:    "prepends extra bhyve options before generated args",
			vm:      vmModels.VM{RID: 102, IgnoreUMSR: true, ExtraBhyveOptions: []string{"-S", " -u \n\n -A "}},
			inOrder: []string{`value="-S"`, `value="-u"`, `value="-A"`},
			want:    []string{`<msrs unknown="ignore"`},
		},
		{
			name: "uefi boot rom",
			vm:   vmModels.VM{RID: 103, BootROM: vmModels.VMBootROMUEFI},
			want: []string{uefiFirmwarePath, "103_vars.fd"},
		},
		{
			name:    "no loader when boot rom none",
			vm:      vmModels.VM{RID: 105, BootROM: vmModels.VMBootROMNone},
			notWant: []string{"<loader"},
		},
		{
			name:    "native ignore umsr format",
			vm:      vmModels.VM{RID: 201, IgnoreUMSR: true},
			want:    []string{`<msrs unknown="ignore"`},
			notWant: []string{`value="-w"`},
		},
		{
			name:    "no msrs when ignore umsr disabled",
			vm:      vmModels.VM{RID: 202},
			notWant: []string{`value="-w"`, `<msrs unknown="ignore"`},
		},
		{
			name: "msrs and apic",
			vm:   vmModels.VM{RID: 203, IgnoreUMSR: true, APIC: true},
			want: []string{`<msrs unknown="ignore"`, `<apic`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vm := tt.vm
			vm.Name = fmt.Sprintf("vm-%d", vm.RID)
			vm.CPUSockets, vm.CPUCores, vm.CPUThreads = 1, 1, 1
			vm.RAM = 1024 * 1024 * 512
			vm.TimeOffset = vmModels.TimeOffsetUTC

			xml, err := (&Service{}).CreateVmXML(vm, t.TempDir())
			if err != nil {
				t.Fatalf("CreateVmXML returned error: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(xml, want) {
					t.Fatalf("expected %q in XML, got: %s", want, xml)
				}
			}
			last := -1
			for _, want := range tt.inOrder {
				idx := strings.Index(xml, want)
				if idx <= last {
					t.Fatalf("expected %v in order, got: %s", tt.inOrder, xml)
				}
				last = idx
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(xml, notWant) {
					t.Fatalf("did not expect %q in XML, got: %s", notWant, xml)
				}
			}
		})
	}
}
