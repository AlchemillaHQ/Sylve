// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	golibvirt "github.com/digitalocean/go-libvirt"
)

func TestExecuteLineVmsQGASendSyntax(t *testing.T) {
	testCases := []string{
		"vms qga send 101 guest-info",
		"vms qga 101 guest-get-osinfo",
		"vms 101 qga send guest-network-get-interfaces",
		"vms 101 qga guest-info",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			var out bytes.Buffer
			ctx := &Context{Out: &out}

			shouldContinue := ExecuteLine(ctx, tc)
			if !shouldContinue {
				t.Fatalf("expected command to keep session running")
			}

			if !strings.Contains(out.String(), "Error: VM service unavailable.") {
				t.Fatalf("expected VM service error, got %q", out.String())
			}
		})
	}
}

func TestHandleVmsListRejectsTrailingArguments(t *testing.T) {
	var out bytes.Buffer
	handleVms(&Context{Out: &out}, []string{"list", "extra"})

	if !strings.Contains(out.String(), "Usage: vms list") {
		t.Fatalf("unexpected list output: %q", out.String())
	}
}

func TestParseVMDeleteArgs(t *testing.T) {
	rid, deleteMACs, deleteRawDisks, deleteVolumes, dryRun, err := parseVMDeleteArgs([]string{
		"701", "--delete-macs", "--delete-raw-disks", "--delete-volumes", "--dry-run",
	})
	if err != nil {
		t.Fatalf("parse VM delete arguments: %v", err)
	}
	if rid != 701 || !deleteMACs || !deleteRawDisks || !deleteVolumes || !dryRun {
		t.Fatalf("parsed delete arguments = %d, %t, %t, %t, %t", rid, deleteMACs, deleteRawDisks, deleteVolumes, dryRun)
	}

	if _, _, _, _, _, err := parseVMDeleteArgs([]string{"701", "--unknown"}); err == nil || !strings.Contains(err.Error(), "Usage: vms delete") {
		t.Fatalf("invalid delete arguments error = %v", err)
	}
}

func TestParseVMPurgeArgs(t *testing.T) {
	rid, deleteMACs, err := parseVMPurgeArgs([]string{"702", "--delete-macs"})
	if err != nil {
		t.Fatalf("parse VM purge arguments: %v", err)
	}
	if rid != 702 || !deleteMACs {
		t.Fatalf("parsed purge arguments = %d, %t", rid, deleteMACs)
	}

	if _, _, err := parseVMPurgeArgs([]string{"702", "--delete-macs", "--delete-macs"}); err == nil || !strings.Contains(err.Error(), "Usage: vms purge") {
		t.Fatalf("duplicate purge arguments error = %v", err)
	}
}

func TestParseVMRIDRejectsOutsideSupportedRange(t *testing.T) {
	for _, value := range []string{"0", "10000", "-1", "not-a-rid"} {
		if _, err := parseVMRID(value); err == nil {
			t.Errorf("parseVMRID(%q) unexpectedly succeeded", value)
		}
	}
	if rid, err := parseVMRID("9999"); err != nil || rid != 9999 {
		t.Fatalf("parseVMRID(9999) = %d, %v", rid, err)
	}

	var out bytes.Buffer
	handleVms(&Context{Out: &out}, []string{"10000", "qga", "send", "guest-info"})
	if !strings.Contains(out.String(), "Invalid RID '10000'") {
		t.Fatalf("out-of-range convenience RID output = %q", out.String())
	}
}

func TestBuildConsoleVMCreateRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := os.WriteFile(path, []byte(`{
		"name":"vm-file","rid":703,
		"storageType":"none","storageEmulationType":"virtio-blk","switchName":"none",
		"cpuSockets":1,"cpuCores":1,"cpuThreads":1,"ram":1073741824,
		"vncEnabled":false,"vncBind":"127.0.0.1","vncResolution":"800x600","vncWait":false,
		"startAtBoot":false,"timeOffset":"utc"
	}`), 0600); err != nil {
		t.Fatalf("write VM request: %v", err)
	}

	request, err := buildConsoleVMCreateRequest([]string{"--file", path})
	if err != nil {
		t.Fatalf("build VM create request: %v", err)
	}
	if request.RID == nil || *request.RID != 703 || request.Name != "vm-file" {
		t.Fatalf("VM create request = %#v", request)
	}

	if _, err := buildConsoleVMCreateRequest([]string{path}); err == nil || !strings.Contains(err.Error(), "unknown VM option") {
		t.Fatalf("invalid VM create arguments error = %v", err)
	}
}

