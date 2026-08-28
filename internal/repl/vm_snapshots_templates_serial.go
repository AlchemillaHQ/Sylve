// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
)

type vmSnapshotService interface {
	ListVMSnapshots(rid uint) ([]vmModels.VMSnapshot, error)
	CreateVMSnapshot(ctx context.Context, rid uint, name, description string) (*vmModels.VMSnapshot, error)
	RollbackVMSnapshotWithDestroyNewer(ctx context.Context, rid, snapshotID uint, destroyNewer bool) (libvirtService.VMSnapshotRollbackResult, error)
	DeleteVMSnapshot(ctx context.Context, rid, snapshotID uint) error
}

type vmTemplateService interface {
	GetVMTemplatesSimple() ([]libvirtServiceInterfaces.SimpleTemplateList, error)
	GetVMTemplate(templateID uint) (*vmModels.VMTemplate, error)
	PreflightConvertVMToTemplate(ctx context.Context, rid uint, request libvirtServiceInterfaces.ConvertToTemplateRequest) error
	PreflightCreateVMsFromTemplate(ctx context.Context, templateID uint, request libvirtServiceInterfaces.CreateFromTemplateRequest) error
	DeleteVMTemplate(ctx context.Context, templateID uint) error
}

type vmTemplateLifecycleService interface {
	RequestActionWithPayload(
		ctx context.Context, guestType string, guestID uint, action, source, requestedBy, payload string,
	) (*taskModels.GuestLifecycleTask, string, error)
	ListActiveTasks(guestType string, guestID uint) ([]taskModels.GuestLifecycleTask, error)
}

var vmSerialConsoleDeviceStat = os.Stat

func handleVMSnapshots(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms snapshots", []cmdHelp{
			{"list <rid>", "List VM snapshots"},
			{"create <rid> --name <name> [--description <text>]", "Create a crash-consistent snapshot; the guest is not quiesced"},
			{"rollback <rid> <snapshot_id> [--destroy-newer]", "Rollback, stopping the VM if needed; acknowledge destruction of newer Sylve or ZFS snapshots"},
			{"delete <rid> <snapshot_id>", "Delete the explicitly selected snapshot"},
		})
		return
	}

	switch args[0] {
	case "list":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms snapshots list <rid>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		vmsSnapshotList(ctx, rid, jsonMode)

	case "create":
		if len(args) < 2 {
			println(ctx, styledErrorf("Usage: vms snapshots create <rid> --name <name> [--description <text>]"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		options, err := parseVMNamedOptions(args[2:], vmAllowed("--name", "--description"), nil)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		name, description, err := consoleprotocol.ValidateVMSnapshotCreate(options["--name"], options["--description"])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsSnapshotCreate(ctx, rid, name, description, jsonMode)

	case "rollback":
		if len(args) < 3 {
			println(ctx, styledErrorf("Usage: vms snapshots rollback <rid> <snapshot_id> [--destroy-newer]"))
			return
		}
		rid, snapshotID, err := parseVMSnapshotIdentifiers(args[1], args[2])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		options, err := parseVMNamedOptions(args[3:], vmAllowed("--destroy-newer"), vmAllowed("--destroy-newer"))
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		destroyNewer, err := vmBoolOptionValue(options, "--destroy-newer")
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsSnapshotRollback(ctx, rid, snapshotID, destroyNewer, jsonMode)

	case "delete":
		if len(args) != 3 {
			println(ctx, styledErrorf("Usage: vms snapshots delete <rid> <snapshot_id>"))
			return
		}
		rid, snapshotID, err := parseVMSnapshotIdentifiers(args[1], args[2])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsSnapshotDelete(ctx, rid, snapshotID, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown vms snapshots command: '%s'", args[0]))
	}
}

func parseVMSnapshotIdentifiers(ridText, snapshotText string) (uint, uint, error) {
	rid, err := parseVMRID(ridText)
	if err != nil {
		return 0, 0, fmt.Errorf("Invalid RID '%s'", ridText)
	}
	snapshotID, err := parsePositiveUint(snapshotText)
	if err != nil {
		return 0, 0, fmt.Errorf("Invalid snapshot ID '%s'", snapshotText)
	}
	return rid, snapshotID, nil
}

func listVMSnapshots(service vmSnapshotService, rid uint) ([]vmModels.VMSnapshot, error) {
	if service == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}
	snapshots, err := service.ListVMSnapshots(rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_vm_snapshots: %w", err)
	}
	if snapshots == nil {
		snapshots = []vmModels.VMSnapshot{}
	}
	return snapshots, nil
}

func formatVMSnapshotList(rid uint, snapshots []vmModels.VMSnapshot) string {
	if len(snapshots) == 0 {
		return fmt.Sprintf("VM %d has no snapshots.", rid)
	}
	rows := make([][]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		parent := "-"
		if snapshot.ParentSnapshotID != nil {
			parent = strconv.FormatUint(uint64(*snapshot.ParentSnapshotID), 10)
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(snapshot.ID), 10), snapshot.Name, parent,
			strconv.Itoa(len(snapshot.RootDatasets)), snapshot.CreatedAt.UTC().Format("2006-01-02 15:04:05Z"),
		})
	}
	return fmt.Sprintf("Snapshots for VM RID %d\n%s", rid, styledTable(
		[]string{"ID", "NAME", "PARENT", "ROOTS", "CREATED"}, rows,
	))
}

