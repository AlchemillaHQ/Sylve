// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
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

const (
	consoleIntegrationVMProbeTimeout   = 5 * time.Second
	consoleIntegrationVMCleanupTimeout = 30 * time.Second
)

type consoleVMNetworkAttachResult struct {
	Attached   bool   `json:"attached"`
	RID        uint   `json:"rid"`
	NetworkID  uint   `json:"networkId"`
	SwitchName string `json:"switchName"`
	Emulation  string `json:"emulation"`
	MacID      *uint  `json:"macId"`
	MAC        string `json:"mac"`
	Enabled    bool   `json:"enabled"`
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

type consoleVMStorageAttachResult struct {
	Attached bool                         `json:"attached"`
	RID      uint                         `json:"rid"`
	Storage  libvirtService.VMStorageInfo `json:"storage"`
}

type consoleVMStorageUpdateResult struct {
	Updated bool                         `json:"updated"`
	RID     uint                         `json:"rid"`
	Storage libvirtService.VMStorageInfo `json:"storage"`
}

type consoleVMStorageDetachResult struct {
	Detached bool                         `json:"detached"`
	RID      uint                         `json:"rid"`
	Storage  libvirtService.VMStorageInfo `json:"storage"`
}

type consoleVMNetworkUpdateResult struct {
	Updated    bool   `json:"updated"`
	RID        uint   `json:"rid"`
	NetworkID  uint   `json:"networkId"`
	SwitchName string `json:"switchName"`
	SwitchType string `json:"switchType"`
	Emulation  string `json:"emulation"`
	MacID      *uint  `json:"macId"`
	MAC        string `json:"mac"`
	Enabled    bool   `json:"enabled"`
}

func TestAcceptanceVMStorageAndDeletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	rid := consoleIntegrationVMRID(t, suite, 20)
	vmName := "vm-storage-" + suite.runID
	firstSwitchName := "vm-storage-a-" + suite.runID
	secondSwitchName := "vm-storage-b-" + suite.runID
	firstAddresses := consoleJailVNETAddressesWithOffset(suite.runID, 30)
	secondAddresses := consoleJailVNETAddressesWithOffset(suite.runID, 40)

	t.Cleanup(func() { cleanupStandardSwitch(t, suite, secondSwitchName) })
	t.Cleanup(func() { cleanupStandardSwitch(t, suite, firstSwitchName) })
	t.Cleanup(func() { cleanupConsoleVM(t, suite, rid) })

	firstSwitch := createConsoleVMTestSwitch(t, suite, firstSwitchName, firstAddresses.bridgeIPv4CIDR)
	secondSwitch := createConsoleVMTestSwitch(t, suite, secondSwitchName, secondAddresses.bridgeIPv4CIDR)

	requestPath := writeConsoleVMRequest(t, suite, "storage", consoleVMCreateRequest(rid, vmName, suite.poolName))
	output := runSylve(t, suite.binaryPath, suite.configPath, "vms", "create", "--file", requestPath, "--json")
	var created consoleVMCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode VM storage test create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.RID != rid {
		t.Fatalf("VM storage test create = %#v", created)
	}
	vm := consoleVMByRID(t, suite, rid)
	if len(vm.Storages) != 1 {
		t.Fatalf("initial VM storage = %#v", vm.Storages)
	}
	baseStorage := vm.Storages[0]
	rootDataset, baseRawDataset := consoleVMDatasets(suite, rid, baseStorage.ID)

	output = runREPLCommand(t, suite.socketPath,
		"vms addnet "+strconv.FormatUint(uint64(rid), 10)+" "+firstSwitchName+" virtio --json")
	var attachedNetwork consoleVMNetworkAttachResult
	if err := json.Unmarshal([]byte(output), &attachedNetwork); err != nil {
		t.Fatalf("decode attached network: %v\noutput: %s", err, output)
	}
	if !attachedNetwork.Attached || attachedNetwork.NetworkID == 0 || attachedNetwork.MacID == nil {
		t.Fatalf("attached network = %#v", attachedNetwork)
	}
	oldMACID := *attachedNetwork.MacID

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "start", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	started := assertConsoleVMAction(t, output, rid, "start")
	waitForConsoleVMTask(t, suite, started.TaskID)
	waitForConsoleVMState(t, rid, "running")

	storageFailure := runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "edit", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--storage-id", strconv.FormatUint(uint64(baseStorage.ID), 10), "--name", "must-not-change", "--json")
	if !strings.Contains(storageFailure, "domain_state_not_shutoff") {
		t.Fatalf("powered-on storage edit error = %q", storageFailure)
	}
	networkFailure := runREPLCommandFailure(t, suite.socketPath,
		"vms editnet "+strconv.FormatUint(uint64(rid), 10)+" "+strconv.FormatUint(uint64(attachedNetwork.NetworkID), 10)+" --emulation e1000")
	if !strings.Contains(networkFailure, "domain_state_not_shutoff") {
		t.Fatalf("powered-on network edit error = %q", networkFailure)
	}
	unchanged := consoleVMByRID(t, suite, rid)
	if unchanged.Storages[0].Name == "must-not-change" || unchanged.Networks[0].Emulation != "virtio" {
		t.Fatalf("powered-on rejection changed VM state: storages=%#v networks=%#v", unchanged.Storages, unchanged.Networks)
	}

	output = runREPLCommand(t, suite.socketPath, "vms stop "+strconv.FormatUint(uint64(rid), 10)+" --json")
	stopped := assertConsoleVMAction(t, output, rid, "stop")
	waitForConsoleVMTask(t, suite, stopped.TaskID)
	waitForConsoleVMState(t, rid, "shut off")

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "editnet", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--network-id", strconv.FormatUint(uint64(attachedNetwork.NetworkID), 10),
		"--switch", secondSwitchName, "--emulation", "e1000", "--generate-mac", "--enabled=false", "--json")
	var updatedNetwork consoleVMNetworkUpdateResult
	if err := json.Unmarshal([]byte(output), &updatedNetwork); err != nil {
		t.Fatalf("decode updated network: %v\noutput: %s", err, output)
	}
	if !updatedNetwork.Updated || updatedNetwork.NetworkID != attachedNetwork.NetworkID ||
		updatedNetwork.SwitchName != secondSwitchName || updatedNetwork.Emulation != "e1000" || updatedNetwork.Enabled ||
		updatedNetwork.MacID == nil || *updatedNetwork.MacID == oldMACID {
		t.Fatalf("updated network = %#v", updatedNetwork)
	}
	newMACID := *updatedNetwork.MacID
	assertConsoleObjectExists(t, suite, oldMACID, true)
	assertConsoleObjectExists(t, suite, newMACID, true)
	vm = consoleVMByRID(t, suite, rid)
	if len(vm.Networks) != 1 || vm.Networks[0].SwitchID != secondSwitch.ID || vm.Networks[0].Enable {
		t.Fatalf("stored updated network = %#v", vm.Networks)
	}
	if firstSwitch.ID == secondSwitch.ID {
		t.Fatal("network update test switches unexpectedly share an ID")
	}

	rawImportPath := filepath.Join(suite.root, "vm-storage-import-"+suite.runID+".raw")
	if err := os.WriteFile(rawImportPath, make([]byte, 2*1024*1024), 0o600); err != nil {
		t.Fatalf("write raw import source: %v", err)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms storage attach "+strconv.FormatUint(uint64(rid), 10)+
			" --type raw --name imported-raw --pool "+suite.poolName+
			" --raw-path "+rawImportPath+" --emulation virtio-blk --json")
	importedRaw := decodeConsoleVMStorageAttach(t, output)
	if importedRaw.Storage.Type != libvirtServiceInterfaces.StorageTypeRaw || importedRaw.Storage.Ownership != libvirtService.VMStorageOwnershipManaged {
		t.Fatalf("imported raw storage = %#v", importedRaw)
	}
	assertConsoleZFSDataset(t, importedRaw.Storage.Backing, true)

	sourceZVOL := suite.poolName + "/vm-storage-source-" + suite.runID
	createConsoleZVOL(t, sourceZVOL, "32M")
	t.Cleanup(func() { destroyConsoleVMDataset(t, sourceZVOL) })
	sourceZVOLGUID := zfsPropertyValue(t, "guid", sourceZVOL)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "attach", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--type", "zvol", "--name", "imported-zvol", "--pool", suite.poolName,
		"--dataset-guid", sourceZVOLGUID, "--emulation", "nvme", "--json")
	importedZVOL := decodeConsoleVMStorageAttach(t, output)
	if importedZVOL.Storage.Type != libvirtServiceInterfaces.StorageTypeZVOL || importedZVOL.Storage.Ownership != libvirtService.VMStorageOwnershipManaged {
		t.Fatalf("imported ZVOL storage = %#v", importedZVOL)
	}
	assertConsoleZFSDataset(t, sourceZVOL, false)
	assertConsoleZFSDataset(t, importedZVOL.Storage.Backing, true)

	imagePath := filepath.Join(suite.root, "vm-storage-"+suite.runID+".iso")
	if err := os.WriteFile(imagePath, []byte("console integration ISO placeholder"), 0o600); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	imageUUID := "vm-storage-image-" + suite.runID
	image := utilitiesModels.Downloads{
		UUID: imageUUID, Path: imagePath, Name: filepath.Base(imagePath), Type: utilitiesModels.DownloadTypePath,
		URL: "file://" + imagePath, Progress: 100, Size: int64(len("console integration ISO placeholder")),
		UType: utilitiesModels.DownloadUTypeOther, Status: utilitiesModels.DownloadStatusDone,
	}
	if err := suite.database.Create(&image).Error; err != nil {
		t.Fatalf("seed image download: %v", err)
	}
	t.Cleanup(func() { _ = suite.database.Delete(&image).Error })
	output = runREPLCommand(t, suite.socketPath,
		"vms storage attach "+strconv.FormatUint(uint64(rid), 10)+
			" --type image --name installer --image-uuid "+imageUUID+" --json")
	attachedImage := decodeConsoleVMStorageAttach(t, output)
	if attachedImage.Storage.Type != libvirtServiceInterfaces.StorageTypeDiskImage ||
		attachedImage.Storage.Ownership != libvirtService.VMStorageOwnershipRetained {
		t.Fatalf("attached image storage = %#v", attachedImage)
	}

	externalFilesystem := suite.poolName + "/vm-storage-share-" + suite.runID
	createConsoleFilesystem(t, externalFilesystem)
	t.Cleanup(func() { destroyConsoleVMDataset(t, externalFilesystem) })
	externalFilesystemGUID := zfsPropertyValue(t, "guid", externalFilesystem)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "attach", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--type", "filesystem", "--name", "shared-data", "--dataset-guid", externalFilesystemGUID,
		"--filesystem-target", "shared_data", "--read-only", "--json")
	attachedFilesystem := decodeConsoleVMStorageAttach(t, output)
	if attachedFilesystem.Storage.Type != libvirtServiceInterfaces.StorageTypeFilesystem ||
		attachedFilesystem.Storage.Ownership != libvirtService.VMStorageOwnershipExternal || !attachedFilesystem.Storage.ReadOnly {
		t.Fatalf("attached filesystem storage = %#v", attachedFilesystem)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "attach", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--type", "raw", "--name", "detach-me", "--pool", suite.poolName, "--size", "16MiB", "--json")
	detachable := decodeConsoleVMStorageAttach(t, output)
	output = runREPLCommand(t, suite.socketPath,
		"vms storage detach "+strconv.FormatUint(uint64(rid), 10)+" "+strconv.FormatUint(uint64(detachable.Storage.ID), 10)+" --json")
	var detached consoleVMStorageDetachResult
	if err := json.Unmarshal([]byte(output), &detached); err != nil {
		t.Fatalf("decode detached storage: %v\noutput: %s", err, output)
	}
	if !detached.Detached || detached.Storage.Backing != detachable.Storage.Backing {
		t.Fatalf("detached storage = %#v", detached)
	}
	assertConsoleZFSDataset(t, detachable.Storage.Backing, true)
	destroyConsoleVMDataset(t, detachable.Storage.Backing)
	assertConsoleZFSDataset(t, detachable.Storage.Backing, false)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "edit", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--storage-id", strconv.FormatUint(uint64(importedRaw.Storage.ID), 10),
		"--name", "renamed-raw", "--emulation", "nvme", "--enabled=false", "--json")
	var updatedStorage consoleVMStorageUpdateResult
	if err := json.Unmarshal([]byte(output), &updatedStorage); err != nil {
		t.Fatalf("decode storage edit: %v\noutput: %s", err, output)
	}
	if !updatedStorage.Updated || updatedStorage.Storage.Name != "renamed-raw" || updatedStorage.Storage.Enabled ||
		updatedStorage.Storage.Emulation != libvirtServiceInterfaces.NVMEStorageEmulation {
		t.Fatalf("updated raw storage = %#v", updatedStorage)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms storage resize "+strconv.FormatUint(uint64(rid), 10)+" "+strconv.FormatUint(uint64(importedRaw.Storage.ID), 10)+" --size 48MiB --json")
	if err := json.Unmarshal([]byte(output), &updatedStorage); err != nil {
		t.Fatalf("decode raw resize: %v\noutput: %s", err, output)
	}
	if updatedStorage.Storage.Size != 48*1024*1024 {
		t.Fatalf("resized raw storage = %#v", updatedStorage.Storage)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "resize", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--storage-id", strconv.FormatUint(uint64(importedZVOL.Storage.ID), 10), "--size", "48MiB", "--json")
	if err := json.Unmarshal([]byte(output), &updatedStorage); err != nil {
		t.Fatalf("decode ZVOL resize: %v\noutput: %s", err, output)
	}
	if updatedStorage.Storage.Size != 48*1024*1024 {
		t.Fatalf("resized ZVOL storage = %#v", updatedStorage.Storage)
	}

	directListOutput := runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "storage", "list", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	replListOutput := runREPLCommand(t, suite.socketPath,
		"vms storage list "+strconv.FormatUint(uint64(rid), 10)+" --json")
	var directStorages, replStorages []libvirtService.VMStorageInfo
	if err := json.Unmarshal([]byte(directListOutput), &directStorages); err != nil {
		t.Fatalf("decode direct storage list: %v\noutput: %s", err, directListOutput)
	}
	if err := json.Unmarshal([]byte(replListOutput), &replStorages); err != nil {
		t.Fatalf("decode REPL storage list: %v\noutput: %s", err, replListOutput)
	}
	if !reflect.DeepEqual(directStorages, replStorages) || len(directStorages) != 5 {
		t.Fatalf("direct/REPL storage lists differ:\ndirect=%#v\nrepl=%#v", directStorages, replStorages)
	}

	directPreviewOutput := runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "delete", "--rid", strconv.FormatUint(uint64(rid), 10), "--dry-run", "--json")
	replPreviewOutput := runREPLCommand(t, suite.socketPath,
		"vms delete "+strconv.FormatUint(uint64(rid), 10)+" --dry-run --json")
	var directPreview, replPreview libvirtService.VMRemovalPreview
	if err := json.Unmarshal([]byte(directPreviewOutput), &directPreview); err != nil {
		t.Fatalf("decode direct deletion preview: %v\noutput: %s", err, directPreviewOutput)
	}
	if err := json.Unmarshal([]byte(replPreviewOutput), &replPreview); err != nil {
		t.Fatalf("decode REPL deletion preview: %v\noutput: %s", err, replPreviewOutput)
	}
	if !reflect.DeepEqual(directPreview, replPreview) || len(directPreview.DeleteRawDatasets) != 0 ||
		len(directPreview.DeleteZVOLDatasets) != 0 || len(directPreview.RetainMACObjectIDs) != 1 {
		t.Fatalf("default deletion previews differ or are destructive:\ndirect=%#v\nrepl=%#v", directPreview, replPreview)
	}
	if vm = consoleVMByRID(t, suite, rid); vm.ID == 0 {
		t.Fatal("deletion preview removed VM registration")
	}

	selectedPreviewOutput := runREPLCommand(t, suite.socketPath,
		"vms delete "+strconv.FormatUint(uint64(rid), 10)+
			" --delete-macs --delete-raw-disks --delete-volumes --dry-run --json")
	var selectedPreview libvirtService.VMRemovalPreview
	if err := json.Unmarshal([]byte(selectedPreviewOutput), &selectedPreview); err != nil {
		t.Fatalf("decode selected deletion preview: %v\noutput: %s", err, selectedPreviewOutput)
	}
	if len(selectedPreview.DeleteRawDatasets) != 2 || len(selectedPreview.DeleteZVOLDatasets) != 1 ||
		len(selectedPreview.DeleteMACObjectIDs) != 1 || !slices.Contains(selectedPreview.RetainedDatasets, externalFilesystem) ||
		!slices.Contains(selectedPreview.RetainedImageUUIDs, imageUUID) {
		t.Fatalf("selected deletion preview = %#v", selectedPreview)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "delete", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--delete-macs", "--delete-raw-disks", "--delete-volumes", "--json")
	var deleted consoleVMDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode storage VM deletion: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.RID != rid || !slices.Contains(deleted.RetainedDatasets, externalFilesystem) {
		t.Fatalf("storage VM deletion = %#v", deleted)
	}
	assertConsoleVMDeleted(t, suite, rid, "", filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(rid), 10)))
	for _, dataset := range []string{baseRawDataset, importedRaw.Storage.Backing, importedZVOL.Storage.Backing} {
		assertConsoleZFSDataset(t, dataset, false)
	}
	assertConsoleZFSDataset(t, rootDataset, true)
	assertConsoleZFSDataset(t, externalFilesystem, true)
	if _, err := os.Stat(rawImportPath); err != nil {
		t.Fatalf("raw import source was not retained: %v", err)
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("image backing was not retained: %v", err)
	}
	assertConsoleObjectExists(t, suite, oldMACID, true)
	assertConsoleObjectExists(t, suite, newMACID, false)
	destroyConsoleVMDataset(t, rootDataset)
	output = runREPLCommand(t, suite.socketPath, "objects delete "+strconv.FormatUint(uint64(oldMACID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("delete retained old MAC output = %q", output)
	}
}

func createConsoleVMTestSwitch(
	t *testing.T,
	suite *consoleIntegrationSuite,
	name, network4 string,
) networkModels.StandardSwitch {
	t.Helper()
	output := runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "create", "--type", "standard", "--name", name,
		"--network4-manual", network4, "--private", "--disable-ipv6", "--json")
	var result switchMutationResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode VM test switch create: %v\noutput: %s", err, output)
	}
	if !result.Created || result.ID == 0 {
		t.Fatalf("VM test switch create = %#v", result)
	}
	var standard networkModels.StandardSwitch
	if err := suite.database.First(&standard, result.ID).Error; err != nil {
		t.Fatalf("load VM test switch: %v", err)
	}
	return standard
}

