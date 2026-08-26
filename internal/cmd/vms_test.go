// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

func TestNewVMsCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newVMsCommand()
	want := map[string]bool{
		"create":    false,
		"list":      false,
		"get":       false,
		"start":     false,
		"stop":      false,
		"shutdown":  false,
		"reboot":    false,
		"config":    false,
		"access":    false,
		"delete":    false,
		"purge":     false,
		"network":   false,
		"storage":   false,
		"snapshots": false,
		"templates": false,
		"qga":       false,
	}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected vms %s command", name)
		}
	}
	for _, removed := range []string{"networks", "addnet", "editnet", "rmnet"} {
		for _, child := range command.Commands {
			if child.Name == removed {
				t.Fatalf("removed vms %s command remains registered", removed)
			}
		}
	}
}

func TestVMSnapshotRollbackUsesExplicitDestructionAcknowledgement(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "snapshots", "rollback", "--rid", "620", "--snapshot-id", "9", "--destroy-newer", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMSnapshotRollback {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMSnapshotRollbackPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode snapshot rollback payload: %v", err)
	}
	if payload.RID != 620 || payload.SnapshotID != 9 || !payload.DestroyNewer || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMTemplateCreateMultipleUsesStorageMappings(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "templates", "create", "--template-id", "11", "--mode", "multiple",
		"--start-rid", "700", "--count", "3", "--name-prefix", "worker",
		"--storage-pool", "4=tank", "--storage-pool", "7=fast",
		"--rewrite-cloud-init-identity", "--cloud-init-prefix", "node", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMTemplateCreate {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMTemplateCreatePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode template create payload: %v", err)
	}
	if payload.TemplateID != 11 || payload.Request.Mode != "multiple" || payload.Request.StartRID != 700 ||
		payload.Request.Count != 3 || payload.Request.NamePrefix != "worker" || len(payload.Request.StoragePools) != 2 ||
		payload.Request.StoragePools[1].SourceStorageID != 7 || payload.Request.StoragePools[1].Pool != "fast" ||
		!payload.Request.RewriteCloudInitIdentity || payload.Request.CloudInitPrefix != "node" || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMTemplateCreateRejectsExplicitOptionsFromOtherMode(t *testing.T) {
	command := newVMTemplatesCommand()
	err := command.Run(context.Background(), []string{
		"templates", "create", "--template-id", "11", "--mode", "single", "--rid", "700", "--count", "0",
	})
	if err == nil || !strings.Contains(err.Error(), "--count is incompatible with single mode") {
		t.Fatalf("single-mode error = %v", err)
	}
	command = newVMTemplatesCommand()
	err = command.Run(context.Background(), []string{
		"templates", "create", "--template-id", "11", "--mode", "multiple", "--start-rid", "700", "--count", "2", "--rid", "701",
	})
	if err == nil || !strings.Contains(err.Error(), "--rid is incompatible with multiple mode") {
		t.Fatalf("multiple-mode error = %v", err)
	}
}

func TestVMSerialAccessUsesTypedPreflightWithoutLaunchingInJSONMode(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "access", "serial", "--rid", "621", "--baud", "9600", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMAccessSerial {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMAccessSerialPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode serial access payload: %v", err)
	}
	if payload.RID != 621 || payload.BaudRate != "9600" || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMSerialAccessRunsInjectedLocalCUAfterDaemonPreflight(t *testing.T) {
	original := runLocalVMSerialConsole
	t.Cleanup(func() { runLocalVMSerialConsole = original })
	var launched consoleprotocol.VMSerialConsoleLaunch
	runLocalVMSerialConsole = func(launch consoleprotocol.VMSerialConsoleLaunch) error {
		launched = launch
		return nil
	}
	want := consoleprotocol.VMSerialConsoleLaunch{RID: 622, DevicePath: "/dev/nmdm622B", BaudRate: "115200"}
	request := captureDirectVMOperationWithResponse(t, consoleprotocol.Response{SerialConsole: &want},
		"vms", "access", "serial", "--rid", "622",
	)
	if request.Operation != consoleprotocol.OperationVMAccessSerial || launched != want {
		t.Fatalf("request = %#v, launched = %#v", request, launched)
	}
}

func TestVMSnapshotTemplateAndAccessHelpRegistersLifecycleCommands(t *testing.T) {
	snapshots := newVMSnapshotsCommand()
	for _, name := range []string{"list", "create", "rollback", "delete"} {
		child := findCLIChildCommand(t, snapshots, name)
		if name == "rollback" && (!strings.Contains(child.Description, "stopped") ||
			!strings.Contains(child.Description, "--destroy-newer") ||
			!strings.Contains(child.Description, "administrator-created")) {
			t.Fatalf("snapshot rollback help = %q", child.Description)
		}
	}
	templates := newVMTemplatesCommand()
	for _, name := range []string{"list", "get", "capture", "create", "delete"} {
		child := findCLIChildCommand(t, templates, name)
		if name == "capture" && (!strings.Contains(strings.ToLower(child.Description), "powered off") || !strings.Contains(child.Description, "retains")) {
			t.Fatalf("template capture help = %q", child.Description)
		}
	}
	for _, child := range templates.Commands {
		if child.Name == "convert" {
			t.Fatal("removed templates convert command remains registered")
		}
	}
	serial := newVMAccessSerialCommand()
	if !strings.Contains(serial.Description, "50-4000000") || !strings.Contains(serial.Description, "/dev/nmdm") {
		t.Fatalf("serial help = %q", serial.Description)
	}
}

func TestVMDeleteHelpDisclosesForceStopAndRetentionDefaults(t *testing.T) {
	command := findCLIChildCommand(t, newVMsCommand(), "delete")
	description := strings.ToLower(command.Description)
	for _, expected := range []string{"force-stopped", "retained", "--dry-run"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("VM delete help missing %q: %q", expected, command.Description)
		}
	}
}

func TestVMStorageCommandHelpRegistersLifecycleOperations(t *testing.T) {
	command := newVMStorageCommand()
	want := map[string]bool{
		"list": false, "attach": false, "edit": false, "resize": false, "detach": false,
	}
	for _, subcommand := range command.Commands {
		if _, exists := want[subcommand.Name]; !exists {
			continue
		}
		want[subcommand.Name] = true
		if subcommand.Name != "list" && !strings.Contains(strings.ToLower(subcommand.Description), "powered off") {
			t.Fatalf("vms storage %s help does not state powered-off requirement: %q", subcommand.Name, subcommand.Description)
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("expected vms storage %s command", name)
		}
	}
	attach := findCLIChildCommand(t, command, "attach")
	for _, accepted := range []string{"raw", "zvol", "image", "iso", "filesystem", "virtio-blk", "virtio-9p"} {
		if !strings.Contains(attach.Description, accepted) {
			t.Fatalf("storage attach help missing accepted value %q: %q", accepted, attach.Description)
		}
	}
}

func TestVMStorageEditPreservesExplicitFalse(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "storage", "edit",
		"--rid", "612", "--storage-id", "7", "--enabled=false", "--read-only=false", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMStorageUpdate {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMStorageUpdatePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode storage update payload: %v", err)
	}
	if payload.Request.Enable == nil || *payload.Request.Enable || payload.Request.ReadOnly == nil || *payload.Request.ReadOnly {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMEditNetworkUsesGenerateMACAndExplicitFalse(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "network", "edit", "--rid", "613", "--network-id", "8",
		"--generate-mac", "--enabled=false", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMNetworkUpdate {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMNetworkUpdatePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode network update payload: %v", err)
	}
	if payload.Request.MacID == nil || *payload.Request.MacID != 0 || payload.Request.Enable == nil || *payload.Request.Enable {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMCreateFlagsUseConservativeDefaults(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "create", "--rid", "615", "--name", "flag-created", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMCreate {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMCreatePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode VM create payload: %v", err)
	}
	created := payload.Request
	if created.RID == nil || *created.RID != 615 || created.Name != "flag-created" || created.CPUSockets != 1 ||
		created.CPUCores != 1 || created.CPUThreads != 1 || created.RAM != 1024*1024*1024 ||
		created.StorageType != libvirtServiceInterfaces.StorageTypeNone || created.VNCEnabled == nil || *created.VNCEnabled ||
		created.VNCWait == nil || *created.VNCWait || created.StartAtBoot == nil || *created.StartAtBoot ||
		created.StartOrder != 0 || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMCreateFlagsOverrideStartOrderAndVNCWait(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "create", "--rid", "619", "--name", "ordered-vm", "--start-order", "7", "--vnc-wait=false", "--json",
	)
	var payload consoleprotocol.VMCreatePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode VM create payload: %v", err)
	}
	if payload.Request.StartOrder != 7 || payload.Request.VNCWait == nil || *payload.Request.VNCWait {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMConfigCPUPinPreservesCommaSeparatedCores(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "config", "cpu", "--rid", "616", "--sockets", "1", "--cores", "2", "--threads", "1",
		"--pin", "0:0,1", "--json",
	)
	var payload consoleprotocol.VMConfigCPUPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode CPU payload: %v", err)
	}
	if len(payload.Request.CPUPinning) != 1 || payload.Request.CPUPinning[0].Socket != 0 ||
		len(payload.Request.CPUPinning[0].Cores) != 2 || payload.Request.CPUPinning[0].Cores[1] != 1 {
		t.Fatalf("CPU pinning = %#v", payload.Request.CPUPinning)
	}
}

func TestVMConfigBhyveOptionPreservesCommas(t *testing.T) {
	const option = "-s 2:0,virtio-net,tap0"
	request := captureDirectVMOperation(t,
		"vms", "config", "bhyve-options", "--rid", "616", "--option="+option, "--json",
	)
	var payload consoleprotocol.VMConfigBhyveOptionsPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode bhyve options payload: %v", err)
	}
	if len(payload.Options) != 1 || payload.Options[0] != option {
		t.Fatalf("bhyve options = %#v", payload.Options)
	}
}

func TestVMConfigBooleanPreservesExplicitFalse(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "config", "qga", "--rid", "617", "--enabled=false", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMConfigQGA {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMConfigBoolPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode QGA payload: %v", err)
	}
	if payload.RID != 617 || payload.Enabled || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMConfigVNCPreservesPasswordByDefault(t *testing.T) {
	request := captureDirectVMOperation(t,
		"vms", "config", "vnc", "--rid", "618", "--enabled=false", "--wait=false", "--json",
	)
	if request.Operation != consoleprotocol.OperationVMConfigVNC {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMConfigVNCPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode VNC payload: %v", err)
	}
	if payload.Changes.Enabled == nil || *payload.Changes.Enabled || payload.Changes.Wait == nil ||
		*payload.Changes.Wait || payload.Changes.Password != nil {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestVMConfigHelpRegistersFocusedCommands(t *testing.T) {
	command := newVMConfigCommand()
	want := map[string]bool{
		"name": false, "description": false, "cpu": false, "memory": false, "vnc": false, "serial": false, "pci": false,
		"autostart": false, "clock": false, "shutdown": false, "boot-rom": false,
		"cloud-init": false, "bhyve-options": false, "unknown-msr": false, "qga": false, "wol": false, "tpm": false,
	}
	for _, child := range command.Commands {
		if _, exists := want[child.Name]; exists {
			want[child.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("expected vms config %s command", name)
		}
	}
	for _, name := range []string{"cpu", "memory", "vnc", "serial", "pci", "clock", "boot-rom", "cloud-init", "bhyve-options", "unknown-msr", "qga", "tpm"} {
		child := findCLIChildCommand(t, command, name)
		if !strings.Contains(strings.ToLower(child.Description), "powered off") {
			t.Fatalf("vms config %s help does not state powered-off requirement: %q", name, child.Description)
		}
	}
}

func TestVMNetworkCommandRegistersNestedLifecycle(t *testing.T) {
	command := newVMNetworkCommand()
	for _, name := range []string{"list", "attach", "edit", "detach"} {
		child := findCLIChildCommand(t, command, name)
		if name != "list" && !strings.Contains(strings.ToLower(child.Description), "powered off") {
			t.Fatalf("vms network %s help = %q", name, child.Description)
		}
	}
}

func TestVMTemplateGetAndCaptureUseTypedOperations(t *testing.T) {
	get := captureDirectVMOperation(t, "vms", "templates", "get", "--template-id", "12", "--json")
	if get.Operation != consoleprotocol.OperationVMTemplateGet {
		t.Fatalf("get operation = %q", get.Operation)
	}
	var getPayload consoleprotocol.VMTemplateGetPayload
	if err := json.Unmarshal(get.Payload, &getPayload); err != nil || getPayload.TemplateID != 12 || !getPayload.JSON {
		t.Fatalf("get payload = %#v, err = %v", getPayload, err)
	}

	capture := captureDirectVMOperation(t, "vms", "templates", "capture", "--rid", "620", "--name", "base", "--json")
	if capture.Operation != consoleprotocol.OperationVMTemplateConvert {
		t.Fatalf("capture operation = %q", capture.Operation)
	}
}

func TestVMQGAInfoUsesTypedOperation(t *testing.T) {
	request := captureDirectVMOperation(t, "vms", "qga", "info", "--rid", "621", "--json")
	if request.Operation != consoleprotocol.OperationVMQGAInfo {
		t.Fatalf("operation = %q", request.Operation)
	}
	var payload consoleprotocol.VMRIDPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil || payload.RID != 621 || !payload.JSON {
		t.Fatalf("payload = %#v, err = %v", payload, err)
	}
}

func TestVMIdentityWOLAndTPMUseTypedOperations(t *testing.T) {
	tests := []struct {
		args      []string
		operation string
	}{
		{[]string{"vms", "config", "name", "--rid", "622", "--name", "renamed", "--json"}, consoleprotocol.OperationVMConfigName},
		{[]string{"vms", "config", "description", "--rid", "622", "--description", "updated", "--json"}, consoleprotocol.OperationVMConfigDescription},
		{[]string{"vms", "config", "wol", "--rid", "622", "--enabled=false", "--json"}, consoleprotocol.OperationVMConfigWOL},
		{[]string{"vms", "config", "tpm", "--rid", "622", "--enabled=true", "--json"}, consoleprotocol.OperationVMConfigTPM},
	}
	for _, test := range tests {
		request := captureDirectVMOperation(t, test.args...)
		if request.Operation != test.operation {
			t.Fatalf("%v operation = %q", test.args, request.Operation)
		}
	}
}

func TestVMCommandsRejectRIDOutsideSupportedRange(t *testing.T) {
	command := newVMsCommand()
	err := command.Run(context.Background(), []string{"vms", "get", "--rid", "10000"})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 9999") {
		t.Fatalf("RID range error = %v", err)
	}
}

func TestEveryVMLeafCommandExposesJSONOutput(t *testing.T) {
	var walk func([]string, *cli.Command)
	walk = func(path []string, command *cli.Command) {
		path = append(path, command.Name)
		if len(command.Commands) != 0 {
			for _, child := range command.Commands {
				walk(path, child)
			}
			return
		}
		for _, flag := range command.Flags {
			for _, name := range flag.Names() {
				if name == "json" {
					return
				}
			}
		}
		t.Errorf("%s does not expose --json", strings.Join(path, " "))
	}
	walk(nil, newVMsCommand())
}

func TestEveryDirectVMRIDFlagDocumentsSupportedRange(t *testing.T) {
	var walk func(*cli.Command)
	walk = func(command *cli.Command) {
		for _, flag := range command.Flags {
			integer, ok := flag.(*cli.IntFlag)
			if ok && integer.Name == "rid" && !strings.Contains(integer.Usage, "1-9999") {
				t.Errorf("%s --rid help = %q", command.FullName(), integer.Usage)
			}
		}
		for _, child := range command.Commands {
			walk(child)
		}
	}
	walk(newVMsCommand())
}

func TestVMActionHelpDistinguishesForceAndGracefulStop(t *testing.T) {
	if usage := newVMActionCommand("stop").Usage; !strings.Contains(strings.ToLower(usage), "force-stop") {
		t.Fatalf("stop help = %q", usage)
	}
	if usage := newVMActionCommand("shutdown").Usage; !strings.Contains(strings.ToLower(usage), "gracefully") {
		t.Fatalf("shutdown help = %q", usage)
	}
}

func findCLIChildCommand(t *testing.T, command *cli.Command, name string) *cli.Command {
	t.Helper()
	for _, child := range command.Commands {
		if child.Name == name {
			return child
		}
	}
	t.Fatalf("command %q not found", name)
	return nil
}

func captureDirectVMOperation(t *testing.T, args ...string) consoleprotocol.Request {
	return captureDirectVMOperationWithResponse(t, consoleprotocol.Response{}, args...)
}

func captureDirectVMOperationWithResponse(
	t *testing.T,
	response consoleprotocol.Response,
	args ...string,
) consoleprotocol.Request {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", "")
	rootDir := t.TempDir()
	dataPath := filepath.Join(rootDir, "data")
	configPath := filepath.Join(rootDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"`+dataPath+`"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	socketPath := consoleprotocol.SocketPath(dataPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	requestCh := make(chan consoleprotocol.Request, 1)
	errorCh := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			errorCh <- err
			return
		}
		defer connection.Close()
		var request consoleprotocol.Request
		if err := json.NewDecoder(connection).Decode(&request); err != nil {
			errorCh <- err
			return
		}
		requestCh <- request
		errorCh <- json.NewEncoder(connection).Encode(response)
	}()

	root := newRootCommand(nil, func() bool { return true })
	commandArgs := append([]string{"sylve", "--config", configPath}, args...)
	if err := root.Run(context.Background(), commandArgs); err != nil {
		t.Fatalf("run direct VM command: %v", err)
	}
	request := <-requestCh
	if err := <-errorCh; err != nil {
		t.Fatalf("serve direct VM command: %v", err)
	}
	return request
}