func vmsSnapshotList(ctx *Context, rid uint, jsonMode bool) {
	if ctx == nil {
		printOperationError(ctx, jsonMode, "Error listing VM snapshots", fmt.Errorf("vm_service_unavailable"))
		return
	}
	snapshots, err := listVMSnapshots(ctx.VirtualMachine, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error listing VM snapshots", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(snapshots))
		return
	}
	println(ctx, formatVMSnapshotList(rid, snapshots))
}

func createVMSnapshot(ctx context.Context, service vmSnapshotService, rid uint, name, description string) (*vmModels.VMSnapshot, error) {
	if service == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}
	created, err := service.CreateVMSnapshot(ctx, rid, name, description)
	if err != nil {
		return nil, fmt.Errorf("failed_to_create_vm_snapshot: %w", err)
	}
	if created == nil {
		return nil, fmt.Errorf("snapshot_creation_returned_empty_result")
	}
	return created, nil
}

func vmsSnapshotCreate(ctx *Context, rid uint, name, description string, jsonMode bool) {
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	created, err := createVMSnapshot(operationContext(ctx), service, rid, name, description)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error creating VM snapshot", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(created))
		return
	}
	println(ctx, styledSuccessf("Snapshot %d (%s) created for VM %d.", created.ID, created.Name, rid))
}

func rollbackVMSnapshot(ctx context.Context, service vmSnapshotService, rid, snapshotID uint, destroyNewer bool) (consoleprotocol.VMSnapshotRollbackOutput, error) {
	output := consoleprotocol.VMSnapshotRollbackOutput{RID: rid, SnapshotID: snapshotID, Warnings: []string{}}
	if service == nil {
		return output, fmt.Errorf("vm_service_unavailable")
	}
	result, err := service.RollbackVMSnapshotWithDestroyNewer(ctx, rid, snapshotID, destroyNewer)
	if err != nil {
		return output, fmt.Errorf("failed_to_rollback_vm_snapshot: %w", err)
	}
	output.RolledBack = true
	output.WasRunning = result.WasRunning
	output.Restarted = result.Restarted
	output.NewerSnapshotsDestroyed = result.NewerSnapshotsDestroyed
	output.Warnings = result.Warnings
	if output.Warnings == nil {
		output.Warnings = []string{}
	}
	return output, nil
}

func formatVMSnapshotRollback(output consoleprotocol.VMSnapshotRollbackOutput) string {
	lines := []string{
		styledSuccessf("VM %d rolled back to snapshot %d.", output.RID, output.SnapshotID),
		styledKeyValue("VM was running:", strconv.FormatBool(output.WasRunning)),
		styledKeyValue("VM restarted:", strconv.FormatBool(output.Restarted)),
		styledKeyValue("Newer snapshots destroyed:", strconv.FormatInt(output.NewerSnapshotsDestroyed, 10)),
	}
	if len(output.Warnings) > 0 {
		lines = append(lines, styledKeyValue("Warnings:", strings.Join(output.Warnings, "; ")))
	}
	return strings.Join(lines, "\n")
}

func vmsSnapshotRollback(ctx *Context, rid, snapshotID uint, destroyNewer, jsonMode bool) {
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	output, err := rollbackVMSnapshot(operationContext(ctx), service, rid, snapshotID, destroyNewer)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error rolling back VM snapshot", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(output))
		return
	}
	println(ctx, formatVMSnapshotRollback(output))
}

