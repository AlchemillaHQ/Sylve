package cmd

import (
	"context"
	"fmt"

	sylvecli "github.com/alchemillahq/sylve/internal/cli"
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
					cmdStr := "notes list"
					if cmd.Bool("json") {
						cmdStr += " --json"
					}
					output, err := sylvecli.ExecuteCommand(cmdStr)
					if err != nil {
						return err
					}
					fmt.Print(output)
					return nil
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
					cmdStr := fmt.Sprintf("notes add %s %s", cmd.String("title"), cmd.String("content"))
					if cmd.Bool("json") {
						cmdStr += " --json"
					}
					output, err := sylvecli.ExecuteCommand(cmdStr)
					if err != nil {
						return err
					}
					fmt.Print(output)
					return nil
				},
			},
			{
				Name:  "get",
				Usage: "Get a note by ID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "id",
						Usage:    "Note ID",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("notes get %d", cmd.Int("id"))
					if cmd.Bool("json") {
						cmdStr += " --json"
					}
					output, err := sylvecli.ExecuteCommand(cmdStr)
					if err != nil {
						return err
					}
					fmt.Print(output)
					return nil
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a note by ID",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "id",
						Usage:    "Note ID",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("notes delete %d", cmd.Int("id"))
					if cmd.Bool("json") {
						cmdStr += " --json"
					}
					output, err := sylvecli.ExecuteCommand(cmdStr)
					if err != nil {
						return err
					}
					fmt.Print(output)
					return nil
				},
			},
		},
	}
}
