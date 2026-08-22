// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	golibvirt "github.com/digitalocean/go-libvirt"
)

type vmConfigMutationResult struct {
	Updated       bool   `json:"updated"`
	RID           uint   `json:"rid"`
	Configuration string `json:"configuration"`
}

func handleVMConfig(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms config", []cmdHelp{
			{"cpu <rid> --sockets <n> --cores <n> --threads <n> (--pin <socket:cores>...|--clear-pinning)", "Replace CPU topology and pinning; powered-off VM required"},
			{"memory <rid> --ram <size>", "Set RAM; powered-off VM required"},
			{"vnc <rid> [--enabled=<bool>] [--port <1-65535>] [--bind <ip>] [--resolution <WxH>] [--wait=<bool>] [--password-file <path>|--clear-password]", "Edit VNC while preserving omitted settings; powered-off VM required"},
			{"serial <rid> --enabled=<true|false>", "Set serial console; powered-off VM required"},
			{"pci <rid> (--device-id <id>...|--clear)", "Replace PCI assignments; powered-off VM required"},
			{"autostart <rid> --enabled=<true|false> --order <n>", "Set start-at-boot and order"},
			{"clock <rid> --time-offset <utc|localtime>", "Set guest clock; powered-off VM required"},
			{"shutdown <rid> --wait-seconds <1-3600>", "Set graceful-shutdown wait time"},
			{"boot-rom <rid> --boot-rom <uefi|uboot|none>", "Set boot ROM; powered-off VM required"},
			{"cloud-init <rid> [replacement files|--clear]", "Replace cloud-init configuration; powered-off VM required"},
			{"bhyve-options <rid> (--option <value>...|--clear)", "Replace extra bhyve options; powered-off VM required"},
			{"unknown-msr <rid> --enabled=<true|false>", "Set unknown-MSR handling; powered-off VM required"},
			{"qga <rid> --enabled=<true|false>", "Set QEMU Guest Agent channel; powered-off VM required"},
		})
		return
	}
	if len(args) < 2 {
		println(ctx, styledErrorf("Usage: vms config <command> <rid> [options]"))
		return
	}

	kind := args[0]
	rid, err := parseVMRID(args[1])
	if err != nil {
		println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
		return
	}
	optionArgs := args[2:]

	switch kind {
	case "cpu":
		options, err := parseVMNamedOptionsRepeated(optionArgs,
			vmAllowed("--sockets", "--cores", "--threads", "--pin", "--clear-pinning"),
			vmAllowed("--clear-pinning"), vmAllowed("--pin"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		sockets, err := vmRepeatedRequiredPositiveInt(options, "--sockets")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		cores, err := vmRepeatedRequiredPositiveInt(options, "--cores")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		threads, err := vmRepeatedRequiredPositiveInt(options, "--threads")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		clear, err := vmRepeatedBoolValue(options, "--clear-pinning")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		pins, err := consoleprotocol.ParseVMCPUPinning(options["--pin"], clear)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		request := libvirtServiceInterfaces.ModifyCPURequest{
			CPUSockets: sockets, CPUCores: cores, CPUThreads: threads, CPUPinning: pins,
		}
		vmsConfigMutation(ctx, rid, "cpu", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyCPU(rid, request)
		})

	case "memory":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--ram"), nil)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		ramText, ok := options["--ram"]
		if !ok {
			printVMConfigParseError(ctx, fmt.Errorf("--ram is required"))
			return
		}
		ram, err := consoleprotocol.ParseVMMemorySize(ramText)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, "memory", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyRAM(rid, ram)
		})

	case "vnc":
		options, err := parseVMConfigOptions(optionArgs,
			vmAllowed("--enabled", "--port", "--bind", "--resolution", "--wait", "--password-file", "--clear-password"),
			vmAllowed("--enabled", "--wait", "--clear-password"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		changes, err := buildConsoleVMVNCChanges(options)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, "vnc", jsonMode, func(*libvirtService.Service) error {
			return updateVMVNCConfiguration(ctx, rid, changes)
		})

	case "serial", "unknown-msr", "qga":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--enabled"), vmAllowed("--enabled"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		enabled, err := vmRequiredBoolOption(options, "--enabled")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, kind, jsonMode, func(service *libvirtService.Service) error {
			switch kind {
			case "serial":
				return service.ModifySerial(rid, enabled)
			case "unknown-msr":
				return service.ModifyIgnoreUMSRs(rid, enabled)
			default:
				return service.ModifyQemuGuestAgent(rid, enabled)
			}
		})

	case "pci":
		options, err := parseVMNamedOptionsRepeated(optionArgs,
			vmAllowed("--device-id", "--clear"), vmAllowed("--clear"), vmAllowed("--device-id"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		clear, err := vmRepeatedBoolValue(options, "--clear")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		deviceIDs, err := consoleprotocol.ParseVMPositiveIntList(options["--device-id"], clear, "device-id")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, "pci", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyPassthrough(rid, deviceIDs)
		})

	case "autostart":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--enabled", "--order"), vmAllowed("--enabled"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		enabled, err := vmRequiredBoolOption(options, "--enabled")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		order, err := vmRequiredNonNegativeIntOption(options, "--order")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, "autostart", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyBootOrder(rid, enabled, order)
		})

	case "clock":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--time-offset"), nil)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		offset := strings.ToLower(strings.TrimSpace(options["--time-offset"]))
		if offset != "utc" && offset != "localtime" {
			printVMConfigParseError(ctx, fmt.Errorf("--time-offset must be utc or localtime"))
			return
		}
		vmsConfigMutation(ctx, rid, "clock", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyClock(rid, offset)
		})

	case "shutdown":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--wait-seconds"), nil)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		wait, err := vmRequiredPositiveIntOption(options, "--wait-seconds")
		if err != nil || wait > 3600 {
			printVMConfigParseError(ctx, fmt.Errorf("--wait-seconds must be between 1 and 3600"))
			return
		}
		vmsConfigMutation(ctx, rid, "shutdown", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyShutdownWaitTime(rid, wait)
		})

	case "boot-rom":
		options, err := parseVMConfigOptions(optionArgs, vmAllowed("--boot-rom"), nil)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		bootROM := strings.ToLower(strings.TrimSpace(options["--boot-rom"]))
		if bootROM != "uefi" && bootROM != "uboot" && bootROM != "none" {
			printVMConfigParseError(ctx, fmt.Errorf("--boot-rom must be uefi, uboot, or none"))
			return
		}
		vmsConfigMutation(ctx, rid, "boot-rom", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyBootROM(rid, bootROM)
		})

	case "cloud-init":
		options, err := parseVMConfigOptions(optionArgs,
			vmAllowed("--data-file", "--metadata-file", "--network-config-file", "--no-network-config", "--clear"),
			vmAllowed("--no-network-config", "--clear"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		clear, err := vmBoolOptionValue(options, "--clear")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		noNetwork, err := vmBoolOptionValue(options, "--no-network-config")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		replacement, err := consoleprotocol.BuildVMCloudInitReplacement(
			options["--data-file"], options["--metadata-file"], options["--network-config-file"], clear, noNetwork,
		)
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		vmsConfigMutation(ctx, rid, "cloud-init", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyCloudInitData(rid, replacement.Data, replacement.Metadata, replacement.NetworkConfig)
		})

	case "bhyve-options":
		options, err := parseVMNamedOptionsRepeated(optionArgs,
			vmAllowed("--option", "--clear"), vmAllowed("--clear"), vmAllowed("--option"))
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		clear, err := vmRepeatedBoolValue(options, "--clear")
		if err != nil {
			printVMConfigParseError(ctx, err)
			return
		}
		extra := options["--option"]
		if clear && len(extra) > 0 {
			printVMConfigParseError(ctx, fmt.Errorf("--option and --clear cannot be used together"))
			return
		}
		if !clear && len(extra) == 0 {
			printVMConfigParseError(ctx, fmt.Errorf("specify one or more --option values or --clear"))
			return
		}
		if clear {
			extra = []string{}
		}
		vmsConfigMutation(ctx, rid, "bhyve-options", jsonMode, func(service *libvirtService.Service) error {
			return service.ModifyExtraBhyveOptions(rid, extra)
		})

	default:
		println(ctx, styledErrorf("Unknown vms config command: '%s'", kind))
	}
}

