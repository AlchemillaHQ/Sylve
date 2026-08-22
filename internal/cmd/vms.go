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

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

func newVMActionCommand(action string) *cli.Command {
	usage := map[string]string{
		"start":    "Start a VM",
		"stop":     "Force-stop a VM",
		"shutdown": "Gracefully shut down a VM",
		"reboot":   "Reboot a VM",
	}[action]
	if usage == "" {
		usage = action + " a VM"
	}
	return &cli.Command{
		Name:  action,
		Usage: usage,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMAction, consoleprotocol.VMActionPayload{
				RID:    rid,
				Action: action,
				JSON:   command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newVMsCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	return &cli.Command{
		Name:  "vms",
		Usage: "Manage virtual machines",
		Commands: []*cli.Command{
			newVMCreateCommand(),
			{
				Name:  "list",
				Usage: "List all VMs",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationVMList, consoleprotocol.JSONPayload{
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get VM details by RID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMGet, consoleprotocol.VMRIDPayload{
						RID:  rid,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			newVMActionCommand("start"),
			newVMActionCommand("stop"),
			newVMActionCommand("shutdown"),
			newVMActionCommand("reboot"),
			newVMConfigCommand(),
			newVMAccessCommand(),
			newVMStorageCommand(),
			newVMSnapshotsCommand(),
			newVMTemplatesCommand(),
			{
				Name:        "delete",
				Usage:       "Delete a VM; disks are retained unless explicitly selected",
				Description: "A running VM is force-stopped before its registration is removed. Disks and MAC objects are retained unless their explicit deletion flags are supplied; use --dry-run to preview the same removal plan.",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.BoolFlag{Name: "delete-macs", Usage: "delete VM MAC objects"},
					&cli.BoolFlag{Name: "delete-raw-disks", Usage: "delete managed raw-disk datasets"},
					&cli.BoolFlag{Name: "delete-volumes", Usage: "delete managed ZVOL datasets"},
					&cli.BoolFlag{Name: "dry-run", Usage: "preview registration, MAC, and storage deletion without changing anything"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMDelete, consoleprotocol.VMDeletePayload{
						RID:            rid,
						DeleteMACs:     command.Bool("delete-macs"),
						DeleteRawDisks: command.Bool("delete-raw-disks"),
						DeleteVolumes:  command.Bool("delete-volumes"),
						DryRun:         command.Bool("dry-run"),
						JSON:           command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "purge",
				Usage: "Purge an orphaned VM registration without deleting its disks",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.BoolFlag{Name: "delete-macs", Usage: "delete VM MAC objects"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMPurge, consoleprotocol.VMPurgePayload{
						RID:        rid,
						DeleteMACs: command.Bool("delete-macs"),
						JSON:       command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "networks",
				Usage: "List networks for a VM",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworks, consoleprotocol.VMRIDPayload{
						RID:  rid,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "addnet",
				Usage: "Attach a network to a powered-off VM",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.StringFlag{Name: "switch", Usage: "Network switch name", Required: true},
					&cli.StringFlag{Name: "emulation", Usage: "Network emulation: virtio or e1000", Required: true},
					&cli.IntFlag{Name: "mac-id", Usage: "Existing MAC object ID"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					macID, err := commandOptionalPositiveUint(command, "mac-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworkAttach, consoleprotocol.VMNetworkAttachPayload{
						RID: rid,
						Request: libvirtServiceInterfaces.NetworkAttachRequest{
							SwitchName: command.String("switch"),
							Emulation:  command.String("emulation"),
							MacID:      macID,
						},
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			newVMEditNetworkCommand(),
			{
				Name:  "rmnet",
				Usage: "Remove a network from a powered-off VM",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.IntFlag{Name: "net-id", Usage: "VM network ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					networkID, err := commandPositiveUint(command, "net-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworkDetach, consoleprotocol.VMNetworkDetachPayload{
						RID:       rid,
						NetworkID: networkID,
						JSON:      command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "qga",
				Usage: "Manage QEMU guest agent commands",
				Commands: []*cli.Command{
					{
						Name:  "send",
						Usage: "Send a command to a VM QEMU guest agent",
						Flags: []cli.Flag{
							jsonFlag,
							&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
							&cli.StringFlag{Name: "command", Usage: "QEMU guest agent command", Required: true},
						},
						Action: func(ctx context.Context, command *cli.Command) error {
							rid, err := commandVMRID(command)
							if err != nil {
								return err
							}
							return executeConsoleOperation(command, consoleprotocol.OperationVMQGASend, consoleprotocol.VMQGASendPayload{
								RID:     rid,
								Command: command.String("command"),
								JSON:    command.Bool("json"),
							}, command.Bool("json"))
						},
					},
				},
			},
		},
	}
}
