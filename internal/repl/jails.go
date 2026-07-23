package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

type jailActionResult struct {
	CTID    uint   `json:"ctId"`
	Name    string `json:"name,omitempty"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	TaskID  uint   `json:"taskId,omitempty"`
	Error   string `json:"error,omitempty"`
}

type jailDeleteResult struct {
	Deleted bool `json:"deleted"`
	CTID    uint `json:"ctId"`
}

type jailNetworkDeleteResult struct {
	Deleted   bool `json:"deleted"`
	CTID      uint `json:"ctId"`
	NetworkID uint `json:"networkId"`
}

const (
	jailBootstrapWaitPollInterval = 2 * time.Second
	jailBootstrapWaitTimeout      = 31 * time.Minute
)

type jailCreateResult struct {
	Created bool   `json:"created"`
	CTID    uint   `json:"ctId"`
	Name    string `json:"name"`
}

type jailBootstrapCreateResult struct {
	Requested bool   `json:"requested"`
	Pool      string `json:"pool"`
	Name      string `json:"name"`
	Major     int    `json:"major"`
	Minor     int    `json:"minor"`
	Type      string `json:"type"`
}

type jailBootstrapDeleteResult struct {
	Deleted bool   `json:"deleted"`
	Pool    string `json:"pool"`
	Name    string `json:"name"`
}

type jailBootstrapCreateResponse struct {
	Requested *jailBootstrapCreateResult
	Entry     *jailServiceInterfaces.BootstrapEntry
}

func processJailCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_create_request: " + err.Error()}
	}
	result, err := createJail(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Jail %d (%s) created successfully.", result.CTID, result.Name))
}

func createJail(ctx *Context, request jailServiceInterfaces.CreateJailRequest) (jailCreateResult, error) {
	if ctx == nil || ctx.Jail == nil {
		return jailCreateResult{}, fmt.Errorf("jail_service_unavailable")
	}
	if request.CTID == nil {
		return jailCreateResult{}, fmt.Errorf("invalid_jail_create_request: missing_ctid")
	}
	if err := ctx.Jail.CreateJail(context.Background(), request); err != nil {
		return jailCreateResult{}, fmt.Errorf("failed_to_create_jail: %w", err)
	}

	return jailCreateResult{
		Created: true,
		CTID:    *request.CTID,
		Name:    request.Name,
	}, nil
}

func validateJailCTID(ctid uint) error {
	if ctid == 0 || ctid > 9999 {
		return fmt.Errorf("invalid_ctid")
	}
	return nil
}

func listJails(ctx *Context) ([]jailServiceInterfaces.SimpleList, error) {
	if ctx == nil || ctx.Jail == nil {
		return nil, fmt.Errorf("jail_service_unavailable")
	}

	jails, err := ctx.Jail.GetJailsSimple()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_jails: %w", err)
	}
	return jails, nil
}

func getJail(ctx *Context, ctid uint) (*jailModels.Jail, error) {
	if err := validateJailCTID(ctid); err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Jail == nil {
		return nil, fmt.Errorf("jail_service_unavailable")
	}

	jail, err := ctx.Jail.GetJailByCTID(ctid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_jail: %w", err)
	}
	return jail, nil
}

func formatJailList(jails []jailServiceInterfaces.SimpleList) string {
	if len(jails) == 0 {
		return "No jails found."
	}

	headers := []string{"CTID", "Name", "State", "Limits"}
	rows := make([][]string, 0, len(jails))
	for _, jail := range jails {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(jail.CTID), 10),
			jail.Name,
			jail.State,
			formatJailLimits(jail),
		})
	}
	return styledTable(headers, rows)
}

func formatJailLimits(jail jailServiceInterfaces.SimpleList) string {
	if jail.ResourceLimits == nil || !*jail.ResourceLimits {
		return "unrestricted"
	}

	limits := make([]string, 0, 2)
	if jail.Cores > 0 {
		limits = append(limits, fmt.Sprintf("%d CPU", jail.Cores))
	}
	if jail.Memory > 0 {
		limits = append(limits, formatMemorySize(jail.Memory))
	}
	if len(limits) == 0 {
		return "enabled"
	}
	return strings.Join(limits, ", ")
}

func formatJailDetails(jail *jailModels.Jail) string {
	lines := []string{
		styledKeyValue("CTID:", strconv.FormatUint(uint64(jail.CTID), 10)),
		styledKeyValue("Name:", jail.Name),
		styledKeyValue("Hostname:", jail.Hostname),
		styledKeyValue("Type:", string(jail.Type)),
		styledKeyValue("Description:", jail.Description),
		styledKeyValue("Cores:", strconv.Itoa(jail.Cores)),
		styledKeyValue("Memory:", strconv.Itoa(jail.Memory)),
	}
	if jail.StartedAt != nil {
		lines = append(lines, styledKeyValue("Started:", jail.StartedAt.Format("2006-01-02 15:04")))
	}
	if jail.StoppedAt != nil {
		lines = append(lines, styledKeyValue("Stopped:", jail.StoppedAt.Format("2006-01-02 15:04")))
	}
	return strings.Join(lines, "\n")
}

func formatJailNetworks(jail *jailModels.Jail) string {
	if len(jail.Networks) == 0 {
		return styledErrorf("Jail '%s' (CTID: %d) has no networks configured.", jail.Name, jail.CTID)
	}

	headers := []string{"ID", "Name", "Switch", "Type", "DHCP", "MAC"}
	rows := make([][]string, 0, len(jail.Networks))
	for _, network := range jail.Networks {
		mac := "auto"
		if network.MacAddressObj != nil && len(network.MacAddressObj.Entries) > 0 {
			mac = network.MacAddressObj.Entries[0].Value
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(network.ID), 10),
			network.Name,
			strconv.FormatUint(uint64(network.SwitchID), 10),
			network.SwitchType,
			strconv.FormatBool(network.DHCP),
			mac,
		})
	}
	return styledTable(headers, rows)
}

func formatJailActionAll(results []jailActionResult) string {
	if len(results) == 0 {
		return "No jails found."
	}

	headers := []string{"CTID", "Name", "Outcome", "Task ID"}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		taskID := ""
		if result.TaskID > 0 {
			taskID = strconv.FormatUint(uint64(result.TaskID), 10)
		}
		outcome := result.Outcome
		if result.Error != "" {
			outcome = result.Error
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(result.CTID), 10),
			result.Name,
			outcome,
			taskID,
		})
	}
	return styledTable(headers, rows)
}

func formatBootstraps(entries []jailServiceInterfaces.BootstrapEntry) string {
	if len(entries) == 0 {
		return "No supported bootstraps found."
	}

	headers := []string{"Name", "Version", "Type", "Status", "Phase", "Error"}
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		status := entry.Status
		if status == "" {
			status = "not installed"
		}
		phase := entry.Phase
		if phase == "" {
			phase = "-"
		}
		errMsg := entry.Error
		if errMsg == "" {
			errMsg = "-"
		}
		rows = append(rows, []string{
			entry.Name,
			fmt.Sprintf("%d.%d", entry.Major, entry.Minor),
			entry.Type,
			status,
			phase,
			errMsg,
		})
	}
	return styledTable(headers, rows)
}

func processJailListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_list_request: " + err.Error()}
	}

	jails, err := listJails(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if jails == nil {
		jails = []jailServiceInterfaces.SimpleList{}
	}
	return operationSuccess(request.JSON, jails, formatJailList(jails))
}

func processJailGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_get_request: " + err.Error()}
	}

	jail, err := getJail(ctx, request.CTID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, jail, formatJailDetails(jail))
}

func processJailActionSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailActionPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_action_request: " + err.Error()}
	}

	if request.All {
		if request.CTID != 0 {
			return socketResponse{Error: "invalid_jail_action_request: all_and_ctid_are_mutually_exclusive"}
		}
		results, err := requestJailActionAll(ctx, request.Action)
		if err != nil {
			return socketResponse{Error: err.Error()}
		}
		return operationSuccess(request.JSON, results, formatJailActionAll(results))
	}

	result, err := requestJailAction(ctx, request.CTID, request.Action)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("Jail %d: %s %s (Task: %d)", result.CTID, result.Action, result.Outcome, result.TaskID),
	)
}

func processJailDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_delete_request: " + err.Error()}
	}

	result, err := deleteJail(ctx, request.CTID, request.Purge)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Jail %d deleted successfully.", result.CTID))
}

func processJailNetworksSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailNetworksPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_networks_request: " + err.Error()}
	}

	jail, err := getJail(ctx, request.CTID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if request.JSON {
		networks := jail.Networks
		if networks == nil {
			networks = []jailModels.Network{}
		}
		return operationSuccess(true, networks, "")
	}
	return operationSuccess(false, jail.Networks, formatJailNetworks(jail))
}

func processJailRemoveNetworkSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.JailRemoveNetworkPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_jail_remove_network_request: " + err.Error()}
	}

	result, err := removeJailNetwork(ctx, request.CTID, request.NetworkID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("Network %d removed from jail %d.", result.NetworkID, result.CTID),
	)
}

func processBootstrapListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.BootstrapListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_bootstrap_list_request: " + err.Error()}
	}

	entries, err := listBootstraps(ctx, request.Pool)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if entries == nil {
		entries = []jailServiceInterfaces.BootstrapEntry{}
	}
	return operationSuccess(request.JSON, entries, formatBootstraps(entries))
}

func processBootstrapCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.BootstrapCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_bootstrap_create_request: " + err.Error()}
	}

	response, err := createBootstrap(ctx, request.Request, request.Wait)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if response.Entry != nil {
		return operationSuccess(
			request.JSON,
			response.Entry,
			styledSuccessf("Bootstrap %s completed successfully.", response.Entry.Name),
		)
	}

	result := *response.Requested
	return operationSuccess(
		request.JSON,
		result,
		styledSuccessf("Bootstrap %s requested. Use 'jails bootstrap list %s' to track progress.", result.Name, result.Pool),
	)
}

func processBootstrapDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.BootstrapDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_bootstrap_delete_request: " + err.Error()}
	}

	result, err := deleteBootstrap(ctx, request.Pool, request.Name)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Bootstrap %s deleted successfully.", result.Name))
}

func handleJails(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "jails", []cmdHelp{
			{"list", "List all jails"},
			{"create [--file <path>] [--ctid <id>] [--name <name>] [--pool <pool>] [--base <uuid>|--bootstrap <name>] [--switch <name>] [--type <type>]", "Create a jail from a JSON file or core options"},
			{"bootstrap", "Manage jail base bootstraps"},
			{"get <ctid>", "Get jail details"},
			{"start <ctid|all>", "Start a jail (or all)"},
			{"stop <ctid|all>", "Stop a jail (or all)"},
			{"restart <ctid|all>", "Restart a jail (or all)"},
			{"delete <ctid> [--purge]", "Delete a jail; --purge also destroys its root dataset"},
			{"networks <ctid>", "List networks for a jail"},
			{"rmnet <ctid> <net_id>", "Remove a network from a jail"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch subCmd {
	case "list":
		if len(subArgs) != 0 {
			println(ctx, styledErrorf("Usage: jails list"))
			return
		}
		jailsList(ctx, jsonMode)

	case "create":
		request, err := buildConsoleJailCreateRequest(subArgs)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		jailsCreate(ctx, request, jsonMode)

	case "bootstrap":
		handleJailBootstrap(ctx, subArgs, jsonMode)

	case "get":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: jails get <ctid>"))
			return
		}
		ctID, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		jailsGet(ctx, ctID, jsonMode)

	case "start", "stop", "restart":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: jails %s <ctid|all>", subCmd))
			return
		}
		if subArgs[0] == "all" {
			jailsActionAll(ctx, subCmd, jsonMode)
		} else {
			ctID, err := parsePositiveUint(subArgs[0])
			if err != nil {
				println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
				return
			}
			jailsAction(ctx, ctID, subCmd, jsonMode)
		}

	case "delete":
		if len(subArgs) < 1 || len(subArgs) > 2 {
			println(ctx, styledErrorf("Usage: jails delete <ctid> [--purge]"))
			return
		}
		ctID, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		purge := len(subArgs) == 2 && subArgs[1] == "--purge"
		if len(subArgs) == 2 && !purge {
			println(ctx, styledErrorf("Usage: jails delete <ctid> [--purge]"))
			return
		}
		jailsDelete(ctx, ctID, purge, jsonMode)

	case "networks":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: jails networks <ctid>"))
			return
		}
		ctID, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		jailsNetworksList(ctx, ctID, jsonMode)

	case "rmnet":
		if len(subArgs) != 2 {
			println(ctx, styledErrorf("Usage: jails rmnet <ctid> <net_id>"))
			return
		}
		ctID, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		netID, err := parsePositiveUint(subArgs[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid network ID '%s'", subArgs[1]))
			return
		}
		jailsRemoveNetwork(ctx, ctID, netID, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown jails command: '%s'. Type 'jails' for help.", subCmd))
	}
}

func buildConsoleJailCreateRequest(args []string) (jailServiceInterfaces.CreateJailRequest, error) {
	const usage = "Usage: jails create [--file <path>] [--ctid <id>] [--name <name>] [--pool <pool>] [--base <uuid>|--bootstrap <name>] [--switch <name>] [--type <freebsd|linux>]"
	if len(args) == 0 {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
	}

	overrides := consoleprotocol.JailCreateOverrides{}
	file := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			if file != "" || i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			file = args[i+1]
			i++

		case "--ctid":
			if overrides.CTID != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			ctid, err := parsePositiveUint(args[i+1])
			if err != nil {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("Invalid CTID '%s'", args[i+1])
			}
			overrides.CTID = &ctid
			i++

		case "--name":
			if overrides.Name != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Name = &value
			i++

		case "--pool":
			if overrides.Pool != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Pool = &value
			i++

		case "--base":
			if overrides.Base != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Base = &value
			i++

		case "--bootstrap":
			if overrides.Bootstrap != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Bootstrap = &value
			i++

		case "--switch":
			if overrides.Switch != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Switch = &value
			i++

		case "--type":
			if overrides.Type != nil || i+1 >= len(args) {
				return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			overrides.Type = &value
			i++

		default:
			return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("unknown jail create option %q", args[i])
		}
	}

	return consoleprotocol.BuildJailCreateRequest(file, overrides)
}

func jailsCreate(ctx *Context, request jailServiceInterfaces.CreateJailRequest, jsonMode bool) {
	result, err := createJail(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error creating jail", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Jail %d (%s) created successfully.", result.CTID, result.Name))
	}
}

func handleJailBootstrap(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "jails bootstrap", []cmdHelp{
			{"list <pool>", "List supported bootstraps and their install status"},
			{"create <pool> <version> <type> [--wait]", "Start a bootstrap installation (for example, 15.0)"},
			{"delete <pool> <name>", "Delete a bootstrap"},
		})
		return
	}

	switch args[0] {
	case "list":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: jails bootstrap list <pool>"))
			return
		}
		jailsBootstrapList(ctx, args[1], jsonMode)

	case "create":
		if len(args) < 4 || len(args) > 5 {
			println(ctx, styledErrorf("Usage: jails bootstrap create <pool> <version> <type> [--wait]"))
			return
		}

		major, minor, err := consoleprotocol.ParseBootstrapVersion(args[2])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}

		wait := len(args) == 5
		if wait && args[4] != "--wait" {
			println(ctx, styledErrorf("Unknown bootstrap create option '%s'", args[4]))
			return
		}
		jailsBootstrapCreate(ctx, jailServiceInterfaces.BootstrapRequest{
			Pool:  args[1],
			Major: major,
			Minor: minor,
			Type:  strings.ToLower(strings.TrimSpace(args[3])),
		}, wait, jsonMode)

	case "delete":
		if len(args) != 3 {
			println(ctx, styledErrorf("Usage: jails bootstrap delete <pool> <name>"))
			return
		}
		jailsBootstrapDelete(ctx, args[1], args[2], jsonMode)

	default:
		println(ctx, styledErrorf("Unknown jails bootstrap command: '%s'. Type 'jails bootstrap' for help.", args[0]))
	}
}

func jailBootstrapName(major, minor int, bootstrapType string) string {
	for _, spec := range jailServiceInterfaces.BootstrapTypes {
		if spec.Type == bootstrapType {
			return fmt.Sprintf(spec.Name, major, minor)
		}
	}

	return fmt.Sprintf("%d.%d-%s", major, minor, bootstrapType)
}

func jailsBootstrapList(ctx *Context, pool string, jsonMode bool) {
	entries, err := listBootstraps(ctx, pool)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching bootstraps", err)
		return
	}

	if jsonMode {
		if entries == nil {
			entries = []jailServiceInterfaces.BootstrapEntry{}
		}
		println(ctx, mustJSON(entries))
		return
	}

	println(ctx, formatBootstraps(entries))
}

func listBootstraps(ctx *Context, pool string) ([]jailServiceInterfaces.BootstrapEntry, error) {
	if strings.TrimSpace(pool) == "" {
		return nil, fmt.Errorf("pool_required")
	}
	if ctx == nil || ctx.Jail == nil {
		return nil, fmt.Errorf("jail_service_unavailable")
	}

	entries, err := ctx.Jail.ListBootstraps(context.Background(), pool)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_bootstraps: %w", err)
	}
	return entries, nil
}

func jailsBootstrapCreate(ctx *Context, request jailServiceInterfaces.BootstrapRequest, wait, jsonMode bool) {
	response, err := createBootstrap(ctx, request, wait)
	if err != nil {
		if jsonMode {
			if response.Entry != nil {
				println(ctx, mustJSON(response.Entry))
			} else {
				println(ctx, mustJSON(errorResponse{Error: err.Error()}))
			}
		} else {
			println(ctx, styledErrorf("Error starting bootstrap: %v", err))
		}
		return
	}

	if response.Entry != nil {
		if jsonMode {
			println(ctx, mustJSON(response.Entry))
		} else {
			println(ctx, styledSuccessf("Bootstrap %s completed successfully.", response.Entry.Name))
		}
		return
	}

	result := *response.Requested
	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Bootstrap %s requested. Use 'jails bootstrap list %s' to track progress.", result.Name, result.Pool))
	}
}

func createBootstrap(
	ctx *Context,
	request jailServiceInterfaces.BootstrapRequest,
	wait bool,
) (jailBootstrapCreateResponse, error) {
	if strings.TrimSpace(request.Pool) == "" || strings.TrimSpace(request.Type) == "" {
		return jailBootstrapCreateResponse{}, fmt.Errorf("invalid_bootstrap_request")
	}
	if ctx == nil || ctx.Jail == nil {
		return jailBootstrapCreateResponse{}, fmt.Errorf("jail_service_unavailable")
	}
	if err := ctx.Jail.CreateBootstrap(context.Background(), request); err != nil {
		return jailBootstrapCreateResponse{}, fmt.Errorf("failed_to_create_bootstrap: %w", err)
	}

	if wait {
		entry, err := waitForJailBootstrap(ctx, request)
		if err != nil {
			if entry.Name != "" {
				return jailBootstrapCreateResponse{Entry: &entry}, err
			}
			return jailBootstrapCreateResponse{}, err
		}
		return jailBootstrapCreateResponse{Entry: &entry}, nil
	}

	return jailBootstrapCreateResponse{Requested: &jailBootstrapCreateResult{
		Requested: true,
		Pool:      request.Pool,
		Name:      jailBootstrapName(request.Major, request.Minor, request.Type),
		Major:     request.Major,
		Minor:     request.Minor,
		Type:      request.Type,
	}}, nil
}

func waitForJailBootstrap(ctx *Context, request jailServiceInterfaces.BootstrapRequest) (jailServiceInterfaces.BootstrapEntry, error) {
	timeout := time.NewTimer(jailBootstrapWaitTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(jailBootstrapWaitPollInterval)
	defer ticker.Stop()

	for {
		entries, err := listBootstraps(ctx, request.Pool)
		if err != nil {
			return jailServiceInterfaces.BootstrapEntry{}, fmt.Errorf("failed_to_list_bootstraps: %w", err)
		}

		for _, entry := range entries {
			if entry.Major != request.Major || entry.Minor != request.Minor || entry.Type != request.Type {
				continue
			}

			switch entry.Status {
			case "completed":
				return entry, nil
			case "failed":
				if entry.Error == "" {
					return entry, fmt.Errorf("bootstrap_failed")
				}
				return entry, fmt.Errorf("bootstrap_failed: %s", entry.Error)
			}
		}

		select {
		case <-timeout.C:
			return jailServiceInterfaces.BootstrapEntry{}, fmt.Errorf(
				"bootstrap_wait_timed_out: %s",
				jailBootstrapName(request.Major, request.Minor, request.Type),
			)
		case <-ticker.C:
		}
	}
}

func jailsBootstrapDelete(ctx *Context, pool, name string, jsonMode bool) {
	result, err := deleteBootstrap(ctx, pool, name)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting bootstrap", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Bootstrap %s deleted successfully.", name))
	}
}

func deleteBootstrap(ctx *Context, pool, name string) (jailBootstrapDeleteResult, error) {
	if strings.TrimSpace(pool) == "" || strings.TrimSpace(name) == "" {
		return jailBootstrapDeleteResult{}, fmt.Errorf("pool_and_name_required")
	}
	if ctx == nil || ctx.Jail == nil {
		return jailBootstrapDeleteResult{}, fmt.Errorf("jail_service_unavailable")
	}
	if err := ctx.Jail.DeleteBootstrap(context.Background(), pool, name); err != nil {
		return jailBootstrapDeleteResult{}, fmt.Errorf("failed_to_delete_bootstrap: %w", err)
	}
	return jailBootstrapDeleteResult{Deleted: true, Pool: pool, Name: name}, nil
}

func jailsList(ctx *Context, jsonMode bool) {
	jails, err := listJails(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching jails", err)
		return
	}

	if jsonMode {
		if jails == nil {
			jails = []jailServiceInterfaces.SimpleList{}
		}
		println(ctx, mustJSON(jails))
		return
	}

	println(ctx, formatJailList(jails))
}

func jailsGet(ctx *Context, ctID uint, jsonMode bool) {
	jail, err := getJail(ctx, ctID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching jail", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(jail))
		return
	}
	println(ctx, formatJailDetails(jail))
}

func requestJailAction(ctx *Context, ctid uint, action string) (jailActionResult, error) {
	if err := validateJailCTID(ctid); err != nil {
		return jailActionResult{}, err
	}
	if action != "start" && action != "stop" && action != "restart" {
		return jailActionResult{}, fmt.Errorf("invalid_jail_action")
	}
	if ctx == nil || ctx.Lifecycle == nil {
		return jailActionResult{}, fmt.Errorf("lifecycle_service_unavailable")
	}

	task, outcome, err := ctx.Lifecycle.RequestAction(
		context.Background(), "jail", ctid, action, "user", "console",
	)
	if err != nil {
		return jailActionResult{}, fmt.Errorf("failed_to_%s_jail: %w", action, err)
	}

	result := jailActionResult{
		CTID:    ctid,
		Action:  action,
		Outcome: outcome,
	}
	if task != nil {
		result.TaskID = task.ID
	}
	return result, nil
}

func jailsAction(ctx *Context, ctID uint, action string, jsonMode bool) {
	result, err := requestJailAction(ctx, ctID, action)
	if err != nil {
		printOperationError(ctx, jsonMode, fmt.Sprintf("Error %s jail", action), err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Jail %d: %s %s (Task: %d)", ctID, action, result.Outcome, result.TaskID))
	}
}

func requestJailActionAll(ctx *Context, action string) ([]jailActionResult, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return nil, fmt.Errorf("invalid_jail_action")
	}
	if ctx == nil || ctx.Lifecycle == nil {
		return nil, fmt.Errorf("lifecycle_service_unavailable")
	}

	jails, err := listJails(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]jailActionResult, 0, len(jails))
	for _, j := range jails {
		r := jailActionResult{
			CTID:   j.CTID,
			Name:   j.Name,
			Action: action,
		}
		task, outcome, err := ctx.Lifecycle.RequestAction(
			context.Background(), "jail", j.CTID, action, "user", "console",
		)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Outcome = outcome
			if task != nil {
				r.TaskID = task.ID
			}
		}
		results = append(results, r)
	}
	return results, nil
}

func jailsActionAll(ctx *Context, action string, jsonMode bool) {
	results, err := requestJailActionAll(ctx, action)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching jails", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(results))
		return
	}
	println(ctx, formatJailActionAll(results))
}

func jailsDelete(ctx *Context, ctID uint, purge, jsonMode bool) {
	result, err := deleteJail(ctx, ctID, purge)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting jail", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Jail %d deleted successfully.", ctID))
	}
}

func deleteJail(ctx *Context, ctid uint, purge bool) (jailDeleteResult, error) {
	if err := validateJailCTID(ctid); err != nil {
		return jailDeleteResult{}, err
	}
	if ctx == nil || ctx.Jail == nil {
		return jailDeleteResult{}, fmt.Errorf("jail_service_unavailable")
	}
	if err := ctx.Jail.DeleteJail(context.Background(), ctid, true, purge); err != nil {
		return jailDeleteResult{}, fmt.Errorf("failed_to_delete_jail: %w", err)
	}
	return jailDeleteResult{Deleted: true, CTID: ctid}, nil
}

func jailsNetworksList(ctx *Context, ctID uint, jsonMode bool) {
	targetJail, err := getJail(ctx, ctID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching jail", err)
		return
	}

	networks := targetJail.Networks

	if jsonMode {
		if networks == nil {
			networks = []jailModels.Network{}
		}
		println(ctx, mustJSON(networks))
		return
	}

	println(ctx, formatJailNetworks(targetJail))
}

func jailsRemoveNetwork(ctx *Context, ctID, netID uint, jsonMode bool) {
	result, err := removeJailNetwork(ctx, ctID, netID)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error removing network", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Network %d removed from jail %d.", netID, ctID))
	}
}

func removeJailNetwork(ctx *Context, ctid, networkID uint) (jailNetworkDeleteResult, error) {
	if err := validateJailCTID(ctid); err != nil {
		return jailNetworkDeleteResult{}, err
	}
	if networkID == 0 {
		return jailNetworkDeleteResult{}, fmt.Errorf("invalid_network_id")
	}

	targetJail, err := getJail(ctx, ctid)
	if err != nil {
		return jailNetworkDeleteResult{}, err
	}
	for _, network := range targetJail.Networks {
		if network.ID == networkID {
			if err := ctx.Jail.DeleteNetwork(ctid, networkID); err != nil {
				return jailNetworkDeleteResult{}, fmt.Errorf("failed_to_remove_network: %w", err)
			}
			return jailNetworkDeleteResult{Deleted: true, CTID: ctid, NetworkID: networkID}, nil
		}
	}

	return jailNetworkDeleteResult{}, fmt.Errorf("network_not_found")
}