func decodeConsoleVMStorageAttach(t *testing.T, output string) consoleVMStorageAttachResult {
	t.Helper()
	var result consoleVMStorageAttachResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode VM storage attach: %v\noutput: %s", err, output)
	}
	if !result.Attached || result.RID == 0 || result.Storage.ID == 0 {
		t.Fatalf("VM storage attach = %#v", result)
	}
	return result
}

func createConsoleFilesystem(t *testing.T, dataset string) {
	t.Helper()
	output, err := exec.Command("zfs", "create", dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("create filesystem dataset %s: %v\n%s", dataset, err, output)
	}
}

func createConsoleZVOL(t *testing.T, dataset, size string) {
	t.Helper()
	output, err := exec.Command("zfs", "create", "-s", "-V", size, dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("create ZVOL dataset %s: %v\n%s", dataset, err, output)
	}
}

func assertConsoleObjectExists(t *testing.T, suite *consoleIntegrationSuite, objectID uint, want bool) {
	t.Helper()
	var object networkModels.Object
	err := suite.database.First(&object, objectID).Error
	if want && err != nil {
		t.Fatalf("network object %d must exist: %v", objectID, err)
	}
	if !want && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("network object %d existence error = %v, want not found", objectID, err)
	}
}

func TestAcceptanceVMCoreWorkflow(t *testing.T) {
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
		output, err := runConsoleVMCommand(
			consoleIntegrationVMProbeTimeout,
			"virsh", "-c", "bhyve:///system", "dominfo", strconv.FormatUint(uint64(rid), 10),
		)
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
		lastOutput, lastErr = runConsoleVMCommand(
			consoleIntegrationVMProbeTimeout,
			"virsh", "-c", "bhyve:///system", "domstate", strconv.FormatUint(uint64(rid), 10),
		)
		if lastErr == nil && strings.TrimSpace(string(lastOutput)) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("VM %d state did not become %q: %v\n%s", rid, want, lastErr, lastOutput)
}

