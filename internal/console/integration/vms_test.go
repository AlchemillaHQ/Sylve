//go:build freebsd

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"gorm.io/gorm"
)

type consoleVMCreateResult struct {
	Created bool   `json:"created"`
	RID     uint   `json:"rid"`
	Name    string `json:"name"`
}

type consoleVMActionResult struct {
	RID     uint   `json:"rid"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	TaskID  uint   `json:"taskId"`
}

type consoleVMNetworkAttachResult struct {
	Attached   bool   `json:"attached"`
	RID        uint   `json:"rid"`
	SwitchName string `json:"switchName"`
	Emulation  string `json:"emulation"`
}

type consoleVMNetworkDetachResult struct {
	Deleted   bool `json:"deleted"`
	RID       uint `json:"rid"`
	NetworkID uint `json:"networkId"`
}

type consoleVMDeleteResult struct {
	Deleted          bool     `json:"deleted"`
	RID              uint     `json:"rid"`
	Warnings         []string `json:"warnings"`
	RetainedDatasets []string `json:"retainedDatasets"`
}

func TestVMCoreWorkflowIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)

	rid := consoleIntegrationVMRID(t, suite, 0)
	retainedRID := consoleIntegrationVMRID(t, suite, 1)
	vmName := "vm-core-" + suite.runID
	retainedName := "vm-retained-" + suite.runID
	switchName := "vm-switch-" + suite.runID
	addresses := consoleJailVNETAddressesWithOffset(suite.runID, 10)

	// Cleanup is registered in reverse dependency order: VMs before their bridge.
	t.Cleanup(func() { cleanupStandardSwitch(t, suite, switchName) })
	t.Cleanup(func() { cleanupConsoleVM(t, suite, retainedRID) })
	t.Cleanup(func() { cleanupConsoleVM(t, suite, rid) })

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "create", "--type", "standard", "--name", switchName,
		"--network4-manual", addresses.bridgeIPv4CIDR,
		"--private", "--disable-ipv6", "--json")
	var switchResult switchMutationResult
	if err := json.Unmarshal([]byte(output), &switchResult); err != nil {
		t.Fatalf("decode CLI VM switch create: %v\noutput: %s", err, output)
	}
	if !switchResult.Created || switchResult.ID == 0 || switchResult.Type != "standard" {
		t.Fatalf("CLI VM switch create result = %#v", switchResult)
	}

	var standard networkModels.StandardSwitch
	if err := suite.database.First(&standard, switchResult.ID).Error; err != nil {
		t.Fatalf("load VM switch: %v", err)
	}
	if bridge := consoleBridge(t, standard.BridgeName); !hasInterfaceGroup(bridge.Groups, "bridge") || len(bridge.BridgeMembers) != 0 {
		t.Fatalf("VM test bridge must start memberless: %#v", bridge.BridgeMembers)
	}

	requestPath := writeConsoleVMRequest(t, suite, "core", consoleVMCreateRequest(rid, vmName, suite.poolName))
	output = runSylve(t, suite.binaryPath, suite.configPath, "vms", "create", "--file", requestPath, "--json")
	var created consoleVMCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode CLI VM create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.RID != rid || created.Name != vmName {
		t.Fatalf("CLI VM create result = %#v", created)
	}

	vm := consoleVMByRID(t, suite, rid)
	if vm.Name != vmName || vm.StartAtBoot || len(vm.Storages) != 1 || vm.Storages[0].Type != vmModels.VMStorageTypeRaw || len(vm.Networks) != 0 {
		t.Fatalf("created VM = %#v", vm)
	}
	rootDataset, rawDataset := consoleVMDatasets(suite, rid, vm.Storages[0].ID)
	assertConsoleZFSDataset(t, rootDataset, true)
	assertConsoleZFSDataset(t, rawDataset, true)
	assertConsoleVMDomain(t, rid, true)
	vmPath := filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(rid), 10))
	if _, err := os.Stat(filepath.Join(vmPath, strconv.FormatUint(uint64(rid), 10)+"_vars.fd")); err != nil {
		t.Fatalf("VM UEFI variables file: %v", err)
	}
	if _, err := os.Stat(fmt.Sprintf("/%s/sylve/virtual-machines/%d/.sylve/vm.json", suite.poolName, rid)); err != nil {
		t.Fatalf("VM metadata file: %v", err)
	}

	output = runREPLCommand(t, suite.socketPath, "vms get "+strconv.FormatUint(uint64(rid), 10)+" --json")
	var inspected vmModels.VM
	if err := json.Unmarshal([]byte(output), &inspected); err != nil {
		t.Fatalf("decode REPL VM get: %v\noutput: %s", err, output)
	}
	if inspected.RID != rid || inspected.Name != vmName || len(inspected.Storages) != 1 {
		t.Fatalf("REPL VM get = %#v", inspected)
	}

	output = runREPLCommand(t, suite.socketPath,
		"vms addnet "+strconv.FormatUint(uint64(rid), 10)+" "+switchName+" virtio --json")
	var attached consoleVMNetworkAttachResult
	if err := json.Unmarshal([]byte(output), &attached); err != nil {
		t.Fatalf("decode REPL VM network attach: %v\noutput: %s", err, output)
	}
	if !attached.Attached || attached.RID != rid || attached.SwitchName != switchName || attached.Emulation != "virtio" {
		t.Fatalf("REPL VM network attach = %#v", attached)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "networks", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	var networks []vmModels.Network
	if err := json.Unmarshal([]byte(output), &networks); err != nil {
		t.Fatalf("decode CLI VM networks: %v\noutput: %s", err, output)
	}
	if len(networks) != 1 || networks[0].SwitchID != standard.ID || networks[0].MacID == nil || *networks[0].MacID == 0 {
		t.Fatalf("CLI VM networks = %#v", networks)
	}
	network := networks[0]
	autoMACID := *network.MacID
	if domainXML := consoleVMDomainXML(t, rid); !strings.Contains(domainXML, "bridge='"+standard.BridgeName+"'") {
		t.Fatalf("VM domain XML missing test bridge %q:\n%s", standard.BridgeName, domainXML)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "start", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	started := assertConsoleVMAction(t, output, rid, "start")
	output = runREPLCommand(t, suite.socketPath,
		"tasks get "+strconv.FormatUint(uint64(started.TaskID), 10)+" --json")
	var inspectedTask taskModels.GuestLifecycleTask
	if err := json.Unmarshal([]byte(output), &inspectedTask); err != nil {
		t.Fatalf("decode REPL lifecycle task: %v\noutput: %s", err, output)
	}
	if inspectedTask.ID != started.TaskID || inspectedTask.GuestType != taskModels.GuestTypeVM || inspectedTask.GuestID != rid || inspectedTask.Action != "start" {
		t.Fatalf("REPL lifecycle task = %#v", inspectedTask)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"tasks", "recent", "--guest-type", taskModels.GuestTypeVM,
		"--guest-id", strconv.FormatUint(uint64(rid), 10), "--limit", "1", "--json")
	var recentTasks []taskModels.GuestLifecycleTask
	if err := json.Unmarshal([]byte(output), &recentTasks); err != nil {
		t.Fatalf("decode CLI recent lifecycle tasks: %v\noutput: %s", err, output)
	}
	if len(recentTasks) != 1 || recentTasks[0].ID != started.TaskID {
		t.Fatalf("CLI recent lifecycle tasks = %#v", recentTasks)
	}
	waitForConsoleVMTask(t, suite, started.TaskID)
	waitForConsoleVMState(t, rid, "running")
	bridge := consoleBridge(t, standard.BridgeName)
	if len(bridge.BridgeMembers) != 1 {
		t.Fatalf("running VM bridge members = %#v", bridge.BridgeMembers)
	}

	purgeError := runREPLCommandFailure(t, suite.socketPath,
		"vms purge "+strconv.FormatUint(uint64(rid), 10))
	if !strings.Contains(purgeError, "vm_not_orphaned") {
		t.Fatalf("purge live VM error = %q", purgeError)
	}

	output = runREPLCommand(t, suite.socketPath, "vms stop "+strconv.FormatUint(uint64(rid), 10)+" --json")
	stopped := assertConsoleVMAction(t, output, rid, "stop")
	waitForConsoleVMTask(t, suite, stopped.TaskID)
	waitForConsoleVMState(t, rid, "shut off")
	bridge = consoleBridge(t, standard.BridgeName)
	if len(bridge.BridgeMembers) != 0 {
		t.Fatalf("stopped VM bridge members = %#v", bridge.BridgeMembers)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "rmnet", "--rid", strconv.FormatUint(uint64(rid), 10), "--net-id", strconv.FormatUint(uint64(network.ID), 10), "--json")
	var detached consoleVMNetworkDetachResult
	if err := json.Unmarshal([]byte(output), &detached); err != nil {
		t.Fatalf("decode CLI VM network detach: %v\noutput: %s", err, output)
	}
	if !detached.Deleted || detached.RID != rid || detached.NetworkID != network.ID {
		t.Fatalf("CLI VM network detach = %#v", detached)
	}
	if vm = consoleVMByRID(t, suite, rid); len(vm.Networks) != 0 {
		t.Fatalf("VM networks after detach = %#v", vm.Networks)
	}
	output = runREPLCommand(t, suite.socketPath, "objects delete "+strconv.FormatUint(uint64(autoMACID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("delete detached VM MAC object output = %q", output)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "delete", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--delete-macs", "--delete-raw-disks", "--delete-volumes", "--json")
	var deleted consoleVMDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode CLI VM delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.RID != rid || len(deleted.Warnings) != 0 || len(deleted.RetainedDatasets) != 0 {
		t.Fatalf("CLI VM delete result = %#v", deleted)
	}
	assertConsoleVMDeleted(t, suite, rid, rootDataset, vmPath)

	retainedPath := writeConsoleVMRequest(t, suite, "retained", consoleVMCreateRequest(retainedRID, retainedName, suite.poolName))
	output = runREPLCommand(t, suite.socketPath, "vms create --file "+retainedPath+" --json")
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode REPL retained VM create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.RID != retainedRID || created.Name != retainedName {
		t.Fatalf("REPL retained VM create = %#v", created)
	}
	retainedVM := consoleVMByRID(t, suite, retainedRID)
	retainedRoot, retainedRaw := consoleVMDatasets(suite, retainedRID, retainedVM.Storages[0].ID)

	output = runREPLCommand(t, suite.socketPath, "vms delete "+strconv.FormatUint(uint64(retainedRID), 10)+" --json")
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL retained VM delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.RID != retainedRID || len(deleted.RetainedDatasets) == 0 {
		t.Fatalf("REPL retained VM delete result = %#v", deleted)
	}
	assertConsoleVMDeleted(t, suite, retainedRID, "", filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(retainedRID), 10)))
	assertConsoleZFSDataset(t, retainedRoot, true)
	assertConsoleZFSDataset(t, retainedRaw, true)
	destroyConsoleVMDataset(t, retainedRoot)
	assertConsoleZFSDataset(t, retainedRoot, false)

	output = runREPLCommand(t, suite.socketPath,
		"switches delete standard "+strconv.FormatUint(uint64(standard.ID), 10)+" --json")
	var deletedSwitch switchMutationResult
	if err := json.Unmarshal([]byte(output), &deletedSwitch); err != nil {
		t.Fatalf("decode REPL VM switch delete: %v\noutput: %s", err, output)
	}
	if !deletedSwitch.Deleted || deletedSwitch.ID != standard.ID || deletedSwitch.Type != "standard" {
		t.Fatalf("REPL VM switch delete = %#v", deletedSwitch)
	}
	assertConsoleInterfaceMissing(t, standard.BridgeName)
}

func consoleVMCreateRequest(rid uint, name, pool string) libvirtServiceInterfaces.CreateVMRequest {
	storageSize := uint64(internal.MinimumVMStorageSize)
	vncEnabled := false
	startAtBoot := false
	return libvirtServiceInterfaces.CreateVMRequest{
		Name:                 name,
		RID:                  &rid,
		Description:          "console integration VM",
		StoragePool:          pool,
		StorageType:          libvirtServiceInterfaces.StorageTypeRaw,
		StorageSize:          &storageSize,
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "none",
		CPUSockets:           1,
		CPUCores:             1,
		CPUThreads:           1,
		RAM:                  256 * 1024 * 1024,
		VNCEnabled:           &vncEnabled,
		StartAtBoot:          &startAtBoot,
		TimeOffset:           libvirtServiceInterfaces.TimeOffsetUTC,
		BootROM:              "uefi",
	}
}

func writeConsoleVMRequest(t *testing.T, suite *consoleIntegrationSuite, suffix string, request libvirtServiceInterfaces.CreateVMRequest) string {
	t.Helper()
	contents, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal VM request: %v", err)
	}
	path := filepath.Join(suite.root, "vm-"+suffix+"-"+suite.runID+".json")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatalf("write VM request %s: %v", path, err)
	}
	return path
}

func consoleIntegrationVMRID(t *testing.T, suite *consoleIntegrationSuite, offset uint) uint {
	t.Helper()
	value, err := strconv.ParseUint(suite.runID, 16, 64)
	if err != nil {
		t.Fatalf("parse suite run ID %q: %v", suite.runID, err)
	}
	for attempt := uint(0); attempt < 7000; attempt++ {
		rid := uint(1000 + (value+uint64(offset)+uint64(attempt))%7000)
		output, err := exec.Command("virsh", "-c", "bhyve:///system", "dominfo", strconv.FormatUint(uint64(rid), 10)).CombinedOutput()
		if err == nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(output)), "failed to get domain") {
			return rid
		}
		t.Fatalf("check VM RID %d availability: %v\n%s", rid, err, output)
	}
	t.Fatal("could not find an unused VM RID")
	return 0
}

func consoleVMByRID(t *testing.T, suite *consoleIntegrationSuite, rid uint) vmModels.VM {
	t.Helper()
	var vm vmModels.VM
	if err := suite.database.
		Preload("Storages").
		Preload("Storages.Dataset").
		Preload("Networks").
		Preload("Networks.AddressObj").
		Preload("Networks.AddressObj.Entries").
		Where("rid = ?", rid).
		First(&vm).Error; err != nil {
		t.Fatalf("load VM %d: %v", rid, err)
	}
	return vm
}

func consoleVMDatasets(suite *consoleIntegrationSuite, rid, storageID uint) (string, string) {
	root := fmt.Sprintf("%s/sylve/virtual-machines/%d", suite.poolName, rid)
	return root, fmt.Sprintf("%s/raw-%d", root, storageID)
}

func assertConsoleVMAction(t *testing.T, output string, rid uint, action string) consoleVMActionResult {
	t.Helper()
	var result consoleVMActionResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode VM %s result: %v\noutput: %s", action, err, output)
	}
	if result.RID != rid || result.Action != action || result.TaskID == 0 {
		t.Fatalf("VM %s result = %#v\noutput: %s", action, result, output)
	}
	return result
}

func waitForConsoleVMTask(t *testing.T, suite *consoleIntegrationSuite, taskID uint) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last taskModels.GuestLifecycleTask
	for time.Now().Before(deadline) {
		var task taskModels.GuestLifecycleTask
		if err := suite.database.First(&task, taskID).Error; err != nil {
			t.Fatalf("load VM lifecycle task %d: %v", taskID, err)
		}
		last = task
		switch task.Status {
		case taskModels.LifecycleTaskStatusSuccess:
			return
		case taskModels.LifecycleTaskStatusFailed:
			t.Fatalf("VM lifecycle task %d failed: %s", taskID, task.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("VM lifecycle task %d did not complete: %#v", taskID, last)
}

func waitForConsoleVMState(t *testing.T, rid uint, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastOutput []byte
	var lastErr error
	for time.Now().Before(deadline) {
		lastOutput, lastErr = exec.Command("virsh", "-c", "bhyve:///system", "domstate", strconv.FormatUint(uint64(rid), 10)).CombinedOutput()
		if lastErr == nil && strings.TrimSpace(string(lastOutput)) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("VM %d state did not become %q: %v\n%s", rid, want, lastErr, lastOutput)
}

func assertConsoleVMDomain(t *testing.T, rid uint, want bool) {
	t.Helper()
	output, err := exec.Command("virsh", "-c", "bhyve:///system", "dominfo", strconv.FormatUint(uint64(rid), 10)).CombinedOutput()
	if want && err != nil {
		t.Fatalf("VM %d domain must exist: %v\n%s", rid, err, output)
	}
	if !want && err == nil {
		t.Fatalf("VM %d domain still exists:\n%s", rid, output)
	}
}

func consoleVMDomainXML(t *testing.T, rid uint) string {
	t.Helper()
	output, err := exec.Command("virsh", "-c", "bhyve:///system", "dumpxml", strconv.FormatUint(uint64(rid), 10)).CombinedOutput()
	if err != nil {
		t.Fatalf("dump VM %d XML: %v\n%s", rid, err, output)
	}
	return string(output)
}

func assertConsoleZFSDataset(t *testing.T, dataset string, want bool) {
	t.Helper()
	output, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput()
	if want && err != nil {
		t.Fatalf("dataset %s must exist: %v\n%s", dataset, err, output)
	}
	if !want && err == nil {
		t.Fatalf("dataset %s still exists: %s", dataset, output)
	}
}

func assertConsoleVMDeleted(t *testing.T, suite *consoleIntegrationSuite, rid uint, dataset, vmPath string) {
	t.Helper()
	var vm vmModels.VM
	if err := suite.database.Where("rid = ?", rid).First(&vm).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("VM %d after delete error = %v, want not found", rid, err)
	}
	assertConsoleVMDomain(t, rid, false)
	if dataset != "" {
		assertConsoleZFSDataset(t, dataset, false)
	}
	if _, err := os.Stat(vmPath); !os.IsNotExist(err) {
		t.Fatalf("VM runtime directory after delete error = %v, want not exist", err)
	}
}

func cleanupConsoleVM(t *testing.T, suite *consoleIntegrationSuite, rid uint) {
	t.Helper()
	if suite.virtualMachine != nil {
		if _, err := suite.virtualMachine.ForceRemoveVM(rid, true, context.Background()); err != nil {
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, "vm_not_found") {
				t.Errorf("force-remove VM %d during cleanup: %v", rid, err)
			}
		}
	}

	destroyConsoleVMDataset(t, fmt.Sprintf("%s/sylve/virtual-machines/%d", suite.poolName, rid))
	if err := os.RemoveAll(filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(rid), 10))); err != nil {
		t.Errorf("remove VM %d runtime directory during cleanup: %v", rid, err)
	}
}

func destroyConsoleVMDataset(t *testing.T, dataset string) {
	t.Helper()
	if _, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput(); err == nil {
		if output, err := exec.Command("zfs", "destroy", "-r", dataset).CombinedOutput(); err != nil {
			t.Errorf("destroy owned VM dataset %s during cleanup: %v\n%s", dataset, err, output)
		}
	}
}
