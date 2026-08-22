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

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

func commandVMRID(command *cli.Command) (uint, error) {
	rid, err := commandPositiveUint(command, "rid")
	if err != nil {
		return 0, err
	}
	if rid > 9999 {
		return 0, fmt.Errorf("--rid must be between 1 and 9999")
	}
	return rid, nil
}

func commandOptionalString(command *cli.Command, name string) *string {
	if !command.IsSet(name) {
		return nil
	}
	value := command.String(name)
	return &value
}

func commandOptionalInt(command *cli.Command, name string) *int {
	if !command.IsSet(name) {
		return nil
	}
	value := command.Int(name)
	return &value
}

func commandOptionalBool(command *cli.Command, name string) *bool {
	if !command.IsSet(name) {
		return nil
	}
	value := command.Bool(name)
	return &value
}

func newVMStorageCommand() *cli.Command {
	return &cli.Command{
		Name:  "storage",
		Usage: "Manage VM storage devices",
		Commands: []*cli.Command{
			{
				Name:        "list",
				Usage:       "List storage attached to a VM",
				Description: "Shows managed, external, and retained backing. The VM may be running.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMStorageList, consoleprotocol.VMRIDPayload{
						RID: rid, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "attach",
				Usage: "Attach raw, ZVOL, ISO/image, or filesystem storage to a powered-off VM",
				Description: "Types: raw, zvol, image (or its iso alias), filesystem. Emulation: virtio-blk, ahci-hd, ahci-cd, nvme; filesystem uses virtio-9p. " +
					"Use --size for new raw/ZVOL storage, --raw-path or --dataset-guid to import, --image-uuid for an image, and --dataset-guid with --filesystem-target for filesystem storage. " +
					"A same-pool ZVOL import may move the source into Sylve's managed namespace. Requires the VM to be powered off.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.StringFlag{Name: "type", Usage: "storage type: raw, zvol, image, iso, or filesystem", Required: true},
					&cli.StringFlag{Name: "name", Usage: "storage attachment name", Required: true},
					&cli.StringFlag{Name: "pool", Usage: "target ZFS pool for raw or ZVOL storage"},
					&cli.StringFlag{Name: "size", Usage: "new raw or ZVOL size, for example 10GiB"},
					&cli.StringFlag{Name: "raw-path", Usage: "absolute raw disk file to import"},
					&cli.StringFlag{Name: "dataset-guid", Usage: "ZVOL or filesystem dataset GUID"},
					&cli.StringFlag{Name: "image-uuid", Usage: "download UUID for image or ISO storage"},
					&cli.StringFlag{Name: "emulation", Usage: "virtio-blk, ahci-hd, ahci-cd, nvme, or virtio-9p"},
					&cli.StringFlag{Name: "filesystem-target", Usage: "9P target name for filesystem storage"},
					&cli.BoolFlag{Name: "read-only", Usage: "attach filesystem storage read-only"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, request, err := vmStorageAttachRequestFromCommand(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMStorageAttach, consoleprotocol.VMStorageAttachPayload{
						RID: rid, Request: request, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:        "edit",
				Usage:       "Edit an attached storage device on a powered-off VM",
				Description: "Supported changes: name, size, emulation, boot order, enabled state, filesystem target, and read-only state. Requires the VM to be powered off.",
				Flags: append(vmStorageUpdateFlags(),
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
				),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, storageID, request, err := vmStorageUpdateRequestFromCommand(command, false)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMStorageUpdate, consoleprotocol.VMStorageUpdatePayload{
						RID: rid, StorageID: storageID, Request: request, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:        "resize",
				Usage:       "Grow a raw disk or ZVOL attached to a powered-off VM",
				Description: "Only growth of raw and ZVOL storage is supported. Images and filesystem storage cannot be resized. Requires the VM to be powered off.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.IntFlag{Name: "storage-id", Usage: "VM storage attachment ID", Required: true},
					&cli.StringFlag{Name: "size", Usage: "new total size, for example 20GiB", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, storageID, request, err := vmStorageUpdateRequestFromCommand(command, true)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMStorageUpdate, consoleprotocol.VMStorageUpdatePayload{
						RID: rid, StorageID: storageID, Request: request, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:        "detach",
				Usage:       "Detach storage from a powered-off VM without destroying its backing",
				Description: "The attachment metadata is removed, but its dataset, volume, image, or file is retained. Requires the VM to be powered off.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.IntFlag{Name: "storage-id", Usage: "VM storage attachment ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					storageID, err := commandPositiveUint(command, "storage-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMStorageDetach, consoleprotocol.VMStorageDetachPayload{
						RID: rid, StorageID: storageID, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
		},
	}
}

func vmStorageUpdateFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
		&cli.IntFlag{Name: "storage-id", Usage: "VM storage attachment ID", Required: true},
		&cli.StringFlag{Name: "name", Usage: "new attachment name"},
		&cli.StringFlag{Name: "size", Usage: "new total size, for example 20GiB"},
		&cli.StringFlag{Name: "emulation", Usage: "virtio-blk, virtio-9p, ahci-hd, ahci-cd, or nvme"},
		&cli.IntFlag{Name: "boot-order", Usage: "non-negative device boot order"},
		&cli.BoolFlag{Name: "enabled", Usage: "set attachment enabled state; accepts true or false"},
		&cli.StringFlag{Name: "filesystem-target", Usage: "new 9P target name"},
		&cli.BoolFlag{Name: "read-only", Usage: "set filesystem read-only state; accepts true or false"},
	}
}

func vmStorageAttachRequestFromCommand(command *cli.Command) (uint, libvirtServiceInterfaces.StorageAttachRequest, error) {
	rid, err := commandVMRID(command)
	if err != nil {
		return 0, libvirtServiceInterfaces.StorageAttachRequest{}, err
	}
	request, err := consoleprotocol.BuildVMStorageAttachRequest(consoleprotocol.VMStorageAttachInput{
		RID:              rid,
		Name:             command.String("name"),
		StorageType:      command.String("type"),
		Pool:             command.String("pool"),
		Size:             command.String("size"),
		RawPath:          command.String("raw-path"),
		DatasetGUID:      command.String("dataset-guid"),
		ImageUUID:        command.String("image-uuid"),
		Emulation:        command.String("emulation"),
		FilesystemTarget: command.String("filesystem-target"),
		ReadOnly:         commandOptionalBool(command, "read-only"),
	})
	return rid, request, err
}

func vmStorageUpdateRequestFromCommand(command *cli.Command, resizeOnly bool) (uint, uint, libvirtServiceInterfaces.StorageUpdateRequest, error) {
	rid, err := commandVMRID(command)
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	storageID, err := commandPositiveUint(command, "storage-id")
	if err != nil {
		return 0, 0, libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	input := consoleprotocol.VMStorageUpdateInput{
		RID: rid, StorageID: storageID, Size: commandOptionalString(command, "size"),
	}
	if !resizeOnly {
		input.Name = commandOptionalString(command, "name")
		input.Emulation = commandOptionalString(command, "emulation")
		input.BootOrder = commandOptionalInt(command, "boot-order")
		input.Enabled = commandOptionalBool(command, "enabled")
		input.FilesystemTarget = commandOptionalString(command, "filesystem-target")
		input.ReadOnly = commandOptionalBool(command, "read-only")
	}
	request, err := consoleprotocol.BuildVMStorageUpdateRequest(input)
	return rid, storageID, request, err
}

func newVMNetworkCommand() *cli.Command {
	return &cli.Command{
		Name:  "network",
		Usage: "Manage VM network attachments",
		Commands: []*cli.Command{
			{
				Name:        "list",
				Usage:       "List network attachments for a VM",
				Description: "The VM may be running.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworks, consoleprotocol.VMRIDPayload{
						RID: rid, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:        "attach",
				Usage:       "Attach a network to a powered-off VM",
				Description: "Emulation must be virtio or e1000. Omit --mac-id to generate a MAC object. Requires the VM to be powered off.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.StringFlag{Name: "switch", Usage: "network switch name", Required: true},
					&cli.StringFlag{Name: "emulation", Usage: "network emulation: virtio or e1000", Required: true},
					&cli.IntFlag{Name: "mac-id", Usage: "existing positive MAC object ID"},
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
							SwitchName: command.String("switch"), Emulation: command.String("emulation"), MacID: macID,
						},
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			newVMEditNetworkCommand(),
			{
				Name:        "detach",
				Usage:       "Detach a network from a powered-off VM",
				Description: "The network attachment is removed and its MAC object is retained. Requires the VM to be powered off.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
					&cli.IntFlag{Name: "network-id", Usage: "VM network attachment ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					networkID, err := commandPositiveUint(command, "network-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworkDetach, consoleprotocol.VMNetworkDetachPayload{
						RID: rid, NetworkID: networkID, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
		},
	}
}

func newVMEditNetworkCommand() *cli.Command {
	return &cli.Command{
		Name:        "edit",
		Usage:       "Edit a network attachment on a powered-off VM",
		Description: "Change switch, emulation (virtio or e1000), MAC object, or enabled state. Use --generate-mac to allocate a new MAC object. Requires the VM to be powered off.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
			&cli.IntFlag{Name: "network-id", Usage: "VM network attachment ID", Required: true},
			&cli.StringFlag{Name: "switch", Usage: "network switch name"},
			&cli.StringFlag{Name: "emulation", Usage: "network emulation: virtio or e1000"},
			&cli.IntFlag{Name: "mac-id", Usage: "existing positive MAC object ID"},
			&cli.BoolFlag{Name: "generate-mac", Usage: "create and assign a new MAC object"},
			&cli.BoolFlag{Name: "enabled", Usage: "set attachment enabled state; accepts true or false"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			networkID, err := commandPositiveUint(command, "network-id")
			if err != nil {
				return err
			}
			macID, err := commandOptionalPositiveUint(command, "mac-id")
			if err != nil {
				return err
			}
			request, err := consoleprotocol.BuildVMNetworkUpdateRequest(consoleprotocol.VMNetworkUpdateInput{
				RID: rid, NetworkID: networkID,
				SwitchName:  commandOptionalString(command, "switch"),
				Emulation:   commandOptionalString(command, "emulation"),
				MacID:       macID,
				GenerateMAC: command.Bool("generate-mac"),
				Enabled:     commandOptionalBool(command, "enabled"),
			})
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMNetworkUpdate, consoleprotocol.VMNetworkUpdatePayload{
				RID: rid, NetworkID: networkID, Request: request, JSON: command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}