func assertConsoleVMDomain(t *testing.T, rid uint, want bool) {
	t.Helper()
	output, err := runConsoleVMCommand(
		consoleIntegrationVMProbeTimeout,
		"virsh", "-c", "bhyve:///system", "dominfo", strconv.FormatUint(uint64(rid), 10),
	)
	if want && err != nil {
		t.Fatalf("VM %d domain must exist: %v\n%s", rid, err, output)
	}
	if !want && err == nil {
		t.Fatalf("VM %d domain still exists:\n%s", rid, output)
	}
}

func consoleVMDomainXML(t *testing.T, rid uint) string {
	t.Helper()
	output, err := runConsoleVMCommand(
		consoleIntegrationVMProbeTimeout,
		"virsh", "-c", "bhyve:///system", "dumpxml", strconv.FormatUint(uint64(rid), 10),
	)
	if err != nil {
		t.Fatalf("dump VM %d XML: %v\n%s", rid, err, output)
	}
	return string(output)
}

func assertConsoleZFSDataset(t *testing.T, dataset string, want bool) {
	t.Helper()
	output, err := runConsoleVMCommand(
		consoleIntegrationVMProbeTimeout,
		"zfs", "list", "-H", "-o", "name", dataset,
	)
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
		ctx, cancel := context.WithTimeout(context.Background(), consoleIntegrationVMCleanupTimeout)
		result := make(chan error, 1)
		go func() {
			_, err := suite.virtualMachine.ForceRemoveVM(rid, true, ctx)
			result <- err
		}()
		var err error
		select {
		case err = <-result:
			cancel()
		case <-ctx.Done():
			cancel()
			t.Errorf("force-remove VM %d timed out after %s", rid, consoleIntegrationVMCleanupTimeout)
			return
		}
		if err != nil {
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
	if _, err := runConsoleVMCommand(
		consoleIntegrationVMProbeTimeout,
		"zfs", "list", "-H", "-o", "name", dataset,
	); err == nil {
		if output, err := runConsoleVMCommand(
			consoleIntegrationVMCleanupTimeout,
			"zfs", "destroy", "-r", dataset,
		); err != nil {
			t.Errorf("destroy owned VM dataset %s during cleanup: %v\n%s", dataset, err, output)
		}
	}
}

func runConsoleVMCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		return output, fmt.Errorf("%s timed out after %s: %w", name, timeout, ctx.Err())
	}
	return output, err
}

