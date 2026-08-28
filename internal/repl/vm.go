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
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

type vmCreateResult struct {
	Created               bool   `json:"created"`
	RID                   uint   `json:"rid"`
	Name                  string `json:"name"`
	StorageAttachmentIDs  []uint `json:"storageAttachmentIds"`
	NetworkAttachmentIDs  []uint `json:"networkAttachmentIds"`
	MACObjectIDs          []uint `json:"macObjectIds"`
	GeneratedMACObjectIDs []uint `json:"generatedMacObjectIds"`
}

type vmNetworkAttachResult struct {
	Attached              bool   `json:"attached"`
	RID                   uint   `json:"rid"`
	NetworkID             uint   `json:"networkId"`
	SwitchName            string `json:"switchName"`
	Emulation             string `json:"emulation"`
	MacID                 *uint  `json:"macId,omitempty"`
	MAC                   string `json:"mac"`
	Enabled               bool   `json:"enabled"`
	GeneratedMACObjectIDs []uint `json:"generatedMacObjectIds"`
}

type vmActionResult struct {
	RID     uint   `json:"rid"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	TaskID  uint   `json:"taskId,omitempty"`
}

type vmNetworkDetachResult struct {
	Deleted              bool   `json:"deleted"`
	RID                  uint   `json:"rid"`
	NetworkID            uint   `json:"networkId"`
	RetainedMACObjectIDs []uint `json:"retainedMacObjectIds"`
}

type vmDeleteResult struct {
	Deleted              bool     `json:"deleted"`
	RID                  uint     `json:"rid"`
	Warnings             []string `json:"warnings"`
	RetainedDatasets     []string `json:"retainedDatasets"`
	DeletedMACObjectIDs  []uint   `json:"deletedMacObjectIds"`
	RetainedMACObjectIDs []uint   `json:"retainedMacObjectIds"`
}

type vmPurgeResult struct {
	Purged   bool     `json:"purged"`
	RID      uint     `json:"rid"`
	Warnings []string `json:"warnings"`
}

func handleVms(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "vms", []cmdHelp{
			{"list", "List all VMs"},
			{"create [--file <host-path>] --rid <1-9999> --name <name> [--storage-type <none|raw|zvol>] [core flags]", "Create from common flags or strict JSON; flags override JSON and absolute host paths are recommended"},
			{"get <rid>", "Get VM details"},
			{"start <rid>", "Start a VM"},
			{"stop <rid>", "Force-stop a VM"},
			{"shutdown <rid>", "Gracefully shut down a VM"},
			{"reboot <rid>", "Reboot a VM"},
			{"delete <rid> [--delete-macs] [--delete-raw-disks] [--delete-volumes] [--dry-run]", "Delete or preview a VM; running VMs are force-stopped and backing is retained by default"},
			{"purge <rid> [--delete-macs]", "Purge an orphaned VM registration"},
			{"config <command> <rid> [options]", "Manage VM configuration"},
			{"access <vnc|serial> <rid> [options]", "Inspect VNC or open a preflighted local serial console"},
			{"storage <list|attach|edit|resize|detach>", "Manage VM storage devices"},
			{"snapshots <list|create|rollback|delete>", "Manage crash-consistent VM snapshots"},
			{"templates <list|get|capture|create|delete>", "Manage VM templates and queued instantiation"},
			{"network <list|attach|edit|detach>", "Manage VM network attachments"},
			{"qga <info|send> <rid> [command]", "Inspect or send commands to QEMU Guest Agent"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	if rid, err := parsePositiveUint(subCmd); err == nil {
		if err := validateVMRID(rid); err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subCmd))
			return
		}
		handleVmsByRID(ctx, rid, subArgs, jsonMode)
		return
	}

	switch subCmd {
	case "list":
		if len(subArgs) != 0 {
			println(ctx, styledErrorf("Usage: vms list"))
			return
		}
		vmsList(ctx, jsonMode)

	case "create":
		request, err := buildConsoleVMCreateRequest(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsCreate(ctx, request, jsonMode)

	case "get":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: vms get <rid>"))
			return
		}
		rid, err := parseVMRID(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subArgs[0]))
			return
		}
		vmsGet(ctx, rid, jsonMode)

	case "start", "stop", "shutdown", "reboot":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: vms %s <rid>", subCmd))
			return
		}
		rid, err := parseVMRID(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subArgs[0]))
			return
		}
		vmsAction(ctx, rid, subCmd, jsonMode)

	case "delete":
		rid, deleteMACs, deleteRawDisks, deleteVolumes, dryRun, err := parseVMDeleteArgs(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		if dryRun {
			vmsDeletePreview(ctx, rid, deleteMACs, deleteRawDisks, deleteVolumes, jsonMode)
		} else {
			vmsDelete(ctx, rid, deleteMACs, deleteRawDisks, deleteVolumes, jsonMode)
		}

	case "purge":
		rid, deleteMACs, err := parseVMPurgeArgs(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsPurge(ctx, rid, deleteMACs, jsonMode)

	case "config":
		handleVMConfig(ctx, subArgs, jsonMode)

	case "access":
		handleVMAccess(ctx, subArgs, jsonMode)

	case "storage":
		handleVMStorage(ctx, subArgs, jsonMode)

	case "snapshots":
		handleVMSnapshots(ctx, subArgs, jsonMode)

	case "templates":
		handleVMTemplates(ctx, subArgs, jsonMode)

	case "network":
		handleVMNetwork(ctx, subArgs, jsonMode)

	case "qga":
		handleVMQGA(ctx, subArgs, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown vms command: '%s'. Type 'vms' for help.", subCmd))
	}
}

func handleVmsByRID(ctx *Context, rid uint, args []string, jsonMode bool) {
	if len(args) == 0 {
		println(ctx, styledErrorf("Missing command for VM RID %d.", rid))
		return
	}
	if args[0] != "qga" {
		println(ctx, styledErrorf("Unknown VM RID command: '%s'. Try: vms %d qga send <command>", args[0], rid))
		return
	}

	qgaArgs := args[1:]
	if len(qgaArgs) > 0 && qgaArgs[0] == "send" {
		qgaArgs = qgaArgs[1:]
	}
	if len(qgaArgs) == 0 {
		println(ctx, styledErrorf("Missing QGA command. Usage: vms <rid> qga send <command>"))
		return
	}
	vmsQGASend(ctx, rid, strings.Join(qgaArgs, " "), jsonMode)
}

func handleVMQGA(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms qga", []cmdHelp{
			{"info <rid>", "Show configuration, reachability, and available capabilities"},
			{"send <rid> <command>", "Send a QEMU Guest Agent command"},
		})
		return
	}
	if args[0] == "info" {
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms qga info <rid>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		vmsQGAInfo(ctx, rid, jsonMode)
		return
	}

	ridIndex := 0
	commandIndex := 1
	if args[0] == "send" {
		ridIndex = 1
		commandIndex = 2
	}
	if len(args) <= commandIndex {
		println(ctx, styledErrorf("Usage: vms qga send <rid> <command>"))
		return
	}

	rid, err := parseVMRID(args[ridIndex])
	if err != nil {
		println(ctx, styledErrorf("Invalid RID '%s'", args[ridIndex]))
		return
	}
	vmsQGASend(ctx, rid, strings.Join(args[commandIndex:], " "), jsonMode)
}

func buildConsoleVMCreateRequest(args []string) (libvirtServiceInterfaces.CreateVMRequest, error) {
	return buildConsoleVMCreateRequestFromArgs(args)
}

func parseVMDeleteArgs(args []string) (uint, bool, bool, bool, bool, error) {
	const usage = "Usage: vms delete <rid> [--delete-macs] [--delete-raw-disks] [--delete-volumes] [--dry-run]"
	if len(args) == 0 {
		return 0, false, false, false, false, fmt.Errorf("%s", usage)
	}

	rid, err := parseVMRID(args[0])
	if err != nil {
		return 0, false, false, false, false, fmt.Errorf("Invalid RID '%s'", args[0])
	}

	deleteMACs := false
	deleteRawDisks := false
	deleteVolumes := false
	dryRun := false
	for _, arg := range args[1:] {
		switch arg {
		case "--delete-macs":
			if deleteMACs {
				return 0, false, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteMACs = true
		case "--delete-raw-disks":
			if deleteRawDisks {
				return 0, false, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteRawDisks = true
		case "--delete-volumes":
			if deleteVolumes {
				return 0, false, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteVolumes = true
		case "--dry-run":
			if dryRun {
				return 0, false, false, false, false, fmt.Errorf("%s", usage)
			}
			dryRun = true
		default:
			return 0, false, false, false, false, fmt.Errorf("%s", usage)
		}
	}

	return rid, deleteMACs, deleteRawDisks, deleteVolumes, dryRun, nil
}

func parseVMPurgeArgs(args []string) (uint, bool, error) {
	const usage = "Usage: vms purge <rid> [--delete-macs]"
	if len(args) == 0 {
		return 0, false, fmt.Errorf("%s", usage)
	}

	rid, err := parseVMRID(args[0])
	if err != nil {
		return 0, false, fmt.Errorf("Invalid RID '%s'", args[0])
	}

	deleteMACs := false
	for _, arg := range args[1:] {
		if arg != "--delete-macs" || deleteMACs {
			return 0, false, fmt.Errorf("%s", usage)
		}
		deleteMACs = true
	}

	return rid, deleteMACs, nil
}

func parseVMRID(value string) (uint, error) {
	rid, err := parsePositiveUint(value)
	if err != nil {
		return 0, err
	}
	if err := validateVMRID(rid); err != nil {
		return 0, err
	}
	return rid, nil
}

func parseVMNetworkID(value string) (uint, error) {
	return parsePositiveUint(value)
}

func validateVMRID(rid uint) error {
	if rid == 0 || rid > 9999 {
		return fmt.Errorf("invalid_rid")
	}
	return nil
}

func listVMs(ctx *Context) ([]vmModels.VM, error) {
	if ctx == nil || ctx.VirtualMachine == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}

	vms, err := ctx.VirtualMachine.ListVMs()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_vms: %w", err)
	}
	return vms, nil
}

func getVM(ctx *Context, rid uint) (*vmModels.VM, error) {
	if err := validateVMRID(rid); err != nil {
		return nil, err
	}

	vms, err := listVMs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].RID == rid {
			return &vms[i], nil
		}
	}
	return nil, fmt.Errorf("vm_not_found")
}

func createVM(ctx *Context, request libvirtServiceInterfaces.CreateVMRequest) (vmCreateResult, error) {
	if request.RID == nil {
		return vmCreateResult{}, fmt.Errorf("invalid_vm_create_request: missing_rid")
	}
	if err := validateVMRID(*request.RID); err != nil {
		return vmCreateResult{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmCreateResult{}, fmt.Errorf("vm_service_unavailable")
	}

	if err := ctx.VirtualMachine.CreateVM(request, operationContext(ctx)); err != nil {
		return vmCreateResult{}, fmt.Errorf("failed_to_create_vm: %w", err)
	}
	vm, err := ctx.VirtualMachine.GetVMByRID(*request.RID)
	if err != nil {
		return vmCreateResult{}, fmt.Errorf("failed_to_read_created_vm_identifiers: %w", err)
	}
	result := vmCreateResult{
		Created: true, RID: vm.RID, Name: vm.Name,
		StorageAttachmentIDs: []uint{}, NetworkAttachmentIDs: []uint{},
		MACObjectIDs: []uint{}, GeneratedMACObjectIDs: []uint{},
	}
	for _, storage := range vm.Storages {
		result.StorageAttachmentIDs = append(result.StorageAttachmentIDs, storage.ID)
	}
	for _, network := range vm.Networks {
		result.NetworkAttachmentIDs = append(result.NetworkAttachmentIDs, network.ID)
		if network.MacID != nil && *network.MacID > 0 {
			result.MACObjectIDs = append(result.MACObjectIDs, *network.MacID)
			if request.MacId == nil || *request.MacId == 0 {
				result.GeneratedMACObjectIDs = append(result.GeneratedMACObjectIDs, *network.MacID)
			}
		}
	}
	slices.Sort(result.StorageAttachmentIDs)
	slices.Sort(result.NetworkAttachmentIDs)
	slices.Sort(result.MACObjectIDs)
	slices.Sort(result.GeneratedMACObjectIDs)
	return result, nil
}

func listVMNetworks(ctx *Context, rid uint) (*vmModels.VM, error) {
	return getVM(ctx, rid)
}

func requestVMAction(ctx *Context, rid uint, action string) (vmActionResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmActionResult{}, err
	}
	if action != "start" && action != "stop" && action != "shutdown" && action != "reboot" {
		return vmActionResult{}, fmt.Errorf("invalid_vm_action")
	}
	if ctx == nil || ctx.Lifecycle == nil {
		return vmActionResult{}, fmt.Errorf("lifecycle_service_unavailable")
	}

	task, outcome, err := ctx.Lifecycle.RequestAction(
		operationContext(ctx), "vm", rid, action, "user", "console",
	)
	if err != nil {
		return vmActionResult{}, fmt.Errorf("failed_to_%s_vm: %w", action, err)
	}

	result := vmActionResult{RID: rid, Action: action, Outcome: outcome}
	if task != nil {
		result.TaskID = task.ID
	}
	return result, nil
}

func attachVMNetwork(ctx *Context, request libvirtServiceInterfaces.NetworkAttachRequest) (vmNetworkAttachResult, error) {
	if err := validateVMRID(request.RID); err != nil {
		return vmNetworkAttachResult{}, err
	}
	request.SwitchName = strings.TrimSpace(request.SwitchName)
	request.Emulation = strings.ToLower(strings.TrimSpace(request.Emulation))
	if request.SwitchName == "" {
		return vmNetworkAttachResult{}, fmt.Errorf("switch_name_required")
	}
	if request.Emulation != "virtio" && request.Emulation != "e1000" {
		return vmNetworkAttachResult{}, fmt.Errorf("invalid_emulation_type")
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmNetworkAttachResult{}, fmt.Errorf("vm_service_unavailable")
	}
	network, err := ctx.VirtualMachine.NetworkAttach(request, operationContext(ctx))
	if err != nil {
		return vmNetworkAttachResult{}, fmt.Errorf("failed_to_attach_vm_network: %w", err)
	}
	if network == nil {
		return vmNetworkAttachResult{}, fmt.Errorf("network_attach_returned_empty_result")
	}

	result := vmNetworkAttachResult{
		Attached:              true,
		RID:                   request.RID,
		NetworkID:             network.ID,
		SwitchName:            request.SwitchName,
		Emulation:             network.Emulation,
		MacID:                 network.MacID,
		MAC:                   vmNetworkMAC(*network),
		Enabled:               network.Enable,
		GeneratedMACObjectIDs: []uint{},
	}
	if (request.MacID == nil || *request.MacID == 0) && network.MacID != nil && *network.MacID > 0 {
		result.GeneratedMACObjectIDs = append(result.GeneratedMACObjectIDs, *network.MacID)
	}
	return result, nil
}

func detachVMNetwork(ctx *Context, rid, networkID uint) (vmNetworkDetachResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmNetworkDetachResult{}, err
	}
	if networkID == 0 {
		return vmNetworkDetachResult{}, fmt.Errorf("invalid_network_id")
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmNetworkDetachResult{}, fmt.Errorf("vm_service_unavailable")
	}
	network, err := getVMNetwork(ctx, rid, networkID)
	if err != nil {
		return vmNetworkDetachResult{}, err
	}
	if err := ctx.VirtualMachine.NetworkDetach(libvirtServiceInterfaces.NetworkDetachRequest{
		RID:       rid,
		NetworkID: networkID,
	}, operationContext(ctx)); err != nil {
		return vmNetworkDetachResult{}, fmt.Errorf("failed_to_detach_vm_network: %w", err)
	}
	retained := []uint{}
	if network.MacID != nil && *network.MacID > 0 {
		retained = append(retained, *network.MacID)
	}
	return vmNetworkDetachResult{Deleted: true, RID: rid, NetworkID: networkID, RetainedMACObjectIDs: retained}, nil
}

func deleteVM(ctx *Context, rid uint, deleteMACs, deleteRawDisks, deleteVolumes bool) (vmDeleteResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmDeleteResult{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmDeleteResult{}, fmt.Errorf("vm_service_unavailable")
	}

	removal, err := ctx.VirtualMachine.RemoveVMWithWarnings(
		rid,
		deleteMACs,
		deleteRawDisks,
		deleteVolumes,
		operationContext(ctx),
	)
	if err != nil {
		return vmDeleteResult{}, fmt.Errorf("failed_to_delete_vm: %w", err)
	}
	if removal.Warnings == nil {
		removal.Warnings = []string{}
	}
	if removal.RetainedDatasets == nil {
		removal.RetainedDatasets = []string{}
	}
	if removal.DeletedMACObjectIDs == nil {
		removal.DeletedMACObjectIDs = []uint{}
	}
	if removal.RetainedMACObjectIDs == nil {
		removal.RetainedMACObjectIDs = []uint{}
	}
	return vmDeleteResult{
		Deleted:              true,
		RID:                  rid,
		Warnings:             removal.Warnings,
		RetainedDatasets:     removal.RetainedDatasets,
		DeletedMACObjectIDs:  removal.DeletedMACObjectIDs,
		RetainedMACObjectIDs: removal.RetainedMACObjectIDs,
	}, nil
}

func getVMNetwork(ctx *Context, rid, networkID uint) (vmModels.Network, error) {
	vm, err := ctx.VirtualMachine.GetVMByRID(rid)
	if err != nil {
		return vmModels.Network{}, fmt.Errorf("failed_to_get_vm_network: %w", err)
	}
	for _, network := range vm.Networks {
		if network.ID == networkID {
			return network, nil
		}
	}
	return vmModels.Network{}, fmt.Errorf("network_not_found: %d", networkID)
}

func purgeVM(ctx *Context, rid uint, deleteMACs bool) (vmPurgeResult, error) {
	if err := validateVMRID(rid); err != nil {
		return vmPurgeResult{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return vmPurgeResult{}, fmt.Errorf("vm_service_unavailable")
	}

	warnings, err := ctx.VirtualMachine.PurgeVMRegistration(rid, deleteMACs)
	if err != nil {
		return vmPurgeResult{}, fmt.Errorf("failed_to_purge_vm: %w", err)
	}
	if warnings == nil {
		warnings = []string{}
	}
	return vmPurgeResult{Purged: true, RID: rid, Warnings: warnings}, nil
}

func sendVMQGA(ctx *Context, rid uint, command string) (json.RawMessage, error) {
	if err := validateVMRID(rid); err != nil {
		return nil, err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("qga_command_required")
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}

	response, err := ctx.VirtualMachine.RunQemuGuestAgentCommand(rid, command)
	if err != nil {
		return nil, fmt.Errorf("qga_command_failed: %w", err)
	}
	return response, nil
}

func inspectVMQGA(ctx *Context, rid uint) (libvirtServiceInterfaces.QemuGuestAgentStatus, error) {
	if err := validateVMRID(rid); err != nil {
		return libvirtServiceInterfaces.QemuGuestAgentStatus{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return libvirtServiceInterfaces.QemuGuestAgentStatus{}, fmt.Errorf("vm_service_unavailable")
	}
	status, err := ctx.VirtualMachine.InspectQemuGuestAgent(rid)
	if err != nil {
		return status, fmt.Errorf("failed_to_inspect_qga: %w", err)
	}
	if status.Capabilities == nil {
		status.Capabilities = []libvirtServiceInterfaces.QGACapability{}
	}
	return status, nil
}

func formatVMList(vms []vmModels.VM) string {
	if len(vms) == 0 {
		return "No VMs found."
	}

	headers := []string{"RID", "Name", "vCPUs", "RAM", "Networks"}
	rows := make([][]string, 0, len(vms))
	for _, vm := range vms {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(vm.RID), 10),
			vm.Name,
			formatVMVCPUs(vm),
			formatMemorySize(vm.RAM),
			strconv.Itoa(len(vm.Networks)),
		})
	}
	return styledTable(headers, rows)
}

func formatVMVCPUs(vm vmModels.VM) string {
	if vm.CPUSockets <= 0 || vm.CPUCores <= 0 || vm.CPUThreads <= 0 {
		return "-"
	}
	return strconv.Itoa(vm.CPUSockets * vm.CPUCores * vm.CPUThreads)
}

func formatVMDetails(vm *vmModels.VM) string {
	lines := []string{
		styledKeyValue("RID:", strconv.FormatUint(uint64(vm.RID), 10)),
		styledKeyValue("Name:", vm.Name),
		styledKeyValue("Description:", vm.Description),
		styledKeyValue("Networks:", strconv.Itoa(len(vm.Networks))),
		styledKeyValue("Storage devices:", strconv.Itoa(len(vm.Storages))),
	}
	return strings.Join(lines, "\n")
}

func formatVMNetworks(vm *vmModels.VM) string {
	if len(vm.Networks) == 0 {
		return fmt.Sprintf("VM '%s' (RID: %d) has no networks configured.", vm.Name, vm.RID)
	}

	headers := []string{"NET ID", "SWITCH", "TYPE", "EMUL", "ENABLED", "MAC"}
	rows := make([][]string, 0, len(vm.Networks))
	for _, network := range vm.Networks {
		mac := "auto"
		if network.AddressObj != nil && len(network.AddressObj.Entries) > 0 {
			mac = network.AddressObj.Entries[0].Value
		}

		switchName := strconv.FormatUint(uint64(network.SwitchID), 10)
		if network.SwitchType == "standard" && network.StandardSwitch != nil {
			switchName = network.StandardSwitch.Name
		} else if network.SwitchType == "manual" && network.ManualSwitch != nil {
			switchName = network.ManualSwitch.Name
		}

		rows = append(rows, []string{
			strconv.FormatUint(uint64(network.ID), 10),
			switchName,
			network.SwitchType,
			network.Emulation,
			strconv.FormatBool(network.Enable),
			mac,
		})
	}
	return fmt.Sprintf("Networks for VM: %s (RID: %d)\n%s", vm.Name, vm.RID, styledTable(headers, rows))
}

func formatQGAResponse(response json.RawMessage) string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, response, "", "  "); err != nil {
		return string(response)
	}
	return pretty.String()
}

func formatQGAInfo(status libvirtServiceInterfaces.QemuGuestAgentStatus) string {
	capabilities := make([]string, 0, len(status.Capabilities))
	for _, capability := range status.Capabilities {
		if capability.Enabled {
			capabilities = append(capabilities, capability.Name)
		}
	}
	lines := []string{
		styledKeyValue("RID:", strconv.FormatUint(uint64(status.RID), 10)),
		styledKeyValue("Enabled:", strconv.FormatBool(status.Enabled)),
		styledKeyValue("Domain state:", status.DomainState),
		styledKeyValue("Reachable:", strconv.FormatBool(status.Reachable)),
		styledKeyValue("Version:", status.Version),
		styledKeyValue("Enabled capabilities:", formatStringList(capabilities)),
	}
	if status.UnavailableReason != "" {
		lines = append(lines, styledKeyValue("Unavailable reason:", status.UnavailableReason))
	}
	return strings.Join(lines, "\n")
}

func vmsList(ctx *Context, jsonMode bool) {
	vms, err := listVMs(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching VMs", err)
		return
	}
	if vms == nil {
		vms = []vmModels.VM{}
	}
	if jsonMode {
		println(ctx, mustJSON(redactVMListSecrets(vms)))
		return
	}
	println(ctx, formatVMList(vms))
}

func vmsCreate(ctx *Context, request libvirtServiceInterfaces.CreateVMRequest, jsonMode bool) {
	result, err := createVM(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error creating VM", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("VM %d (%s) created successfully.", result.RID, result.Name))
}

func vmsGet(ctx *Context, rid uint, jsonMode bool) {
	vm, err := getVM(ctx, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching VM", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(redactVMSecrets(*vm)))
		return
	}
	println(ctx, formatVMDetails(vm))
}

func vmsAction(ctx *Context, rid uint, action string, jsonMode bool) {
	result, err := requestVMAction(ctx, rid, action)
	if err != nil {
		printOperationError(ctx, jsonMode, fmt.Sprintf("Error %s VM", action), err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("VM %d: %s %s (Task: %d)", result.RID, result.Action, result.Outcome, result.TaskID))
}

func vmsNetworksList(ctx *Context, rid uint, jsonMode bool) {
	vm, err := listVMNetworks(ctx, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching VM networks", err)
		return
	}
	if jsonMode {
		networks := vm.Networks
		if networks == nil {
			networks = []vmModels.Network{}
		}
		println(ctx, mustJSON(networks))
		return
	}
	println(ctx, formatVMNetworks(vm))
}

func vmsNetworkAttach(ctx *Context, request libvirtServiceInterfaces.NetworkAttachRequest, jsonMode bool) {
	result, err := attachVMNetwork(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error attaching VM network", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Network attached to VM %d.", result.RID))
}

func vmsNetworkDetach(ctx *Context, rid, networkID uint, jsonMode bool) {
	result, err := detachVMNetwork(ctx, rid, networkID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error removing VM network", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Network %d removed from VM %d.", result.NetworkID, result.RID))
}

func vmsDelete(ctx *Context, rid uint, deleteMACs, deleteRawDisks, deleteVolumes bool, jsonMode bool) {
	result, err := deleteVM(ctx, rid, deleteMACs, deleteRawDisks, deleteVolumes)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting VM", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("VM %d deleted successfully.", result.RID))
}

func vmsPurge(ctx *Context, rid uint, deleteMACs bool, jsonMode bool) {
	result, err := purgeVM(ctx, rid, deleteMACs)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error purging VM", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("VM %d registration purged successfully.", result.RID))
}

func vmsQGASend(ctx *Context, rid uint, command string, jsonMode bool) {
	response, err := sendVMQGA(ctx, rid, command)
	if err != nil {
		if !jsonMode && err.Error() == "vm_service_unavailable" {
			println(ctx, styledErrorf("Error: VM service unavailable."))
			return
		}
		printOperationError(ctx, jsonMode, "QGA command failed", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(response))
		return
	}
	println(ctx, formatQGAResponse(response))
}

func vmsQGAInfo(ctx *Context, rid uint, jsonMode bool) {
	status, err := inspectVMQGA(ctx, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error inspecting QGA", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(status))
		return
	}
	println(ctx, formatQGAInfo(status))
}

func processVMListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JSONPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_list_request: " + err.Error()}
	}
	vms, err := listVMs(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if vms == nil {
		vms = []vmModels.VM{}
	}
	return operationSuccess(request.JSON, redactVMListSecrets(vms), formatVMList(vms))
}

func processVMGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_get_request: " + err.Error()}
	}
	vm, err := getVM(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, redactVMSecrets(*vm), formatVMDetails(vm))
}

func processVMCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_create_request: " + err.Error()}
	}
	result, err := createVM(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("VM %d (%s) created successfully.", result.RID, result.Name))
}

func processVMActionSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMActionPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_action_request: " + err.Error()}
	}
	result, err := requestVMAction(ctx, request.RID, request.Action)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("VM %d: %s %s (Task: %d)", result.RID, result.Action, result.Outcome, result.TaskID),
	)
}

func processVMDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_delete_request: " + err.Error()}
	}
	if request.DryRun {
		return processVMDeletePreviewSocketRequest(ctx, request)
	}
	result, err := deleteVM(ctx, request.RID, request.DeleteMACs, request.DeleteRawDisks, request.DeleteVolumes)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("VM %d deleted successfully.", result.RID))
}

func processVMPurgeSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMPurgePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_purge_request: " + err.Error()}
	}
	result, err := purgeVM(ctx, request.RID, request.DeleteMACs)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("VM %d registration purged successfully.", result.RID))
}

func processVMNetworksSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_networks_request: " + err.Error()}
	}
	vm, err := listVMNetworks(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	networks := vm.Networks
	if networks == nil {
		networks = []vmModels.Network{}
	}
	return operationSuccess(request.JSON, networks, formatVMNetworks(vm))
}

func processVMNetworkAttachSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMNetworkAttachPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_network_attach_request: " + err.Error()}
	}
	request.Request.RID = request.RID
	result, err := attachVMNetwork(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Network attached to VM %d.", result.RID))
}

func processVMNetworkDetachSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMNetworkDetachPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_network_detach_request: " + err.Error()}
	}
	result, err := detachVMNetwork(ctx, request.RID, request.NetworkID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("Network %d removed from VM %d.", result.NetworkID, result.RID),
	)
}

func processVMQGASendSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMQGASendPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_qga_request: " + err.Error()}
	}
	response, err := sendVMQGA(ctx, request.RID, request.Command)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, response, formatQGAResponse(response))
}

func processVMQGAInfoSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_qga_info_request: " + err.Error()}
	}
	status, err := inspectVMQGA(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, status, formatQGAInfo(status))
}
