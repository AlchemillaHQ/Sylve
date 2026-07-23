package cmd

import (
	"context"
	"fmt"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func newSwitchesCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	return &cli.Command{
		Name:  "switches",
		Usage: "Manage network switches",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List standard and manual switches",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationSwitchList, consoleprotocol.SwitchListPayload{
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "create",
				Usage: "Create a standard or manual switch",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "type", Usage: "Switch type: standard or manual", Required: true},
					&cli.StringFlag{Name: "name", Usage: "Switch name", Required: true},
					&cli.StringFlag{Name: "bridge", Usage: "Existing bridge interface for a manual switch"},
					&cli.IntFlag{Name: "mtu", Usage: "Standard switch MTU"},
					&cli.IntFlag{Name: "vlan", Usage: "Standard switch VLAN"},
					&cli.IntFlag{Name: "network4", Usage: "IPv4 network object ID"},
					&cli.IntFlag{Name: "gateway4", Usage: "IPv4 gateway object ID"},
					&cli.IntFlag{Name: "network6", Usage: "IPv6 network object ID"},
					&cli.IntFlag{Name: "gateway6", Usage: "IPv6 gateway object ID"},
					&cli.StringFlag{Name: "network4-manual", Usage: "Manual IPv4 CIDR"},
					&cli.StringFlag{Name: "gateway4-manual", Usage: "Manual IPv4 gateway"},
					&cli.StringFlag{Name: "network6-manual", Usage: "Manual IPv6 CIDR"},
					&cli.StringFlag{Name: "gateway6-manual", Usage: "Manual IPv6 gateway"},
					&cli.StringFlag{Name: "ports", Usage: "Comma-separated physical ports"},
					&cli.BoolFlag{Name: "private", Usage: "Create a private standard switch"},
					&cli.BoolFlag{Name: "dhcp", Usage: "Enable DHCP on a standard switch"},
					&cli.BoolFlag{Name: "disable-ipv6", Usage: "Disable IPv6 on a standard switch"},
					&cli.BoolFlag{Name: "slaac", Usage: "Enable SLAAC on a standard switch"},
					&cli.BoolFlag{Name: "default-route", Usage: "Install the standard switch default route"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					request, err := buildSwitchCreatePayload(command)
					if err != nil {
						return err
					}
					request.JSON = command.Bool("json")
					return executeConsoleOperation(command, consoleprotocol.OperationSwitchCreate, request, command.Bool("json"))
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a standard or manual switch",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "type", Usage: "Switch type: standard or manual", Required: true},
					&cli.IntFlag{Name: "id", Usage: "Switch ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationSwitchDelete, consoleprotocol.SwitchDeletePayload{
						Type: command.String("type"),
						ID:   id,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "edit",
				Usage: "Edit a standard or manual switch",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "type", Usage: "Switch type: standard or manual", Required: true},
					&cli.IntFlag{Name: "id", Usage: "Switch ID", Required: true},
					&cli.StringFlag{Name: "name", Usage: "Manual switch name"},
					&cli.StringFlag{Name: "bridge", Usage: "Manual bridge interface"},
					&cli.IntFlag{Name: "mtu", Usage: "Standard switch MTU"},
					&cli.IntFlag{Name: "vlan", Usage: "Standard switch VLAN"},
					&cli.IntFlag{Name: "network4", Usage: "IPv4 network object ID (0 clears)"},
					&cli.IntFlag{Name: "gateway4", Usage: "IPv4 gateway object ID (0 clears)"},
					&cli.IntFlag{Name: "network6", Usage: "IPv6 network object ID (0 clears)"},
					&cli.IntFlag{Name: "gateway6", Usage: "IPv6 gateway object ID (0 clears)"},
					&cli.StringFlag{Name: "network4-manual", Usage: "Manual IPv4 CIDR (empty clears)"},
					&cli.StringFlag{Name: "gateway4-manual", Usage: "Manual IPv4 gateway (empty clears)"},
					&cli.StringFlag{Name: "network6-manual", Usage: "Manual IPv6 CIDR (empty clears)"},
					&cli.StringFlag{Name: "gateway6-manual", Usage: "Manual IPv6 gateway (empty clears)"},
					&cli.StringFlag{Name: "ports", Usage: "Comma-separated physical ports"},
					&cli.BoolFlag{Name: "private", Usage: "Set standard switch private mode"},
					&cli.BoolFlag{Name: "dhcp", Usage: "Set standard switch DHCP mode"},
					&cli.BoolFlag{Name: "disable-ipv6", Usage: "Set standard switch IPv6 disabled state"},
					&cli.BoolFlag{Name: "slaac", Usage: "Set standard switch SLAAC mode"},
					&cli.BoolFlag{Name: "default-route", Usage: "Set standard switch default route state"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					request, err := buildSwitchEditPayload(command)
					if err != nil {
						return err
					}
					request.JSON = command.Bool("json")
					return executeConsoleOperation(command, consoleprotocol.OperationSwitchEdit, request, command.Bool("json"))
				},
			},
		},
	}
}

