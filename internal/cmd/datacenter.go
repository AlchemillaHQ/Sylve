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
	"github.com/urfave/cli/v3"
)

func datacenterJSONFlag() cli.Flag {
	return &cli.BoolFlag{Name: "json", Usage: "output in JSON format"}
}

func newDatacenterCommand() *cli.Command {
	return &cli.Command{
		Name:  "datacenter",
		Usage: "Manage replicated datacenter state",
		Commands: []*cli.Command{
			newDatacenterNotesCommand(),
			newDatacenterClusterCommand(),
		},
	}
}

func newDatacenterNotesCommand() *cli.Command {
	return &cli.Command{
		Name:  "notes",
		Usage: "Manage replicated datacenter notes",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List datacenter notes",
				Flags: []cli.Flag{datacenterJSONFlag()},
				Action: func(_ context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationDatacenterNoteList, consoleprotocol.DatacenterNoteListPayload{
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get a datacenter note",
				Flags: []cli.Flag{
					datacenterJSONFlag(),
					&cli.IntFlag{Name: "id", Usage: "note ID", Required: true},
				},
				Action: func(_ context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationDatacenterNoteGet, consoleprotocol.DatacenterNoteGetPayload{
						ID: id, JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "add",
				Usage: "Add a datacenter note",
				Flags: append(datacenterNoteTextFlags(false), datacenterJSONFlag()),
				Action: func(_ context.Context, command *cli.Command) error {
					return executeDatacenterNoteMutation(command, consoleprotocol.OperationDatacenterNoteAdd, 0)
				},
			},
			{
				Name:  "update",
				Usage: "Update a datacenter note",
				Flags: append(datacenterNoteTextFlags(true), datacenterJSONFlag()),
				Action: func(_ context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeDatacenterNoteMutation(command, consoleprotocol.OperationDatacenterNoteUpdate, id)
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a datacenter note",
				Flags: []cli.Flag{
					datacenterJSONFlag(),
					&cli.IntFlag{Name: "id", Usage: "note ID", Required: true},
				},
				Action: func(_ context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeDatacenterNoteMutation(command, consoleprotocol.OperationDatacenterNoteDelete, id)
				},
			},
		},
	}
}

func newDatacenterClusterCommand() *cli.Command {
	return &cli.Command{
		Name:  "cluster",
		Usage: "Inspect and recover cluster membership",
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "Show local cluster and consensus status",
				Flags: []cli.Flag{datacenterJSONFlag()},
				Action: func(_ context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationDatacenterClusterStatus,
						consoleprotocol.DatacenterClusterReadPayload{JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
			{
				Name:  "members",
				Usage: "List authoritative Raft members",
				Flags: []cli.Flag{datacenterJSONFlag()},
				Action: func(_ context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationDatacenterClusterMembers,
						consoleprotocol.DatacenterClusterReadPayload{JSON: command.Bool("json")}, command.Bool("json"))
				},
			},
		},
	}
}

func datacenterNoteTextFlags(withID bool) []cli.Flag {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "title", Usage: "note title", Required: true},
		&cli.StringFlag{Name: "content", Usage: "note content", Required: true},
	}
	if withID {
		flags = append([]cli.Flag{&cli.IntFlag{Name: "id", Usage: "note ID", Required: true}}, flags...)
	}
	return flags
}

func executeDatacenterNoteMutation(command *cli.Command, operation string, id uint) error {
	payload := consoleprotocol.DatacenterNoteMutationPayload{
		ID: id, Title: command.String("title"), Content: command.String("content"), JSON: command.Bool("json"),
	}
	return executeConsoleOperation(command, operation, payload, payload.JSON)
}
