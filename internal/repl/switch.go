// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

type switchListResult struct {
	Standard []networkModels.StandardSwitch `json:"standard"`
	Manual   []networkModels.ManualSwitch   `json:"manual"`
}

type switchCreateResult struct {
	Created bool   `json:"created"`
	ID      uint   `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
}

type switchDeleteResult struct {
	Deleted bool   `json:"deleted"`
	Type    string `json:"type"`
	ID      uint   `json:"id"`
}

type switchEditResult struct {
	Updated bool   `json:"updated"`
	Type    string `json:"type"`
	ID      uint   `json:"id"`
}

func handleSwitches(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "switches", []cmdHelp{
			{"list", "List all standard and manual switches"},
			{"create standard <name> [options]", "Create a standard switch; --ports is optional"},
			{"create manual <name> <bridge>", "Register an existing bridge as a manual switch"},
			{"delete <standard|manual> <id>", "Delete a switch"},
			{"edit standard <id> [options]", "Patch a standard switch; booleans accept --dhcp or --dhcp=false"},
			{"edit manual <id> [--name <name>] [--bridge <bridge>]", "Patch a manual switch"},
		})
		return
	}

	switch cleanArgs[0] {
	case "list":
		if len(cleanArgs) != 1 {
			println(ctx, styledErrorf("Usage: switches list"))
			return
		}
		switchesList(ctx, jsonMode)

	case "create":
		request, err := buildConsoleSwitchCreateRequest(cleanArgs[1:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		switchesCreate(ctx, request, jsonMode)

	case "delete":
		if len(cleanArgs) != 3 {
			println(ctx, styledErrorf("Usage: switches delete <standard|manual> <id>"))
			return
		}
		switchType, err := normalizeSwitchType(cleanArgs[1])
		if err != nil {
			println(ctx, styledErrorf("Unknown switch type '%s'. Use 'standard' or 'manual'.", cleanArgs[1]))
			return
		}
		id, err := parsePositiveUint(cleanArgs[2])
		if err != nil {
			println(ctx, styledErrorf("Invalid switch ID '%s'", cleanArgs[2]))
			return
		}
		switchesDelete(ctx, switchType, id, jsonMode)

	case "edit":
		request, err := buildConsoleSwitchEditRequest(cleanArgs[1:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		switchesEdit(ctx, request, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown switches command: '%s'. Type 'switches' for help.", cleanArgs[0]))
	}
}

func buildConsoleSwitchEditRequest(args []string) (consoleprotocol.SwitchEditPayload, error) {
	const usage = "Usage: switches edit <standard|manual> <id> [--option <value> ...]"
	if len(args) < 2 {
		return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("%s", usage)
	}

	switchType, err := normalizeSwitchType(args[0])
	if err != nil {
		return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("invalid_switch_type")
	}
	id, err := parsePositiveUint(args[1])
	if err != nil {
		return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("invalid_switch_id")
	}

	switch switchType {
	case "standard":
		options, err := parseSwitchOptions(args[2:], standardSwitchEditOptionNames, standardSwitchEditBooleanOptionNames)
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		request, err := buildStandardSwitchEditRequest(id, options)
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		return consoleprotocol.SwitchEditPayload{Type: switchType, Standard: &request}, nil

	case "manual":
		options, err := parseSwitchOptions(args[2:], manualSwitchEditOptionNames, nil)
		if err != nil {
			return consoleprotocol.SwitchEditPayload{}, err
		}
		name := switchEditStringOption(options, "--name")
		bridge := switchEditStringOption(options, "--bridge")
		if name == nil && bridge == nil {
			return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("specify --name or --bridge for manual switch edits")
		}
		return consoleprotocol.SwitchEditPayload{
			Type:   switchType,
			Manual: &consoleprotocol.ManualSwitchEditRequest{ID: id, Name: name, Bridge: bridge},
		}, nil
	}

	return consoleprotocol.SwitchEditPayload{}, fmt.Errorf("invalid_switch_type")
}

func buildConsoleSwitchCreateRequest(args []string) (consoleprotocol.SwitchCreatePayload, error) {
	const usage = "Usage: switches create <standard|manual> <name> [options]"
	if len(args) < 2 {
		return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("%s", usage)
	}

	switchType, err := normalizeSwitchType(args[0])
	if err != nil {
		return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("invalid_switch_type")
	}
	name := strings.TrimSpace(args[1])
	if name == "" {
		return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("invalid_switch_name")
	}

	switch switchType {
	case "standard":
		options, err := parseSwitchOptions(args[2:], standardSwitchEditOptionNames, standardSwitchEditBooleanOptionNames)
		if err != nil {
			return consoleprotocol.SwitchCreatePayload{}, err
		}
		request, err := buildStandardSwitchCreateRequest(name, options)
		if err != nil {
			return consoleprotocol.SwitchCreatePayload{}, err
		}
		return consoleprotocol.SwitchCreatePayload{Type: switchType, Standard: &request}, nil

	case "manual":
		if len(args) != 3 {
			return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("Usage: switches create manual <name> <bridge>")
		}
		bridge := strings.TrimSpace(args[2])
		if bridge == "" {
			return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("invalid_manual_bridge")
		}
		return consoleprotocol.SwitchCreatePayload{
			Type:   switchType,
			Manual: &consoleprotocol.ManualSwitchCreateRequest{Name: name, Bridge: bridge},
		}, nil
	}

	return consoleprotocol.SwitchCreatePayload{}, fmt.Errorf("invalid_switch_type")
}

func buildStandardSwitchCreateRequest(
	name string,
	options map[string]string,
) (consoleprotocol.StandardSwitchCreateRequest, error) {
	request := consoleprotocol.StandardSwitchCreateRequest{Name: name, Ports: []string{}}
	var err error
	if request.MTU, err = switchCreateIntOption(options, "--mtu"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.VLAN, err = switchCreateIntOption(options, "--vlan"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.Network4, err = switchCreateUintOption(options, "--network4"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.Gateway4, err = switchCreateUintOption(options, "--gateway4"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.Network6, err = switchCreateUintOption(options, "--network6"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.Gateway6, err = switchCreateUintOption(options, "--gateway6"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if ports, err := switchEditPortsOption(options); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	} else if ports != nil {
		request.Ports = append(request.Ports, (*ports)...)
	}
	if request.Private, err = switchCreateBoolOption(options, "--private"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.DHCP, err = switchCreateBoolOption(options, "--dhcp"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.DisableIPv6, err = switchCreateBoolOption(options, "--disable-ipv6"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.SLAAC, err = switchCreateBoolOption(options, "--slaac"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	if request.DefaultRoute, err = switchCreateBoolOption(options, "--default-route"); err != nil {
		return consoleprotocol.StandardSwitchCreateRequest{}, err
	}
	request.Network4Manual = switchCreateStringOption(options, "--network4-manual")
	request.Gateway4Manual = switchCreateStringOption(options, "--gateway4-manual")
	request.Network6Manual = switchCreateStringOption(options, "--network6-manual")
	request.Gateway6Manual = switchCreateStringOption(options, "--gateway6-manual")
	return request, nil
}

func switchCreateIntOption(options map[string]string, name string) (int, error) {
	value, err := switchEditIntOption(options, name)
	if err != nil || value == nil {
		return 0, err
	}
	return *value, nil
}

func switchCreateUintOption(options map[string]string, name string) (uint, error) {
	value, err := switchCreateIntOption(options, name)
	if err != nil {
		return 0, err
	}
	return uint(value), nil
}

func switchCreateBoolOption(options map[string]string, name string) (bool, error) {
	value, err := switchEditBoolOption(options, name)
	if err != nil || value == nil {
		return false, err
	}
	return *value, nil
}

func switchCreateStringOption(options map[string]string, name string) string {
	value := switchEditStringOption(options, name)
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

var standardSwitchEditOptionNames = map[string]bool{
	"--mtu":             true,
	"--vlan":            true,
	"--network4":        true,
	"--gateway4":        true,
	"--network6":        true,
	"--gateway6":        true,
	"--network4-manual": true,
	"--gateway4-manual": true,
	"--network6-manual": true,
	"--gateway6-manual": true,
	"--ports":           true,
	"--private":         true,
	"--dhcp":            true,
	"--disable-ipv6":    true,
	"--slaac":           true,
	"--default-route":   true,
}

var standardSwitchEditBooleanOptionNames = map[string]bool{
	"--private":       true,
	"--dhcp":          true,
	"--disable-ipv6":  true,
	"--slaac":         true,
	"--default-route": true,
}

var manualSwitchEditOptionNames = map[string]bool{
	"--name":   true,
	"--bridge": true,
}

func parseSwitchOptions(args []string, allowed, booleanOptions map[string]bool) (map[string]string, error) {
	options := make(map[string]string, len(args))
	for i := 0; i < len(args); {
		name, value, assigned := strings.Cut(args[i], "=")
		if !allowed[name] {
			return nil, fmt.Errorf("unknown switch option %q", name)
		}
		if _, exists := options[name]; exists {
			return nil, fmt.Errorf("switch option %q was specified more than once", name)
		}
		if booleanOptions[name] {
			if assigned {
				options[name] = value
				i++
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				options[name] = args[i+1]
				i += 2
				continue
			}
			options[name] = "true"
			i++
			continue
		}
		if assigned || i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return nil, fmt.Errorf("switch option %q requires a value", name)
		}
		options[name] = args[i+1]
		i += 2
	}
	return options, nil
}

func buildStandardSwitchEditRequest(id uint, options map[string]string) (consoleprotocol.StandardSwitchEditRequest, error) {
	request := consoleprotocol.StandardSwitchEditRequest{ID: id}
	var err error
	if request.MTU, err = switchEditIntOption(options, "--mtu"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.VLAN, err = switchEditIntOption(options, "--vlan"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Network4, err = switchEditUintOption(options, "--network4"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Gateway4, err = switchEditUintOption(options, "--gateway4"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Network6, err = switchEditUintOption(options, "--network6"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Gateway6, err = switchEditUintOption(options, "--gateway6"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	request.Network4Manual = switchEditStringOption(options, "--network4-manual")
	request.Gateway4Manual = switchEditStringOption(options, "--gateway4-manual")
	request.Network6Manual = switchEditStringOption(options, "--network6-manual")
	request.Gateway6Manual = switchEditStringOption(options, "--gateway6-manual")
	if request.Ports, err = switchEditPortsOption(options); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.Private, err = switchEditBoolOption(options, "--private"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.DHCP, err = switchEditBoolOption(options, "--dhcp"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.DisableIPv6, err = switchEditBoolOption(options, "--disable-ipv6"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.SLAAC, err = switchEditBoolOption(options, "--slaac"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if request.DefaultRoute, err = switchEditBoolOption(options, "--default-route"); err != nil {
		return consoleprotocol.StandardSwitchEditRequest{}, err
	}
	if !standardSwitchEditChanged(request) {
		return consoleprotocol.StandardSwitchEditRequest{}, fmt.Errorf("specify at least one standard switch edit option")
	}
	return request, nil
}

func switchEditIntOption(options map[string]string, name string) (*int, error) {
	value, exists := options[name]
	if !exists {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return nil, fmt.Errorf("invalid value for %s", name)
	}
	return &parsed, nil
}

func switchEditUintOption(options map[string]string, name string) (*uint, error) {
	value, err := switchEditIntOption(options, name)
	if err != nil || value == nil {
		return nil, err
	}
	parsed := uint(*value)
	return &parsed, nil
}

func switchEditStringOption(options map[string]string, name string) *string {
	value, exists := options[name]
	if !exists {
		return nil
	}
	return &value
}

func switchEditBoolOption(options map[string]string, name string) (*bool, error) {
	value, exists := options[name]
	if !exists {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid boolean value for %s", name)
	}
	return &parsed, nil
}

func switchEditPortsOption(options map[string]string) (*[]string, error) {
	value, exists := options["--ports"]
	if !exists {
		return nil, nil
	}

	entries := strings.Split(value, ",")
	ports := make([]string, 0, len(entries))
	for _, entry := range entries {
		port := strings.TrimSpace(entry)
		if port == "" {
			return nil, fmt.Errorf("--ports must contain comma-separated port names")
		}
		ports = append(ports, port)
	}
	return &ports, nil
}

func normalizeSwitchType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "std", "standard":
		return "standard", nil
	case "manual":
		return "manual", nil
	default:
		return "", fmt.Errorf("invalid_switch_type")
	}
}

func listSwitches(ctx *Context) (switchListResult, error) {
	if ctx == nil || ctx.Network == nil {
		return switchListResult{}, fmt.Errorf("network_service_unavailable")
	}

	standard, err := ctx.Network.GetStandardSwitches()
	if err != nil {
		return switchListResult{}, fmt.Errorf("failed_to_list_standard_switches: %w", err)
	}
	manual, err := ctx.Network.GetManualSwitches()
	if err != nil {
		return switchListResult{}, fmt.Errorf("failed_to_list_manual_switches: %w", err)
	}
	if standard == nil {
		standard = []networkModels.StandardSwitch{}
	}
	if manual == nil {
		manual = []networkModels.ManualSwitch{}
	}
	return switchListResult{Standard: standard, Manual: manual}, nil
}

func deleteSwitch(ctx *Context, switchType string, id uint) (switchDeleteResult, error) {
	if id == 0 || id > uint(^uint(0)>>1) {
		return switchDeleteResult{}, fmt.Errorf("invalid_switch_id")
	}
	if ctx == nil || ctx.Network == nil {
		return switchDeleteResult{}, fmt.Errorf("network_service_unavailable")
	}

	switchType, err := normalizeSwitchType(switchType)
	if err != nil {
		return switchDeleteResult{}, err
	}
	if switchType == "standard" {
		if err := ctx.Network.DeleteStandardSwitch(int(id)); err != nil {
			return switchDeleteResult{}, fmt.Errorf("failed_to_delete_standard_switch: %w", err)
		}
	} else if err := ctx.Network.DeleteManualSwitch(id); err != nil {
		return switchDeleteResult{}, fmt.Errorf("failed_to_delete_manual_switch: %w", err)
	}

	return switchDeleteResult{Deleted: true, Type: switchType, ID: id}, nil
}

func createSwitch(ctx *Context, request consoleprotocol.SwitchCreatePayload) (switchCreateResult, error) {
	switchType, err := normalizeSwitchType(request.Type)
	if err != nil {
		return switchCreateResult{}, err
	}
	if ctx == nil || ctx.Network == nil {
		return switchCreateResult{}, fmt.Errorf("network_service_unavailable")
	}

	switch switchType {
	case "standard":
		if request.Standard == nil {
			return switchCreateResult{}, fmt.Errorf("invalid_standard_switch_create_request")
		}
		standard := *request.Standard
		standard.Name = strings.TrimSpace(standard.Name)
		if standard.Name == "" || standard.MTU < 0 || standard.VLAN < 0 {
			return switchCreateResult{}, fmt.Errorf("invalid_standard_switch_create_request")
		}
		for index, port := range standard.Ports {
			standard.Ports[index] = strings.TrimSpace(port)
			if standard.Ports[index] == "" {
				return switchCreateResult{}, fmt.Errorf("invalid_standard_switch_ports")
			}
		}
		manual := networkModels.StandardSwitchManualAddresses{
			Network4: standard.Network4Manual,
			Gateway4: standard.Gateway4Manual,
			Network6: standard.Network6Manual,
			Gateway6: standard.Gateway6Manual,
		}
		if err := ctx.Network.NewStandardSwitch(
			standard.Name,
			standard.MTU,
			standard.VLAN,
			standard.Network4,
			standard.Network6,
			standard.Gateway4,
			standard.Gateway6,
			standard.Ports,
			standard.Private,
			standard.DHCP,
			standard.DisableIPv6,
			standard.SLAAC,
			standard.DefaultRoute,
			manual,
		); err != nil {
			return switchCreateResult{}, fmt.Errorf("failed_to_create_standard_switch: %w", err)
		}
		switches, err := ctx.Network.GetStandardSwitches()
		if err != nil {
			return switchCreateResult{}, fmt.Errorf("reload_created_standard_switch: %w", err)
		}
		for _, created := range switches {
			if created.Name == standard.Name {
				return switchCreateResult{Created: true, ID: created.ID, Type: switchType, Name: standard.Name}, nil
			}
		}
		return switchCreateResult{}, fmt.Errorf("reload_created_standard_switch: switch_not_found")

	case "manual":
		if request.Manual == nil {
			return switchCreateResult{}, fmt.Errorf("invalid_manual_switch_create_request")
		}
		name := strings.TrimSpace(request.Manual.Name)
		bridge := strings.TrimSpace(request.Manual.Bridge)
		if name == "" || bridge == "" {
			return switchCreateResult{}, fmt.Errorf("invalid_manual_switch_create_request")
		}
		created, err := ctx.Network.CreateManualSwitch(name, bridge)
		if err != nil {
			return switchCreateResult{}, fmt.Errorf("failed_to_create_manual_switch: %w", err)
		}
		return switchCreateResult{Created: true, ID: created.ID, Type: switchType, Name: name}, nil
	}

	return switchCreateResult{}, fmt.Errorf("invalid_switch_type")
}

type standardSwitchEditConfig struct {
	MTU            int
	VLAN           int
	Network4       uint
	Gateway4       uint
	Network6       uint
	Gateway6       uint
	Network4Manual string
	Gateway4Manual string
	Network6Manual string
	Gateway6Manual string
	DisableIPv6    bool
	SLAAC          bool
	Private        bool
	DefaultRoute   bool
	DHCP           bool
	Ports          []string
}

func editSwitch(ctx *Context, request consoleprotocol.SwitchEditPayload) (switchEditResult, error) {
	switchType, err := normalizeSwitchType(request.Type)
	if err != nil {
		return switchEditResult{}, err
	}
	if ctx == nil || ctx.Network == nil {
		return switchEditResult{}, fmt.Errorf("network_service_unavailable")
	}

	switch switchType {
	case "standard":
		if request.Standard == nil || request.Standard.ID == 0 {
			return switchEditResult{}, fmt.Errorf("invalid_standard_switch_edit_request")
		}
		switchModel, err := standardSwitchForEdit(ctx, request.Standard.ID)
		if err != nil {
			return switchEditResult{}, err
		}
		config := standardSwitchEditConfigFromModel(switchModel)
		if err := applyStandardSwitchEditRequest(&config, *request.Standard); err != nil {
			return switchEditResult{}, err
		}

		manual := networkModels.StandardSwitchManualAddresses{
			Network4: config.Network4Manual,
			Gateway4: config.Gateway4Manual,
			Network6: config.Network6Manual,
			Gateway6: config.Gateway6Manual,
		}
		if err := ctx.Network.EditStandardSwitch(
			request.Standard.ID,
			config.MTU,
			config.VLAN,
			config.Network4,
			config.Network6,
			config.Gateway4,
			config.Gateway6,
			config.Ports,
			config.Private,
			config.DHCP,
			config.DisableIPv6,
			config.SLAAC,
			config.DefaultRoute,
			manual,
		); err != nil {
			return switchEditResult{}, fmt.Errorf("failed_to_update_standard_switch: %w", err)
		}
		return switchEditResult{Updated: true, Type: switchType, ID: request.Standard.ID}, nil

	case "manual":
		if request.Manual == nil || request.Manual.ID == 0 {
			return switchEditResult{}, fmt.Errorf("invalid_manual_switch_edit_request")
		}
		switchModel, err := manualSwitchForEdit(ctx, request.Manual.ID)
		if err != nil {
			return switchEditResult{}, err
		}
		name := switchModel.Name
		bridge := switchModel.Bridge
		changed := false
		if request.Manual.Name != nil {
			name = strings.TrimSpace(*request.Manual.Name)
			changed = true
		}
		if request.Manual.Bridge != nil {
			bridge = strings.TrimSpace(*request.Manual.Bridge)
			changed = true
		}
		if !changed || name == "" || bridge == "" {
			return switchEditResult{}, fmt.Errorf("invalid_manual_switch_edit_request")
		}
		if _, err := ctx.Network.UpdateManualSwitch(request.Manual.ID, name, bridge); err != nil {
			return switchEditResult{}, fmt.Errorf("failed_to_update_manual_switch: %w", err)
		}
		return switchEditResult{Updated: true, Type: switchType, ID: request.Manual.ID}, nil
	}

	return switchEditResult{}, fmt.Errorf("invalid_switch_type")
}

func standardSwitchForEdit(ctx *Context, id uint) (networkModels.StandardSwitch, error) {
	switches, err := ctx.Network.GetStandardSwitches()
	if err != nil {
		return networkModels.StandardSwitch{}, fmt.Errorf("failed_to_list_standard_switches: %w", err)
	}
	for _, switchModel := range switches {
		if switchModel.ID == id {
			return switchModel, nil
		}
	}
	return networkModels.StandardSwitch{}, fmt.Errorf("switch_not_found")
}

func manualSwitchForEdit(ctx *Context, id uint) (networkModels.ManualSwitch, error) {
	switches, err := ctx.Network.GetManualSwitches()
	if err != nil {
		return networkModels.ManualSwitch{}, fmt.Errorf("failed_to_list_manual_switches: %w", err)
	}
	for _, switchModel := range switches {
		if switchModel.ID == id {
			return switchModel, nil
		}
	}
	return networkModels.ManualSwitch{}, fmt.Errorf("switch_not_found")
}

func standardSwitchEditConfigFromModel(switchModel networkModels.StandardSwitch) standardSwitchEditConfig {
	config := standardSwitchEditConfig{
		MTU:            switchModel.MTU,
		VLAN:           switchModel.VLAN,
		Network4Manual: switchModel.NetworkManual,
		Gateway4Manual: switchModel.GatewayManual,
		Network6Manual: switchModel.Network6Manual,
		Gateway6Manual: switchModel.Gateway6Manual,
		DisableIPv6:    switchModel.DisableIPv6,
		SLAAC:          switchModel.SLAAC,
		Private:        switchModel.Private,
		DefaultRoute:   switchModel.DefaultRoute,
		DHCP:           switchModel.DHCP,
		Ports:          make([]string, 0, len(switchModel.Ports)),
	}
	if switchModel.NetworkID != nil {
		config.Network4 = *switchModel.NetworkID
	}
	if switchModel.GatewayAddressID != nil {
		config.Gateway4 = *switchModel.GatewayAddressID
	}
	if switchModel.Network6ID != nil {
		config.Network6 = *switchModel.Network6ID
	}
	if switchModel.Gateway6AddressID != nil {
		config.Gateway6 = *switchModel.Gateway6AddressID
	}
	for _, port := range switchModel.Ports {
		config.Ports = append(config.Ports, port.Name)
	}
	return config
}

func applyStandardSwitchEditRequest(config *standardSwitchEditConfig, request consoleprotocol.StandardSwitchEditRequest) error {
	if !standardSwitchEditChanged(request) {
		return fmt.Errorf("specify at least one standard switch edit option")
	}
	if request.MTU != nil {
		config.MTU = *request.MTU
	}
	if request.VLAN != nil {
		config.VLAN = *request.VLAN
	}
	applySwitchObjectEdit(&config.Network4, &config.Network4Manual, request.Network4, request.Network4Manual)
	applySwitchObjectEdit(&config.Gateway4, &config.Gateway4Manual, request.Gateway4, request.Gateway4Manual)
	applySwitchObjectEdit(&config.Network6, &config.Network6Manual, request.Network6, request.Network6Manual)
	applySwitchObjectEdit(&config.Gateway6, &config.Gateway6Manual, request.Gateway6, request.Gateway6Manual)
	if request.Ports != nil {
		config.Ports = append([]string(nil), (*request.Ports)...)
	}
	if request.Private != nil {
		config.Private = *request.Private
	}
	if request.DHCP != nil {
		config.DHCP = *request.DHCP
	}
	if request.DisableIPv6 != nil {
		config.DisableIPv6 = *request.DisableIPv6
	}
	if request.SLAAC != nil {
		config.SLAAC = *request.SLAAC
	}
	if request.DefaultRoute != nil {
		config.DefaultRoute = *request.DefaultRoute
	}
	if len(config.Ports) == 0 {
		return fmt.Errorf("switch_ports_required")
	}
	if request.DHCP == nil && switchEditIPv4AddressProvided(request) {
		config.DHCP = false
	}
	if switchEditIPv6AddressProvided(request) {
		if request.DisableIPv6 == nil {
			config.DisableIPv6 = false
		}
		if request.SLAAC == nil {
			config.SLAAC = false
		}
	}

	return nil
}

func applySwitchObjectEdit(objectID *uint, manual *string, objectPatch *uint, manualPatch *string) {
	if objectPatch != nil {
		*objectID = *objectPatch
		if *objectPatch != 0 && manualPatch == nil {
			*manual = ""
		}
	}
	if manualPatch != nil {
		*manual = strings.TrimSpace(*manualPatch)
		if *manual != "" && objectPatch == nil {
			*objectID = 0
		}
	}
}

func switchEditIPv4AddressProvided(request consoleprotocol.StandardSwitchEditRequest) bool {
	return (request.Network4 != nil && *request.Network4 != 0) ||
		(request.Gateway4 != nil && *request.Gateway4 != 0) ||
		(request.Network4Manual != nil && strings.TrimSpace(*request.Network4Manual) != "") ||
		(request.Gateway4Manual != nil && strings.TrimSpace(*request.Gateway4Manual) != "")
}

func switchEditIPv6AddressProvided(request consoleprotocol.StandardSwitchEditRequest) bool {
	return (request.Network6 != nil && *request.Network6 != 0) ||
		(request.Gateway6 != nil && *request.Gateway6 != 0) ||
		(request.Network6Manual != nil && strings.TrimSpace(*request.Network6Manual) != "") ||
		(request.Gateway6Manual != nil && strings.TrimSpace(*request.Gateway6Manual) != "")
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

func formatSwitches(result switchListResult) string {
	if len(result.Standard) == 0 && len(result.Manual) == 0 {
		return "No switches found."
	}

	headers := []string{"ID", "Name", "Type", "Bridge", "VLAN", "Ports/Details"}
	rows := make([][]string, 0, len(result.Standard)+len(result.Manual))
	for _, standard := range result.Standard {
		ports := make([]string, 0, len(standard.Ports))
		for _, port := range standard.Ports {
			ports = append(ports, port.Name)
		}
		portsText := strings.Join(ports, ",")
		if portsText == "" {
			portsText = "-"
		}

		vlan := "-"
		if standard.VLAN > 0 {
			vlan = strconv.Itoa(standard.VLAN)
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(standard.ID), 10),
			standard.Name,
			"standard",
			standard.BridgeName,
			vlan,
			portsText,
		})
	}
	for _, manual := range result.Manual {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(manual.ID), 10),
			manual.Name,
			"manual",
			manual.Bridge,
			"-",
			"external",
		})
	}
	return styledTable(headers, rows)
}

func switchesList(ctx *Context, jsonMode bool) {
	result, err := listSwitches(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching switches", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, formatSwitches(result))
}

func switchesDelete(ctx *Context, switchType string, id uint, jsonMode bool) {
	result, err := deleteSwitch(ctx, switchType, id)
	if err != nil {
		if !jsonMode && strings.Contains(err.Error(), "switch_in_use") {
			println(ctx, styledErrorf("Cannot delete switch because it is currently attached to a VM or jail."))
			return
		}
		printOperationError(ctx, jsonMode, "Error deleting switch", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("%s switch %d deleted successfully.", result.Type, result.ID))
}

func switchesCreate(ctx *Context, request consoleprotocol.SwitchCreatePayload, jsonMode bool) {
	result, err := createSwitch(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error creating switch", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("%s switch %d (%s) created successfully.", result.Type, result.ID, result.Name))
}

func switchesEdit(ctx *Context, request consoleprotocol.SwitchEditPayload, jsonMode bool) {
	result, err := editSwitch(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error updating switch", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("%s switch %d updated successfully.", result.Type, result.ID))
}

func processSwitchListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.SwitchListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_switch_list_request: " + err.Error()}
	}
	result, err := listSwitches(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, formatSwitches(result))
}

func processSwitchCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.SwitchCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_switch_create_request: " + err.Error()}
	}
	result, err := createSwitch(ctx, request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("%s switch %d (%s) created successfully.", result.Type, result.ID, result.Name),
	)
}

func processSwitchDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.SwitchDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_switch_delete_request: " + err.Error()}
	}
	result, err := deleteSwitch(ctx, request.Type, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("%s switch %d deleted successfully.", result.Type, result.ID),
	)
}

func processSwitchEditSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.SwitchEditPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_switch_edit_request: " + err.Error()}
	}
	result, err := editSwitch(ctx, request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("%s switch %d updated successfully.", result.Type, result.ID),
	)
}