var standardSwitchCreateOptionNames = []string{
	"mtu",
	"vlan",
	"network4",
	"gateway4",
	"network6",
	"gateway6",
	"network4-manual",
	"gateway4-manual",
	"network6-manual",
	"gateway6-manual",
	"ports",
	"private",
	"dhcp",
	"disable-ipv6",
	"slaac",
	"default-route",
}

func buildSwitchCreatePayload(command *cli.Command) (consoleprotocol.SwitchCreatePayload, error) {
	switchType := strings.ToLower(strings.TrimSpace(command.String("type")))
	name := strings.TrimSpace(command.String("name"))
	if name == "" {
		return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("--name is required")
	}

	switch switchType {
	case "standard", "std":
		if command.IsSet("bridge") {
			return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("--bridge is only valid for manual switches")
		}
		request, err := buildStandardSwitchCreateRequest(command, name)
		if err != nil {
			return consoleprotocol.SwitchCreatePayload{}, err
		}
		return consoleprotocol.SwitchCreatePayload{Type: "standard", Standard: &request}, nil

	case "manual":
		if standardSwitchCreateOptionsSet(command) {
			return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("standard switch options are not valid for manual switches")
		}
		bridge := strings.TrimSpace(command.String("bridge"))
		if bridge == "" {
			return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("--bridge is required for manual switches")
		}
		return consoleprotocol.SwitchCreatePayload{
			Type:   "manual",
			Manual: &consoleprotocol.ManualSwitchCreateRequest{Name: name, Bridge: bridge},
		}, nil

	default:
		return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("--type must be standard or manual")
	}
}

func buildStandardSwitchCreateRequest(command *cli.Command, name string) (consoleprotocol.StandardSwitchCreateRequest, error) {
	mtu, err := switchCreateNonNegativeInt(command, "mtu")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	vlan, err := switchCreateNonNegativeInt(command, "vlan")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	network4, err := switchCreateNonNegativeUint(command, "network4")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	gateway4, err := switchCreateNonNegativeUint(command, "gateway4")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	network6, err := switchCreateNonNegativeUint(command, "network6")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	gateway6, err := switchCreateNonNegativeUint(command, "gateway6")
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	ports, err := switchCreatePorts(command)
	if err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}

	return consoleprotocol.StandardSwitchCreateRequest{
		Name:           name,
		MTU:            mtu,
		VLAN:           vlan,
		Network4:       network4,
		Gateway4:       gateway4,
		Network6:       network6,
		Gateway6:       gateway6,
		Network4Manual: strings.TrimSpace(command.String("network4-manual")),
		Gateway4Manual: strings.TrimSpace(command.String("gateway4-manual")),
		Network6Manual: strings.TrimSpace(command.String("network6-manual")),
		Gateway6Manual: strings.TrimSpace(command.String("gateway6-manual")),
		Ports:          ports,
		Private:        command.Bool("private"),
		DHCP:           command.Bool("dhcp"),
		DisableIPv6:    command.Bool("disable-ipv6"),
		SLAAC:          command.Bool("slaac"),
		DefaultRoute:   command.Bool("default-route"),
	}, nil
}

func standardSwitchCreateOptionsSet(command *cli.Command) bool {
	for _, name := range standardSwitchCreateOptionNames {
		if command.IsSet(name) {
			return true
		}
	}
	return false
}

func switchCreateNonNegativeInt(command *cli.Command, name string) (int, error) {
	value := command.Int(name)
	if value < 0 {
		return 0, fmt.Errorf("--%s must not be negative", name)
	}
	return value, nil
}

func switchCreateNonNegativeUint(command *cli.Command, name string) (uint, error) {
	value, err := switchCreateNonNegativeInt(command, name)
	if err != nil {
		return 0, err
	}
	return uint(value), nil
}

func switchCreatePorts(command *cli.Command) ([]string, error) {
	if !command.IsSet("ports") {
		return []string{}, nil
	}

	values := strings.Split(command.String("ports"), ",")
	ports := make([]string, 0, len(values))
	for _, value := range values {
		port := strings.TrimSpace(value)
		if port == "" {
			return nil, fmt.Errorf("--ports must contain comma-separated port names")
		}
		ports = append(ports, port)
	}
	return ports, nil
}