type consoleVMConfigMutationResult struct {
	Updated       bool   `json:"updated"`
	RID           uint   `json:"rid"`
	Configuration string `json:"configuration"`
}

func TestAcceptanceVMCoreLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	rid := consoleIntegrationVMRID(t, suite, 50)
	failedRID := consoleIntegrationVMRID(t, suite, 52)
	name := "vm-config-" + suite.runID
	t.Cleanup(func() { cleanupConsoleVM(t, suite, rid) })

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "create", "--rid", strconv.FormatUint(uint64(rid), 10), "--name", name,
		"--description", "flag-created VM", "--cpu-sockets", "1", "--cpu-cores", "1", "--cpu-threads", "1",
		"--ram", "256MiB", "--storage-pool", suite.poolName, "--storage-type", "raw",
		"--storage-size", "256MiB", "--storage-emulation", "virtio-blk",
		"--vnc-enabled=false", "--start-at-boot=false", "--time-offset", "utc", "--json")
	var created consoleVMCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode flag-created VM: %v\noutput: %s", err, output)
	}
	if !created.Created || created.RID != rid || created.Name != name {
		t.Fatalf("flag-created VM result = %#v", created)
	}
	vm := consoleVMByRID(t, suite, rid)
	if vm.RAM != 256*1024*1024 || vm.VNCEnabled || vm.StartAtBoot || vm.TimeOffset != vmModels.TimeOffsetUTC ||
		len(vm.Storages) != 1 || vm.Storages[0].Type != vmModels.VMStorageTypeRaw {
		t.Fatalf("flag-created VM = %#v", vm)
	}
	rootDataset, rawDataset := consoleVMDatasets(suite, rid, vm.Storages[0].ID)

	var deleted consoleVMDeleteResult
	failure := runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "create", "--rid", strconv.FormatUint(uint64(failedRID), 10), "--name", "vm-create-failed-"+suite.runID,
		"--storage-pool", suite.poolName, "--storage-type", "raw", "--storage-size", "256MiB",
		"--switch", "missing-switch-"+suite.runID, "--json")
	if !strings.Contains(failure, "switch_not_found") {
		t.Fatalf("failed creation error = %q", failure)
	}
	var failedVM vmModels.VM
	if err := suite.database.Where("rid = ?", failedRID).First(&failedVM).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed creation left VM row: %v %#v", err, failedVM)
	}
	failedRoot := fmt.Sprintf("%s/sylve/virtual-machines/%d", suite.poolName, failedRID)
	assertConsoleZFSDataset(t, failedRoot, false)
	assertConsoleVMDomain(t, failedRID, false)
	if _, err := os.Stat(filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(failedRID), 10))); !os.IsNotExist(err) {
		t.Fatalf("failed creation left runtime directory: %v", err)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "shutdown", "--rid", strconv.FormatUint(uint64(rid), 10), "--wait-seconds", "25", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "shutdown", true)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "start", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	started := assertConsoleVMAction(t, output, rid, "start")
	waitForConsoleVMTask(t, suite, started.TaskID)
	waitForConsoleVMState(t, rid, "running")

	failure = runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "config", "memory", "--rid", strconv.FormatUint(uint64(rid), 10), "--ram", "512MiB", "--json")
	if !strings.Contains(failure, "domain_state_not_shutoff") {
		t.Fatalf("powered-on memory edit error = %q", failure)
	}
	if vm = consoleVMByRID(t, suite, rid); vm.RAM != 256*1024*1024 {
		t.Fatalf("powered-on rejection changed RAM: %d", vm.RAM)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms config shutdown "+strconv.FormatUint(uint64(rid), 10)+" --wait-seconds 26 --json")
	assertConsoleVMConfigMutation(t, output, rid, "shutdown", true)

	output = runREPLCommand(t, suite.socketPath, "vms stop "+strconv.FormatUint(uint64(rid), 10)+" --json")
	stopped := assertConsoleVMAction(t, output, rid, "stop")
	waitForConsoleVMTask(t, suite, stopped.TaskID)
	waitForConsoleVMState(t, rid, "shut off")

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "cpu", "--rid", strconv.FormatUint(uint64(rid), 10),
		"--sockets", "1", "--cores", "2", "--threads", "1", "--clear-pinning", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "cpu", true)
	output = runREPLCommand(t, suite.socketPath,
		"vms config memory "+strconv.FormatUint(uint64(rid), 10)+" --ram 384MiB --json")
	assertConsoleVMConfigMutation(t, output, rid, "memory", true)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "memory", "--rid", strconv.FormatUint(uint64(rid), 10), "--ram", "384MiB", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "memory", false)

	vncPort := reserveConsoleTCPPort(t)
	password := "vnc-secret-" + suite.runID
	passwordPath := filepath.Join(suite.root, "vnc-password-"+suite.runID)
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write VNC password: %v", err)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "vnc", "--rid", strconv.FormatUint(uint64(rid), 10), "--enabled=true",
		"--port", strconv.Itoa(vncPort), "--bind", "127.0.0.1", "--resolution", "800x600", "--wait=false",
		"--password-file", passwordPath, "--json")
	if strings.Contains(output, password) {
		t.Fatalf("VNC mutation output exposed password: %s", output)
	}
	assertConsoleVMConfigMutation(t, output, rid, "vnc", true)

	output = runREPLCommand(t, suite.socketPath,
		"vms config serial "+strconv.FormatUint(uint64(rid), 10)+" --enabled=true --json")
	assertConsoleVMConfigMutation(t, output, rid, "serial", true)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "qga", "--rid", strconv.FormatUint(uint64(rid), 10), "--enabled=true", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "qga", true)
	failure = runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "qga", "send", "--rid", strconv.FormatUint(uint64(rid), 10), "--command", "guest-ping", "--json")
	if !strings.Contains(failure, "qga_command_failed") {
		t.Fatalf("stopped QGA send error = %q", failure)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms config unknown-msr "+strconv.FormatUint(uint64(rid), 10)+" --enabled=true --json")
	assertConsoleVMConfigMutation(t, output, rid, "unknown-msr", true)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "clock", "--rid", strconv.FormatUint(uint64(rid), 10), "--time-offset", "localtime", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "clock", true)
	output = runREPLCommand(t, suite.socketPath,
		"vms config autostart "+strconv.FormatUint(uint64(rid), 10)+" --enabled=true --order 7 --json")
	assertConsoleVMConfigMutation(t, output, rid, "autostart", true)

	cloudDirectory := t.TempDir()
	cloudData := filepath.Join(cloudDirectory, "user-data.yaml")
	cloudMetadata := filepath.Join(cloudDirectory, "metadata.yaml")
	cloudNetwork := filepath.Join(cloudDirectory, "network.yaml")
	writeConsoleFixture(t, cloudData, "#cloud-config\nusers: []\n")
	writeConsoleFixture(t, cloudMetadata, "instance-id: "+name+"\nlocal-hostname: "+name+"\n")
	writeConsoleFixture(t, cloudNetwork, "version: 2\nethernets: {}\n")
	output = runREPLCommand(t, suite.socketPath,
		"vms config cloud-init "+strconv.FormatUint(uint64(rid), 10)+" --data-file "+cloudData+
			" --metadata-file "+cloudMetadata+" --network-config-file "+cloudNetwork+" --json")
	assertConsoleVMConfigMutation(t, output, rid, "cloud-init", true)

	vm = consoleVMByRID(t, suite, rid)
	if vm.CPUSockets != 1 || vm.CPUCores != 2 || vm.CPUThreads != 1 || vm.RAM != 384*1024*1024 ||
		!vm.VNCEnabled || vm.VNCPort != vncPort || vm.VNCPassword != password || !vm.Serial || !vm.QemuGuestAgent ||
		!vm.IgnoreUMSR || !vm.StartAtBoot || vm.StartOrder != 7 || vm.ShutdownWaitTime != 26 ||
		vm.TimeOffset != vmModels.TimeOffsetLocal || vm.CloudInitData == "" || vm.CloudInitMetaData == "" || vm.CloudInitNetworkConfig == "" {
		t.Fatalf("configured VM = %#v", vm)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "get", "--rid", strconv.FormatUint(uint64(rid), 10), "--json")
	if strings.Contains(output, password) {
		t.Fatalf("VM get exposed VNC password: %s", output)
	}
	var inspected vmModels.VM
	if err := json.Unmarshal([]byte(output), &inspected); err != nil || inspected.VNCPassword != "" {
		t.Fatalf("safe VM get = %#v err=%v output=%s", inspected, err, output)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms access vnc "+strconv.FormatUint(uint64(rid), 10)+" --json")
	var vncInfo consoleprotocol.VMVNCAccessInfo
	if err := json.Unmarshal([]byte(output), &vncInfo); err != nil {
		t.Fatalf("decode stopped VNC access: %v\noutput: %s", err, output)
	}
	if !vncInfo.Enabled || vncInfo.Available || vncInfo.PasswordConfigured != true || vncInfo.UnavailableReason != "vm_not_running" ||
		strings.Contains(output, password) {
		t.Fatalf("stopped VNC access = %#v output=%s", vncInfo, output)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "bhyve-options", "--rid", strconv.FormatUint(uint64(rid), 10), "--option=-S", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "bhyve-options", true)
	output = runREPLCommand(t, suite.socketPath,
		"vms config pci "+strconv.FormatUint(uint64(rid), 10)+" --clear --json")
	assertConsoleVMConfigMutation(t, output, rid, "pci", false)
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "config", "boot-rom", "--rid", strconv.FormatUint(uint64(rid), 10), "--boot-rom", "none", "--json")
	assertConsoleVMConfigMutation(t, output, rid, "boot-rom", true)
	vm = consoleVMByRID(t, suite, rid)
	if len(vm.ExtraBhyveOptions) != 1 || vm.ExtraBhyveOptions[0] != "-S" || vm.BootROM != vmModels.VMBootROMNone ||
		vm.ShutdownWaitTime != 26 {
		t.Fatalf("final VM configuration = %#v", vm)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "delete", "--rid", strconv.FormatUint(uint64(rid), 10), "--delete-raw-disks", "--json")
	if err := json.Unmarshal([]byte(output), &deleted); err != nil || !deleted.Deleted {
		t.Fatalf("delete configuration VM: result=%#v err=%v output=%s", deleted, err, output)
	}
	assertConsoleVMDeleted(t, suite, rid, rootDataset, filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(rid), 10)))
	assertConsoleZFSDataset(t, rawDataset, false)
}

