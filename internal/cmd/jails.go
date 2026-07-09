package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	sylvecli "github.com/alchemillahq/sylve/internal/cli"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/urfave/cli/v3"
)

func newJailsCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	ctidFlag := &cli.IntFlag{
		Name:     "ctid",
		Usage:    "Jail CTID",
		Required: true,
	}

	ctidOptFlag := &cli.IntFlag{
		Name:  "ctid",
		Usage: "Jail CTID",
	}

	allFlag := &cli.BoolFlag{
		Name:  "all",
		Usage: "apply to all jails",
	}

	consoleCmd := &cli.Command{
		Name:  "console",
		Usage: "Open an interactive shell into a jail",
		Flags: []cli.Flag{ctidFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ctid := cmd.Int("ctid")

			jlsOut, err := exec.Command("/usr/sbin/jls", "--libxo", "json", "jid", "name").Output()
			if err != nil {
				return fmt.Errorf("could not list jails: %w", err)
			}

			var jlsData struct {
				JailInformation struct {
					Jail []struct {
						JID  any    `json:"jid"`
						Name string `json:"name"`
					} `json:"jail"`
				} `json:"jail-information"`
			}
			if err := json.Unmarshal(jlsOut, &jlsData); err != nil {
				return fmt.Errorf("could not parse jls output: %w", err)
			}

			hash := utils.HashIntToNLetters(ctid, 5)
			var jid int
			found := false
			for _, j := range jlsData.JailInformation.Jail {
				if j.Name == hash {
					switch v := j.JID.(type) {
					case float64:
						jid = int(v)
					case string:
						jid, err = strconv.Atoi(strings.TrimSpace(v))
						if err != nil {
							continue
						}
					default:
						continue
					}
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("jail with CTID %d is not running", ctid)
			}

			c := exec.Command("jexec", strconv.Itoa(jid), "su", "-l", "root")
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}

	startCmd := &cli.Command{
		Name:  "start",
		Usage: "Start a jail (or all jails with --all)",
		Flags: []cli.Flag{jsonFlag, ctidOptFlag, allFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			all := cmd.Bool("all")
			ctid := cmd.Int("ctid")
			if all && cmd.IsSet("ctid") {
				return fmt.Errorf("--all and --ctid are mutually exclusive")
			}
			if !all && !cmd.IsSet("ctid") {
				return fmt.Errorf("specify --ctid <id> or --all")
			}
			cmdStr := fmt.Sprintf("jails start %d", ctid)
			if all {
				cmdStr = "jails start all"
			}
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
	}

	stopCmd := &cli.Command{
		Name:  "stop",
		Usage: "Stop a jail (or all jails with --all)",
		Flags: []cli.Flag{jsonFlag, ctidOptFlag, allFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			all := cmd.Bool("all")
			ctid := cmd.Int("ctid")
			if all && cmd.IsSet("ctid") {
				return fmt.Errorf("--all and --ctid are mutually exclusive")
			}
			if !all && !cmd.IsSet("ctid") {
				return fmt.Errorf("specify --ctid <id> or --all")
			}
			cmdStr := fmt.Sprintf("jails stop %d", ctid)
			if all {
				cmdStr = "jails stop all"
			}
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
	}

	restartCmd := &cli.Command{
		Name:  "restart",
		Usage: "Restart a jail (or all jails with --all)",
		Flags: []cli.Flag{jsonFlag, ctidOptFlag, allFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			all := cmd.Bool("all")
			ctid := cmd.Int("ctid")
			if all && cmd.IsSet("ctid") {
				return fmt.Errorf("--all and --ctid are mutually exclusive")
			}
			if !all && !cmd.IsSet("ctid") {
				return fmt.Errorf("specify --ctid <id> or --all")
			}
			cmdStr := fmt.Sprintf("jails restart %d", ctid)
			if all {
				cmdStr = "jails restart all"
			}
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
	}

	return &cli.Command{
		Name:  "jails",
		Usage: "Manage jails",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List all jails",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := "jails list"
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
				Usage: "Get jail details by CTID",
				Flags: []cli.Flag{jsonFlag, ctidFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("jails get %d", cmd.Int("ctid"))
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
			startCmd,
			stopCmd,
			restartCmd,
			{
				Name:  "delete",
				Usage: "Delete a jail by CTID",
				Flags: []cli.Flag{jsonFlag, ctidFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("jails delete %d", cmd.Int("ctid"))
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
				Name:  "networks",
				Usage: "List networks for a jail",
				Flags: []cli.Flag{jsonFlag, ctidFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("jails networks %d", cmd.Int("ctid"))
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
				Name:  "rmnet",
				Usage: "Remove a network from a jail",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "ctid",
						Usage:    "Jail CTID",
						Required: true,
					},
					&cli.IntFlag{
						Name:     "net-id",
						Usage:    "Network ID",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cmdStr := fmt.Sprintf("jails rmnet %d %d", cmd.Int("ctid"), cmd.Int("net-id"))
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
			consoleCmd,
		},
	}
}