func buildSwitchEditPayload(command *cli.Command) (consoleprotocol.SwitchEditPayload, error) {
	switchType := strings.ToLower(strings.TrimSpace(command.String("type")))
	switch switchType {
	case "standard", "std":
		id, err := commandPositiveUint(command, "id")
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		request, err := buildStandardSwitchEditRequest(command, id)
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		return consoleprotocol.SwitchEditPayload{Type: "standard", Standard: &request}, nil

	case "manual":
		id, err := commandPositiveUint(command, "id")
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		name := optionalSwitchEditString(command, "name")
		bridge := optionalSwitchEditString(command, "bridge")
		if name == nil && bridge == nil {
			return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("specify --name or --bridge for manual switch edits")
		}
		return consoleprotocol.SwitchEditPayload{
			Type:   "manual",
			Manual: &consoleprotocol.ManualSwitchEditRequest{ID: id, Name: name, Bridge: bridge},
		}, nil

	default:
		return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("--type must be standard or manual")
	}
}

func buildStandardSwitchEditRequest(command *cli.Command, id uint) (consoleprotocol.StandardSwitchEditRequest, error) {
	request := consoleprotocol.StandardSwitchEditRequest{ID: id}
	var err error
	if request.MTU, err = optionalSwitchEditInt(command, "mtu"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.VLAN, err = optionalSwitchEditInt(command, "vlan"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Network4, err = optionalSwitchEditUint(command, "network4"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Gateway4, err = optionalSwitchEditUint(command, "gateway4"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Network6, err = optionalSwitchEditUint(command, "network6"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Gateway6, err = optionalSwitchEditUint(command, "gateway6"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	request.Network4Manual = optionalSwitchEditString(command, "network4-manual")
	request.Gateway4Manual = optionalSwitchEditString(command, "gateway4-manual")
	request.Network6Manual = optionalSwitchEditString(command, "network6-manual")
	request.Gateway6Manual = optionalSwitchEditString(command, "gateway6-manual")
	if request.Ports, err = optionalSwitchEditPorts(command); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	request.Private = optionalSwitchEditBool(command, "private")
	request.DHCP = optionalSwitchEditBool(command, "dhcp")
	request.DisableIPv6 = optionalSwitchEditBool(command, "disable-ipv6")
	request.SLAAC = optionalSwitchEditBool(command, "slaac")
	request.DefaultRoute = optionalSwitchEditBool(command, "default-route")

	if !standardSwitchEditChanged(request) {
		return consoleprotocol.StandardSwitchEditRequest{}, fmt.Errorf("specify at least one standard switch edit option")
	}
	return request, nil
}

func optionalSwitchEditInt(command *cli.Command, name string) (*int, error) {
	if !command.IsSet(name) {
		return nil, nil
	}
	value := command.Int(name)
	if value < 0 {
		return nil, fmt.Errorf("--%s must not be negative", name)
	}
	return &value, nil
}

func optionalSwitchEditUint(command *cli.Command, name string) (*uint, error) {
	value, err := optionalSwitchEditInt(command, name)
	if err != nil || value == nil {
		return nil, err
	}
	result := uint(*value)
	return &result, nil
}

func optionalSwitchEditString(command *cli.Command, name string) *string {
	if !command.IsSet(name) {
		return nil
	}
	value := command.String(name)
	return &value
}

func optionalSwitchEditBool(command *cli.Command, name string) *bool {
	if !command.IsSet(name) {
		return nil
	}
	value := command.Bool(name)
	return &value
}

func optionalSwitchEditPorts(command *cli.Command) (*[]string, error) {
	if !command.IsSet("ports") {
		return nil, nil
	}

	values := strings.Split(command.String("ports"), ",")
	ports := make([]string, 0, len(values))
	for _, value := range values {
		port := strings.TrimSpace(value)
		if port == "" {
			return nil, fmt.Errorf("--ports must contain comma-separated port names")
		}
		ports = append(ports, port)
	}
	return &ports, nil
}

func standardSwitchEditChanged(request consoleprotocol.StandardSwitchEditRequest) bool {
	return request.MTU != nil ||
		request.VLAN != nil ||
		request.Network4 != nil ||
		request.Gateway4 != nil ||
		request.Network6 != nil ||
		request.Gateway6 != nil ||
		request.Network4Manual != nil ||
		request.Gateway4Manual != nil ||
		request.Network6Manual != nil ||
		request.Gateway6Manual != nil ||
		request.Ports != nil ||
		request.Private != nil ||
		request.DHCP != nil ||
		request.DisableIPv6 != nil ||
		request.SLAAC != nil ||
		request.DefaultRoute != nil
}
