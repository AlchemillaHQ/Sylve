// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"reflect"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/beevik/etree"
	"gorm.io/gorm"
)

func newCPUPinValidationTestDB(t *testing.T) *gorm.DB {
	return testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMCPUPinning{})
}

func seedPinnedVM(t *testing.T, db *gorm.DB, rid uint, socket int, cores []int) {
	t.Helper()

	vm := vmModels.VM{
		RID:  rid,
		Name: "seed-vm",
	}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	pin := vmModels.VMCPUPinning{
		VMID:       vm.ID,
		HostSocket: socket,
		HostCPU:    cores,
	}
	if err := db.Create(&pin).Error; err != nil {
		t.Fatalf("failed to seed vm cpu pinning: %v", err)
	}
}

func TestValidateCPUPins_AllowsSameLocalIndexAcrossDifferentSockets(t *testing.T) {
	db := newCPUPinValidationTestDB(t)

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 2,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{10}},
			{Socket: 1, Cores: []int{10}},
		},
	}

	if err := validateCPUPins(db, req, 32, 2, 16); err != nil {
		t.Fatalf("expected pinning to be valid, got error: %v", err)
	}
}

func TestValidateCPUPins_DoesNotConflictAcrossDifferentSocketsForSameLocalCore(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{10})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 1, Cores: []int{10}},
		},
	}

	if err := validateCPUPins(db, req, 32, 2, 16); err != nil {
		t.Fatalf("expected no conflict for different socket/global core, got error: %v", err)
	}
}

func TestValidateCPUPins_ConflictsOnSameGlobalCore(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{10})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{10}},
		},
	}

	err := validateCPUPins(db, req, 32, 2, 16)
	if err == nil {
		t.Fatalf("expected core conflict, got nil")
	}

	if !strings.Contains(err.Error(), "core_conflict: core=10 already_pinned_by_rid=116") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCPUPins_SingleSocketUsesLocalCoreIndices(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 116, 0, []int{2, 5})

	rid := uint(200)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   4,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 0, Cores: []int{0, 1, 3, 4}},
		},
	}

	if err := validateCPUPins(db, req, 8, 1, 8); err != nil {
		t.Fatalf("expected valid single-socket pinning, got error: %v", err)
	}
}

func TestValidateCPUPins_DualSocketScenarioFromProductionPayload(t *testing.T) {
	db := newCPUPinValidationTestDB(t)
	seedPinnedVM(t, db, 121, 0, []int{32, 33, 34, 35, 36, 37, 38, 39})

	rid := uint(107)
	req := libvirtServiceInterfaces.CreateVMRequest{
		RID:        &rid,
		CPUSockets: 1,
		CPUCores:   32,
		CPUThreads: 1,
		CPUPinning: []libvirtServiceInterfaces.CPUPinning{
			{Socket: 1, Cores: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39}},
		},
	}

	if err := validateCPUPins(db, req, 80, 2, 40); err != nil {
		t.Fatalf("expected production dual-socket payload to be valid, got error: %v", err)
	}
}