func TestFormatVMListIncludesComputeResources(t *testing.T) {
	vm := vmModels.VM{
		RID:        108,
		Name:       "Alpine",
		CPUSockets: 1,
		CPUCores:   2,
		CPUThreads: 2,
		RAM:        2 * 1024 * 1024 * 1024,
		Networks:   []vmModels.Network{{}},
	}

	output := formatVMList([]vmModels.VM{vm})
	for _, want := range []string{"vCPUs", "RAM", "Alpine", "4", "2 GiB", "1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("VM list missing %q:\n%s", want, output)
		}
	}

	if got := formatVMVCPUs(vmModels.VM{}); got != "-" {
		t.Fatalf("zero VM vCPUs = %q, want -", got)
	}
	if got := formatMemorySize(512 * 1024 * 1024); got != "512 MiB" {
		t.Fatalf("VM RAM = %q, want 512 MiB", got)
	}
}

func TestBuildConsoleVMCreateRequestFromFlags(t *testing.T) {
	request, err := buildConsoleVMCreateRequest([]string{
		"--rid", "820", "--name", "console-created", "--ram", "2GiB",
		"--vnc-enabled=false", "--vnc-wait=false", "--start-at-boot=false", "--start-order", "8",
	})
	if err != nil {
		t.Fatalf("build console VM create request: %v", err)
	}
	if request.RID == nil || *request.RID != 820 || request.Name != "console-created" || request.RAM != 2*1024*1024*1024 ||
		request.StorageType != libvirtServiceInterfaces.StorageTypeNone || request.VNCEnabled == nil || *request.VNCEnabled ||
		request.VNCWait == nil || *request.VNCWait || request.StartAtBoot == nil || *request.StartAtBoot || request.StartOrder != 8 {
		t.Fatalf("request = %#v", request)
	}
}

func TestParseVMNamedOptionsRepeated(t *testing.T) {
	options, err := parseVMNamedOptionsRepeated(
		[]string{"--option=-S", "--option", "value", "--clear=false"},
		vmAllowed("--option", "--clear"), vmAllowed("--clear"), vmAllowed("--option"),
	)
	if err != nil {
		t.Fatalf("parse repeated options: %v", err)
	}
	if len(options["--option"]) != 2 || options["--option"][0] != "-S" || options["--option"][1] != "value" ||
		len(options["--clear"]) != 1 || options["--clear"][0] != "false" {
		t.Fatalf("options = %#v", options)
	}
	if _, err := parseVMNamedOptionsRepeated(
		[]string{"--clear", "--clear"}, vmAllowed("--clear"), vmAllowed("--clear"), nil,
	); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate non-repeatable option error = %v", err)
	}
}

func TestMergeVMVNCConfigurationPreservesOmittedSettingsAndPassword(t *testing.T) {
	vm := vmModels.VM{
		RID: 821, VNCEnabled: true, VNCPort: 5901, VNCBind: "127.0.0.1",
		VNCResolution: "1024x768", VNCPassword: "do-not-expose", VNCWait: true,
	}
	wait := false
	request, err := mergeVMVNCConfiguration(vm, consoleprotocol.VMVNCChanges{Wait: &wait})
	if err != nil {
		t.Fatalf("merge VNC configuration: %v", err)
	}
	if request.VNCEnabled == nil || !*request.VNCEnabled || request.VNCPort != 5901 || request.VNCBind != "127.0.0.1" ||
		request.VNCResolution != "1024x768" || request.VNCPassword != "do-not-expose" || request.VNCWait == nil || *request.VNCWait {
		t.Fatalf("request = %#v", request)
	}
}

