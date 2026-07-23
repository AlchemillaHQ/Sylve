package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/urfave/cli/v3"
)

type jailCreateInput struct {
	File      string
	Overrides consoleprotocol.JailCreateOverrides
}

func buildJailCreateRequest(input jailCreateInput) (jailServiceInterfaces.CreateJailRequest, error) {
	return consoleprotocol.BuildJailCreateRequest(input.File, input.Overrides)
}

func jailCreateInputFromCommand(command *cli.Command) (jailCreateInput, error) {
	input := jailCreateInput{File: command.String("file")}
	input.Overrides.Name = jailCreateStringOverride(command, "name")
	input.Overrides.Pool = jailCreateStringOverride(command, "pool")
	input.Overrides.Base = jailCreateStringOverride(command, "base")
	input.Overrides.Bootstrap = jailCreateStringOverride(command, "bootstrap")
	input.Overrides.Switch = jailCreateStringOverride(command, "switch")
	input.Overrides.Type = jailCreateStringOverride(command, "type")
	if command.IsSet("ctid") {
		ctid, err := commandJailCTID(command, "ctid")
		if err != nil {
			return jailCreateInput{}, err
		}
		input.Overrides.CTID = &ctid
	}
	return input, nil
}

func jailCreateStringOverride(command *cli.Command, name string) *string {
	if !command.IsSet(name) {
		return nil
	}
	value := command.String(name)
	return &value
}

func commandJailCTID(command *cli.Command, name string) (uint, error) {
	ctid, err := commandPositiveUint(command, name)
	if err != nil {
		return 0, err
	}
	if ctid > 9999 {
		return 0, fmt.Errorf("--%s must be between 1 and 9999", name)
	}
	return ctid, nil
}

func newJailActionCommand(action string) *cli.Command {
	return &cli.Command{
		Name:  action,
		Usage: action + " a jail (or all jails with --all)",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "output in JSON format"},
			&cli.IntFlag{Name: "ctid", Usage: "Jail CTID (1-9999)"},
			&cli.BoolFlag{Name: "all", Usage: "apply to all jails"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			all := command.Bool("all")
			if all && command.IsSet("ctid") {
				return fmt.Errorf("--all and --ctid are mutually exclusive")
			}
			if !all && !command.IsSet("ctid") {
				return fmt.Errorf("specify --ctid <id> or --all")
			}
			ctid := uint(0)
			if !all {
				var err error
				ctid, err = commandJailCTID(command, "ctid")
				if err != nil {
					return err
				}
			}
			return executeConsoleOperation(command, consoleprotocol.OperationJailAction, consoleprotocol.JailActionPayload{
				CTID:   ctid,
				Action: action,
				All:    all,
				JSON:   command.Bool("json"),
			}, command.Bool("json"))
		},
	}
}

func newJailsCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	ctidFlag := &cli.IntFlag{
		Name:     "ctid",
		Usage:    "Jail CTID (1-9999)",
		Required: true,
	}

	createCmd := &cli.Command{
		Name:        "create",
		Usage:       "Create a jail from flags or a JSON request file",
		Description: "Use --file with a complete CreateJailRequest JSON document, or provide all core flags. Explicit flags override fields from --file.",
		Flags: []cli.Flag{
			jsonFlag,
			&cli.IntFlag{
				Name:  "ctid",
				Usage: "Jail CTID (1-9999)",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Jail name",
			},
			&cli.StringFlag{
				Name:  "pool",
				Usage: "ZFS pool",
			},
			&cli.StringFlag{
				Name:  "base",
				Usage: "Base download UUID (mutually exclusive with --bootstrap)",
			},
			&cli.StringFlag{
				Name:  "bootstrap",
				Usage: "Bootstrap name (mutually exclusive with --base)",
			},
			&cli.StringFlag{
				Name:  "switch",
				Usage: "Network switch name, none, or inherit",
			},
			&cli.StringFlag{
				Name:  "type",
				Usage: "Jail type: freebsd or linux",
			},
			&cli.StringFlag{
				Name:  "file",
				Usage: "Path to a complete CreateJailRequest JSON file",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input, err := jailCreateInputFromCommand(cmd)
			if err != nil {
				return err
			}
			request, err := buildJailCreateRequest(input)
			if err != nil {
				return err
			}

			return executeConsoleOperation(cmd, consoleprotocol.OperationJailCreate, consoleprotocol.JailCreatePayload{
				Request: request,
				JSON:    cmd.Bool("json"),
			}, cmd.Bool("json"))
		},
	}

	bootstrapCmd := &cli.Command{
		Name:  "bootstrap",
		Usage: "Manage jail base bootstraps",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List supported bootstraps and their install status",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{
						Name:     "pool",
						Usage:    "ZFS pool",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return executeConsoleOperation(cmd, consoleprotocol.OperationBootstrapList, consoleprotocol.BootstrapListPayload{
						Pool: cmd.String("pool"),
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "create",
				Usage: "Start an asynchronous bootstrap installation",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{
						Name:     "pool",
						Usage:    "ZFS pool",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "version",
						Usage:    "FreeBSD version in major.minor form (for example, 15.0)",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "type",
						Usage:    "Bootstrap type: base or minimal",
						Required: true,
					},
					&cli.BoolFlag{
						Name:  "wait",
						Usage: "wait for bootstrap completion",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					major, minor, err := consoleprotocol.ParseBootstrapVersion(cmd.String("version"))
					if err != nil {
						return err
					}
					bootstrapType := strings.ToLower(strings.TrimSpace(cmd.String("type")))

					return executeConsoleOperation(cmd, consoleprotocol.OperationBootstrapCreate, consoleprotocol.BootstrapCreatePayload{
						Request: jailServiceInterfaces.BootstrapRequest{
							Pool:  cmd.String("pool"),
							Major: major,
							Minor: minor,
							Type:  bootstrapType,
						},
						Wait: cmd.Bool("wait"),
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a bootstrap by name",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{
						Name:     "pool",
						Usage:    "ZFS pool",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "name",
						Usage:    "Bootstrap name (for example, 15-0-Base)",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return executeConsoleOperation(cmd, consoleprotocol.OperationBootstrapDelete, consoleprotocol.BootstrapDeletePayload{
						Pool: cmd.String("pool"),
						Name: cmd.String("name"),
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
		},
	}

	consoleCmd := &cli.Command{
		Name:  "console",
		Usage: "Open a local interactive shell into a jail (not available in the REPL)",
		Flags: []cli.Flag{ctidFlag},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ctid, err := commandJailCTID(cmd, "ctid")
			if err != nil {
				return err
			}

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

			hash := utils.HashIntToNLetters(int(ctid), 5)
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

	startCmd := newJailActionCommand("start")
	stopCmd := newJailActionCommand("stop")
	restartCmd := newJailActionCommand("restart")

	return &cli.Command{
		Name:  "jails",
		Usage: "Manage jails",
		Commands: []*cli.Command{
			createCmd,
			bootstrapCmd,
			{
				Name:  "list",
				Usage: "List all jails",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return executeConsoleOperation(cmd, consoleprotocol.OperationJailList, consoleprotocol.JailListPayload{
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "get",
				Usage: "Get jail details by CTID",
				Flags: []cli.Flag{jsonFlag, ctidFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					ctid, err := commandJailCTID(cmd, "ctid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationJailGet, consoleprotocol.JailGetPayload{
						CTID: ctid,
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			startCmd,
			stopCmd,
			restartCmd,
			{
				Name:  "delete",
				Usage: "Delete a jail by CTID",
				Flags: []cli.Flag{
					jsonFlag,
					ctidFlag,
					&cli.BoolFlag{Name: "purge", Usage: "destroy the jail root dataset"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					ctid, err := commandJailCTID(cmd, "ctid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationJailDelete, consoleprotocol.JailDeletePayload{
						CTID:  ctid,
						Purge: cmd.Bool("purge"),
						JSON:  cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "networks",
				Usage: "List networks for a jail",
				Flags: []cli.Flag{jsonFlag, ctidFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					ctid, err := commandJailCTID(cmd, "ctid")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationJailNetworks, consoleprotocol.JailNetworksPayload{
						CTID: ctid,
						JSON: cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			{
				Name:  "rmnet",
				Usage: "Remove a network from a jail",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{
						Name:     "ctid",
						Usage:    "Jail CTID (1-9999)",
						Required: true,
					},
					&cli.IntFlag{
						Name:     "net-id",
						Usage:    "Network ID",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					ctid, err := commandJailCTID(cmd, "ctid")
					if err != nil {
						return err
					}
					networkID, err := commandPositiveUint(cmd, "net-id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(cmd, consoleprotocol.OperationJailRemoveNetwork, consoleprotocol.JailRemoveNetworkPayload{
						CTID:      ctid,
						NetworkID: networkID,
						JSON:      cmd.Bool("json"),
					}, cmd.Bool("json"))
				},
			},
			consoleCmd,
		},
	}
}
