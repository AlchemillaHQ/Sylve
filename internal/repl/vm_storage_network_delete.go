// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/dustin/go-humanize"
)

type vmStorageAttachResult struct {
	Attached bool                         `json:"attached"`
	RID      uint                         `json:"rid"`
	Storage  libvirtService.VMStorageInfo `json:"storage"`
}

type vmStorageUpdateResult struct {
	Updated bool                         `json:"updated"`
	RID     uint                         `json:"rid"`
	Storage libvirtService.VMStorageInfo `json:"storage"`
}

type vmStorageDetachResult struct {
	Detached bool                         `json:"detached"`
	RID      uint                         `json:"rid"`
	Storage  libvirtService.VMStorageInfo `json:"storage"`
}

type vmNetworkUpdateResult struct {
	Updated    bool   `json:"updated"`
	RID        uint   `json:"rid"`
	NetworkID  uint   `json:"networkId"`
	SwitchName string `json:"switchName"`
	SwitchType string `json:"switchType"`
	Emulation  string `json:"emulation"`
	MacID      *uint  `json:"macId,omitempty"`
	MAC        string `json:"mac"`
	Enabled    bool   `json:"enabled"`
}

var (
	vmStorageAttachOptionNames = vmAllowed(
		"--type", "--name", "--pool", "--size", "--raw-path", "--dataset-guid",
		"--image-uuid", "--emulation", "--filesystem-target", "--read-only",
	)
	vmStorageAttachBooleanOptions = vmAllowed("--read-only")
	vmStorageUpdateOptionNames    = vmAllowed(
		"--name", "--size", "--emulation", "--boot-order", "--enabled", "--filesystem-target", "--read-only",
	)
	vmStorageUpdateBooleanOptions = vmAllowed("--enabled", "--read-only")
	vmNetworkUpdateOptionNames    = vmAllowed(
		"--switch", "--emulation", "--mac-id", "--generate-mac", "--enabled",
	)
	vmNetworkUpdateBooleanOptions = vmAllowed("--generate-mac", "--enabled")
)

