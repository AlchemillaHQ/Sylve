package cmd

import (
	"context"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func newNotesCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	return &cli.Command{
		Name:  "notes",
		Usage: "Manage notes",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List all notes",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return executeConsoleOperation(cmd, consoleprotocol.OperationNoteList, consoleprotocol.NoteListPayload{
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "add",
				Usage: "Add a new note",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{
						Name:     "title",
						Usage:    "Note title",
						Aliases:  []string{"t"},
						Required: true,
					},
					&cli.StringFlag{
						Name:     "content",
						Usage:    "Note content",
						Aliases:  []string{"c"},
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return executeConsoleOperation(cmd, consoleprotocol.OperationNoteAdd, consoleprotocol.NoteAddPayload{
						Title:   cmd.String("title"),
						Content: cmd.String("content"),
						JSON:    cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get a note by ID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "id",
						Usage:    "Note ID (greater than zero)",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, err := commandPositiveUint(cmd, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationNoteGet, consoleprotocol.NoteGetPayload{
						ID:   id,
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a note by ID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "id",
						Usage:    "Note ID (greater than zero)",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id, err := commandPositiveUint(cmd, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationNoteDelete, consoleprotocol.NoteDeletePayload{
						ID:   id,
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
		},
	}
}