func TestUpdateRequestedCPUXMLUsesRequestedTopologyAndPersistedPins(t *testing.T) {
	const currentXML = `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0">
		<vcpu>2</vcpu>
		<cpu><topology sockets="1" cores="2" threads="1"/></cpu>
		<bhyve:commandline><bhyve:arg value="-p 0:0"/></bhyve:commandline>
	</domain>`
	req := libvirtServiceInterfaces.ModifyCPURequest{
		CPUSockets: 2,
		CPUCores:   3,
		CPUThreads: 2,
	}

	updatedXML, err := (&Service{}).updateRequestedCPUXML(currentXML, req, nil)
	if err != nil {
		t.Fatalf("update requested CPU XML: %v", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(updatedXML); err != nil {
		t.Fatalf("parse updated CPU XML: %v", err)
	}
	if got := strings.TrimSpace(doc.FindElement("//vcpu").Text()); got != "12" {
		t.Fatalf("vcpu = %q, want 12", got)
	}
	topology := doc.FindElement("//cpu/topology")
	if topology == nil {
		t.Fatal("updated CPU topology is missing")
	}
	if got := topology.SelectAttrValue("sockets", ""); got != "2" {
		t.Fatalf("sockets = %q, want 2", got)
	}
	if got := topology.SelectAttrValue("cores", ""); got != "3" {
		t.Fatalf("cores = %q, want 3", got)
	}
	if got := topology.SelectAttrValue("threads", ""); got != "2" {
		t.Fatalf("threads = %q, want 2", got)
	}
	if strings.Contains(updatedXML, "-p 0:0") {
		t.Fatal("stale CPU pinning remained in updated XML")
	}
}

func assertNativeCPUPins(t *testing.T, domainXML string, want []libvirtServiceInterfaces.VCPUPin) *etree.Document {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		t.Fatalf("parse domain XML: %v", err)
	}
	if !nativeCPUPinsMatch(doc.Root(), want) {
		t.Fatalf("unexpected native CPU pins, want %+v: %s", want, domainXML)
	}
	return doc
}

func TestBuildVCPUPinsPreservesHostSocketOffsetsAndOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		pins             []vmModels.VMCPUPinning
		logicalPerSocket int
		want             []libvirtServiceInterfaces.VCPUPin
	}{
		{name: "no pinning"},
		{
			name: "single socket unsorted cores",
			pins: []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: []int{5, 1}}}, logicalPerSocket: 8,
			want: []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "5"}, {VCPU: 1, CPUSet: "1"}},
		},
		{
			name: "second socket partial pinning",
			pins: []vmModels.VMCPUPinning{{HostSocket: 1, HostCPU: []int{0, 25}}}, logicalPerSocket: 40,
			want: []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "40"}, {VCPU: 1, CPUSet: "65"}},
		},
		{
			name: "unsorted host sockets",
			pins: []vmModels.VMCPUPinning{
				{HostSocket: 1, HostCPU: []int{3, 0}}, {HostSocket: 0, HostCPU: []int{3}},
			},
			logicalPerSocket: 8,
			want: []libvirtServiceInterfaces.VCPUPin{
				{VCPU: 0, CPUSet: "11"}, {VCPU: 1, CPUSet: "8"}, {VCPU: 2, CPUSet: "3"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := buildVCPUPins(test.pins, test.logicalPerSocket); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("native pins = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCreateVmXMLProducesNativeCPUPinning(t *testing.T) {
	t.Parallel()
	vm := vmModels.VM{
		RID: 100, CPUSockets: 2, CPUCores: 2, CPUThreads: 1, RAM: 268435456,
		TimeOffset: vmModels.TimeOffsetUTC, BootROM: vmModels.VMBootROMNone,
		CPUPinning:        []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: []int{5, 1}}},
		ExtraBhyveOptions: []string{"-S"},
	}
	domainXML, err := (&Service{}).CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("create domain XML: %v", err)
	}
	doc := assertNativeCPUPins(t, domainXML, []libvirtServiceInterfaces.VCPUPin{
		{VCPU: 0, CPUSet: "5"}, {VCPU: 1, CPUSet: "1"},
	})
	if got := doc.FindElement("//vcpu").Text(); got != "4" {
		t.Fatalf("vcpu count = %s, want 4", got)
	}
	if strings.Contains(domainXML, `value="-p`) || !strings.Contains(domainXML, `value="-S"`) {
		t.Fatalf("unexpected bhyve arguments: %s", domainXML)
	}
	vm.CPUPinning = nil
	domainXML, err = (&Service{}).CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("create unpinned domain XML: %v", err)
	}
	if strings.Contains(domainXML, "cputune") {
		t.Fatalf("unpinned VM has cputune: %s", domainXML)
	}
}