func deleteVMSnapshot(ctx context.Context, service vmSnapshotService, rid, snapshotID uint) (consoleprotocol.VMSnapshotDeleteOutput, error) {
	output := consoleprotocol.VMSnapshotDeleteOutput{RID: rid, SnapshotID: snapshotID}
	if service == nil {
		return output, fmt.Errorf("vm_service_unavailable")
	}
	if err := service.DeleteVMSnapshot(ctx, rid, snapshotID); err != nil {
		return output, fmt.Errorf("failed_to_delete_vm_snapshot: %w", err)
	}
	output.Deleted = true
	return output, nil
}

func vmsSnapshotDelete(ctx *Context, rid, snapshotID uint, jsonMode bool) {
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	output, err := deleteVMSnapshot(operationContext(ctx), service, rid, snapshotID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting VM snapshot", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(output))
		return
	}
	println(ctx, styledSuccessf("Snapshot %d deleted from VM %d.", snapshotID, rid))
}

func handleVMTemplates(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms templates", []cmdHelp{
			{"list", "List templates and source storage mapping IDs"},
			{"get <template_id>", "Get a template and its configuration and mappings"},
			{"capture <rid> --name <name>", "Queue capture from a powered-off VM; source VM is retained"},
			{"create <template_id> --mode <single|multiple> [target and storage options]", "Queue creation of one or up to 200 VMs"},
			{"delete <template_id>", "Delete a template when no create task is active"},
		})
		return
	}

	switch args[0] {
	case "list":
		if len(args) != 1 {
			println(ctx, styledErrorf("Usage: vms templates list"))
			return
		}
		vmsTemplateList(ctx, jsonMode)

	case "get":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms templates get <template_id>"))
			return
		}
		templateID, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid template ID '%s'", args[1]))
			return
		}
		vmsTemplateGet(ctx, templateID, jsonMode)

	case "capture":
		if len(args) < 2 {
			println(ctx, styledErrorf("Usage: vms templates capture <rid> --name <name>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		options, err := parseVMNamedOptions(args[2:], vmAllowed("--name"), nil)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		request, err := consoleprotocol.BuildVMTemplateConvertRequest(options["--name"])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsTemplateCapture(ctx, rid, request, jsonMode)

	case "create":
		if len(args) < 2 {
			println(ctx, styledErrorf("Usage: vms templates create <template_id> [options]"))
			return
		}
		templateID, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid template ID '%s'", args[1]))
			return
		}
		request, err := parseConsoleVMTemplateCreateRequest(args[2:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		vmsTemplateCreate(ctx, templateID, request, jsonMode)

	case "delete":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms templates delete <template_id>"))
			return
		}
		templateID, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid template ID '%s'", args[1]))
			return
		}
		vmsTemplateDelete(ctx, templateID, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown vms templates command: '%s'", args[0]))
	}
}

func parseConsoleVMTemplateCreateRequest(args []string) (libvirtServiceInterfaces.CreateFromTemplateRequest, error) {
	options, err := parseVMNamedOptionsRepeated(
		args,
		vmAllowed("--mode", "--rid", "--name", "--start-rid", "--count", "--name-prefix", "--storage-pool", "--rewrite-cloud-init-identity", "--cloud-init-prefix"),
		vmAllowed("--rewrite-cloud-init-identity"), vmAllowed("--storage-pool"),
	)
	if err != nil {
		return libvirtServiceInterfaces.CreateFromTemplateRequest{}, err
	}
	value := func(name string) string {
		if len(options[name]) == 0 {
			return ""
		}
		return options[name][0]
	}
	request := libvirtServiceInterfaces.CreateFromTemplateRequest{
		Mode: value("--mode"), Name: value("--name"), NamePrefix: value("--name-prefix"),
		CloudInitPrefix: value("--cloud-init-prefix"),
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "single"
	}
	if mode == "single" {
		for _, incompatible := range []string{"--start-rid", "--count", "--name-prefix"} {
			if _, supplied := options[incompatible]; supplied {
				return request, fmt.Errorf("%s is incompatible with single mode", incompatible)
			}
		}
	}
	if mode == "multiple" {
		for _, incompatible := range []string{"--rid", "--name"} {
			if _, supplied := options[incompatible]; supplied {
				return request, fmt.Errorf("%s is incompatible with multiple mode", incompatible)
			}
		}
	}
	if text := value("--rid"); text != "" {
		rid, err := parseVMRID(text)
		if err != nil {
			return request, fmt.Errorf("--rid must be between 1 and 9999")
		}
		request.RID = rid
	}
	if text := value("--start-rid"); text != "" {
		rid, err := parseVMRID(text)
		if err != nil {
			return request, fmt.Errorf("--start-rid must be between 1 and 9999")
		}
		request.StartRID = rid
	}
	if text := value("--count"); text != "" {
		count, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return request, fmt.Errorf("--count must be an integer")
		}
		request.Count = count
	}
	request.RewriteCloudInitIdentity, err = vmRepeatedBoolValue(options, "--rewrite-cloud-init-identity")
	if err != nil {
		return request, err
	}
	request.StoragePools, err = consoleprotocol.ParseVMTemplateStoragePoolAssignments(options["--storage-pool"])
	if err != nil {
		return request, err
	}
	return consoleprotocol.ValidateVMTemplateCreateRequest(request)
}