func assertConsoleVMConfigMutation(
	t *testing.T,
	output string,
	rid uint,
	configuration string,
	wantUpdated bool,
) consoleVMConfigMutationResult {
	t.Helper()
	var result consoleVMConfigMutationResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode %s configuration mutation: %v\noutput: %s", configuration, err, output)
	}
	if result.RID != rid || result.Configuration != configuration || result.Updated != wantUpdated {
		t.Fatalf("%s configuration result = %#v, want updated=%t", configuration, result, wantUpdated)
	}
	return result
}

func reserveConsoleTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve VNC port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release VNC port: %v", err)
	}
	return port
}

func writeConsoleFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func TestAcceptanceVMSnapshotsTemplatesAndSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	sourceRID := consoleIntegrationVMRID(t, suite, 80)
	failedStartRID, singleRID := consecutiveAvailableConsoleVMRIDsExcluding(t, suite, 2, map[uint]struct{}{sourceRID: {}})
	multipleStartRID, multipleSecondRID := consecutiveAvailableConsoleVMRIDsExcluding(t, suite, 2, map[uint]struct{}{
		sourceRID: {}, failedStartRID: {}, singleRID: {},
	})
	sourceName := "vm-template-source-" + suite.runID
	templateName := "vm-template-" + suite.runID

	for _, rid := range []uint{sourceRID, failedStartRID, singleRID, multipleStartRID, multipleSecondRID} {
		rid := rid
		t.Cleanup(func() { cleanupConsoleVM(t, suite, rid) })
	}

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "create", "--rid", strconv.FormatUint(uint64(sourceRID), 10), "--name", sourceName,
		"--ram", "256MiB", "--storage-pool", suite.poolName, "--storage-type", "raw",
		"--storage-size", "256MiB", "--storage-emulation", "virtio-blk", "--json")
	var created consoleVMCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil || !created.Created || created.RID != sourceRID {
		t.Fatalf("create snapshot/template source VM: result=%#v err=%v output=%s", created, err, output)
	}
	sourceVM := consoleVMByRID(t, suite, sourceRID)
	if len(sourceVM.Storages) != 1 {
		t.Fatalf("source VM storage = %#v", sourceVM.Storages)
	}
	sourceRoot, _ := consoleVMDatasets(suite, sourceRID, sourceVM.Storages[0].ID)

	disabled := runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "access", "serial", "--rid", strconv.FormatUint(uint64(sourceRID), 10), "--json")
	if !strings.Contains(disabled, "vm_serial_console_disabled") {
		t.Fatalf("disabled serial preflight = %q", disabled)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms config serial "+strconv.FormatUint(uint64(sourceRID), 10)+" --enabled=true --json")
	assertConsoleVMConfigMutation(t, output, sourceRID, "serial", true)
	stopped := runREPLCommandFailure(t, suite.socketPath,
		"vms access serial "+strconv.FormatUint(uint64(sourceRID), 10)+" --baud 115200 --json")
	if !strings.Contains(stopped, "vm_console_requires_running_vm") {
		t.Fatalf("stopped serial preflight = %q", stopped)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "snapshots", "create", "--rid", strconv.FormatUint(uint64(sourceRID), 10),
		"--name", "baseline", "--description", "stopped source snapshot", "--json")
	var firstSnapshot vmModels.VMSnapshot
	if err := json.Unmarshal([]byte(output), &firstSnapshot); err != nil || firstSnapshot.ID == 0 || firstSnapshot.Name != "baseline" {
		t.Fatalf("create baseline snapshot: snapshot=%#v err=%v output=%s", firstSnapshot, err, output)
	}

	output = runREPLCommand(t, suite.socketPath,
		"vms snapshots create "+strconv.FormatUint(uint64(sourceRID), 10)+" --name newer --json")
	var secondSnapshot vmModels.VMSnapshot
	if err := json.Unmarshal([]byte(output), &secondSnapshot); err != nil || secondSnapshot.ID == 0 {
		t.Fatalf("create newer snapshot: snapshot=%#v err=%v output=%s", secondSnapshot, err, output)
	}
	rollbackFailure := runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "snapshots", "rollback", "--rid", strconv.FormatUint(uint64(sourceRID), 10),
		"--snapshot-id", strconv.FormatUint(uint64(firstSnapshot.ID), 10), "--json")
	if !strings.Contains(rollbackFailure, "newer_snapshots_require_acknowledgement") {
		t.Fatalf("unacknowledged rollback = %q", rollbackFailure)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "snapshots", "rollback", "--rid", strconv.FormatUint(uint64(sourceRID), 10),
		"--snapshot-id", strconv.FormatUint(uint64(firstSnapshot.ID), 10), "--destroy-newer", "--json")
	var rollback consoleprotocol.VMSnapshotRollbackOutput
	if err := json.Unmarshal([]byte(output), &rollback); err != nil || !rollback.RolledBack || rollback.NewerSnapshotsDestroyed != 1 {
		t.Fatalf("acknowledged rollback = %#v err=%v output=%s", rollback, err, output)
	}

	administratorSnapshotName := "administrator-kept-" + suite.runID
	administratorSnapshot := sourceRoot + "@" + administratorSnapshotName
	if snapshotOutput, err := runConsoleVMCommand(
		consoleIntegrationVMCleanupTimeout,
		"zfs", "snapshot", "-r", administratorSnapshot,
	); err != nil {
		t.Fatalf("create administrator snapshot: %v\n%s", err, snapshotOutput)
	}
	t.Cleanup(func() {
		_, _ = runConsoleVMCommand(
			consoleIntegrationVMCleanupTimeout,
			"zfs", "destroy", "-r", administratorSnapshot,
		)
	})
	rollbackFailure = runSylveFailure(t, suite.binaryPath, suite.configPath,
		"vms", "snapshots", "rollback", "--rid", strconv.FormatUint(uint64(sourceRID), 10),
		"--snapshot-id", strconv.FormatUint(uint64(firstSnapshot.ID), 10), "--json")
	if !strings.Contains(rollbackFailure, "failed_to_rollback_snapshot") {
		t.Fatalf("unacknowledged rollback with administrator snapshot = %q", rollbackFailure)
	}
	assertConsoleZFSDataset(t, administratorSnapshot, true)

	output = runREPLCommand(t, suite.socketPath,
		"vms snapshots rollback "+strconv.FormatUint(uint64(sourceRID), 10)+" "+
			strconv.FormatUint(uint64(firstSnapshot.ID), 10)+" --destroy-newer --json")
	if err := json.Unmarshal([]byte(output), &rollback); err != nil || !rollback.RolledBack || rollback.NewerSnapshotsDestroyed != 0 {
		t.Fatalf("acknowledged rollback with administrator snapshot = %#v err=%v output=%s", rollback, err, output)
	}
	assertConsoleZFSDataset(t, administratorSnapshot, false)

	output = runREPLCommand(t, suite.socketPath,
		"vms snapshots create "+strconv.FormatUint(uint64(sourceRID), 10)+" --name disposable --json")
	var disposable vmModels.VMSnapshot
	if err := json.Unmarshal([]byte(output), &disposable); err != nil || disposable.ID == 0 {
		t.Fatalf("create disposable snapshot: snapshot=%#v err=%v output=%s", disposable, err, output)
	}
	output = runREPLCommand(t, suite.socketPath,
		"vms snapshots delete "+strconv.FormatUint(uint64(sourceRID), 10)+" "+strconv.FormatUint(uint64(disposable.ID), 10)+" --json")
	var snapshotDelete consoleprotocol.VMSnapshotDeleteOutput
	if err := json.Unmarshal([]byte(output), &snapshotDelete); err != nil || !snapshotDelete.Deleted {
		t.Fatalf("delete snapshot = %#v err=%v output=%s", snapshotDelete, err, output)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "snapshots", "list", "--rid", strconv.FormatUint(uint64(sourceRID), 10), "--json")
	var snapshots []vmModels.VMSnapshot
	if err := json.Unmarshal([]byte(output), &snapshots); err != nil || len(snapshots) != 1 || snapshots[0].ID != firstSnapshot.ID {
		t.Fatalf("snapshot list = %#v err=%v output=%s", snapshots, err, output)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "templates", "convert", "--rid", strconv.FormatUint(uint64(sourceRID), 10),
		"--name", templateName, "--json")
	var captureTask consoleprotocol.VMTemplateTaskOutput
	if err := json.Unmarshal([]byte(output), &captureTask); err != nil || captureTask.TaskID == 0 || captureTask.SourceRID != sourceRID {
		t.Fatalf("queue template capture = %#v err=%v output=%s", captureTask, err, output)
	}
	waitForConsoleVMTask(t, suite, captureTask.TaskID)
	if retained := consoleVMByRID(t, suite, sourceRID); retained.Name != sourceName {
		t.Fatalf("template conversion did not retain source VM: %#v", retained)
	}

	var template vmModels.VMTemplate
	if err := suite.database.Where("name = ?", templateName).First(&template).Error; err != nil {
		t.Fatalf("load captured template: %v", err)
	}
	t.Cleanup(func() {
		if suite.virtualMachine != nil {
			_ = suite.virtualMachine.DeleteVMTemplate(context.Background(), template.ID)
		}
	})
	output = runREPLCommand(t, suite.socketPath, "vms templates list --json")
	var templates []consoleprotocol.VMTemplateInfo
	if err := json.Unmarshal([]byte(output), &templates); err != nil {
		t.Fatalf("decode template list: %v\noutput: %s", err, output)
	}
	listed := findConsoleVMTemplate(t, templates, template.ID)
	if len(listed.Storages) != 1 || listed.Storages[0].SourceStorageID != sourceVM.Storages[0].ID {
		t.Fatalf("template storage mappings = %#v", listed.Storages)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "templates", "create", "--template-id", strconv.FormatUint(uint64(template.ID), 10),
		"--mode", "single", "--rid", strconv.FormatUint(uint64(singleRID), 10),
		"--name", "vm-template-single-"+suite.runID, "--json")
	var singleTask consoleprotocol.VMTemplateTaskOutput
	if err := json.Unmarshal([]byte(output), &singleTask); err != nil || singleTask.TaskID == 0 {
		t.Fatalf("queue single template VM = %#v err=%v output=%s", singleTask, err, output)
	}
	waitForConsoleVMTask(t, suite, singleTask.TaskID)
	if vm := consoleVMByRID(t, suite, singleRID); len(vm.Storages) != 1 {
		t.Fatalf("single template VM = %#v", vm)
	}

	failed := runREPLCommandFailure(t, suite.socketPath,
		"vms templates create "+strconv.FormatUint(uint64(template.ID), 10)+
			" --mode multiple --start-rid "+strconv.FormatUint(uint64(failedStartRID), 10)+
			" --count 2 --name-prefix vm-template-failed --json")
	if !strings.Contains(failed, "rid_range_contains_used_values") {
		t.Fatalf("failed multi-target preflight = %q", failed)
	}
	assertConsoleVMAbsentWithoutArtifacts(t, suite, failedStartRID)

	output = runREPLCommand(t, suite.socketPath,
		"vms templates create "+strconv.FormatUint(uint64(template.ID), 10)+
			" --mode multiple --start-rid "+strconv.FormatUint(uint64(multipleStartRID), 10)+
			" --count 2 --name-prefix vm-template-multi --storage-pool "+
			strconv.FormatUint(uint64(sourceVM.Storages[0].ID), 10)+"="+suite.poolName+
			" --rewrite-cloud-init-identity --cloud-init-prefix node --json")
	var multipleTask consoleprotocol.VMTemplateTaskOutput
	if err := json.Unmarshal([]byte(output), &multipleTask); err != nil || multipleTask.TaskID == 0 {
		t.Fatalf("queue multiple template VMs = %#v err=%v output=%s", multipleTask, err, output)
	}
	waitForConsoleVMTask(t, suite, multipleTask.TaskID)
	for _, rid := range []uint{multipleStartRID, multipleSecondRID} {
		if vm := consoleVMByRID(t, suite, rid); len(vm.Storages) != 1 {
			t.Fatalf("multiple template VM %d = %#v", rid, vm)
		}
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"vms", "templates", "delete", "--template-id", strconv.FormatUint(uint64(template.ID), 10), "--json")
	var templateDelete consoleprotocol.VMTemplateDeleteOutput
	if err := json.Unmarshal([]byte(output), &templateDelete); err != nil || !templateDelete.Deleted {
		t.Fatalf("delete template = %#v err=%v output=%s", templateDelete, err, output)
	}
	if err := suite.database.First(&vmModels.VMTemplate{}, template.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("template row after deletion: %v", err)
	}

	for _, rid := range []uint{singleRID, multipleStartRID, multipleSecondRID, sourceRID} {
		output = runSylve(t, suite.binaryPath, suite.configPath,
			"vms", "delete", "--rid", strconv.FormatUint(uint64(rid), 10), "--delete-raw-disks", "--json")
		var deleted consoleVMDeleteResult
		if err := json.Unmarshal([]byte(output), &deleted); err != nil || !deleted.Deleted {
			t.Fatalf("delete snapshot/template VM %d: result=%#v err=%v output=%s", rid, deleted, err, output)
		}
	}
	assertConsoleZFSDataset(t, sourceRoot, false)
}