func TestDescribeVMVNCAccessNeverReturnsPassword(t *testing.T) {
	info := describeVMVNCAccess(vmModels.VM{
		RID: 822, Name: "running-vnc", State: golibvirt.DomainRunning,
		VNCEnabled: true, VNCPort: 5902, VNCBind: "0.0.0.0", VNCResolution: "800x600",
		VNCPassword: "highly-secret", VNCWait: false,
	})
	if !info.Enabled || !info.Available || info.Endpoint != "127.0.0.1:5902" || !info.PasswordConfigured ||
		info.UnavailableReason != "" {
		t.Fatalf("VNC access info = %#v", info)
	}
	encoded := mustJSON(info)
	if strings.Contains(encoded, "highly-secret") || strings.Contains(strings.ToLower(encoded), "password\"") {
		t.Fatalf("VNC access JSON exposed a password field or value: %s", encoded)
	}
	formatted := formatVMVNCAccess(info)
	if !strings.Contains(formatted, "Wait for client:") || !strings.Contains(formatted, "false") {
		t.Fatalf("VNC access text omitted wait state: %s", formatted)
	}

	disabled := describeVMVNCAccess(vmModels.VM{RID: 823, Name: "disabled", VNCPassword: "still-secret"})
	if disabled.Available || disabled.UnavailableReason != "vnc_disabled" || !disabled.PasswordConfigured {
		t.Fatalf("disabled VNC info = %#v", disabled)
	}
}

func TestVMJSONInspectionRedactsVNCPassword(t *testing.T) {
	vm := vmModels.VM{RID: 824, Name: "redacted", VNCPassword: "secret"}
	redacted := redactVMSecrets(vm)
	if redacted.VNCPassword != "" || vm.VNCPassword != "secret" {
		t.Fatalf("redaction changed source or retained secret: source=%#v redacted=%#v", vm, redacted)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted VM: %v", err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("redacted VM JSON exposed password: %s", encoded)
	}
}

type vmTemplateServiceStub struct {
	templates       map[uint]vmModels.VMTemplate
	preflightErr    error
	convertRID      uint
	convertRequest  libvirtServiceInterfaces.ConvertToTemplateRequest
	createTemplate  uint
	createRequest   libvirtServiceInterfaces.CreateFromTemplateRequest
	deletedTemplate uint
}

func (s *vmTemplateServiceStub) GetVMTemplatesSimple() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
	result := make([]libvirtServiceInterfaces.SimpleTemplateList, 0, len(s.templates))
	for id, template := range s.templates {
		result = append(result, libvirtServiceInterfaces.SimpleTemplateList{ID: id, Name: template.Name, SourceVMName: template.SourceVMName})
	}
	return result, nil
}

func (s *vmTemplateServiceStub) GetVMTemplate(id uint) (*vmModels.VMTemplate, error) {
	template, ok := s.templates[id]
	if !ok {
		return nil, errors.New("template_not_found")
	}
	return &template, nil
}

func (s *vmTemplateServiceStub) PreflightConvertVMToTemplate(_ context.Context, rid uint, request libvirtServiceInterfaces.ConvertToTemplateRequest) error {
	s.convertRID = rid
	s.convertRequest = request
	return s.preflightErr
}

func (s *vmTemplateServiceStub) PreflightCreateVMsFromTemplate(_ context.Context, id uint, request libvirtServiceInterfaces.CreateFromTemplateRequest) error {
	s.createTemplate = id
	s.createRequest = request
	return s.preflightErr
}

func (s *vmTemplateServiceStub) DeleteVMTemplate(_ context.Context, id uint) error {
	s.deletedTemplate = id
	return nil
}

type vmTemplateLifecycleStub struct {
	task        *taskModels.GuestLifecycleTask
	outcome     string
	requestType string
	requestID   uint
	action      string
	payload     string
	active      []taskModels.GuestLifecycleTask
}

func (s *vmTemplateLifecycleStub) RequestActionWithPayload(
	_ context.Context, guestType string, guestID uint, action, _, _, payload string,
) (*taskModels.GuestLifecycleTask, string, error) {
	s.requestType = guestType
	s.requestID = guestID
	s.action = action
	s.payload = payload
	return s.task, s.outcome, nil
}

func (s *vmTemplateLifecycleStub) ListActiveTasks(string, uint) ([]taskModels.GuestLifecycleTask, error) {
	return s.active, nil
}