func handleVMAccess(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms access", []cmdHelp{
			{"vnc <rid>", "Show safe VNC connection information without the password"},
			{"serial <rid> [--baud <50-4000000>]", "Preflight and open local cu; the VM must be in a supported running state"},
		})
		return
	}
	if args[0] == "vnc" {
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: vms access vnc <rid>"))
			return
		}
		rid, err := parseVMRID(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
			return
		}
		vmsAccessVNC(ctx, rid, jsonMode)
		return
	}
	if args[0] != "serial" || len(args) < 2 {
		println(ctx, styledErrorf("Usage: vms access <vnc|serial> <rid> [--baud <rate>]"))
		return
	}
	rid, err := parseVMRID(args[1])
	if err != nil {
		println(ctx, styledErrorf("Invalid RID '%s'", args[1]))
		return
	}
	options, err := parseVMNamedOptions(args[2:], vmAllowed("--baud"), nil)
	if err != nil {
		println(ctx, styledErrorf("%v", err))
		return
	}
	vmsAccessSerial(ctx, rid, options["--baud"], jsonMode)
}

func buildConsoleVMCreateRequestFromArgs(args []string) (libvirtServiceInterfaces.CreateVMRequest, error) {
	allowed := vmAllowed(
		"--file", "--rid", "--name", "--description",
		"--cpu-sockets", "--cpu-cores", "--cpu-threads", "--ram",
		"--storage-pool", "--storage-type", "--storage-size", "--storage-emulation",
		"--iso", "--cloud-init-image", "--cloud-init-data-file", "--cloud-init-metadata-file",
		"--cloud-init-network-config-file", "--switch", "--network-emulation", "--boot-rom",
		"--vnc-enabled", "--vnc-port", "--vnc-bind", "--vnc-resolution", "--vnc-password-file",
		"--start-at-boot", "--time-offset",
	)
	options, err := parseVMNamedOptions(args, allowed, vmAllowed("--vnc-enabled", "--start-at-boot"))
	if err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	overrides := consoleprotocol.VMCreateOverrides{
		Name:                       vmStringOption(options, "--name"),
		Description:                vmStringOption(options, "--description"),
		RAM:                        vmStringOption(options, "--ram"),
		StoragePool:                vmStringOption(options, "--storage-pool"),
		StorageType:                vmStringOption(options, "--storage-type"),
		StorageSize:                vmStringOption(options, "--storage-size"),
		StorageEmulation:           vmStringOption(options, "--storage-emulation"),
		ISO:                        vmStringOption(options, "--iso"),
		CloudInitImage:             vmStringOption(options, "--cloud-init-image"),
		CloudInitDataFile:          vmStringOption(options, "--cloud-init-data-file"),
		CloudInitMetadataFile:      vmStringOption(options, "--cloud-init-metadata-file"),
		CloudInitNetworkConfigFile: vmStringOption(options, "--cloud-init-network-config-file"),
		Switch:                     vmStringOption(options, "--switch"),
		NetworkEmulation:           vmStringOption(options, "--network-emulation"),
		BootROM:                    vmStringOption(options, "--boot-rom"),
		VNCBind:                    vmStringOption(options, "--vnc-bind"),
		VNCResolution:              vmStringOption(options, "--vnc-resolution"),
		VNCPasswordFile:            vmStringOption(options, "--vnc-password-file"),
		TimeOffset:                 vmStringOption(options, "--time-offset"),
	}
	if ridText, exists := options["--rid"]; exists {
		rid, err := parseVMRID(ridText)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("Invalid RID '%s'", ridText)
		}
		overrides.RID = &rid
	}
	if overrides.CPUSockets, err = vmIntOption(options, "--cpu-sockets"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	if overrides.CPUCores, err = vmIntOption(options, "--cpu-cores"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	if overrides.CPUThreads, err = vmIntOption(options, "--cpu-threads"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	if overrides.VNCEnabled, err = vmBoolOption(options, "--vnc-enabled"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	if overrides.VNCPort, err = vmIntOption(options, "--vnc-port"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	if overrides.StartAtBoot, err = vmBoolOption(options, "--start-at-boot"); err != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, err
	}
	return consoleprotocol.BuildVMCreateRequest(options["--file"], overrides)
}

func vmAllowed(names ...string) map[string]bool {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	return allowed
}

func parseVMConfigOptions(args []string, allowed, boolean map[string]bool) (map[string]string, error) {
	return parseVMNamedOptions(args, allowed, boolean)
}

func printVMConfigParseError(ctx *Context, err error) {
	println(ctx, styledErrorf("%v", err))
}

func vmRepeatedRequiredPositiveInt(options map[string][]string, name string) (int, error) {
	values := options[name]
	if len(values) != 1 {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func vmRepeatedBoolValue(options map[string][]string, name string) (bool, error) {
	values := options[name]
	if len(values) == 0 {
		return false, nil
	}
	if len(values) != 1 {
		return false, fmt.Errorf("%s was specified more than once", name)
	}
	parsed, err := strconv.ParseBool(values[0])
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s", name)
	}
	return parsed, nil
}

func vmRequiredBoolOption(options map[string]string, name string) (bool, error) {
	value, exists := options[name]
	if !exists {
		return false, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s", name)
	}
	return parsed, nil
}

func vmRequiredPositiveIntOption(options map[string]string, name string) (int, error) {
	value, exists := options[name]
	if !exists {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func vmRequiredNonNegativeIntOption(options map[string]string, name string) (int, error) {
	value, exists := options[name]
	if !exists {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func buildConsoleVMVNCChanges(options map[string]string) (consoleprotocol.VMVNCChanges, error) {
	var err error
	input := consoleprotocol.VMVNCChangeInput{
		Bind:         vmStringOption(options, "--bind"),
		Resolution:   vmStringOption(options, "--resolution"),
		PasswordFile: options["--password-file"],
	}
	if input.Enabled, err = vmBoolOption(options, "--enabled"); err != nil {
		return consoleprotocol.VMVNCChanges{}, err
	}
	if input.Port, err = vmIntOption(options, "--port"); err != nil {
		return consoleprotocol.VMVNCChanges{}, err
	}
	if input.Wait, err = vmBoolOption(options, "--wait"); err != nil {
		return consoleprotocol.VMVNCChanges{}, err
	}
	if input.ClearPassword, err = vmBoolOptionValue(options, "--clear-password"); err != nil {
		return consoleprotocol.VMVNCChanges{}, err
	}
	return consoleprotocol.BuildVMVNCChanges(input)
}

func applyVMConfigMutation(
	ctx *Context,
	rid uint,
	configuration string,
	mutate func(*libvirtService.Service) error,
) (vmConfigMutationResult, error) {
	result := vmConfigMutationResult{RID: rid, Configuration: configuration}
	if err := validateVMRID(rid); err != nil {
		return result, err
	}
	if ctx == nil || ctx.VirtualMachine == nil {
		return result, fmt.Errorf("vm_service_unavailable")
	}
	err := mutate(ctx.VirtualMachine)
	if err != nil {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "no_changes_detected") {
			return result, nil
		}
		return result, fmt.Errorf("failed_to_update_vm_%s: %w", strings.ReplaceAll(configuration, "-", "_"), err)
	}
	result.Updated = true
	return result, nil
}

func printVMConfigMutation(ctx *Context, result vmConfigMutationResult, err error, jsonMode bool) {
	if err != nil {
		printOperationError(ctx, jsonMode, "Error updating VM configuration", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	if result.Updated {
		println(ctx, styledSuccessf("VM %d %s configuration updated.", result.RID, result.Configuration))
		return
	}
	println(ctx, styledSuccessf("VM %d %s configuration already matched.", result.RID, result.Configuration))
}

func vmsConfigMutation(
	ctx *Context,
	rid uint,
	configuration string,
	jsonMode bool,
	mutate func(*libvirtService.Service) error,
) {
	result, err := applyVMConfigMutation(ctx, rid, configuration, mutate)
	printVMConfigMutation(ctx, result, err, jsonMode)
}

func updateVMVNCConfiguration(ctx *Context, rid uint, changes consoleprotocol.VMVNCChanges) error {
	vm, err := getVM(ctx, rid)
	if err != nil {
		return err
	}
	request, err := mergeVMVNCConfiguration(*vm, changes)
	if err != nil {
		return err
	}
	return ctx.VirtualMachine.ModifyVNC(rid, request)
}

func mergeVMVNCConfiguration(
	vm vmModels.VM,
	changes consoleprotocol.VMVNCChanges,
) (libvirtServiceInterfaces.ModifyVNCRequest, error) {
	enabled := vm.VNCEnabled
	port := vm.VNCPort
	bind := vm.VNCBind
	resolution := vm.VNCResolution
	wait := vm.VNCWait
	password := vm.VNCPassword
	if changes.Enabled != nil {
		enabled = *changes.Enabled
	}
	if changes.Port != nil {
		port = *changes.Port
	}
	if changes.Bind != nil {
		bind = *changes.Bind
	}
	if changes.Resolution != nil {
		resolution = *changes.Resolution
	}
	if changes.Wait != nil {
		wait = *changes.Wait
	}
	if changes.Password != nil {
		password = *changes.Password
	}
	if enabled && (port < 1 || port > 65535) {
		return libvirtServiceInterfaces.ModifyVNCRequest{}, fmt.Errorf("vnc_port_required_when_enabling: provide --port between 1 and 65535")
	}
	if strings.TrimSpace(resolution) == "" {
		return libvirtServiceInterfaces.ModifyVNCRequest{}, fmt.Errorf("vnc_resolution_required: provide --resolution")
	}
	return libvirtServiceInterfaces.ModifyVNCRequest{
		VNCEnabled: &enabled, VNCPort: port, VNCBind: bind, VNCResolution: resolution,
		VNCPassword: password, VNCWait: &wait,
	}, nil
}

func getVMVNCAccessInfo(ctx *Context, rid uint) (consoleprotocol.VMVNCAccessInfo, error) {
	vm, err := getVM(ctx, rid)
	if err != nil {
		return consoleprotocol.VMVNCAccessInfo{}, err
	}
	return describeVMVNCAccess(*vm), nil
}

func describeVMVNCAccess(vm vmModels.VM) consoleprotocol.VMVNCAccessInfo {
	info := consoleprotocol.VMVNCAccessInfo{
		RID: vm.RID, Name: vm.Name, Enabled: vm.VNCEnabled,
		DomainState: vmDomainStateName(vm.State), BindAddress: libvirtService.NormalizeVNCBindAddress(vm.VNCBind),
		Port: vm.VNCPort, Resolution: vm.VNCResolution, Wait: vm.VNCWait,
		PasswordConfigured: vm.VNCPassword != "",
	}
	if !info.Enabled {
		info.UnavailableReason = "vnc_disabled"
		return info
	}
	if info.Port < 1 || info.Port > 65535 {
		info.UnavailableReason = "vnc_endpoint_not_configured"
		return info
	}
	dialAddress := libvirtService.NormalizeVNCBindAddressForDial(info.BindAddress)
	info.Endpoint = net.JoinHostPort(dialAddress, strconv.Itoa(info.Port))
	if vm.State != golibvirt.DomainRunning {
		info.UnavailableReason = "vm_not_running"
		return info
	}
	info.Available = true
	return info
}

func vmDomainStateName(state golibvirt.DomainState) string {
	switch state {
	case golibvirt.DomainRunning:
		return "running"
	case golibvirt.DomainBlocked:
		return "blocked"
	case golibvirt.DomainPaused:
		return "paused"
	case golibvirt.DomainShutdown:
		return "shutdown"
	case golibvirt.DomainShutoff:
		return "shut off"
	case golibvirt.DomainCrashed:
		return "crashed"
	case golibvirt.DomainPmsuspended:
		return "suspended"
	default:
		return "unknown"
	}
}

func formatVMVNCAccess(info consoleprotocol.VMVNCAccessInfo) string {
	lines := []string{
		styledKeyValue("RID:", strconv.FormatUint(uint64(info.RID), 10)),
		styledKeyValue("Name:", info.Name),
		styledKeyValue("Enabled:", strconv.FormatBool(info.Enabled)),
		styledKeyValue("Available:", strconv.FormatBool(info.Available)),
		styledKeyValue("Domain state:", info.DomainState),
		styledKeyValue("Bind address:", info.BindAddress),
		styledKeyValue("Port:", strconv.Itoa(info.Port)),
		styledKeyValue("Endpoint:", emptyDisplayValue(info.Endpoint)),
		styledKeyValue("Resolution:", emptyDisplayValue(info.Resolution)),
		styledKeyValue("Wait for client:", strconv.FormatBool(info.Wait)),
		styledKeyValue("Password configured:", strconv.FormatBool(info.PasswordConfigured)),
	}
	if info.UnavailableReason != "" {
		lines = append(lines, styledKeyValue("Unavailable reason:", info.UnavailableReason))
	}
	return strings.Join(lines, "\n")
}

func emptyDisplayValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func vmsAccessVNC(ctx *Context, rid uint, jsonMode bool) {
	info, err := getVMVNCAccessInfo(ctx, rid)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error inspecting VM VNC access", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(info))
		return
	}
	println(ctx, formatVMVNCAccess(info))
}

func redactVMSecrets(vm vmModels.VM) vmModels.VM {
	vm.VNCPassword = ""
	return vm
}

func redactVMListSecrets(vms []vmModels.VM) []vmModels.VM {
	redacted := make([]vmModels.VM, len(vms))
	for index := range vms {
		redacted[index] = redactVMSecrets(vms[index])
	}
	return redacted
}

func processVMConfigMutationResult(
	ctx *Context, rid uint, jsonMode bool, configuration string,
	mutate func(*libvirtService.Service) error,
) socketResponse {
	result, err := applyVMConfigMutation(ctx, rid, configuration, mutate)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	message := styledSuccessf("VM %d %s configuration updated.", rid, configuration)
	if !result.Updated {
		message = styledSuccessf("VM %d %s configuration already matched.", rid, configuration)
	}
	return operationSuccess(jsonMode, result, message)
}

func processVMConfigCPUSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigCPUPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_cpu_request: " + err.Error()}
	}
	if request.Request.CPUPinning == nil {
		return socketResponse{Error: "invalid_vm_config_cpu_request: cpuPinning must be an explicit array"}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "cpu", func(service *libvirtService.Service) error {
		return service.ModifyCPU(request.RID, request.Request)
	})
}

func processVMConfigMemorySocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigMemoryPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_memory_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "memory", func(service *libvirtService.Service) error {
		return service.ModifyRAM(request.RID, request.RAM)
	})
}

func processVMConfigVNCSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigVNCPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_vnc_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "vnc", func(service *libvirtService.Service) error {
		return updateVMVNCConfiguration(ctx, request.RID, request.Changes)
	})
}

func processVMConfigBoolSocketRequest(
	ctx *Context, payload json.RawMessage, configuration string,
	mutate func(*libvirtService.Service, uint, bool) error,
) socketResponse {
	var request consoleprotocol.VMConfigBoolPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_" + strings.ReplaceAll(configuration, "-", "_") + "_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, configuration, func(service *libvirtService.Service) error {
		return mutate(service, request.RID, request.Enabled)
	})
}

func processVMConfigPCISocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigPCIPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_pci_request: " + err.Error()}
	}
	if request.DeviceIDs == nil {
		return socketResponse{Error: "invalid_vm_config_pci_request: deviceIds must be an explicit array"}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "pci", func(service *libvirtService.Service) error {
		return service.ModifyPassthrough(request.RID, request.DeviceIDs)
	})
}

func processVMConfigAutostartSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigAutostartPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_autostart_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "autostart", func(service *libvirtService.Service) error {
		return service.ModifyBootOrder(request.RID, request.Enabled, request.Order)
	})
}

func processVMConfigClockSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigClockPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_clock_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "clock", func(service *libvirtService.Service) error {
		return service.ModifyClock(request.RID, request.TimeOffset)
	})
}

func processVMConfigShutdownSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigShutdownPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_shutdown_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "shutdown", func(service *libvirtService.Service) error {
		return service.ModifyShutdownWaitTime(request.RID, request.WaitSeconds)
	})
}

func processVMConfigBootROMSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigBootROMPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_boot_rom_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "boot-rom", func(service *libvirtService.Service) error {
		return service.ModifyBootROM(request.RID, request.BootROM)
	})
}

func processVMConfigCloudInitSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigCloudInitPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_cloud_init_request: " + err.Error()}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "cloud-init", func(service *libvirtService.Service) error {
		return service.ModifyCloudInitData(request.RID, request.Data, request.Metadata, request.NetworkConfig)
	})
}

func processVMConfigBhyveOptionsSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMConfigBhyveOptionsPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_config_bhyve_options_request: " + err.Error()}
	}
	if request.Options == nil {
		return socketResponse{Error: "invalid_vm_config_bhyve_options_request: options must be an explicit array"}
	}
	return processVMConfigMutationResult(ctx, request.RID, request.JSON, "bhyve-options", func(service *libvirtService.Service) error {
		return service.ModifyExtraBhyveOptions(request.RID, request.Options)
	})
}

func processVMAccessVNCSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.VMRIDPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_vm_access_vnc_request: " + err.Error()}
	}
	info, err := getVMVNCAccessInfo(ctx, request.RID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, info, formatVMVNCAccess(info))
}
