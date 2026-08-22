// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

var runLocalVMSerialConsole = func(launch consoleprotocol.VMSerialConsoleLaunch) error {
	command := exec.Command("cu", "-l", launch.DevicePath, "-s", launch.BaudRate)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	term := strings.TrimSpace(os.Getenv("TERM"))
	if term == "" {
		term = "xterm"
	}
	command.Env = append(os.Environ(), "TERM="+term)
	if err := command.Run(); err != nil {
		return fmt.Errorf("serial_console_session_failed: %w (the device may already be in use)", err)
	}
	return nil
}

func newVMSnapshotsCommand() *cli.Command {
	baseFlags := func() []cli.Flag {
		return []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output stable machine-readable JSON"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
		}
	}
	return &cli.Command{
		Name:  "snapshots",
		Usage: "Manage crash-consistent VM snapshots",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List snapshots for a VM", Flags: baseFlags(),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMSnapshotList,
						consoleprotocol.VMRIDPayload{RID: rid, JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
			{
				Name:        "create",
				Usage:       "Create a crash-consistent VM snapshot",
				Description: "The guest is not quiesced and roots on different pools are captured sequentially; the VM may remain running.",
				Flags: append(baseFlags(),
					&cli.StringFlag{Name: "name", Usage: "snapshot name (maximum 128 characters)", Required: true},
					&cli.StringFlag{Name: "description", Usage: "optional description (maximum 4096 characters)"},
				),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					name, description, err := consoleprotocol.ValidateVMSnapshotCreate(command.String("name"), command.String("description"))
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMSnapshotCreate,
						consoleprotocol.VMSnapshotCreatePayload{RID: rid, Name: name, Description: description, JSON: command.Bool("json")},
						command.Bool("json"))
				},
			},
			{
				Name:        "rollback",
				Usage:       "Roll a VM back to a snapshot",
				Description: "The VM is stopped and restarted when necessary. Newer Sylve or administrator-created ZFS snapshots are destroyed only with --destroy-newer.",
				Flags: append(baseFlags(),
					&cli.IntFlag{Name: "snapshot-id", Usage: "positive snapshot ID", Required: true},
					&cli.BoolFlag{Name: "destroy-newer", Usage: "explicitly allow destruction of snapshots newer than the target"},
				),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					snapshotID, err := commandPositiveUint(command, "snapshot-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMSnapshotRollback,
						consoleprotocol.VMSnapshotRollbackPayload{RID: rid, SnapshotID: snapshotID, DestroyNewer: command.Bool("destroy-newer"), JSON: command.Bool("json")},
						command.Bool("json"))
				},
			},
			{
				Name:        "delete",
				Usage:       "Delete one VM snapshot",
				Description: "Deletes the explicitly selected snapshot from every recorded ZFS root and preserves child lineage.",
				Flags:       append(baseFlags(), &cli.IntFlag{Name: "snapshot-id", Usage: "positive snapshot ID", Required: true}),
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					snapshotID, err := commandPositiveUint(command, "snapshot-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMSnapshotDelete,
						consoleprotocol.VMSnapshotDeletePayload{RID: rid, SnapshotID: snapshotID, JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
		},
	}
}