func handleVMStorage(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms storage", []cmdHelp{
			{"list <rid>", "List storage and ownership information"},
			{"attach <rid> --type <raw|zvol|image|iso|filesystem> --name <name> [options]", "Attach storage; ISO is an image alias and the VM must be powered off"},
			{"edit <rid> <storage_id> [options]", "Edit storage; VM must be powered off"},
			{"resize <rid> <storage_id> --size <size>", "Grow raw or ZVOL storage; VM must be powered off"},
			{"detach <rid> <storage_id>", "Detach and retain backing; VM must be powered off"},
		})
		return
	}

	switch args[0] {
	case "list":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms storage list <rid>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		vmsStorageList(ctx, rid, jsonMode)

	case "attach":
		if len(args) < 2 {
			println(ctx, styledErrorf("Usage: vms storage attach <rid> --type <type> --name <name> [options]"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		options, err := parseVMNamedOptions(args[2:], vmStorageAttachOptionNames, vmStorageAttachBooleanOptions)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		input, err := vmStorageAttachInputFromOptions(rid, options)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		request, err := consoleprotocol.BuildVMStorageAttachRequest(input)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsStorageAttach(ctx, request, jsonMode)

	case "edit":
		rid, storageID, request, err := parseVMStorageUpdateArgs(args[1:], false)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsStorageUpdate(ctx, rid, storageID, request, jsonMode)

	case "resize":
		rid, storageID, request, err := parseVMStorageUpdateArgs(args[1:], true)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsStorageUpdate(ctx, rid, storageID, request, jsonMode)

	case "detach":
		if len(args) != 3 {
			println(ctx, styledErrorf("Usage: vms storage detach <rid> <storage_id>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		storageID, err := parsePositiveUint(args[2])
		if err != nil {
			println(ctx, styledErrorf("Invalid storage ID '%s'", args[2]))
			return
		}
		vmsStorageDetach(ctx, rid, storageID, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown vms storage command: '%s'", args[0]))
	}
}

func parseVMStorageUpdateArgs(
	args []string,
	resizeOnly bool,
) (uint, uint, libvirtServiceInterfaces.StorageUpdateRequest, error) {
	usage := "Usage: vms storage edit <rid> <storage_id> [options]"
	allowed := vmStorageUpdateOptionNames
	boolean := vmStorageUpdateBooleanOptions
	if resizeOnly {
		usage = "Usage: vms storage resize <rid> <storage_id> --size <size>"
		allowed = vmAllowed("--size")
		boolean = nil
	}
	if len(args) < 2 {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("%s", usage)
	}
	rid, err := parseVMRID(args[0])
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("Invalid RID '%s'", args[0])
	}
	storageID, err := parsePositiveUint(args[1])
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("Invalid storage ID '%s'", args[1])
	}
	options, err := parseVMNamedOptions(args[2:], allowed, boolean)
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	input := consoleprotocol.VMStorageUpdateInput{
		RID: rid, StorageID: storageID,
		Name:             vmStringOption(options, "--name"),
		Size:             vmStringOption(options, "--size"),
		Emulation:        vmStringOption(options, "--emulation"),
		FilesystemTarget: vmStringOption(options, "--filesystem-target"),
	}
	if input.BootOrder, err = vmIntOption(options, "--boot-order"); err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	if input.Enabled, err = vmBoolOption(options, "--enabled"); err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	if input.ReadOnly, err = vmBoolOption(options, "--read-only"); err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	request, err := consoleprotocol.BuildVMStorageUpdateRequest(input)
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	return rid, storageID, request, nil
}

func vmStorageAttachInputFromOptions(rid uint, options map[string]string) (consoleprotocol.VMStorageAttachInput, error) {
	input := consoleprotocol.VMStorageAttachInput{
		RID:              rid,
		Name:             vmOption(options, "--name"),
		StorageType:      vmOption(options, "--type"),
		Pool:             vmOption(options, "--pool"),
		Size:             vmOption(options, "--size"),
		RawPath:          vmOption(options, "--raw-path"),
		DatasetGUID:      vmOption(options, "--dataset-guid"),
		ImageUUID:        vmOption(options, "--image-uuid"),
		Emulation:        vmOption(options, "--emulation"),
		FilesystemTarget: vmOption(options, "--filesystem-target"),
	}
	var err error
	if input.ReadOnly, err = vmBoolOption(options, "--read-only"); err != nil {
		return consoleprotocol.VMStorageAttachInput{}, err
	}
	return input, nil
}

func parseVMNetworkUpdateArgs(args []string) (libvirtServiceInterfaces.NetworkUpdateRequest, error) {
	const usage = "Usage: vms editnet <rid> <network_id> [--switch <name>] [--emulation <virtio|e1000>] [--mac-id <id>|--generate-mac] [--enabled=<true|false>]"
	if len(args) < 2 {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("%s", usage)
	}
	rid, err := parseVMRID(args[0])
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("Invalid RID '%s'", args[0])
	}
	networkID, err := parsePositiveUint(args[1])
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("Invalid network ID '%s'", args[1])
	}
	options, err := parseVMNamedOptions(args[2:], vmNetworkUpdateOptionNames, vmNetworkUpdateBooleanOptions)
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, err
	}
	macID, err := vmUintOption(options, "--mac-id")
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, err
	}
	generateMAC, err := vmBoolOptionValue(options, "--generate-mac")
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, err
	}
	enabled, err := vmBoolOption(options, "--enabled")
	if err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, err
	}
	return consoleprotocol.BuildVMNetworkUpdateRequest(consoleprotocol.VMNetworkUpdateInput{
		RID: rid, NetworkID: networkID,
		SwitchName:  vmStringOption(options, "--switch"),
		Emulation:   vmStringOption(options, "--emulation"),
		MacID:       macID,
		GenerateMAC: generateMAC,
		Enabled:     enabled,
	})
}

func parseVMNamedOptions(args []string, allowed, booleanOptions map[string]bool) (map[string]string, error) {
	repeated, err := parseVMNamedOptionsRepeated(args, allowed, booleanOptions, nil)
	if err != nil {
		return nil, err
	}
	options := make(map[string]string, len(repeated))
	for name, values := range repeated {
		options[name] = values[0]
	}
	return options, nil
}

func parseVMNamedOptionsRepeated(
	args []string,
	allowed, booleanOptions, repeatableOptions map[string]bool,
) (map[string][]string, error) {
	options := make(map[string][]string, len(args))
	for index := 0; index < len(args); {
		name, value, assigned := strings.Cut(args[index], "=")
		if !allowed[name] {
			return nil, fmt.Errorf("unknown VM option %q", name)
		}
		if _, exists := options[name]; exists && !repeatableOptions[name] {
			return nil, fmt.Errorf("VM option %q was specified more than once", name)
		}
		if assigned {
			if value == "" {
				return nil, fmt.Errorf("VM option %q requires a value", name)
			}
			options[name] = append(options[name], value)
			index++
			continue
		}
		if booleanOptions[name] {
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
				options[name] = append(options[name], args[index+1])
				index += 2
			} else {
				options[name] = append(options[name], "true")
				index++
			}
			continue
		}
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
			return nil, fmt.Errorf("VM option %q requires a value", name)
		}
		options[name] = append(options[name], args[index+1])
		index += 2
	}
	return options, nil
}