type vmSnapshotServiceStub struct {
	destroyNewer bool
}

func (*vmSnapshotServiceStub) ListVMSnapshots(uint) ([]vmModels.VMSnapshot, error) { return nil, nil }
func (*vmSnapshotServiceStub) CreateVMSnapshot(context.Context, uint, string, string) (*vmModels.VMSnapshot, error) {
	return nil, nil
}
func (s *vmSnapshotServiceStub) RollbackVMSnapshotWithDestroyNewer(
	_ context.Context, _, _ uint, destroyNewer bool,
) (libvirtService.VMSnapshotRollbackResult, error) {
	s.destroyNewer = destroyNewer
	return libvirtService.VMSnapshotRollbackResult{Warnings: []string{}, NewerSnapshotsDestroyed: 2}, nil
}
func (*vmSnapshotServiceStub) DeleteVMSnapshot(context.Context, uint, uint) error { return nil }

func TestParseConsoleVMTemplateCreateRequestMatchesDirectBuilder(t *testing.T) {
	request, err := parseConsoleVMTemplateCreateRequest([]string{
		"--mode", "multiple", "--start-rid", "730", "--count", "2", "--name-prefix", "node",
		"--storage-pool", "4=tank", "--rewrite-cloud-init-identity", "--cloud-init-prefix", "guest",
	})
	if err != nil {
		t.Fatalf("parse template request: %v", err)
	}
	if request.Mode != "multiple" || request.StartRID != 730 || request.Count != 2 || request.NamePrefix != "node" ||
		len(request.StoragePools) != 1 || request.StoragePools[0].SourceStorageID != 4 || !request.RewriteCloudInitIdentity {
		t.Fatalf("request = %#v", request)
	}
}

func TestVMTemplateQueueUsesPreflightAndLifecyclePayload(t *testing.T) {
	service := &vmTemplateServiceStub{}
	lifecycle := &vmTemplateLifecycleStub{task: &taskModels.GuestLifecycleTask{ID: 91}, outcome: "queued"}
	convert := libvirtServiceInterfaces.ConvertToTemplateRequest{Name: "base"}
	output, err := queueVMTemplateConvert(context.Background(), service, lifecycle, 740, convert)
	if err != nil {
		t.Fatalf("queue conversion: %v", err)
	}
	if service.convertRID != 740 || service.convertRequest != convert || lifecycle.requestType != taskModels.GuestTypeVMTemplate ||
		lifecycle.requestID != 740 || lifecycle.action != "convert" || output.TaskID != 91 || output.SourceRID != 740 ||
		output.Action != "capture" {
		t.Fatalf("service=%#v lifecycle=%#v output=%#v", service, lifecycle, output)
	}
	var captured libvirtServiceInterfaces.ConvertToTemplateRequest
	if err := json.Unmarshal([]byte(lifecycle.payload), &captured); err != nil || captured != convert {
		t.Fatalf("captured payload = %#v, err = %v", captured, err)
	}

	create := libvirtServiceInterfaces.CreateFromTemplateRequest{Mode: "single", RID: 741, StoragePools: []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{}}
	output, err = queueVMTemplateCreate(context.Background(), service, lifecycle, 12, create)
	if err != nil {
		t.Fatalf("queue creation: %v", err)
	}
	if service.createTemplate != 12 || service.createRequest.RID != 741 || lifecycle.requestID != 12 || lifecycle.action != "create" ||
		output.TemplateID != 12 {
		t.Fatalf("service=%#v lifecycle=%#v output=%#v", service, lifecycle, output)
	}
}

func TestVMTemplatePreflightFailureDoesNotQueue(t *testing.T) {
	service := &vmTemplateServiceStub{preflightErr: errors.New("vm_must_be_shut_off")}
	lifecycle := &vmTemplateLifecycleStub{task: &taskModels.GuestLifecycleTask{ID: 91}}
	_, err := queueVMTemplateConvert(context.Background(), service, lifecycle, 742, libvirtServiceInterfaces.ConvertToTemplateRequest{Name: "base"})
	if err == nil || !strings.Contains(err.Error(), "template_convert_preflight_failed") || lifecycle.action != "" {
		t.Fatalf("error = %v, lifecycle = %#v", err, lifecycle)
	}
}