func TestUpdateCPUPinningXMLMigratesLegacyFormats(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		args        string
		wantOptions []string
	}{
		{"separate pin arguments", `<bhyve:arg value="-p 0:40"/><bhyve:arg value="-p 1:65"/>`, nil},
		{"combined pin arguments", `<bhyve:arg value="-p 0:40 -p 1:65"/>`, nil},
		{"compact pin arguments", `<bhyve:arg value="-p0:40"/><bhyve:arg value="-p1:65"/>`, nil},
		{"split option and mapping", `<bhyve:arg value="-p"/><bhyve:arg value="0:40"/><bhyve:arg value="-p"/><bhyve:arg value="1:65"/>`, nil},
		{"mixed options", `<bhyve:arg value="-S"/><bhyve:arg value="-p 0:40"/><bhyve:arg value="-u"/><bhyve:arg value="-p 1:65"/><bhyve:arg value="-w"/>`, []string{"-S", "-u", "-w"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><vcpu>8</vcpu><cpu><topology sockets="2" cores="4" threads="1"/></cpu><devices><graphics type="vnc" port="5900"/></devices><bhyve:commandline>` + test.args + `</bhyve:commandline></domain>`
			want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "40"}, {VCPU: 1, CPUSet: "65"}}
			updated, changed, err := updateCPUPinningXML(original, want)
			if err != nil || !changed {
				t.Fatalf("migrate CPU pinning: changed=%t err=%v", changed, err)
			}
			doc := assertNativeCPUPins(t, updated, want)
			if strings.Contains(updated, "-p") {
				t.Fatalf("legacy pinning remained: %s", updated)
			}
			for _, path := range []string{"//vcpu", "//cpu/topology", "//devices/graphics"} {
				if doc.FindElement(path) == nil {
					t.Fatalf("unrelated XML missing at %s: %s", path, updated)
				}
			}
			if got := doc.FindElement("//vcpu").Text(); got != "8" {
				t.Fatalf("migration changed topology: vcpu=%s", got)
			}
			var options []string
			for _, arg := range doc.FindElements("//bhyve:arg") {
				options = append(options, arg.SelectAttrValue("value", ""))
			}
			if !reflect.DeepEqual(options, test.wantOptions) {
				t.Fatalf("unrelated options changed: got %q, want %q", options, test.wantOptions)
			}
			if len(test.wantOptions) == 0 && doc.FindElement("//bhyve:commandline") != nil {
				t.Fatalf("empty commandline remained: %s", updated)
			}
			again, changed, err := updateCPUPinningXML(updated, want)
			if err != nil || changed || again != updated {
				t.Fatalf("migration is not idempotent: changed=%t err=%v XML=%s", changed, err, again)
			}
		})
	}
}

func TestUpdateCPUPinningXMLPreservesUnrelatedTuningAndQuotedOptions(t *testing.T) {
	t.Parallel()
	original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><cputune><shares>2048</shares><vcpupin vcpu="0" cpuset="2"/><emulatorpin cpuset="0-3"/></cputune><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-p 0:2"/><bhyve:arg value="-o key='keep -p 7:8  unchanged'"/><bhyve:arg value="-s 10:0,passthru,2/0/0"/></bhyve:commandline></domain>`
	want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "4"}}
	updated, changed, err := updateCPUPinningXML(original, want)
	if err != nil || !changed {
		t.Fatalf("update CPU pins: changed=%t err=%v", changed, err)
	}
	doc := assertNativeCPUPins(t, updated, want)
	if doc.FindElement("//shares").Text() != "2048" || doc.FindElement("//emulatorpin").SelectAttrValue("cpuset", "") != "0-3" {
		t.Fatalf("unrelated cputune changed: %s", updated)
	}
	var options []string
	for _, arg := range doc.FindElements("//bhyve:arg") {
		options = append(options, arg.SelectAttrValue("value", ""))
	}
	if !reflect.DeepEqual(options, []string{"-S", "-o key='keep -p 7:8  unchanged'", "-s 10:0,passthru,2/0/0"}) {
		t.Fatalf("unrelated bhyve options changed: %s", updated)
	}
	if strings.Contains(updated, "-p 0:2") || len(doc.Root().FindElements("cputune")) != 1 {
		t.Fatalf("stale or duplicate CPU pinning: %s", updated)
	}
}

