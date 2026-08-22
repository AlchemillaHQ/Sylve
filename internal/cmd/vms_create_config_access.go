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
	"fmt"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

func newVMCreateCommand() *cli.Command {
	return &cli.Command{
		Name:        "create",
		Usage:       "Create a VM from common flags or a complete JSON request",
		Description: "Without --file, --rid and --name are required; CPU defaults to 1/1/1, RAM to 1GiB, storage and networking to none, VNC and autostart to disabled, and clock to UTC. With --file, explicitly supplied flags override matching JSON fields. Storage types: none, raw, zvol. Network emulation: virtio or e1000. Time offset: utc or localtime. Input files are read on the Sylve host; absolute paths are recommended.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
			&cli.StringFlag{Name: "file", Usage: "optional complete strict CreateVMRequest JSON file"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)"},
			&cli.StringFlag{Name: "name", Usage: "VM name"},
			&cli.StringFlag{Name: "description", Usage: "VM description"},
			&cli.IntFlag{Name: "cpu-sockets", Usage: "positive CPU socket count"},
			&cli.IntFlag{Name: "cpu-cores", Usage: "positive cores per socket"},
			&cli.IntFlag{Name: "cpu-threads", Usage: "positive threads per core"},
			&cli.StringFlag{Name: "ram", Usage: "RAM size, for example 1GiB"},
			&cli.StringFlag{Name: "storage-pool", Usage: "ZFS pool for initial raw or ZVOL storage"},
			&cli.StringFlag{Name: "storage-type", Usage: "initial storage type: none, raw, or zvol"},
			&cli.StringFlag{Name: "storage-size", Usage: "initial storage size, for example 20GiB"},
			&cli.StringFlag{Name: "storage-emulation", Usage: "virtio-blk, ahci-hd, or nvme"},
			&cli.StringFlag{Name: "iso", Usage: "ISO or disk-image download UUID"},
			&cli.StringFlag{Name: "cloud-init-image", Usage: "cloud-init-capable image UUID; mutually exclusive with --iso"},
			&cli.StringFlag{Name: "cloud-init-data-file", Usage: "cloud-init user-data file"},
			&cli.StringFlag{Name: "cloud-init-metadata-file", Usage: "cloud-init metadata file"},
			&cli.StringFlag{Name: "cloud-init-network-config-file", Usage: "optional cloud-init network configuration file"},
			&cli.StringFlag{Name: "switch", Usage: "network switch name or none"},
			&cli.StringFlag{Name: "network-emulation", Usage: "network emulation: virtio or e1000"},
			&cli.StringFlag{Name: "boot-rom", Usage: "boot ROM: uefi, uboot, or none (architecture dependent)"},
			&cli.BoolFlag{Name: "vnc-enabled", Usage: "enable or disable VNC; accepts true or false"},
			&cli.IntFlag{Name: "vnc-port", Usage: "VNC TCP port (1-65535)"},
			&cli.StringFlag{Name: "vnc-bind", Usage: "VNC bind IP address"},
			&cli.StringFlag{Name: "vnc-resolution", Usage: "VNC resolution, for example 1024x768"},
			&cli.StringFlag{Name: "vnc-password-file", Usage: "read the VNC password from a file"},
			&cli.BoolFlag{Name: "start-at-boot", Usage: "start VM at host boot; accepts true or false"},
			&cli.StringFlag{Name: "time-offset", Usage: "guest clock offset: utc or localtime"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			overrides, err := vmCreateOverridesFromCommand(command)
			if err != nil {
				return err
			}
			request, err := consoleprotocol.BuildVMCreateRequest(command.String("file"), overrides)
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMCreate, consoleprotocol.VMCreatePayload{
				Request: request, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func vmCreateOverridesFromCommand(command *cli.Command) (consoleprotocol.VMCreateOverrides, error) {
	overrides := consoleprotocol.VMCreateOverrides{
		Name:                       commandOptionalString(command, "name"),
		Description:                commandOptionalString(command, "description"),
		CPUSockets:                 commandOptionalInt(command, "cpu-sockets"),
		CPUCores:                   commandOptionalInt(command, "cpu-cores"),
		CPUThreads:                 commandOptionalInt(command, "cpu-threads"),
		RAM:                        commandOptionalString(command, "ram"),
		StoragePool:                commandOptionalString(command, "storage-pool"),
		StorageType:                commandOptionalString(command, "storage-type"),
		StorageSize:                commandOptionalString(command, "storage-size"),
		StorageEmulation:           commandOptionalString(command, "storage-emulation"),
		ISO:                        commandOptionalString(command, "iso"),
		CloudInitImage:             commandOptionalString(command, "cloud-init-image"),
		CloudInitDataFile:          commandOptionalString(command, "cloud-init-data-file"),
		CloudInitMetadataFile:      commandOptionalString(command, "cloud-init-metadata-file"),
		CloudInitNetworkConfigFile: commandOptionalString(command, "cloud-init-network-config-file"),
		Switch:                     commandOptionalString(command, "switch"),
		NetworkEmulation:           commandOptionalString(command, "network-emulation"),
		BootROM:                    commandOptionalString(command, "boot-rom"),
		VNCEnabled:                 commandOptionalBool(command, "vnc-enabled"),
		VNCPort:                    commandOptionalInt(command, "vnc-port"),
		VNCBind:                    commandOptionalString(command, "vnc-bind"),
		VNCResolution:              commandOptionalString(command, "vnc-resolution"),
		VNCPasswordFile:            commandOptionalString(command, "vnc-password-file"),
		StartAtBoot:                commandOptionalBool(command, "start-at-boot"),
		TimeOffset:                 commandOptionalString(command, "time-offset"),
	}
	if command.IsSet("rid") {
		rid, err := commandVMRID(command)
		if err != nil {
			return consoleprotocol.VMCreateOverrides{}, err
		}
		overrides.RID = &rid
	}
	return overrides, nil
}

func newVMConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Manage VM configuration",
		Commands: []*cli.Command{
			newVMConfigCPUCommand(),
			newVMConfigMemoryCommand(),
			newVMConfigVNCCommand(),
			newVMConfigBoolCommand("serial", consoleprotocol.OperationVMConfigSerial, "serial console"),
			newVMConfigPCICommand(),
			newVMConfigAutostartCommand(),
			newVMConfigClockCommand(),
			newVMConfigShutdownCommand(),
			newVMConfigBootROMCommand(),
			newVMConfigCloudInitCommand(),
			newVMConfigBhyveOptionsCommand(),
			newVMConfigBoolCommand("unknown-msr", consoleprotocol.OperationVMConfigUnknownMSR, "unknown MSR handling"),
			newVMConfigBoolCommand("qga", consoleprotocol.OperationVMConfigQGA, "QEMU Guest Agent"),
		},
	}
}

func vmConfigBaseFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
		&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
	}
}

