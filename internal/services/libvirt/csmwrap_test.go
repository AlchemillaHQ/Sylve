// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/beevik/etree"
)

func csmwrapTestVM(t *testing.T) vmModels.VM {
	t.Helper()
	if runtime.GOARCH != "amd64" {
		t.Skip("CSMWrap only available on amd64")
	}
	return vmModels.VM{RID: 199, BootROM: vmModels.VMBootROMUEFICSMWrap,
		CPUSockets: 1, CPUCores: 2, CPUThreads: 1, RAM: 1 << 30, TimeOffset: vmModels.TimeOffsetUTC}
}

func TestCSMWrapModeParsingAndNormalization(t *testing.T) {
	mode, err := parseBootROMValue(" UEFI_CSMWRAP ")
	if runtime.GOARCH != "amd64" {
		if err == nil {
			t.Fatal("accepted unsupported mode")
		}
		return
	}
	if err != nil || mode != vmModels.VMBootROMUEFICSMWrap {
		t.Fatalf("parse: %v", err)
	}
	vm := vmModels.VM{BootROM: mode}
	if err := vm.AfterFind(nil); err != nil || vm.BootROM != mode {
		t.Fatal("VM model lost boot mode")
	}
	template := buildVMTemplateFromVM(vm, "legacy", nil)
	if err := template.AfterFind(nil); err != nil || template.BootROM != mode {
		t.Fatal("template model lost boot mode")
	}
}