func vmOption(options map[string]string, name string) string {
	return strings.TrimSpace(options[name])
}

func vmStringOption(options map[string]string, name string) *string {
	value, exists := options[name]
	if !exists {
		return nil
	}
	return &value
}

func vmIntOption(options map[string]string, name string) (*int, error) {
	value, exists := options[name]
	if !exists {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("invalid integer value for %s", name)
	}
	return &parsed, nil
}

func vmUintOption(options map[string]string, name string) (*uint, error) {
	value, exists := options[name]
	if !exists {
		return nil, nil
	}
	parsed, err := parsePositiveUint(value)
	if err != nil {
		return nil, fmt.Errorf("invalid positive integer value for %s", name)
	}
	return &parsed, nil
}

func vmBoolOption(options map[string]string, name string) (*bool, error) {
	value, exists := options[name]
	if !exists {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid boolean value for %s", name)
	}
	return &parsed, nil
}

func vmBoolOptionValue(options map[string]string, name string) (bool, error) {
	value, err := vmBoolOption(options, name)
	if err != nil || value == nil {
		return false, err
	}
	return *value, nil
}

func listVMStorage(ctx *Context, rid uint) ([]libvirtService.VMStorageInfo, error) {
	if err := validateVMRID(rid); err != nil {
		return nil, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}
	storages, err := ctx.VirtualMachine.ListVMStorage(rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_vm_storage: %w", err)
	}
	if storages == nil {
		storages = []libvirtService.VMStorageInfo{}
	}
	return storages, nil
}

func attachVMStorage(
	ctx *Context,
	request libvirtServiceInterfaces.StorageAttachRequest,
) (vmStorageAttachResult, error) {
	if err := validateVMRID(request.RID); err != nil {
		return vmStorageAttachResult{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmStorageAttachResult{}, fmt.Errorf("vm_service_unavailable")
	}
	storage, err := ctx.VirtualMachine.StorageAttach(request, context.Background())
	if err != nil {
		return vmStorageAttachResult{}, fmt.Errorf("failed_to_attach_vm_storage: %w", err)
	}
	if storage == nil {
		return vmStorageAttachResult{}, fmt.Errorf("storage_attach_returned_empty_result")
	}
	return vmStorageAttachResult{
		Attached: true, RID: request.RID, Storage: libvirtService.DescribeVMStorage(request.RID, *storage),
	}, nil
}

func updateVMStorage(
	ctx *Context,
	rid, storageID uint,
	request libvirtServiceInterfaces.StorageUpdateRequest,
) (vmStorageUpdateResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmStorageUpdateResult{}, err
	}
	if storageID == 0 {
		return vmStorageUpdateResult{}, fmt.Errorf("invalid_storage_id")
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmStorageUpdateResult{}, fmt.Errorf("vm_service_unavailable")
	}
	request.RID = rid
	request.ID = storageID
	storage, err := ctx.VirtualMachine.StorageUpdate(request, context.Background())
	if err != nil {
		return vmStorageUpdateResult{}, fmt.Errorf("failed_to_update_vm_storage: %w", err)
	}
	if storage == nil {
		return vmStorageUpdateResult{}, fmt.Errorf("storage_update_returned_empty_result")
	}
	return vmStorageUpdateResult{
		Updated: true, RID: rid, Storage: libvirtService.DescribeVMStorage(rid, *storage),
	}, nil
}

func detachVMStorage(ctx *Context, rid, storageID uint) (vmStorageDetachResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmStorageDetachResult{}, err
	}
	if storageID == 0 {
		return vmStorageDetachResult{}, fmt.Errorf("invalid_storage_id")
	}
	storages, err := listVMStorage(ctx, rid)
	if err != nil {
		return vmStorageDetachResult{}, err
	}
	var detached libvirtService.VMStorageInfo
	found := false
	for _, storage := range storages {
		if storage.ID == storageID {
			detached = storage
			found = true
			break
		}
	}
	if !found {
		return vmStorageDetachResult{}, fmt.Errorf("storage_not_found: %d", storageID)
	}
	if err := ctx.VirtualMachine.StorageDetach(libvirtServiceInterfaces.StorageDetachRequest{
		RID: rid, StorageID: storageID,
	}, context.Background()); err != nil {
		return vmStorageDetachResult{}, fmt.Errorf("failed_to_detach_vm_storage: %w", err)
	}
	return vmStorageDetachResult{Detached: true, RID: rid, Storage: detached}, nil
}