func TestDeleteVMTemplateRejectsActiveCreateTask(t *testing.T) {
	service := &vmTemplateServiceStub{}
	lifecycle := &vmTemplateLifecycleStub{active: []taskModels.GuestLifecycleTask{{Action: "create"}}}
	_, err := deleteVMTemplate(context.Background(), service, lifecycle, 13)
	if err == nil || !strings.Contains(err.Error(), "vm_template_in_use") || service.deletedTemplate != 0 {
		t.Fatalf("error = %v, deleted = %d", err, service.deletedTemplate)
	}
}

func TestListVMTemplatesProvidesStorageMappingIDs(t *testing.T) {
	service := &vmTemplateServiceStub{templates: map[uint]vmModels.VMTemplate{
		4: {
			ID: 4, Name: "base", SourceVMName: "source", SourceVMRID: 744,
			Storages: []vmModels.VMTemplateStorage{{SourceStorageID: 8, Pool: "tank", Type: vmModels.VMStorageTypeRaw}},
		},
	}}
	templates, err := listVMTemplates(service)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) != 1 || len(templates[0].Storages) != 1 || templates[0].Storages[0].SourceStorageID != 8 {
		t.Fatalf("templates = %#v", templates)
	}
}

func TestGetVMTemplateReturnsFullStableTemplate(t *testing.T) {
	service := &vmTemplateServiceStub{templates: map[uint]vmModels.VMTemplate{
		5: {ID: 5, Name: "base", SourceVMName: "source", SourceVMRID: 745},
	}}
	template, err := getVMTemplate(service, 5)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if template.ID != 5 || template.Storages == nil || template.Networks == nil || template.ExtraBhyveOptions == nil {
		t.Fatalf("template = %#v", template)
	}
	if _, err := getVMTemplate(service, 0); err == nil || !strings.Contains(err.Error(), "invalid_template_id") {
		t.Fatalf("invalid template error = %v", err)
	}
}

func TestRollbackVMSnapshotPassesExplicitAcknowledgement(t *testing.T) {
	service := &vmSnapshotServiceStub{}
	output, err := rollbackVMSnapshot(service, 745, 3, true)
	if err != nil || !service.destroyNewer || !output.RolledBack || output.NewerSnapshotsDestroyed != 2 || output.Warnings == nil {
		t.Fatalf("output = %#v, service = %#v, err = %v", output, service, err)
	}
}

func TestSerialConsoleTUIUsesConstrainedInjectedCommand(t *testing.T) {
	original := newLocalVMSerialConsoleCommand
	t.Cleanup(func() { newLocalVMSerialConsoleCommand = original })
	var captured consoleprotocol.VMSerialConsoleLaunch
	newLocalVMSerialConsoleCommand = func(launch consoleprotocol.VMSerialConsoleLaunch) *exec.Cmd {
		captured = launch
		return exec.Command("true")
	}
	launch := consoleprotocol.VMSerialConsoleLaunch{RID: 746, DevicePath: "/dev/nmdm746B", BaudRate: "115200"}
	if command := execLocalVMSerialConsole(launch); command == nil {
		t.Fatal("serial console command is nil")
	}
	if captured != launch {
		t.Fatalf("captured launch = %#v", captured)
	}
}

func TestParseVMStorageAttachOptionsPreservesExplicitFalse(t *testing.T) {
	options, err := parseVMNamedOptions([]string{
		"--type", "filesystem",
		"--name", "shared data",
		"--dataset-guid", "12345",
		"--filesystem-target", "shared_data",
		"--read-only=false",
	}, vmStorageAttachOptionNames, vmStorageAttachBooleanOptions)
	if err != nil {
		t.Fatalf("parse storage attach options: %v", err)
	}
	input, err := vmStorageAttachInputFromOptions(621, options)
	if err != nil {
		t.Fatalf("build storage attach input: %v", err)
	}
	request, err := consoleprotocol.BuildVMStorageAttachRequest(input)
	if err != nil {
		t.Fatalf("build storage attach request: %v", err)
	}
	if request.StorageType != libvirtServiceInterfaces.StorageTypeFilesystem || request.ReadOnly == nil || *request.ReadOnly ||
		request.Emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		t.Fatalf("request = %#v", request)
	}
}