func listVMTemplates(service vmTemplateService) ([]consoleprotocol.VMTemplateInfo, error) {
	if service == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}
	simple, err := service.GetVMTemplatesSimple()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_vm_templates: %w", err)
	}
	result := make([]consoleprotocol.VMTemplateInfo, 0, len(simple))
	for _, item := range simple {
		template, err := service.GetVMTemplate(item.ID)
		if err != nil {
			return nil, fmt.Errorf("failed_to_get_vm_template_%d: %w", item.ID, err)
		}
		info := consoleprotocol.VMTemplateInfo{
			ID: template.ID, Name: template.Name, SourceVMName: template.SourceVMName,
			SourceVMRID: template.SourceVMRID, Storages: []consoleprotocol.VMTemplateStorageInfo{},
		}
		for _, storage := range template.Storages {
			info.Storages = append(info.Storages, consoleprotocol.VMTemplateStorageInfo{
				SourceStorageID: storage.SourceStorageID, Type: string(storage.Type), Pool: storage.Pool,
			})
		}
		result = append(result, info)
	}
	return result, nil
}

func formatVMTemplateList(templates []consoleprotocol.VMTemplateInfo) string {
	if len(templates) == 0 {
		return "No VM templates found."
	}
	rows := make([][]string, 0, len(templates))
	for _, template := range templates {
		mappings := make([]string, 0, len(template.Storages))
		for _, storage := range template.Storages {
			mappings = append(mappings, fmt.Sprintf("%d=%s (%s)", storage.SourceStorageID, storage.Pool, storage.Type))
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(template.ID), 10), template.Name,
			fmt.Sprintf("%s (%d)", template.SourceVMName, template.SourceVMRID),
			formatStringList(mappings),
		})
	}
	return styledTable([]string{"ID", "NAME", "SOURCE VM", "STORAGE MAPPINGS"}, rows)
}

func vmsTemplateList(ctx *Context, jsonMode bool) {
	var service vmTemplateService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	templates, err := listVMTemplates(service)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error listing VM templates", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(templates))
		return
	}
	println(ctx, formatVMTemplateList(templates))
}

func getVMTemplate(service vmTemplateService, templateID uint) (*vmModels.VMTemplate, error) {
	if templateID == 0 {
		return nil, fmt.Errorf("invalid_template_id")
	}
	if service == nil {
		return nil, fmt.Errorf("vm_service_unavailable")
	}
	template, err := service.GetVMTemplate(templateID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_template: %w", err)
	}
	if template == nil {
		return nil, fmt.Errorf("vm_template_not_found")
	}
	if template.Storages == nil {
		template.Storages = []vmModels.VMTemplateStorage{}
	}
	if template.Networks == nil {
		template.Networks = []vmModels.VMTemplateNetwork{}
	}
	if template.ExtraBhyveOptions == nil {
		template.ExtraBhyveOptions = []string{}
	}
	return template, nil
}