func TestUpdateCPUPinningXMLPreservesCommandlineArgumentPayloads(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args string
		want []string
	}{
		{
			name: "device filename",
			args: `<bhyve:arg value="-s 10:0,ahci-cd,/images/os -preview.iso,ro"/>`,
			want: []string{"-s 10:0,ahci-cd,/images/os -preview.iso,ro"},
		},
		{
			name: "unquoted text",
			args: `<bhyve:arg value="-o system.product_name=keep -p 7:8  unchanged"/>`,
			want: []string{"-o system.product_name=keep -p 7:8  unchanged"},
		},
		{
			name: "split text operand",
			args: `<bhyve:arg value="-o"/><bhyve:arg value="system.product_name=keep -p 7:8  unchanged"/>`,
			want: []string{"-o", "system.product_name=keep -p 7:8  unchanged"},
		},
		{
			name: "split filename starting with pin prefix",
			args: `<bhyve:arg value="-k"/><bhyve:arg value="-preview.conf"/>`,
			want: []string{"-k", "-preview.conf"},
		},
		{
			name: "split filename resembling a valid pin",
			args: `<bhyve:arg value="-k"/><bhyve:arg value="-p0:2"/>`,
			want: []string{"-k", "-p0:2"},
		},
		{
			name: "clustered option with split filename",
			args: `<bhyve:arg value="-Sk"/><bhyve:arg value="-preview.conf"/>`,
			want: []string{"-Sk", "-preview.conf"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, legacy := range []bool{false, true} {
				args := test.args
				if legacy {
					args += `<bhyve:arg value="-p 0:2"/>`
				}
				original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><cputune><vcpupin vcpu="0" cpuset="2"/></cputune><bhyve:commandline>` + args + `</bhyve:commandline></domain>`
				want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "2"}}
				updated, changed, err := updateCPUPinningXML(original, want)
				if err != nil || changed != legacy {
					t.Fatalf("update CPU pinning: legacy=%t changed=%t err=%v", legacy, changed, err)
				}
				if !legacy && updated != original {
					t.Fatalf("already-native XML changed: %s", updated)
				}
				doc := assertNativeCPUPins(t, updated, want)
				var got []string
				for _, arg := range doc.FindElements("//bhyve:arg") {
					got = append(got, arg.SelectAttrValue("value", ""))
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("argument payloads changed: got %q, want %q", got, test.want)
				}
				again, changed, err := updateCPUPinningXML(updated, want)
				if err != nil || changed || again != updated {
					t.Fatalf("migration is not idempotent: changed=%t err=%v XML=%s", changed, err, again)
				}
			}
		})
	}
}

func TestUpdateCPUPinningXMLPreservesArgumentsAfterOptionTerminator(t *testing.T) {
	t.Parallel()
	original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><cputune><vcpupin vcpu="0" cpuset="2"/></cputune><bhyve:commandline><bhyve:arg value="--"/><bhyve:arg value="-p0:2"/></bhyve:commandline></domain>`
	want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "2"}}
	updated, changed, err := updateCPUPinningXML(original, want)
	if err != nil || changed || updated != original {
		t.Fatalf("arguments after option terminator changed: changed=%t err=%v XML=%s", changed, err, updated)
	}
}

func TestUpdateCPUPinningXMLClearsBothFormats(t *testing.T) {
	t.Parallel()
	for _, tuning := range []string{
		`<cputune><vcpupin vcpu="0" cpuset="2"/></cputune>`,
		`<cputune><shares>2048</shares><vcpupin vcpu="0" cpuset="2"/></cputune>`,
	} {
		original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0">` + tuning + `<bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-p 0:2"/></bhyve:commandline></domain>`
		updated, changed, err := updateCPUPinningXML(original, nil)
		if err != nil || !changed {
			t.Fatalf("clear pinning: changed=%t err=%v", changed, err)
		}
		doc := assertNativeCPUPins(t, updated, nil)
		if strings.Contains(updated, "-p") || !strings.Contains(updated, `value="-S"`) {
			t.Fatalf("unexpected remaining bhyve arguments: %s", updated)
		}
		if strings.Contains(tuning, "shares") {
			if doc.FindElement("//shares") == nil {
				t.Fatalf("unrelated tuning removed: %s", updated)
			}
		} else if doc.FindElement("//cputune") != nil {
			t.Fatalf("empty cputune remained: %s", updated)
		}
	}
}

func TestUpdateCPUPinningXMLVerifiesNativePins(t *testing.T) {
	t.Parallel()
	want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "2"}, {VCPU: 1, CPUSet: "4"}}
	for _, original := range []string{
		`<domain/>`,
		`<domain><cputune><vcpupin vcpu="0" cpuset="1"/></cputune></domain>`,
		`<domain><cputune><vcpupin vcpu="0" cpuset="2"/><vcpupin vcpu="0" cpuset="2"/></cputune></domain>`,
	} {
		updated, changed, err := updateCPUPinningXML(original, want)
		if err != nil || !changed {
			t.Fatalf("repair native pinning: changed=%t err=%v", changed, err)
		}
		assertNativeCPUPins(t, updated, want)
	}
	original := `<domain><cputune><vcpupin vcpu="1" cpuset="4"/><vcpupin vcpu="0" cpuset="2"/></cputune></domain>`
	updated, changed, err := updateCPUPinningXML(original, want)
	if err != nil || changed || updated != original {
		t.Fatalf("already-native XML was rewritten: changed=%t err=%v XML=%s", changed, err, updated)
	}
}

func TestUpdateCPUPinningXMLRejectsMalformedLegacyPins(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"-p", "-p missing", "-p -S", "-p 0:-1", "-p -1:0", "-p 0:1:2", "-p-1:0", "-p0:-1", "-pbad"} {
		original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><bhyve:commandline><bhyve:arg value="` + value + `"/></bhyve:commandline></domain>`
		if _, _, err := updateCPUPinningXML(original, nil); err == nil {
			t.Fatalf("expected malformed pin %q to fail", value)
		}
	}
	for _, original := range []string{"", "<domain>"} {
		if _, _, err := updateCPUPinningXML(original, nil); err == nil {
			t.Fatalf("expected malformed XML %q to fail", original)
		}
	}
}

func TestUpdateCPUPinningXMLPreservesOtherCommandlineNamespaces(t *testing.T) {
	t.Parallel()
	original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0" xmlns:other="urn:other"><other:commandline><other:arg value="-p 0:20"/></other:commandline><bhyve:commandline><bhyve:arg value="-p 0:2"/></bhyve:commandline></domain>`
	updated, _, err := updateCPUPinningXML(original, []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "2"}})
	if err != nil {
		t.Fatalf("update CPU pinning: %v", err)
	}
	if !strings.Contains(updated, `<other:arg value="-p 0:20"`) || strings.Contains(updated, `<bhyve:arg`) {
		t.Fatalf("commandline namespace was not respected: %s", updated)
	}
}

