package cmd

import (
	"context"
	"fmt"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func newObjectsCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	objectRequestFlags := []cli.Flag{
		&cli.StringFlag{Name: "name", Usage: "Object name", Required: true},
		&cli.StringFlag{Name: "type", Usage: "Object type: host, network, port, country, list, mac, fqdn, or duid", Required: true},
		&cli.StringSliceFlag{Name: "value", Usage: "Object value; repeat for multiple values", Required: true},
	}
	objectEditFlags := []cli.Flag{
		&cli.StringFlag{Name: "name", Usage: "Replacement object name"},
		&cli.StringFlag{Name: "type", Usage: "Replacement object type"},
		&cli.StringSliceFlag{Name: "value", Usage: "Replacement object value; repeat for multiple values"},
	}

	return &cli.Command{
		Name:  "objects",
		Usage: "Manage network objects",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List network objects",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "type", Usage: "Optional object type filter"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationObjectList, consoleprotocol.ObjectListPayload{
						Type: command.String("type"),
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "create",
				Usage: "Create a network object",
				Flags: append([]cli.Flag{jsonFlag}, objectRequestFlags...),
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationObjectCreate, consoleprotocol.ObjectCreatePayload{
						Request: networkObjectRequestFromCommand(command),
						JSON:    command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "edit",
				Usage: "Patch a network object; --value replaces all values",
				Flags: append([]cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "id", Usage: "Object ID", Required: true},
				}, objectEditFlags...),
				Action: func(ctx context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					request, err := networkObjectEditRequestFromCommand(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationObjectEdit, consoleprotocol.ObjectEditPayload{
						ID:      id,
						Request: request,
						JSON:    command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a network object",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "id", Usage: "Object ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationObjectDelete, consoleprotocol.ObjectDeletePayload{
						ID:   id,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
		},
	}
}

func networkObjectRequestFromCommand(command *cli.Command) consoleprotocol.NetworkObjectRequest {
	return consoleprotocol.NetworkObjectRequest{
		Name:   command.String("name"),
		Type:   command.String("type"),
		Values: command.StringSlice("value"),
	}
}

func networkObjectEditRequestFromCommand(command *cli.Command) (consoleprotocol.NetworkObjectEditRequest, error) {
	request := consoleprotocol.NetworkObjectEditRequest{}
	if command.IsSet("name") {
		value := command.String("name")
		request.Name = &value
	}
	if command.IsSet("type") {
		value := command.String("type")
		request.Type = &value
	}
	if command.IsSet("value") {
		values := append([]string(nil), command.StringSlice("value")...)
		request.Values = &values
	}
	if request.Name == nil && request.Type == nil && request.Values == nil {
		return consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("specify --name, --type, or --value")
	}
	return request, nil
}