func formatVMTemplate(template *vmModels.VMTemplate) string {
	storages := make([]string, 0, len(template.Storages))
	for _, storage := range template.Storages {
		storages = append(storages, fmt.Sprintf("%d: %s on %s (%s)", storage.SourceStorageID, storage.Type, storage.Pool, storage.Emulation))
	}
	networks := make([]string, 0, len(template.Networks))
	for _, network := range template.Networks {
		networks = append(networks, fmt.Sprintf("%s: %s (%s)", network.Name, network.SwitchName, network.Emulation))
	}
	return strings.Join([]string{
		styledKeyValue("ID:", strconv.FormatUint(uint64(template.ID), 10)),
		styledKeyValue("Name:", template.Name),
		styledKeyValue("Source VM:", fmt.Sprintf("%s (%d)", template.SourceVMName, template.SourceVMRID)),
		styledKeyValue("Description:", template.Description),
		styledKeyValue("vCPUs:", strconv.Itoa(template.CPUSockets*template.CPUCores*template.CPUThreads)),
		styledKeyValue("RAM:", formatMemorySize(template.RAM)),
		styledKeyValue("Storage mappings:", formatStringList(storages)),
		styledKeyValue("Network mappings:", formatStringList(networks)),
	}, "\n")
}

func vmsTemplateGet(ctx *Context, templateID uint, jsonMode bool) {
	var service vmTemplateService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	template, err := getVMTemplate(service, templateID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching VM template", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(template))
		return
	}
	println(ctx, formatVMTemplate(template))
}