func newVMConfigCPUCommand() *cli.Command {
	return &cli.Command{
		Name:                      "cpu",
		Usage:                     "Replace CPU topology and pinning for a powered-off VM",
		Description:               "Requires the VM to be powered off. Supply every topology field and either repeat --pin socket:core,core or use --clear-pinning; pinning is never cleared implicitly.",
		DisableSliceFlagSeparator: true,
		Flags: append(vmConfigBaseFlags(),
			&cli.IntFlag{Name: "sockets", Usage: "positive CPU socket count", Required: true},
			&cli.IntFlag{Name: "cores", Usage: "positive cores per socket", Required: true},
			&cli.IntFlag{Name: "threads", Usage: "positive threads per core", Required: true},
			&cli.StringSliceFlag{Name: "pin", Usage: "host pin in socket:core,core form; repeat per socket"},
			&cli.BoolFlag{Name: "clear-pinning", Usage: "explicitly clear CPU pinning"},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			pins, err := consoleprotocol.ParseVMCPUPinning(command.StringSlice("pin"), command.Bool("clear-pinning"))
			if err != nil {
				return err
			}
			request := libvirtServiceInterfaces.ModifyCPURequest{
				CPUSockets: command.Int("sockets"), CPUCores: command.Int("cores"),
				CPUThreads: command.Int("threads"), CPUPinning: pins,
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigCPU, consoleprotocol.VMConfigCPUPayload{
				RID: rid, Request: request, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigMemoryCommand() *cli.Command {
	return &cli.Command{
		Name:        "memory",
		Usage:       "Set RAM for a powered-off VM",
		Description: "RAM accepts human-readable sizes such as 2GiB. Requires the VM to be powered off.",
		Flags:       append(vmConfigBaseFlags(), &cli.StringFlag{Name: "ram", Usage: "RAM size", Required: true}),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			ram, err := consoleprotocol.ParseVMMemorySize(command.String("ram"))
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigMemory, consoleprotocol.VMConfigMemoryPayload{
				RID: rid, RAM: ram, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigVNCCommand() *cli.Command {
	return &cli.Command{
		Name:        "vnc",
		Usage:       "Edit VNC settings for a powered-off VM",
		Description: "Omitted settings, including the password, are preserved. Use --password-file to replace the password or --clear-password to remove it. Enabling VNC requires a configured port and resolution. Requires the VM to be powered off.",
		Flags: append(vmConfigBaseFlags(),
			&cli.BoolFlag{Name: "enabled", Usage: "enable or disable VNC; accepts true or false"},
			&cli.IntFlag{Name: "port", Usage: "VNC TCP port (1-65535)"},
			&cli.StringFlag{Name: "bind", Usage: "VNC bind IP address"},
			&cli.StringFlag{Name: "resolution", Usage: "resolution such as 1024x768"},
			&cli.BoolFlag{Name: "wait", Usage: "wait for a VNC client before boot; accepts true or false"},
			&cli.StringFlag{Name: "password-file", Usage: "read replacement password from a file"},
			&cli.BoolFlag{Name: "clear-password", Usage: "explicitly clear VNC authentication"},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			changes, err := consoleprotocol.BuildVMVNCChanges(consoleprotocol.VMVNCChangeInput{
				Enabled: commandOptionalBool(command, "enabled"), Port: commandOptionalInt(command, "port"),
				Bind: commandOptionalString(command, "bind"), Resolution: commandOptionalString(command, "resolution"),
				Wait: commandOptionalBool(command, "wait"), PasswordFile: command.String("password-file"),
				ClearPassword: command.Bool("clear-password"),
			})
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigVNC, consoleprotocol.VMConfigVNCPayload{
				RID: rid, Changes: changes, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigBoolCommand(name, operation, label string) *cli.Command {
	return &cli.Command{
		Name: name, Usage: "Enable or disable " + label, Description: "Requires the VM to be powered off.",
		Flags: append(vmConfigBaseFlags(), &cli.BoolFlag{Name: "enabled", Usage: "enabled state: true or false", Required: true}),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, operation, consoleprotocol.VMConfigBoolPayload{
				RID: rid, Enabled: command.Bool("enabled"), JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigPCICommand() *cli.Command {
	return &cli.Command{
		Name:        "pci",
		Usage:       "Replace PCI passthrough assignments for a powered-off VM",
		Description: "Repeat --device-id to provide the complete assignment, or use --clear. Requires the VM to be powered off.",
		Flags: append(vmConfigBaseFlags(),
			&cli.StringSliceFlag{Name: "device-id", Usage: "positive passthrough record ID; repeat for multiple devices"},
			&cli.BoolFlag{Name: "clear", Usage: "explicitly clear all PCI assignments"},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			deviceIDs, err := consoleprotocol.ParseVMPositiveIntList(command.StringSlice("device-id"), command.Bool("clear"), "device-id")
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigPCI, consoleprotocol.VMConfigPCIPayload{
				RID: rid, DeviceIDs: deviceIDs, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigAutostartCommand() *cli.Command {
	return &cli.Command{
		Name:        "autostart",
		Usage:       "Set start-at-boot and start order",
		Description: "Both values are required because the service updates them together. The VM may be running.",
		Flags: append(vmConfigBaseFlags(),
			&cli.BoolFlag{Name: "enabled", Usage: "start at boot: true or false", Required: true},
			&cli.IntFlag{Name: "order", Usage: "non-negative start order", Required: true},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			if command.Int("order") < 0 {
				return fmt.Errorf("--order must be zero or greater")
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigAutostart, consoleprotocol.VMConfigAutostartPayload{
				RID: rid, Enabled: command.Bool("enabled"), Order: command.Int("order"), JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigClockCommand() *cli.Command {
	return &cli.Command{
		Name:        "clock",
		Usage:       "Set guest clock offset for a powered-off VM",
		Description: "Accepted values: utc or localtime. Requires the VM to be powered off.",
		Flags:       append(vmConfigBaseFlags(), &cli.StringFlag{Name: "time-offset", Usage: "utc or localtime", Required: true}),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			offset := strings.ToLower(strings.TrimSpace(command.String("time-offset")))
			if offset != "utc" && offset != "localtime" {
				return fmt.Errorf("--time-offset must be utc or localtime")
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigClock, consoleprotocol.VMConfigClockPayload{
				RID: rid, TimeOffset: offset, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigShutdownCommand() *cli.Command {
	return &cli.Command{
		Name:        "shutdown",
		Usage:       "Set graceful-shutdown wait time",
		Description: "Accepted range: 1-3600 seconds. The VM may be running.",
		Flags:       append(vmConfigBaseFlags(), &cli.IntFlag{Name: "wait-seconds", Usage: "shutdown wait in seconds (1-3600)", Required: true}),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			wait := command.Int("wait-seconds")
			if wait < 1 || wait > 3600 {
				return fmt.Errorf("--wait-seconds must be between 1 and 3600")
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigShutdown, consoleprotocol.VMConfigShutdownPayload{
				RID: rid, WaitSeconds: wait, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigBootROMCommand() *cli.Command {
	return &cli.Command{
		Name:        "boot-rom",
		Usage:       "Set boot ROM for a powered-off VM",
		Description: "Accepted values: uefi, uboot, or none. Availability depends on host architecture. Requires the VM to be powered off.",
		Flags:       append(vmConfigBaseFlags(), &cli.StringFlag{Name: "boot-rom", Usage: "uefi, uboot, or none", Required: true}),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			bootROM := strings.ToLower(strings.TrimSpace(command.String("boot-rom")))
			if bootROM != "uefi" && bootROM != "uboot" && bootROM != "none" {
				return fmt.Errorf("--boot-rom must be uefi, uboot, or none")
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigBootROM, consoleprotocol.VMConfigBootROMPayload{
				RID: rid, BootROM: bootROM, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigCloudInitCommand() *cli.Command {
	return &cli.Command{
		Name:        "cloud-init",
		Usage:       "Replace or clear cloud-init configuration for a powered-off VM",
		Description: "Replacement is complete: provide data and metadata files plus exactly one of --network-config-file or --no-network-config. Use --clear alone to remove all cloud-init configuration. Requires the VM to be powered off.",
		Flags: append(vmConfigBaseFlags(),
			&cli.StringFlag{Name: "data-file", Usage: "cloud-init user-data file"},
			&cli.StringFlag{Name: "metadata-file", Usage: "cloud-init metadata file"},
			&cli.StringFlag{Name: "network-config-file", Usage: "cloud-init network configuration file"},
			&cli.BoolFlag{Name: "no-network-config", Usage: "explicitly replace network configuration with empty content"},
			&cli.BoolFlag{Name: "clear", Usage: "explicitly clear all cloud-init configuration"},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			replacement, err := consoleprotocol.BuildVMCloudInitReplacement(
				command.String("data-file"), command.String("metadata-file"), command.String("network-config-file"),
				command.Bool("clear"), command.Bool("no-network-config"),
			)
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigCloudInit, consoleprotocol.VMConfigCloudInitPayload{
				RID: rid, Data: replacement.Data, Metadata: replacement.Metadata,
				NetworkConfig: replacement.NetworkConfig, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMConfigBhyveOptionsCommand() *cli.Command {
	return &cli.Command{
		Name:                      "bhyve-options",
		Usage:                     "Replace extra bhyve options for a powered-off VM",
		Description:               "Repeat --option to provide the complete option list, or use --clear. Requires the VM to be powered off.",
		DisableSliceFlagSeparator: true,
		Flags: append(vmConfigBaseFlags(),
			&cli.StringSliceFlag{Name: "option", Usage: "complete bhyve option; repeat for multiple options"},
			&cli.BoolFlag{Name: "clear", Usage: "explicitly clear all extra bhyve options"},
		),
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			options := command.StringSlice("option")
			if command.Bool("clear") && len(options) > 0 {
				return fmt.Errorf("--option and --clear cannot be used together")
			}
			if !command.Bool("clear") && len(options) == 0 {
				return fmt.Errorf("specify one or more --option values or --clear")
			}
			if command.Bool("clear") {
				options = []string{}
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMConfigBhyveOptions, consoleprotocol.VMConfigBhyveOptionsPayload{
				RID: rid, Options: options, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMAccessCommand() *cli.Command {
	return &cli.Command{
		Name:  "access",
		Usage: "Inspect VM console access",
		Commands: []*cli.Command{
			{
				Name:        "vnc",
				Usage:       "Show safe VNC connection information",
				Description: "Reports configuration and availability without returning the VNC password.",
				Flags:       vmConfigBaseFlags(),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMAccessVNC, consoleprotocol.VMRIDPayload{
						RID: rid, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			newVMAccessSerialCommand(),
		},
	}
}