func newVMTemplatesCommand() *cli.Command {
	jsonFlag := func() cli.Flag { return &cli.BoolFlag{Name: "json", Usage: "output stable machine-readable JSON"} }
	return &cli.Command{
		Name: "templates", Usage: "Manage VM templates",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List templates and source storage mapping IDs", Flags: []cli.Flag{jsonFlag()},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationVMTemplateList,
						consoleprotocol.JSONPayload{JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
			{
				Name:        "get",
				Usage:       "Get a VM template by ID",
				Description: "Shows a template summary. Use --json for the complete configuration and mappings.",
				Flags:       []cli.Flag{jsonFlag(), &cli.IntFlag{Name: "template-id", Usage: "positive template ID", Required: true}},
				Action: func(ctx context.Context, command *cli.Command) error {
					templateID, err := commandPositiveUint(command, "template-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMTemplateGet,
						consoleprotocol.VMTemplateGetPayload{TemplateID: templateID, JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
			{
				Name:        "capture",
				Usage:       "Queue capture of a powered-off VM as a template",
				Description: "Runs existing storage/network preflight and retains the source VM. The source VM must be powered off.",
				Flags: []cli.Flag{
					jsonFlag(),
					&cli.IntFlag{Name: "rid", Usage: "source VM RID (1-9999)", Required: true},
					&cli.StringFlag{Name: "name", Usage: "unique template name (maximum 120 characters)", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandVMRID(command)
					if err != nil {
						return err
					}
					request, err := consoleprotocol.BuildVMTemplateConvertRequest(command.String("name"))
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMTemplateConvert,
						consoleprotocol.VMTemplateConvertPayload{RID: rid, Request: request, JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
			newVMTemplateCreateCommand(),
			{
				Name:        "delete",
				Usage:       "Delete a template and its managed template datasets",
				Description: "The explicitly selected template cannot be deleted while a create task is active.",
				Flags:       []cli.Flag{jsonFlag(), &cli.IntFlag{Name: "template-id", Usage: "positive template ID", Required: true}},
				Action: func(ctx context.Context, command *cli.Command) error {
					templateID, err := commandPositiveUint(command, "template-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMTemplateDelete,
						consoleprotocol.VMTemplateDeletePayload{TemplateID: templateID, JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
		},
	}
}

func newVMTemplateCreateCommand() *cli.Command {
	return &cli.Command{
		Name:                      "create",
		Usage:                     "Queue creation of one or more VMs from a template",
		Description:               "Mode single accepts --rid and optional --name. Mode multiple accepts --start-rid, --count (1-200), and optional --name-prefix. Repeat --storage-pool SOURCE_STORAGE_ID=POOL to override placement.",
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output stable machine-readable JSON"},
			&cli.IntFlag{Name: "template-id", Usage: "positive template ID", Required: true},
			&cli.StringFlag{Name: "mode", Usage: "creation mode: single or multiple", Value: "single"},
			&cli.IntFlag{Name: "rid", Usage: "target RID (1-9999) in single mode"},
			&cli.StringFlag{Name: "name", Usage: "target VM name in single mode"},
			&cli.IntFlag{Name: "start-rid", Usage: "first target RID (1-9999) in multiple mode"},
			&cli.IntFlag{Name: "count", Usage: "target count (1-200) in multiple mode"},
			&cli.StringFlag{Name: "name-prefix", Usage: "target name prefix in multiple mode"},
			&cli.StringSliceFlag{Name: "storage-pool", Usage: "storage mapping SOURCE_STORAGE_ID=POOL; repeat per override"},
			&cli.BoolFlag{Name: "rewrite-cloud-init-identity", Usage: "rewrite instance-id and local-hostname for each target"},
			&cli.StringFlag{Name: "cloud-init-prefix", Usage: "identity prefix; requires --rewrite-cloud-init-identity"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			templateID, err := commandPositiveUint(command, "template-id")
			if err != nil {
				return err
			}
			assignments, err := consoleprotocol.ParseVMTemplateStoragePoolAssignments(command.StringSlice("storage-pool"))
			if err != nil {
				return err
			}
			request := libvirtServiceInterfaces.CreateFromTemplateRequest{
				Mode: command.String("mode"), Name: command.String("name"), NamePrefix: command.String("name-prefix"),
				Count: command.Int("count"), StoragePools: assignments,
				RewriteCloudInitIdentity: command.Bool("rewrite-cloud-init-identity"), CloudInitPrefix: command.String("cloud-init-prefix"),
			}
			mode := strings.ToLower(strings.TrimSpace(command.String("mode")))
			if mode == "single" {
				for _, incompatible := range []string{"start-rid", "count", "name-prefix"} {
					if command.IsSet(incompatible) {
						return fmt.Errorf("--%s is incompatible with single mode", incompatible)
					}
				}
			}
			if mode == "multiple" {
				for _, incompatible := range []string{"rid", "name"} {
					if command.IsSet(incompatible) {
						return fmt.Errorf("--%s is incompatible with multiple mode", incompatible)
					}
				}
			}
			if command.IsSet("rid") {
				rid, err := commandVMRID(command)
				if err != nil {
					return err
				}
				request.RID = rid
			}
			if command.IsSet("start-rid") {
				value := command.Int("start-rid")
				if value < 1 || value > 9999 {
					return fmt.Errorf("--start-rid must be between 1 and 9999")
				}
				request.StartRID = uint(value)
			}
			request, err = consoleprotocol.ValidateVMTemplateCreateRequest(request)
			if err != nil {
				return err
			}
			return executeConsoleOperation(command, consoleprotocol.OperationVMTemplateCreate,
				consoleprotocol.VMTemplateCreatePayload{TemplateID: templateID, Request: request, JSON: command.Bool("json")}, command.Bool("json"))
		},
	}
}

func newVMAccessSerialCommand() *cli.Command {
	return &cli.Command{
		Name:        "serial",
		Usage:       "Open the local serial console after daemon preflight",
		Description: "Requires serial to be enabled, a supported running domain state, an owned replication lease, /dev/nmdm<RID>B, and a baud rate from 50-4000000. JSON mode reports readiness without opening cu.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "report readiness as JSON without opening the console"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID (1-9999)", Required: true},
			&cli.StringFlag{Name: "baud", Usage: "baud rate (50-4000000)", Value: "115200"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandVMRID(command)
			if err != nil {
				return err
			}
			return executeVMSerialConsoleOperation(command, consoleprotocol.VMAccessSerialPayload{
				RID: rid, BaudRate: command.String("baud"), JSON: command.Bool("json"),
			})
		},
	}
}

func executeVMSerialConsoleOperation(command *cli.Command, payload consoleprotocol.VMAccessSerialPayload) error {
	socketPath, err := consoleSocketPath(command.String("config"))
	if err != nil {
		printConsoleOperationError(payload.JSON, err)
		return err
	}
	response, err := consoleprotocol.ExecuteOperationResponse(socketPath, consoleprotocol.OperationVMAccessSerial, payload)
	if err != nil {
		printConsoleOperationError(payload.JSON, err)
		return err
	}
	fmt.Print(response.Output)
	if payload.JSON {
		return nil
	}
	if response.SerialConsole == nil {
		return fmt.Errorf("serial_console_launch_unavailable")
	}
	return runLocalVMSerialConsole(*response.SerialConsole)
}