func queueVMTemplateConvert(
	ctx context.Context, service vmTemplateService, lifecycle vmTemplateLifecycleService,
	rid uint, request libvirtServiceInterfaces.ConvertToTemplateRequest,
) (consoleprotocol.VMTemplateTaskOutput, error) {
	output := consoleprotocol.VMTemplateTaskOutput{SourceRID: rid, Action: "capture"}
	if service == nil {
		return output, fmt.Errorf("vm_service_unavailable")
	}
	if lifecycle == nil {
		return output, fmt.Errorf("lifecycle_service_unavailable")
	}
	if err := service.PreflightConvertVMToTemplate(ctx, rid, request); err != nil {
		return output, fmt.Errorf("template_convert_preflight_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return output, fmt.Errorf("invalid_template_convert_request: %w", err)
	}
	task, outcome, err := lifecycle.RequestActionWithPayload(
		ctx, taskModels.GuestTypeVMTemplate, rid, "convert", taskModels.LifecycleTaskSourceUser, "console", string(payload),
	)
	if err != nil {
		return output, fmt.Errorf("failed_to_enqueue_lifecycle_task: %w", err)
	}
	if task == nil || task.ID == 0 {
		return output, fmt.Errorf("failed_to_enqueue_lifecycle_task: lifecycle_task_missing")
	}
	output.TaskID = task.ID
	output.Outcome = outcome
	return output, nil
}

func vmsTemplateCapture(ctx *Context, rid uint, request libvirtServiceInterfaces.ConvertToTemplateRequest, jsonMode bool) {
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := queueVMTemplateConvert(operationContext(ctx), service, lifecycle, rid, request)
	printVMTemplateTask(ctx, output, err, jsonMode)
}

func queueVMTemplateCreate(
	ctx context.Context, service vmTemplateService, lifecycle vmTemplateLifecycleService,
	templateID uint, request libvirtServiceInterfaces.CreateFromTemplateRequest,
) (consoleprotocol.VMTemplateTaskOutput, error) {
	output := consoleprotocol.VMTemplateTaskOutput{TemplateID: templateID, Action: "create"}
	if service == nil {
		return output, fmt.Errorf("vm_service_unavailable")
	}
	if lifecycle == nil {
		return output, fmt.Errorf("lifecycle_service_unavailable")
	}
	if err := service.PreflightCreateVMsFromTemplate(ctx, templateID, request); err != nil {
		return output, fmt.Errorf("template_create_preflight_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return output, fmt.Errorf("invalid_template_create_request: %w", err)
	}
	task, outcome, err := lifecycle.RequestActionWithPayload(
		ctx, taskModels.GuestTypeVMTemplate, templateID, "create", taskModels.LifecycleTaskSourceUser, "console", string(payload),
	)
	if err != nil {
		return output, fmt.Errorf("failed_to_enqueue_lifecycle_task: %w", err)
	}
	if task == nil || task.ID == 0 {
		return output, fmt.Errorf("failed_to_enqueue_lifecycle_task: lifecycle_task_missing")
	}
	output.TaskID = task.ID
	output.Outcome = outcome
	return output, nil
}

func vmsTemplateCreate(ctx *Context, templateID uint, request libvirtServiceInterfaces.CreateFromTemplateRequest, jsonMode bool) {
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := queueVMTemplateCreate(operationContext(ctx), service, lifecycle, templateID, request)
	printVMTemplateTask(ctx, output, err, jsonMode)
}

func printVMTemplateTask(ctx *Context, output consoleprotocol.VMTemplateTaskOutput, err error, jsonMode bool) {
	if err != nil {
		printOperationError(ctx, jsonMode, "Error queueing VM template operation", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(output))
		return
	}
	target := fmt.Sprintf("template %d", output.TemplateID)
	if output.SourceRID != 0 {
		target = fmt.Sprintf("source VM %d", output.SourceRID)
	}
	println(ctx, styledSuccessf("Template %s for %s: %s (Task: %d).", output.Action, target, output.Outcome, output.TaskID))
}

func deleteVMTemplate(
	ctx context.Context, service vmTemplateService, lifecycle vmTemplateLifecycleService, templateID uint,
) (consoleprotocol.VMTemplateDeleteOutput, error) {
	output := consoleprotocol.VMTemplateDeleteOutput{TemplateID: templateID}
	if service == nil {
		return output, fmt.Errorf("vm_service_unavailable")
	}
	if lifecycle == nil {
		return output, fmt.Errorf("lifecycle_service_unavailable")
	}
	active, err := lifecycle.ListActiveTasks(taskModels.GuestTypeVMTemplate, templateID)
	if err != nil {
		return output, fmt.Errorf("failed_to_check_vm_template_usage: %w", err)
	}
	for _, task := range active {
		if strings.EqualFold(strings.TrimSpace(task.Action), "create") {
			return output, fmt.Errorf("vm_template_in_use: vm_template_creation_in_progress")
		}
	}
	if err := service.DeleteVMTemplate(ctx, templateID); err != nil {
		return output, fmt.Errorf("failed_to_delete_vm_template: %w", err)
	}
	output.Deleted = true
	return output, nil
}

func vmsTemplateDelete(ctx *Context, templateID uint, jsonMode bool) {
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := deleteVMTemplate(operationContext(ctx), service, lifecycle, templateID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting VM template", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(output))
		return
	}
	println(ctx, styledSuccessf("VM template %d deleted.", templateID))
}

func getVMSerialConsoleAccess(ctx *Context, rid uint, baudRate string) (libvirtService.VMSerialConsoleAccessInfo, error) {
	request, err := libvirtService.ParseVMSerialConsoleRequest(strconv.FormatUint(uint64(rid), 10), baudRate)
	if err != nil {
		return libvirtService.VMSerialConsoleAccessInfo{}, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return libvirtService.VMSerialConsoleAccessInfo{}, fmt.Errorf("vm_service_unavailable")
	}
	return libvirtService.PreflightVMSerialConsole(ctx.VirtualMachine, request, vmSerialConsoleDeviceStat)
}

func formatVMSerialConsoleAccess(info libvirtService.VMSerialConsoleAccessInfo) string {
	return strings.Join([]string{
		styledKeyValue("RID:", strconv.FormatUint(uint64(info.RID), 10)),
		styledKeyValue("Name:", info.Name),
		styledKeyValue("Available:", strconv.FormatBool(info.Available)),
		styledKeyValue("Domain state:", info.DomainState),
		styledKeyValue("Device:", info.DevicePath),
		styledKeyValue("Baud:", info.BaudRate),
	}, "\n")
}

func vmsAccessSerial(ctx *Context, rid uint, baudRate string, jsonMode bool) {
	info, err := getVMSerialConsoleAccess(ctx, rid, baudRate)
	if err != nil {
		printOperationError(ctx, jsonMode, "VM serial console unavailable", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(info))
		return
	}
	println(ctx, formatVMSerialConsoleAccess(info))
	ctx.queueSerialConsole(consoleprotocol.VMSerialConsoleLaunch{
		RID: info.RID, BaudRate: info.BaudRate, DevicePath: info.DevicePath,
	})
}

func processVMSnapshotListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_snapshot_list_request: " + err.Error()}
	}
	if err := validateVMRID(request.RID); err != nil {
		return socketResponse{Error: err.Error()}
	}
	if ctx == nil {
		return socketResponse{Error: "vm_service_unavailable"}
	}
	snapshots, err := listVMSnapshots(ctx.VirtualMachine, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, snapshots, formatVMSnapshotList(request.RID, snapshots))
}

func processVMSnapshotCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMSnapshotCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_snapshot_create_request: " + err.Error()}
	}
	if err := validateVMRID(request.RID); err != nil {
		return socketResponse{Error: err.Error()}
	}
	name, description, err := consoleprotocol.ValidateVMSnapshotCreate(request.Name, request.Description)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	created, err := createVMSnapshot(operationContext(ctx), service, request.RID, name, description)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, created, styledSuccessf("Snapshot %d (%s) created for VM %d.", created.ID, created.Name, request.RID))
}

func processVMSnapshotRollbackSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMSnapshotRollbackPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_snapshot_rollback_request: " + err.Error()}
	}
	if err := validateVMRID(request.RID); err != nil || request.SnapshotID == 0 {
		return socketResponse{Error: "invalid_snapshot_rollback_identifiers"}
	}
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	output, err := rollbackVMSnapshot(operationContext(ctx), service, request.RID, request.SnapshotID, request.DestroyNewer)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, output, formatVMSnapshotRollback(output))
}

func processVMSnapshotDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMSnapshotDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_snapshot_delete_request: " + err.Error()}
	}
	if err := validateVMRID(request.RID); err != nil || request.SnapshotID == 0 {
		return socketResponse{Error: "invalid_snapshot_delete_identifiers"}
	}
	var service vmSnapshotService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	output, err := deleteVMSnapshot(operationContext(ctx), service, request.RID, request.SnapshotID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, output, styledSuccessf("Snapshot %d deleted from VM %d.", request.SnapshotID, request.RID))
}

func processVMTemplateListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JSONPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_template_list_request: " + err.Error()}
	}
	var service vmTemplateService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	templates, err := listVMTemplates(service)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, templates, formatVMTemplateList(templates))
}

func processVMTemplateGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMTemplateGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_template_get_request: " + err.Error()}
	}
	var service vmTemplateService
	if ctx != nil {
		service = ctx.VirtualMachine
	}
	template, err := getVMTemplate(service, request.TemplateID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, template, formatVMTemplate(template))
}

func processVMTemplateConvertSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMTemplateConvertPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_template_convert_request: " + err.Error()}
	}
	if err := validateVMRID(request.RID); err != nil {
		return socketResponse{Error: err.Error()}
	}
	normalized, err := consoleprotocol.BuildVMTemplateConvertRequest(request.Request.Name)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := queueVMTemplateConvert(operationContext(ctx), service, lifecycle, request.RID, normalized)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	text := styledSuccessf("Template capture queued for VM %d: %s (Task: %d).", request.RID, output.Outcome, output.TaskID)
	return operationSuccess(request.JSON, output, text)
}

func processVMTemplateCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMTemplateCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_template_create_request: " + err.Error()}
	}
	if request.TemplateID == 0 {
		return socketResponse{Error: "invalid_template_id"}
	}
	normalized, err := consoleprotocol.ValidateVMTemplateCreateRequest(request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := queueVMTemplateCreate(operationContext(ctx), service, lifecycle, request.TemplateID, normalized)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	text := styledSuccessf("VM creation queued from template %d: %s (Task: %d).", request.TemplateID, output.Outcome, output.TaskID)
	return operationSuccess(request.JSON, output, text)
}

func processVMTemplateDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMTemplateDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_template_delete_request: " + err.Error()}
	}
	if request.TemplateID == 0 {
		return socketResponse{Error: "invalid_template_id"}
	}
	var service vmTemplateService
	var lifecycle vmTemplateLifecycleService
	if ctx != nil {
		service = ctx.VirtualMachine
		lifecycle = ctx.Lifecycle
	}
	output, err := deleteVMTemplate(operationContext(ctx), service, lifecycle, request.TemplateID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, output, styledSuccessf("VM template %d deleted.", request.TemplateID))
}

func processVMAccessSerialSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMAccessSerialPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_access_serial_request: " + err.Error()}
	}
	info, err := getVMSerialConsoleAccess(ctx, request.RID, request.BaudRate)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	response := operationSuccess(request.JSON, info, formatVMSerialConsoleAccess(info))
	if !request.JSON {
		response.SerialConsole = &consoleprotocol.VMSerialConsoleLaunch{
			RID: info.RID, BaudRate: info.BaudRate, DevicePath: info.DevicePath,
		}
	}
	return response
}