func TestUpdateCPUUsesNativePinsAndRequestedTopology(t *testing.T) {
	t.Parallel()
	original := `<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><vcpu>2</vcpu><cpu><topology sockets="1" cores="2" threads="1"/></cpu><cputune><vcpupin vcpu="0" cpuset="0"/><shares>2048</shares></cputune><bhyve:commandline><bhyve:arg value="-p 0:0 -p 1:1"/><bhyve:arg value="-S"/></bhyve:commandline></domain>`
	pins := []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: []int{3, 2}}}
	updated, err := (&Service{}).updateCPU(original, 2, 2, 2, pins)
	if err != nil {
		t.Fatalf("update CPU: %v", err)
	}
	doc := assertNativeCPUPins(t, updated, []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "3"}, {VCPU: 1, CPUSet: "2"}})
	if doc.FindElement("//vcpu").Text() != "8" || doc.FindElement("//cpu/topology").SelectAttrValue("sockets", "") != "2" {
		t.Fatalf("requested topology not applied: %s", updated)
	}
	if strings.Contains(updated, "-p") || doc.FindElement("//shares") == nil {
		t.Fatalf("unexpected CPU update: %s", updated)
	}
	updated, err = (&Service{}).updateCPU(updated, 1, 1, 1, nil)
	if err != nil {
		t.Fatalf("clear CPU pinning: %v", err)
	}
	assertNativeCPUPins(t, updated, nil)
}