func updateVMNetwork(
	ctx *Context,
	request libvirtServiceInterfaces.NetworkUpdateRequest,
) (vmNetworkUpdateResult, error) {
	if err := validateVMRID(request.RID); err != nil {
		return vmNetworkUpdateResult{}, err
	}
	if request.NetworkID == 0 {
		return vmNetworkUpdateResult{}, fmt.Errorf("invalid_network_id")
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmNetworkUpdateResult{}, fmt.Errorf("vm_service_unavailable")
	}
	network, err := ctx.VirtualMachine.NetworkUpdate(request, context.Background())
	if err != nil {
		return vmNetworkUpdateResult{}, fmt.Errorf("failed_to_update_vm_network: %w", err)
	}
	if network == nil {
		return vmNetworkUpdateResult{}, fmt.Errorf("network_update_returned_empty_result")
	}
	return vmNetworkUpdateResult{
		Updated: true, RID: request.RID, NetworkID: network.ID,
		SwitchName: vmNetworkSwitchName(*network), SwitchType: network.SwitchType,
		Emulation: network.Emulation, MacID: network.MacID, MAC: vmNetworkMAC(*network), Enabled: network.Enable,
	}, nil
}

func vmNetworkSwitchName(network vmModels.Network) string {
	if network.SwitchType == "standard" && network.StandardSwitch != nil {
		return network.StandardSwitch.Name
	}
	if network.SwitchType == "manual" && network.ManualSwitch != nil {
		return network.ManualSwitch.Name
	}
	return strconv.FormatUint(uint64(network.SwitchID), 10)
}

func vmNetworkMAC(network vmModels.Network) string {
	if network.AddressObj != nil && len(network.AddressObj.Entries) > 0 {
		return network.AddressObj.Entries[0].Value
	}
	return network.MAC
}

func previewVMDeletion(
	ctx *Context,
	rid uint,
	deleteMACs, deleteRawDisks, deleteVolumes bool,
) (libvirtService.VMRemovalPreview, error) {
	if err := validateVMRID(rid); err != nil {
		return libvirtService.VMRemovalPreview{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return libvirtService.VMRemovalPreview{}, fmt.Errorf("vm_service_unavailable")
	}
	preview, err := ctx.VirtualMachine.PreviewVMRemoval(
		rid, deleteMACs, deleteRawDisks, deleteVolumes, context.Background(),
	)
	if err != nil {
		return libvirtService.VMRemovalPreview{}, fmt.Errorf("failed_to_preview_vm_deletion: %w", err)
	}
	return preview, nil
}

func formatVMStorageList(rid uint, storages []libvirtService.VMStorageInfo) string {
	if len(storages) == 0 {
		return fmt.Sprintf("VM %d has no storage devices configured.", rid)
	}
	headers := []string{"ID", "NAME", "TYPE", "EMULATION", "SIZE", "ENABLED", "OWNERSHIP", "BACKING"}
	rows := make([][]string, 0, len(storages))
	for _, storage := range storages {
		size := "-"
		if storage.Size > 0 {
			size = humanize.IBytes(uint64(storage.Size))
		}
		backing := storage.Backing
		if backing == "" {
			backing = "-"
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(storage.ID), 10), storage.Name, string(storage.Type),
			string(storage.Emulation), size, strconv.FormatBool(storage.Enabled), storage.Ownership, backing,
		})
	}
	return fmt.Sprintf("Storage for VM RID %d\n%s", rid, styledTable(headers, rows))
}