func consecutiveAvailableConsoleVMRIDsExcluding(
	t *testing.T, suite *consoleIntegrationSuite, count int, excluded map[uint]struct{},
) (uint, uint) {
	t.Helper()
	for start := uint(1000); start+uint(count)-1 <= 8999; start++ {
		available := true
		for offset := 0; offset < count; offset++ {
			rid := start + uint(offset)
			if _, skip := excluded[rid]; skip {
				available = false
				break
			}
			var rows int64
			if err := suite.database.Model(&vmModels.VM{}).Where("rid = ?", rid).Count(&rows).Error; err != nil {
				t.Fatalf("check VM RID %d in database: %v", rid, err)
			}
			if rows != 0 {
				available = false
				break
			}
			if _, err := runConsoleVMCommand(
				consoleIntegrationVMProbeTimeout,
				"virsh", "-c", "bhyve:///system", "dominfo", strconv.FormatUint(uint64(rid), 10),
			); err == nil {
				available = false
				break
			}
		}
		if available {
			return start, start + uint(count) - 1
		}
	}
	t.Fatal("could not find consecutive unused VM RIDs")
	return 0, 0
}

func findConsoleVMTemplate(t *testing.T, templates []consoleprotocol.VMTemplateInfo, id uint) consoleprotocol.VMTemplateInfo {
	t.Helper()
	for _, template := range templates {
		if template.ID == id {
			return template
		}
	}
	t.Fatalf("template %d not found in %#v", id, templates)
	return consoleprotocol.VMTemplateInfo{}
}

func assertConsoleVMAbsentWithoutArtifacts(t *testing.T, suite *consoleIntegrationSuite, rid uint) {
	t.Helper()
	var vm vmModels.VM
	if err := suite.database.Where("rid = ?", rid).First(&vm).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("failed template preflight left VM %d row: err=%v vm=%#v", rid, err, vm)
	}
	assertConsoleVMDomain(t, rid, false)
	assertConsoleZFSDataset(t, fmt.Sprintf("%s/sylve/virtual-machines/%d", suite.poolName, rid), false)
	if _, err := os.Stat(filepath.Join(suite.dataPath, "vms", strconv.FormatUint(uint64(rid), 10))); !os.IsNotExist(err) {
		t.Fatalf("failed template preflight left VM %d runtime path: %v", rid, err)
	}
}