func TestParseVMStorageUpdateArgs(t *testing.T) {
	rid, storageID, request, err := parseVMStorageUpdateArgs([]string{
		"622", "7", "--enabled=false", "--read-only", "false", "--boot-order", "0",
	}, false)
	if err != nil {
		t.Fatalf("parse storage update: %v", err)
	}
	if rid != 622 || storageID != 7 || request.Enable == nil || *request.Enable || request.ReadOnly == nil ||
		*request.ReadOnly || request.BootOrder == nil || *request.BootOrder != 0 {
		t.Fatalf("parsed update = rid %d storage %d request %#v", rid, storageID, request)
	}

	_, _, resized, err := parseVMStorageUpdateArgs([]string{"622", "7", "--size", "8GiB"}, true)
	if err != nil {
		t.Fatalf("parse storage resize: %v", err)
	}
	if resized.Size == nil || *resized.Size != 8*1024*1024*1024 {
		t.Fatalf("resize request = %#v", resized)
	}

	if _, _, _, err := parseVMStorageUpdateArgs([]string{"622", "7", "--name", "not-allowed"}, true); err == nil ||
		!strings.Contains(err.Error(), "unknown VM option") {
		t.Fatalf("resize extra option error = %v", err)
	}
}

func TestParseVMNetworkUpdateArgs(t *testing.T) {
	request, err := parseVMNetworkUpdateArgs([]string{
		"623", "8", "--switch", "lan0", "--emulation", "VIRTIO", "--generate-mac", "--enabled=false",
	})
	if err != nil {
		t.Fatalf("parse network update: %v", err)
	}
	if request.RID != 623 || request.NetworkID != 8 || request.SwitchName == nil || *request.SwitchName != "lan0" ||
		request.Emulation == nil || *request.Emulation != "virtio" || request.MacID == nil || *request.MacID != 0 ||
		request.Enable == nil || *request.Enable {
		t.Fatalf("request = %#v", request)
	}

	if _, err := parseVMNetworkUpdateArgs([]string{"623", "8", "--enabled=maybe"}); err == nil ||
		!strings.Contains(err.Error(), "invalid boolean") {
		t.Fatalf("invalid enabled error = %v", err)
	}
}

func TestParseVMNetworkAttachArgsUsesNamedOptions(t *testing.T) {
	request, err := parseVMNetworkAttachArgs([]string{
		"623", "--switch", "lan0", "--emulation", "VIRTIO", "--mac-id", "19",
	})
	if err != nil {
		t.Fatalf("parse network attach: %v", err)
	}
	if request.RID != 623 || request.SwitchName != "lan0" || request.Emulation != "virtio" ||
		request.MacID == nil || *request.MacID != 19 {
		t.Fatalf("request = %#v", request)
	}
	if _, err := parseVMNetworkAttachArgs([]string{"623", "lan0", "virtio"}); err == nil ||
		!strings.Contains(err.Error(), "unknown VM option") {
		t.Fatalf("positional attach options error = %v", err)
	}
}

func TestRemovedFlatVMCommandsAreUnknown(t *testing.T) {
	for _, command := range []string{"networks", "addnet", "editnet", "rmnet"} {
		var out bytes.Buffer
		handleVms(&Context{Out: &out}, []string{command})
		if !strings.Contains(out.String(), "Unknown vms command") {
			t.Fatalf("vms %s output = %q", command, out.String())
		}
	}
}

func TestFormatVMStorageListShowsOwnership(t *testing.T) {
	output := formatVMStorageList(624, []libvirtService.VMStorageInfo{
		{
			ID: 1, Name: "root", Type: libvirtServiceInterfaces.StorageTypeRaw,
			Emulation: libvirtServiceInterfaces.VirtIOStorageEmulation,
			Size:      1024 * 1024 * 1024, Enabled: true, Ownership: libvirtService.VMStorageOwnershipManaged,
			Backing: "tank/sylve/virtual-machines/624/raw-1",
		},
	})
	for _, want := range []string{"root", "raw", "virtio-blk", "1.0 GiB", "managed", "raw-1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("storage output missing %q:\n%s", want, output)
		}
	}
}
