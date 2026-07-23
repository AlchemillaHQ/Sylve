package cmd

import (
	"context"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/urfave/cli/v3"
)

func newVMActionCommand(action string) *cli.Command {
	return &cli.Command{
		Name:  action,
		Usage: action + " a VM",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
			&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			rid, err := commandPositiveUint(command, "rid")
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
			{
				Name:        "create",
				Usage:       "Create a VM from a JSON request file",
				Description: "Use --file with a complete CreateVMRequest JSON document.",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "file", Usage: "Path to a complete CreateVMRequest JSON file", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					request, err := consoleprotocol.LoadVMCreateRequest(command.String("file"))
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMCreate, consoleprotocol.VMCreatePayload{
						Request: request,
						JSON:    command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "list",
				Usage: "List all VMs",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationVMList, consoleprotocol.VMListPayload{
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get VM details by RID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMGet, consoleprotocol.VMGetPayload{
						RID:  rid,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			newVMActionCommand("start"),
			newVMActionCommand("stop"),
			newVMActionCommand("shutdown"),
			newVMActionCommand("reboot"),
			{
				Name:  "delete",
				Usage: "Delete a VM; disks are retained unless explicitly selected",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
					&cli.BoolFlag{Name: "delete-macs", Usage: "delete VM MAC objects"},
					&cli.BoolFlag{Name: "delete-raw-disks", Usage: "delete managed raw-disk datasets"},
					&cli.BoolFlag{Name: "delete-volumes", Usage: "delete managed ZVOL datasets"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMDelete, consoleprotocol.VMDeletePayload{
						RID:            rid,
						DeleteMACs:     command.Bool("delete-macs"),
						DeleteRawDisks: command.Bool("delete-raw-disks"),
						DeleteVolumes:  command.Bool("delete-volumes"),
						JSON:           command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "purge",
				Usage: "Purge an orphaned VM registration without deleting its disks",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
					&cli.BoolFlag{Name: "delete-macs", Usage: "delete VM MAC objects"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
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
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworks, consoleprotocol.VMNetworksPayload{
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
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
					&cli.StringFlag{Name: "switch", Usage: "Network switch name", Required: true},
					&cli.StringFlag{Name: "emulation", Usage: "Network emulation: virtio or e1000", Required: true},
					&cli.IntFlag{Name: "mac-id", Usage: "Existing MAC object ID"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
					if err != nil {
						return err
					}
					macID, err := commandOptionalPositiveUint(command, "mac-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationVMNetworkAttach, consoleprotocol.VMNetworkAttachPayload{
						Request: libvirtServiceInterfaces.NetworkAttachRequest{
							RID:        rid,
							SwitchName: command.String("switch"),
							Emulation:  command.String("emulation"),
							MacId:      macID,
						},
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "rmnet",
				Usage: "Remove a network from a powered-off VM",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
					&cli.IntFlag{Name: "net-id", Usage: "VM network ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					rid, err := commandPositiveUint(command, "rid")
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
							&cli.IntFlag{Name: "rid", Usage: "VM RID", Required: true},
							&cli.StringFlag{Name: "command", Usage: "QEMU guest agent command", Required: true},
						},
						Action: func(ctx context.Context, command *cli.Command) error {
							rid, err := commandPositiveUint(command, "rid")
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