func TestCSMWrapXMLHelperFirstAndRemoval(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	vm := csmwrapTestVM(t)
	vm.Storages = []vmModels.Storage{
		{Enable: true, Type: vmModels.VMStorageTypeZVol, Pool: "testpool", ID: 1, Emulation: "ahci-hd"},
	}
	service := &Service{}
	for range 2 {
		domainXML, err := service.CreateVmXML(vm, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		doc := etree.NewDocument()
		if err := doc.ReadFromString(domainXML); err != nil {
			t.Fatal(err)
		}
		args := doc.FindElements("//commandline/arg")
		if len(args) != 2 || !strings.HasPrefix(args[0].SelectAttrValue("value", ""), "-s 9:0,ahci-cd,") ||
			!strings.HasSuffix(args[0].SelectAttrValue("value", ""), ",ro,bootindex=0") || !strings.HasPrefix(args[1].SelectAttrValue("value", ""), "-s 10:0,ahci-hd,") {
			t.Fatalf("helper ordering: %+v", args)
		}
		if !strings.HasSuffix(doc.FindElement("//loader").Text(), "199_csmwrap_vars.fd,fwcfg=qemu") {
			t.Fatalf("loader does not use isolated variables and QEMU fw_cfg: %s", domainXML)
		}
		if strings.Count(domainXML, "CSMWrap.iso") != 1 {
			t.Fatal("duplicate helper")
		}
	}
	vm.Storages = nil
	if generated, err := service.CreateVmXML(vm, t.TempDir()); err != nil || strings.Count(generated, "CSMWrap.iso") != 1 {
		t.Fatalf("installer-free helper missing: %v", err)
	}
	vm.BootROM = vmModels.VMBootROMUEFI
	if generated, err := service.CreateVmXML(vm, t.TempDir()); err != nil || strings.Contains(generated, "CSMWrap.iso") || strings.Contains(generated, "199_csmwrap_vars.fd") || strings.Contains(generated, "fwcfg=qemu") {
		t.Fatalf("disabled helper remains: %v", err)
	}
}

func TestCSMWrapGuardsBeforeCreationSideEffects(t *testing.T) {
	vm := csmwrapTestVM(t)
	vm.CPUCores = 1
	if _, err := (&Service{}).CreateVmXML(vm, t.TempDir()); err == nil || !strings.Contains(err.Error(), "csmwrap_requires_two_vcpus") {
		t.Fatalf("XML guard: %v", err)
	}
	request := libvirtServiceInterfaces.CreateVMRequest{BootROM: "uefi_csmwrap", CPUSockets: 1, CPUCores: 1, CPUThreads: 1}
	if err := (&Service{}).validateCreate(request, context.Background()); err == nil || !strings.Contains(err.Error(), "csmwrap_requires_two_vcpus") {
		t.Fatalf("creation guard: %v", err)
	}
	vm.CPUCores = 2
	request.CPUCores = 2
	for _, options := range [][]string{{"-s 9:0,virtio-net,tap0"}, {"-s", "0:9:0,ahci-cd,test.iso"}} {
		vm.ExtraBhyveOptions = options
		if _, err := (&Service{}).CreateVmXML(vm, t.TempDir()); err == nil || !strings.Contains(err.Error(), "csmwrap_pci_slot_in_use") {
			t.Fatalf("PCI conflict: %v", err)
		}
		request.ExtraBhyveOptions = options
		if err := (&Service{}).validateCreate(request, context.Background()); err == nil || !strings.Contains(err.Error(), "csmwrap_pci_slot_in_use") {
			t.Fatalf("creation PCI conflict: %v", err)
		}
	}
}

func TestCSMWrapTemplateBootModeRoundTrip(t *testing.T) {
	vm := csmwrapTestVM(t)
	template := buildVMTemplateFromVM(vm, "legacy", nil)
	instantiated := buildVMFromTemplate(template, vmTemplateCreateTarget{RID: 200}, 0, "", "", "")
	if instantiated.BootROM != vm.BootROM {
		t.Fatal("template lost helper mode")
	}
}

func TestCSMWrapTopologyValidation(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, test := range []struct {
		sockets, cores, threads int
		valid                   bool
	}{
		{1, 1, 1, false}, {1, 2, 1, true}, {2, 1, 1, true}, {1, 1, 2, true},
		{0, 2, 1, false}, {-1, 2, 1, false}, {maxInt, 2, 2, false},
	} {
		if err := validateCSMWrapCPUs(test.sockets, test.cores, test.threads); (err == nil) != test.valid {
			t.Fatalf("topology %+v: %v", test, err)
		}
	}
}

func TestCSMWrapStorageSynchronizationAndInstallerEjection(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	vm := csmwrapTestVM(t)
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	if err := db.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db}
	vmPath, err := service.GetVMConfigDirectory(vm.RID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, vmVarsFilename(vm.RID, vm.BootROM)), []byte("CSMWrap firmware state"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, vmVarsFilename(vm.RID, vmModels.VMBootROMUEFI)), []byte("ordinary UEFI state"), 0644); err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(config.GetDownloadsPath("path"), "installer.iso")
	if err := os.MkdirAll(filepath.Dir(installer), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installer, []byte("ISO fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&utilitiesModels.Downloads{UUID: "installer", Type: utilitiesModels.DownloadTypePath,
		Path: installer, Name: "installer.iso", URL: "file://installer", Status: utilitiesModels.DownloadStatusDone}).Error; err != nil {
		t.Fatal(err)
	}
	media := vmModels.Storage{VMID: vm.ID, Enable: true, Type: vmModels.VMStorageTypeDiskImage,
		DownloadUUID: "installer", Emulation: vmModels.AHCICDStorageEmulation, BootOrder: -100}
	if err := db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	domainXML, err := service.CreateVmXML(vm, vmPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		t.Fatal(err)
	}
	doc.FindElement("//commandline/arg").CreateAttr("value", "-s 9:0,ahci-cd,/old/revision/CSMWrap.iso,ro,bootindex=0")
	for range 2 {
		if err := service.reconcileVMStorageDocument(context.Background(), db, vm.RID, doc); err != nil {
			t.Fatal(err)
		}
		args := doc.FindElements("//commandline/arg")
		if len(args) != 2 || !strings.Contains(args[0].SelectAttrValue("value", ""), "/firmware/csmwrap/CSMWrap.iso,ro") ||
			!strings.HasSuffix(args[1].SelectAttrValue("value", ""), "installer.iso,ro") {
			t.Fatalf("managed and installer media not independently attached: %v", args)
		}
	}
	if err := db.Delete(&media).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileVMStorageDocument(context.Background(), db, vm.RID, doc); err != nil {
		t.Fatal(err)
	}
	args := doc.FindElements("//commandline/arg")
	if len(args) != 1 || !strings.Contains(args[0].SelectAttrValue("value", ""), "CSMWrap.iso,ro") {
		t.Fatal("installer ejection removed or duplicated helper")
	}
	if err := db.Model(&vm).Update("boot_rom", vmModels.VMBootROMUEFI).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileVMStorageDocument(context.Background(), db, vm.RID, doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.FindElements("//commandline/arg")) != 0 {
		t.Fatal("helper remains after disabling CSMWrap")
	}
	if !strings.HasSuffix(doc.FindElement("//loader").Text(), "199_vars.fd") {
		t.Fatal("ordinary UEFI variables not restored after disabling CSMWrap")
	}
}