func formatVMRemovalPreview(preview libvirtService.VMRemovalPreview) string {
	lines := []string{
		fmt.Sprintf("VM registration: %s (RID: %d)", preview.Registration.Name, preview.Registration.RID),
		fmt.Sprintf("MAC objects to delete: %s", formatUintList(preview.DeleteMACObjectIDs)),
		fmt.Sprintf("MAC objects to retain: %s", formatUintList(preview.RetainMACObjectIDs)),
		fmt.Sprintf("Raw datasets to destroy: %s", formatStringList(preview.DeleteRawDatasets)),
		fmt.Sprintf("ZVOL datasets to destroy: %s", formatStringList(preview.DeleteZVOLDatasets)),
		fmt.Sprintf("Container datasets to remove if empty: %s", formatStringList(preview.DeleteContainerDatasets)),
		fmt.Sprintf("Snapshots to destroy: %s", formatStringList(preview.DeleteSnapshots)),
		fmt.Sprintf("Datasets to retain: %s", formatStringList(preview.RetainedDatasets)),
		fmt.Sprintf("Image UUIDs to retain: %s", formatStringList(preview.RetainedImageUUIDs)),
		fmt.Sprintf("Expected warnings: %s", formatStringList(preview.Warnings)),
	}
	return strings.Join(lines, "\n")
}

func formatUintList(values []uint) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatUint(uint64(value), 10))
	}
	return strings.Join(parts, ", ")
}

func formatStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func vmsStorageList(ctx *Context, rid uint, jsonMode bool) {
	storages, err := listVMStorage(ctx, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching VM storage", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(storages))
		return
	}
	println(ctx, formatVMStorageList(rid, storages))
}

func vmsStorageAttach(ctx *Context, request libvirtServiceInterfaces.StorageAttachRequest, jsonMode bool) {
	result, err := attachVMStorage(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error attaching VM storage", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Storage %d attached to VM %d; detach will retain %s.", result.Storage.ID, result.RID, result.Storage.Backing))
}

func vmsStorageUpdate(
	ctx *Context,
	rid, storageID uint,
	request libvirtServiceInterfaces.StorageUpdateRequest,
	jsonMode bool,
) {
	result, err := updateVMStorage(ctx, rid, storageID, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error updating VM storage", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Storage %d updated on VM %d.", result.Storage.ID, result.RID))
}

func vmsStorageDetach(ctx *Context, rid, storageID uint, jsonMode bool) {
	result, err := detachVMStorage(ctx, rid, storageID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error detaching VM storage", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Storage %d detached from VM %d; backing retained: %s.", storageID, rid, result.Storage.Backing))
}

func vmsNetworkUpdate(ctx *Context, request libvirtServiceInterfaces.NetworkUpdateRequest, jsonMode bool) {
	result, err := updateVMNetwork(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error updating VM network", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Network %d updated on VM %d.", result.NetworkID, result.RID))
}

func vmsDeletePreview(ctx *Context, rid uint, deleteMACs, deleteRawDisks, deleteVolumes bool, jsonMode bool) {
	preview, err := previewVMDeletion(ctx, rid, deleteMACs, deleteRawDisks, deleteVolumes)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error previewing VM deletion", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(preview))
		return
	}
	println(ctx, formatVMRemovalPreview(preview))
}

func processVMStorageListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_storage_list_request: " + err.Error()}
	}
	storages, err := listVMStorage(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, storages, formatVMStorageList(request.RID, storages))
}

func processVMStorageAttachSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMStorageAttachPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_storage_attach_request: " + err.Error()}
	}
	request.Request.RID = request.RID
	result, err := attachVMStorage(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON, result,
		styledSuccessf("Storage %d attached to VM %d; detach retains its backing.", result.Storage.ID, result.RID),
	)
}

func processVMStorageUpdateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMStorageUpdatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_storage_update_request: " + err.Error()}
	}
	result, err := updateVMStorage(ctx, request.RID, request.StorageID, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON, result,
		styledSuccessf("Storage %d updated on VM %d.", result.Storage.ID, result.RID),
	)
}

func processVMStorageDetachSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMStorageDetachPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_storage_detach_request: " + err.Error()}
	}
	result, err := detachVMStorage(ctx, request.RID, request.StorageID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON, result,
		styledSuccessf("Storage %d detached from VM %d; backing retained: %s.", request.StorageID, request.RID, result.Storage.Backing),
	)
}

func processVMNetworkUpdateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMNetworkUpdatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_network_update_request: " + err.Error()}
	}
	request.Request.RID = request.RID
	request.Request.NetworkID = request.NetworkID
	result, err := updateVMNetwork(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON, result,
		styledSuccessf("Network %d updated on VM %d.", result.NetworkID, result.RID),
	)
}

func processVMDeletePreviewSocketRequest(ctx *Context, request consoleprotocol.VMDeletePayload) socketResponse {
	preview, err := previewVMDeletion(
		ctx, request.RID, request.DeleteMACs, request.DeleteRawDisks, request.DeleteVolumes,
	)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, preview, formatVMRemovalPreview(preview))
}
