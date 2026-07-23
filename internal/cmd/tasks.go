package cmd

import (
	"context"
	"fmt"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func taskFilterFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
		&cli.StringFlag{Name: "guest-type", Usage: "filter by guest type"},
		&cli.IntFlag{Name: "guest-id", Usage: "filter by guest ID"},
	}
}

func commandTaskFilters(command *cli.Command) (string, uint, error) {
	guestID, err := commandOptionalPositiveUint(command, "guest-id")
	if err != nil {
		return "", 0, err
	}
	if guestID == nil {
		return command.String("guest-type"), 0, nil
	}
	return command.String("guest-type"), *guestID, nil
}

func newTasksCommand() *cli.Command {
	recentFlags := append(taskFilterFlags(), &cli.IntFlag{
		Name:  "limit",
		Value: 50,
		Usage: "maximum number of tasks to return (1-200)",
	})

	return &cli.Command{
		Name:  "tasks",
		Usage: "Inspect lifecycle tasks",
		Commands: []*cli.Command{
			{
				Name:  "active",
				Usage: "List queued and running lifecycle tasks",
				Flags: taskFilterFlags(),
				Action: func(ctx context.Context, command *cli.Command) error {
					guestType, guestID, err := commandTaskFilters(command)
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationTaskListActive, consoleprotocol.TaskActivePayload{
						GuestType: guestType,
						GuestID:   guestID,
						JSON:      command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "recent",
				Usage: "List recent lifecycle tasks",
				Flags: recentFlags,
				Action: func(ctx context.Context, command *cli.Command) error {
					guestType, guestID, err := commandTaskFilters(command)
					if err != nil {
						return err
					}
					limit := command.Int("limit")
					if limit < 1 || limit > 200 {
						return fmt.Errorf("--limit must be between 1 and 200")
					}
					return executeConsoleOperation(command, consoleprotocol.OperationTaskListRecent, consoleprotocol.TaskRecentPayload{
						GuestType: guestType,
						GuestID:   guestID,
						Limit:     limit,
						JSON:      command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get a lifecycle task by ID",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
					&cli.IntFlag{Name: "id", Usage: "lifecycle task ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					taskID, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationTaskGet, consoleprotocol.TaskGetPayload{
						TaskID: taskID,
						JSON:   command.Bool("json"),
					}, command.Bool("json"))
				},
			},
		},
	}
}