func TestCSMWrapNativeDeviceAllocationDuringEdits(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(`<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0">
		<os><loader>/firmware.fd,/vms/199/199_csmwrap_vars.fd,fwcfg=qemu</loader></os><devices>
		<controller type="usb"><address type="pci" slot="0x02"/></controller></devices>
		<bhyve:commandline><bhyve:arg value="-s 9:0,ahci-cd,/helper.iso,ro,bootindex=0"/>
		<bhyve:arg value="-s 10:0,ahci-hd,/disk"/></bhyve:commandline></domain>`); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		doc.FindElement("//devices").CreateElement("interface").CreateAttr("type", "bridge")
	}
	if err := addressCSMWrapDocumentDevices(doc); err != nil {
		t.Fatal(err)
	}
	used := map[int]bool{9: true, 10: true}
	for _, address := range doc.FindElements("//devices//address[@type='pci']") {
		slot, err := strconv.ParseInt(address.SelectAttrValue("slot", ""), 0, 32)
		if err != nil || used[int(slot)] {
			t.Fatalf("PCI slot collision: %d: %v", slot, err)
		}
		used[int(slot)] = true
	}
	before, _ := doc.WriteToString()
	if err := addressCSMWrapDocumentDevices(doc); err != nil {
		t.Fatal(err)
	}
	after, _ := doc.WriteToString()
	if before != after {
		t.Fatal("existing PCI identities changed")
	}
	updated, err := updateVNC(after, 5900, "127.0.0.1", "1024x768", "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ReadFromString(updated); err != nil {
		t.Fatal(err)
	}
	if doc.FindElement("//video/address") == nil {
		t.Fatal("VNC edit left an automatic PCI address")
	}
}

func TestCSMWrapStorageSyncPreservesExtraStorageSlots(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	vm := csmwrapTestVM(t)
	vm.ExtraBhyveOptions = []string{"-s", "11:0,ahci-hd,/custom/disk"}
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	if err := db.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db}
	vmPath, err := service.GetVMConfigDirectory(vm.RID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, vmVarsFilename(vm.RID, vm.BootROM)), []byte("CSMWrap firmware state"), 0644); err != nil {
		t.Fatal(err)
	}
	storage := vmModels.Storage{VMID: vm.ID, Enable: true, Type: vmModels.VMStorageTypeZVol,
		Pool: "tank", Emulation: vmModels.AHCIHDStorageEmulation}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	vm.Storages = []vmModels.Storage{storage}
	xml, err := service.CreateVmXML(vm, vmPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := service.reconcileVMStorageDocument(context.Background(), db, vm.RID, doc); err != nil {
			t.Fatal(err)
		}
		args := doc.FindElements("//commandline/arg")
		if len(args) != 4 || !strings.Contains(args[0].SelectAttrValue("value", ""), "CSMWrap.iso,ro") ||
			args[1].SelectAttrValue("value", "") != "-s" || args[2].SelectAttrValue("value", "") != vm.ExtraBhyveOptions[1] ||
			!strings.HasPrefix(args[3].SelectAttrValue("value", ""), "-s 10:0,ahci-hd,") {
			t.Fatal("custom disk lost or managed disk collided with its PCI slot")
		}
	}
}

func TestCSMWrapVarsAreIsolatedAndPreserved(t *testing.T) {
	path := t.TempDir()
	ordinary := filepath.Join(path, "199_vars.fd")
	if err := os.WriteFile(ordinary, []byte("ordinary UEFI state"), 0644); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(path, "199_csmwrap_vars.fd")
	if err := os.WriteFile(managed, []byte("CSMWrap firmware state"), 0644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := (&Service{}).ensureVMBootROMArtifacts(199, vmModels.VMBootROMUEFICSMWrap, path); err != nil {
			t.Fatal(err)
		}
	}
	unchanged, err := os.ReadFile(ordinary)
	if err != nil || string(unchanged) != "ordinary UEFI state" {
		t.Fatalf("ordinary variables changed: %v", err)
	}
	unchanged, err = os.ReadFile(managed)
	if err != nil || string(unchanged) != "CSMWrap firmware state" {
		t.Fatalf("saved CSMWrap variables changed: %v", err)
	}
}

func TestCSMWrapSnapshotRestoreIgnoresOldHelperRevision(t *testing.T) {
	vm := csmwrapTestVM(t)
	if err := json.Unmarshal([]byte(`{"csmwrapSHA256":"old-helper-revision"}`), &vm); err != nil {
		t.Fatal(err)
	}
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMCPUPinning{}, &vmModels.Storage{},
		&vmModels.VMStorageDataset{}, &vmModels.Network{}, &vmModels.VMSnapshot{})
	current := vm
	current.BootROM = vmModels.VMBootROMUEFI
	if err := db.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db}
	if err := service.restoreVMDatabaseFromSnapshotConfig(vm.RID, vm); err != nil {
		t.Fatal(err)
	}
	var restored vmModels.VM
	if err := db.First(&restored, current.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.BootROM != vm.BootROM {
		t.Fatal("snapshot lost helper mode")
	}
	vm.CPUCores = 1
	if err := service.restoreVMDatabaseFromSnapshotConfig(vm.RID, vm); err == nil || !strings.Contains(err.Error(), "csmwrap_requires_two_vcpus") {
		t.Fatalf("CPU requirement not enforced on restore: %v", err)
	}
}
