// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

func TestLoadVMCreateRequest(t *testing.T) {
	rid := uint(701)
	want := libvirtServiceInterfaces.CreateVMRequest{
		Name:        "vm-file-request",
		RID:         &rid,
		StorageType: libvirtServiceInterfaces.StorageTypeNone,
		TimeOffset:  libvirtServiceInterfaces.TimeOffsetUTC,
	}
	contents, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatalf("write request: %v", err)
	}

	got, err := LoadVMCreateRequest(path)
	if err != nil {
		t.Fatalf("load request: %v", err)
	}
	if got.RID == nil || *got.RID != rid || got.Name != want.Name || got.StorageType != want.StorageType {
		t.Fatalf("loaded request = %#v", got)
	}
}

func TestLoadVMCreateRequestRejectsUnknownAndMultipleDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte(`{"name":"vm","rid":701,"unexpected":true}`), 0600); err != nil {
		t.Fatalf("write unknown-field request: %v", err)
	}
	if _, err := LoadVMCreateRequest(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{} {}`), 0600); err != nil {
		t.Fatalf("write multiple-documents request: %v", err)
	}
	if _, err := LoadVMCreateRequest(path); err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("multiple-documents error = %v", err)
	}
}

func TestBuildVMCreateRequestFlagOnlyDefaults(t *testing.T) {
	rid := uint(801)
	name := "flag-created"
	request, err := BuildVMCreateRequest("", VMCreateOverrides{RID: &rid, Name: &name})
	if err != nil {
		t.Fatalf("build flag-only VM create request: %v", err)
	}
	if request.RID == nil || *request.RID != rid || request.Name != name || request.CPUSockets != 1 ||
		request.CPUCores != 1 || request.CPUThreads != 1 || request.RAM != 1024*1024*1024 {
		t.Fatalf("core defaults = %#v", request)
	}
	if request.StorageType != libvirtServiceInterfaces.StorageTypeNone ||
		request.StorageEmulationType != libvirtServiceInterfaces.VirtIOStorageEmulation || request.StorageSize != nil ||
		request.SwitchName != "none" {
		t.Fatalf("storage/network defaults = %#v", request)
	}
	if request.VNCEnabled == nil || *request.VNCEnabled || request.VNCWait == nil || *request.VNCWait ||
		request.StartAtBoot == nil || *request.StartAtBoot || request.StartOrder != 0 ||
		request.TimeOffset != libvirtServiceInterfaces.TimeOffsetUTC || request.CloudInit == nil || *request.CloudInit {
		t.Fatalf("boolean defaults = %#v", request)
	}
}

func TestBuildVMCreateRequestExplicitFlagsOverrideJSON(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "vm.json")
	fileRID := uint(802)
	trueValue := true
	requestFile := libvirtServiceInterfaces.CreateVMRequest{
		RID: &fileRID, Name: "from-file", Description: "keep-me",
		StoragePool: "tank", StorageType: libvirtServiceInterfaces.StorageTypeRaw,
		StorageSize:          uint64Pointer(2 * 1024 * 1024 * 1024),
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "bridge0", SwitchEmulationType: "virtio", MacId: uintPointer(91),
		CPUSockets: 2, CPUCores: 2, CPUThreads: 1, RAM: 2 * 1024 * 1024 * 1024,
		VNCEnabled: &trueValue, VNCPort: 5902, VNCBind: "127.0.0.1", VNCResolution: "1024x768",
		VNCWait: &trueValue, StartAtBoot: &trueValue, StartOrder: 4, TimeOffset: libvirtServiceInterfaces.TimeOffsetLocal,
	}
	contents, err := json.Marshal(requestFile)
	if err != nil {
		t.Fatalf("marshal VM request: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}

	overrideRID := uint(803)
	overrideName := "from-flags"
	storageType := "none"
	vncEnabled := false
	vncWait := false
	startAtBoot := false
	startOrder := 7
	request, err := BuildVMCreateRequest(path, VMCreateOverrides{
		RID: &overrideRID, Name: &overrideName, StorageType: &storageType,
		Switch: &storageType, VNCEnabled: &vncEnabled, VNCWait: &vncWait,
		StartAtBoot: &startAtBoot, StartOrder: &startOrder,
	})
	if err != nil {
		t.Fatalf("build overlaid VM create request: %v", err)
	}
	if request.RID == nil || *request.RID != overrideRID || request.Name != overrideName || request.Description != "keep-me" ||
		request.CPUSockets != 2 || request.TimeOffset != libvirtServiceInterfaces.TimeOffsetLocal {
		t.Fatalf("unrelated JSON values were not preserved: %#v", request)
	}
	if request.StorageType != libvirtServiceInterfaces.StorageTypeNone || request.StorageSize != nil || request.StoragePool != "" ||
		request.SwitchName != "none" || request.SwitchEmulationType != "" || request.MacId != nil ||
		request.VNCEnabled == nil || *request.VNCEnabled || request.VNCPort != 0 || request.VNCWait == nil || *request.VNCWait ||
		request.StartAtBoot == nil || *request.StartAtBoot || request.StartOrder != 7 {
		t.Fatalf("explicit overrides were not applied: %#v", request)
	}
}

func TestBuildVMCreateRequestISOOverrideClearsCloudInitConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.json")
	rid := uint(807)
	trueValue := true
	falseValue := false
	requestFile := libvirtServiceInterfaces.CreateVMRequest{
		RID: &rid, Name: "cloud-source", ISO: "cloud-image-uuid",
		StorageType:          libvirtServiceInterfaces.StorageTypeNone,
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "none", CPUSockets: 1, CPUCores: 1, CPUThreads: 1, RAM: 1024 * 1024 * 1024,
		VNCEnabled: &falseValue, CloudInit: &trueValue, CloudInitData: "#cloud-config\n",
		CloudInitMetaData: "instance-id: cloud-source\n", CloudInitNetworkConfig: "version: 2\n",
		TimeOffset: libvirtServiceInterfaces.TimeOffsetUTC,
	}
	contents, err := json.Marshal(requestFile)
	if err != nil {
		t.Fatalf("marshal VM request: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}

	iso := "installer-uuid"
	request, err := BuildVMCreateRequest(path, VMCreateOverrides{ISO: &iso})
	if err != nil {
		t.Fatalf("override cloud-init image with ISO: %v", err)
	}
	if request.ISO != iso || request.CloudInit == nil || *request.CloudInit || request.CloudInitData != "" ||
		request.CloudInitMetaData != "" || request.CloudInitNetworkConfig != "" {
		t.Fatalf("ISO override retained cloud-init configuration: %#v", request)
	}
}

func TestBuildVMCreateRequestPreservesJSONVNCServiceDefault(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "vm.json")
	rid := uint(805)
	requestFile := libvirtServiceInterfaces.CreateVMRequest{
		RID: &rid, Name: "vnc-service-default",
		StorageType:          libvirtServiceInterfaces.StorageTypeNone,
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "none", CPUSockets: 1, CPUCores: 1, CPUThreads: 1, RAM: 1024 * 1024 * 1024,
		VNCPort: 5905, VNCBind: "127.0.0.1", VNCResolution: "800x600",
		TimeOffset: libvirtServiceInterfaces.TimeOffsetUTC,
	}
	contents, err := json.Marshal(requestFile)
	if err != nil {
		t.Fatalf("marshal VM request: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}

	request, err := BuildVMCreateRequest(path, VMCreateOverrides{})
	if err != nil {
		t.Fatalf("build VM request using service VNC default: %v", err)
	}
	if request.VNCEnabled != nil || request.VNCPort != 5905 || request.VNCResolution != "800x600" {
		t.Fatalf("VNC defaults were changed: %#v", request)
	}
}

func TestBuildVMCreateRequestRejectsIncompatibleFlags(t *testing.T) {
	rid := uint(804)
	name := "invalid-flags"
	none := "none"
	size := "1GiB"
	iso := "iso"
	cloud := "cloud"
	network := "virtio"
	vncPort := 5900
	negativeOrder := -1
	tests := []struct {
		name      string
		overrides VMCreateOverrides
		want      string
	}{
		{name: "storage details without storage", overrides: VMCreateOverrides{RID: &rid, Name: &name, StorageType: &none, StorageSize: &size}, want: "require storage-type"},
		{name: "two media modes", overrides: VMCreateOverrides{RID: &rid, Name: &name, ISO: &iso, CloudInitImage: &cloud}, want: "cannot be used together"},
		{name: "network details without switch", overrides: VMCreateOverrides{RID: &rid, Name: &name, NetworkEmulation: &network}, want: "requires a switch"},
		{name: "VNC port while disabled", overrides: VMCreateOverrides{RID: &rid, Name: &name, VNCPort: &vncPort}, want: "incompatible with VNC disabled"},
		{name: "negative start order", overrides: VMCreateOverrides{RID: &rid, Name: &name, StartOrder: &negativeOrder}, want: "start-order must be zero or greater"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildVMCreateRequest("", tc.overrides)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestBuildVMCreateRequestRejectsIncompatibleJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.json")
	rid := uint(806)
	requestFile := libvirtServiceInterfaces.CreateVMRequest{
		RID: &rid, Name: "invalid-json-combination",
		StoragePool: "tank", StorageType: libvirtServiceInterfaces.StorageTypeNone,
		StorageSize:          uint64Pointer(1024 * 1024 * 1024),
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "none", CPUSockets: 1, CPUCores: 1, CPUThreads: 1, RAM: 1024 * 1024 * 1024,
		TimeOffset: libvirtServiceInterfaces.TimeOffsetUTC,
	}
	contents, err := json.Marshal(requestFile)
	if err != nil {
		t.Fatalf("marshal VM request: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}
	if _, err := BuildVMCreateRequest(path, VMCreateOverrides{}); err == nil || !strings.Contains(err.Error(), "incompatible with storage-type none") {
		t.Fatalf("incompatible JSON error = %v", err)
	}
}

func TestBuildVMVNCChangesPreservesOmittedPassword(t *testing.T) {
	enabled := false
	changes, err := BuildVMVNCChanges(VMVNCChangeInput{Enabled: &enabled})
	if err != nil {
		t.Fatalf("build VNC changes: %v", err)
	}
	if changes.Enabled == nil || *changes.Enabled || changes.Password != nil {
		t.Fatalf("changes = %#v", changes)
	}

	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	changes, err = BuildVMVNCChanges(VMVNCChangeInput{PasswordFile: passwordFile})
	if err != nil {
		t.Fatalf("build password replacement: %v", err)
	}
	if changes.Password == nil || *changes.Password != "secret-value" {
		t.Fatalf("password replacement = %#v", changes)
	}
}

func TestParseVMCPUPinningRequiresExplicitReplacement(t *testing.T) {
	pins, err := ParseVMCPUPinning([]string{"0:0,1", "1:2"}, false)
	if err != nil {
		t.Fatalf("parse CPU pinning: %v", err)
	}
	if len(pins) != 2 || pins[0].Socket != 0 || len(pins[0].Cores) != 2 || pins[1].Socket != 1 {
		t.Fatalf("pins = %#v", pins)
	}
	cleared, err := ParseVMCPUPinning(nil, true)
	if err != nil || cleared == nil || len(cleared) != 0 {
		t.Fatalf("clear pinning = %#v, %v", cleared, err)
	}
	if _, err := ParseVMCPUPinning(nil, false); err == nil || !strings.Contains(err.Error(), "clear-pinning") {
		t.Fatalf("implicit clear error = %v", err)
	}
}

func TestBuildVMCloudInitReplacementRequiresCompleteIntent(t *testing.T) {
	directory := t.TempDir()
	dataPath := filepath.Join(directory, "data.yaml")
	metadataPath := filepath.Join(directory, "metadata.yaml")
	if err := os.WriteFile(dataPath, []byte("users: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, []byte("instance-id: vm-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement, err := BuildVMCloudInitReplacement(dataPath, metadataPath, "", false, true)
	if err != nil {
		t.Fatalf("build cloud-init replacement: %v", err)
	}
	if replacement.Data == "" || replacement.Metadata == "" || replacement.NetworkConfig != "" {
		t.Fatalf("replacement = %#v", replacement)
	}
	if _, err := BuildVMCloudInitReplacement(dataPath, metadataPath, "", false, false); err == nil ||
		!strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("implicit network clear error = %v", err)
	}
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}

func uintPointer(value uint) *uint {
	return &value
}

func TestParseVMTemplateStoragePoolAssignments(t *testing.T) {
	assignments, err := ParseVMTemplateStoragePoolAssignments([]string{"4=tank", "7=fast"})
	if err != nil {
		t.Fatalf("parse storage mappings: %v", err)
	}
	if len(assignments) != 2 || assignments[0].SourceStorageID != 4 || assignments[0].Pool != "tank" ||
		assignments[1].SourceStorageID != 7 || assignments[1].Pool != "fast" {
		t.Fatalf("assignments = %#v", assignments)
	}
	for _, values := range [][]string{{"missing-separator"}, {"0=tank"}, {"4="}, {"4=tank", "4=fast"}} {
		if _, err := ParseVMTemplateStoragePoolAssignments(values); err == nil {
			t.Fatalf("values %#v unexpectedly passed", values)
		}
	}
}

func TestValidateVMTemplateCreateRequestModes(t *testing.T) {
	single, err := ValidateVMTemplateCreateRequest(libvirtServiceInterfaces.CreateFromTemplateRequest{RID: 710})
	if err != nil || single.Mode != "single" || single.StoragePools == nil {
		t.Fatalf("single = %#v, err = %v", single, err)
	}
	multiple, err := ValidateVMTemplateCreateRequest(libvirtServiceInterfaces.CreateFromTemplateRequest{
		Mode: "MULTIPLE", StartRID: 720, Count: 3, NamePrefix: "node",
		RewriteCloudInitIdentity: true, CloudInitPrefix: "guest",
	})
	if err != nil || multiple.Mode != "multiple" || multiple.StartRID != 720 || multiple.Count != 3 {
		t.Fatalf("multiple = %#v, err = %v", multiple, err)
	}

	tests := []struct {
		name    string
		request libvirtServiceInterfaces.CreateFromTemplateRequest
		want    string
	}{
		{name: "single range options", request: libvirtServiceInterfaces.CreateFromTemplateRequest{RID: 710, Count: 2}, want: "incompatible"},
		{name: "multiple single options", request: libvirtServiceInterfaces.CreateFromTemplateRequest{Mode: "multiple", RID: 710, StartRID: 720, Count: 2}, want: "incompatible"},
		{name: "too many", request: libvirtServiceInterfaces.CreateFromTemplateRequest{Mode: "multiple", StartRID: 720, Count: 201}, want: "between 1 and 200"},
		{name: "range overflow", request: libvirtServiceInterfaces.CreateFromTemplateRequest{Mode: "multiple", StartRID: 9999, Count: 2}, want: "RID range"},
		{name: "prefix without rewrite", request: libvirtServiceInterfaces.CreateFromTemplateRequest{RID: 710, CloudInitPrefix: "guest"}, want: "requires"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateVMTemplateCreateRequest(tc.request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateVMSnapshotCreateIdentifiesFields(t *testing.T) {
	if _, _, err := ValidateVMSnapshotCreate("", "description"); err == nil || !strings.Contains(err.Error(), "snapshot name") {
		t.Fatalf("missing name error = %v", err)
	}
	if _, _, err := ValidateVMSnapshotCreate("snapshot", strings.Repeat("x", 4097)); err == nil || !strings.Contains(err.Error(), "snapshot description") {
		t.Fatalf("description error = %v", err)
	}
}

func TestBuildVMStorageAttachRequestVariants(t *testing.T) {
	readOnly := false
	tests := []struct {
		name       string
		input      VMStorageAttachInput
		attachType libvirtServiceInterfaces.StorageAttachType
		storage    libvirtServiceInterfaces.StorageType
		emulation  libvirtServiceInterfaces.StorageEmulationType
		wantSize   int64
	}{
		{
			name: "new raw with human size",
			input: VMStorageAttachInput{
				RID: 501, Name: "root", StorageType: "raw", Pool: "tank", Size: "2GiB",
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeNew,
			storage:    libvirtServiceInterfaces.StorageTypeRaw,
			emulation:  libvirtServiceInterfaces.VirtIOStorageEmulation,
			wantSize:   2 * 1024 * 1024 * 1024,
		},
		{
			name: "raw import",
			input: VMStorageAttachInput{
				RID: 502, Name: "imported", StorageType: "raw", Pool: "tank", RawPath: "/images/disk.raw",
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeImport,
			storage:    libvirtServiceInterfaces.StorageTypeRaw,
			emulation:  libvirtServiceInterfaces.VirtIOStorageEmulation,
		},
		{
			name: "zvol import",
			input: VMStorageAttachInput{
				RID: 503, Name: "volume", StorageType: "zvol", Pool: "tank", DatasetGUID: "12345", Emulation: "nvme",
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeImport,
			storage:    libvirtServiceInterfaces.StorageTypeZVOL,
			emulation:  libvirtServiceInterfaces.NVMEStorageEmulation,
		},
		{
			name: "download image",
			input: VMStorageAttachInput{
				RID: 504, Name: "installer", StorageType: "image", ImageUUID: "image-uuid",
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeImport,
			storage:    libvirtServiceInterfaces.StorageTypeDiskImage,
			emulation:  libvirtServiceInterfaces.AHCICDStorageEmulation,
		},
		{
			name: "ISO alias",
			input: VMStorageAttachInput{
				RID: 505, Name: "installer-iso", StorageType: "iso", ImageUUID: "iso-uuid",
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeImport,
			storage:    libvirtServiceInterfaces.StorageTypeDiskImage,
			emulation:  libvirtServiceInterfaces.AHCICDStorageEmulation,
		},
		{
			name: "external filesystem",
			input: VMStorageAttachInput{
				RID: 506, Name: "share", StorageType: "filesystem", DatasetGUID: "67890",
				FilesystemTarget: "shared_data", ReadOnly: &readOnly,
			},
			attachType: libvirtServiceInterfaces.StorageAttachTypeNew,
			storage:    libvirtServiceInterfaces.StorageTypeFilesystem,
			emulation:  libvirtServiceInterfaces.VirtIO9PStorageEmulation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request, err := BuildVMStorageAttachRequest(tc.input)
			if err != nil {
				t.Fatalf("build storage attach request: %v", err)
			}
			if request.RID != tc.input.RID || request.Name != tc.input.Name || request.AttachType != tc.attachType ||
				request.StorageType != tc.storage || request.Emulation != tc.emulation {
				t.Fatalf("request = %#v", request)
			}
			if tc.wantSize > 0 && (request.Size == nil || *request.Size != tc.wantSize) {
				t.Fatalf("size = %v, want %d", request.Size, tc.wantSize)
			}
		})
	}
}

func TestBuildVMStorageAttachRequestRejectsIncompatibleOptions(t *testing.T) {
	tests := []struct {
		name  string
		input VMStorageAttachInput
		want  string
	}{
		{
			name:  "RID out of range",
			input: VMStorageAttachInput{RID: 10000, Name: "disk", StorageType: "raw", Pool: "tank", Size: "1GiB"},
			want:  "rid must be between",
		},
		{
			name:  "raw import and size",
			input: VMStorageAttachInput{RID: 501, Name: "disk", StorageType: "raw", Pool: "tank", RawPath: "/disk.raw", Size: "1GiB"},
			want:  "size cannot be used",
		},
		{
			name:  "relative raw path",
			input: VMStorageAttachInput{RID: 501, Name: "disk", StorageType: "raw", Pool: "tank", RawPath: "disk.raw"},
			want:  "raw-path must be absolute",
		},
		{
			name:  "filesystem block emulation",
			input: VMStorageAttachInput{RID: 501, Name: "share", StorageType: "filesystem", DatasetGUID: "1", FilesystemTarget: "share", Emulation: "virtio-blk"},
			want:  "requires virtio-9p",
		},
		{
			name:  "image with pool",
			input: VMStorageAttachInput{RID: 501, Name: "iso", StorageType: "image", ImageUUID: "uuid", Pool: "tank"},
			want:  "incompatible with image",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildVMStorageAttachRequest(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestBuildVMStorageUpdateRequestPreservesExplicitFalse(t *testing.T) {
	enabled := false
	readOnly := false
	size := "4GiB"
	emulation := "virtio-blk"
	request, err := BuildVMStorageUpdateRequest(VMStorageUpdateInput{
		RID: 506, StorageID: 7, Size: &size, Emulation: &emulation, Enabled: &enabled, ReadOnly: &readOnly,
	})
	if err != nil {
		t.Fatalf("build storage update request: %v", err)
	}
	if request.Size == nil || *request.Size != 4*1024*1024*1024 || request.Enable == nil || *request.Enable ||
		request.ReadOnly == nil || *request.ReadOnly {
		t.Fatalf("request = %#v", request)
	}

	if _, err := BuildVMStorageUpdateRequest(VMStorageUpdateInput{RID: 506, StorageID: 7}); err == nil ||
		!strings.Contains(err.Error(), "at least one") {
		t.Fatalf("empty update error = %v", err)
	}
}

func TestBuildVMNetworkUpdateRequest(t *testing.T) {
	enabled := false
	emulation := "VIRTIO"
	request, err := BuildVMNetworkUpdateRequest(VMNetworkUpdateInput{
		RID: 507, NetworkID: 8, Emulation: &emulation, GenerateMAC: true, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("build network update request: %v", err)
	}
	if request.Emulation == nil || *request.Emulation != "virtio" || request.MacID == nil || *request.MacID != 0 ||
		request.Enable == nil || *request.Enable {
		t.Fatalf("request = %#v", request)
	}

	macID := uint(9)
	if _, err := BuildVMNetworkUpdateRequest(VMNetworkUpdateInput{
		RID: 507, NetworkID: 8, MacID: &macID, GenerateMAC: true,
	}); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("conflicting MAC error = %v", err)
	}
	if _, err := BuildVMNetworkUpdateRequest(VMNetworkUpdateInput{RID: 507, NetworkID: 8}); err == nil ||
		!strings.Contains(err.Error(), "at least one") {
		t.Fatalf("empty update error = %v", err)
	}
}
