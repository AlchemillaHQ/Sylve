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
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

type vmCreateResult struct {
	Created bool   `json:"created"`
	RID     uint   `json:"rid"`
	Name    string `json:"name"`
}

type vmNetworkAttachResult struct {
	Attached   bool   `json:"attached"`
	RID        uint   `json:"rid"`
	SwitchName string `json:"switchName"`
	Emulation  string `json:"emulation"`
	MacID      *uint  `json:"macId,omitempty"`
}

type vmActionResult struct {
	RID     uint   `json:"rid"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	TaskID  uint   `json:"taskId,omitempty"`
}

type vmNetworkDetachResult struct {
	Deleted   bool `json:"deleted"`
	RID       uint `json:"rid"`
	NetworkID uint `json:"networkId"`
}

type vmDeleteResult struct {
	Deleted          bool     `json:"deleted"`
	RID              uint     `json:"rid"`
	Warnings         []string `json:"warnings"`
	RetainedDatasets []string `json:"retainedDatasets"`
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
			{"create --file <path>", "Create a VM from a JSON request file"},
			{"get <rid>", "Get VM details"},
			{"start <rid>", "Start a VM"},
			{"stop <rid>", "Force-stop a VM"},
			{"shutdown <rid>", "Gracefully shut down a VM"},
			{"reboot <rid>", "Reboot a VM"},
			{"delete <rid> [--delete-macs] [--delete-raw-disks] [--delete-volumes]", "Delete a VM"},
			{"purge <rid> [--delete-macs]", "Purge an orphaned VM registration"},
			{"networks <rid>", "List networks for a VM"},
			{"addnet <rid> <switch> <virtio|e1000> [mac_id]", "Attach a network to a powered-off VM"},
			{"rmnet <rid> <net_id>", "Remove a network from a powered-off VM"},
			{"qga send <rid> <command>", "Send a QGA command to a VM"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	// Convenience form: vms <rid> qga send <command>
	if rid, err := parseVMRID(subCmd); err == nil {
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
		rid, deleteMACs, deleteRawDisks, deleteVolumes, err := parseVMDeleteArgs(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsDelete(ctx, rid, deleteMACs, deleteRawDisks, deleteVolumes, jsonMode)

	case "purge":
		rid, deleteMACs, err := parseVMPurgeArgs(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsPurge(ctx, rid, deleteMACs, jsonMode)

	case "networks":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: vms networks <rid>"))
			return
		}
		rid, err := parseVMRID(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subArgs[0]))
			return
		}
		vmsNetworksList(ctx, rid, jsonMode)

	case "addnet":
		if len(subArgs) < 3 || len(subArgs) > 4 {
			println(ctx, styledErrorf("Usage: vms addnet <rid> <switch> <virtio|e1000> [mac_id]"))
			return
		}
		rid, err := parseVMRID(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subArgs[0]))
			return
		}
		request := libvirtServiceInterfaces.NetworkAttachRequest{
			RID:        rid,
			SwitchName: subArgs[1],
			Emulation:  subArgs[2],
		}
		if len(subArgs) == 4 {
			macID, err := parseVMNetworkID(subArgs[3])
			if err != nil {
				println(ctx, styledErrorf("Invalid MAC object ID '%s'", subArgs[3]))
				return
			}
			request.MacId = &macID
		}
		vmsNetworkAttach(ctx, request, jsonMode)

	case "rmnet":
		if len(subArgs) != 2 {
			println(ctx, styledErrorf("Usage: vms rmnet <rid> <network_id>"))
			return
		}
		rid, err := parseVMRID(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", subArgs[0]))
			return
		}
		networkID, err := parseVMNetworkID(subArgs[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid network ID '%s'", subArgs[1]))
			return
		}
		vmsNetworkDetach(ctx, rid, networkID, jsonMode)

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
		println(ctx, styledErrorf("Usage: vms qga send <rid> <command>"))
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
	const usage = "Usage: vms create --file <path>"
	if len(args) != 2 || args[0] != "--file" || strings.TrimSpace(args[1]) == "" {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("%s", usage)
	}
	return consoleprotocol.LoadVMCreateRequest(args[1])
}

func parseVMDeleteArgs(args []string) (uint, bool, bool, bool, error) {
	const usage = "Usage: vms delete <rid> [--delete-macs] [--delete-raw-disks] [--delete-volumes]"
	if len(args) == 0 {
		return 0, false, false, false, fmt.Errorf("%s", usage)
	}

	rid, err := parseVMRID(args[0])
	if err != nil {
		return 0, false, false, false, fmt.Errorf("Invalid RID '%s'", args[0])
	}

	deleteMACs := false
	deleteRawDisks := false
	deleteVolumes := false
	for _, arg := range args[1:] {
		switch arg {
		case "--delete-macs":
			if deleteMACs {
				return 0, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteMACs = true
		case "--delete-raw-disks":
			if deleteRawDisks {
				return 0, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteRawDisks = true
		case "--delete-volumes":
			if deleteVolumes {
				return 0, false, false, false, fmt.Errorf("%s", usage)
			}
			deleteVolumes = true
		default:
			return 0, false, false, false, fmt.Errorf("%s", usage)
		}
	}

	return rid, deleteMACs, deleteRawDisks, deleteVolumes, nil
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
	return parsePositiveUint(value)
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

	if err := ctx.VirtualMachine.CreateVM(request, context.Background()); err != nil {
		return vmCreateResult{}, fmt.Errorf("failed_to_create_vm: %w", err)
	}
	return vmCreateResult{Created: true, RID: *request.RID, Name: request.Name}, nil
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
		context.Background(), "vm", rid, action, "user", "console",
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
	if err := ctx.VirtualMachine.NetworkAttach(request); err != nil {
		return vmNetworkAttachResult{}, fmt.Errorf("failed_to_attach_vm_network: %w", err)
	}

	return vmNetworkAttachResult{
		Attached:   true,
		RID:        request.RID,
		SwitchName: request.SwitchName,
		Emulation:  request.Emulation,
		MacID:      request.MacId,
	}, nil
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
	if err := ctx.VirtualMachine.NetworkDetach(rid, networkID); err != nil {
		return vmNetworkDetachResult{}, fmt.Errorf("failed_to_detach_vm_network: %w", err)
	}
	return vmNetworkDetachResult{Deleted: true, RID: rid, NetworkID: networkID}, nil
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
		context.Background(),
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
	return vmDeleteResult{
		Deleted:          true,
		RID:              rid,
		Warnings:         removal.Warnings,
		RetainedDatasets: removal.RetainedDatasets,
	}, nil
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

	headers := []string{"NET ID", "SWITCH", "TYPE", "EMUL", "MAC"}
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
		println(ctx, mustJSON(vms))
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
		println(ctx, mustJSON(vm))
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

func processVMListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMListPayload
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
	return operationSuccess(request.JSON, vms, formatVMList(vms))
}

func processVMGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_get_request: " + err.Error()}
	}
	vm, err := getVM(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, vm, formatVMDetails(vm))
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
	var request consoleprotocol.VMNetworksPayload
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
