package repl

import (
	"context"
	"strconv"

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

func handleJails(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "jails", []cmdHelp{
			{"list", "List all jails"},
			{"get <ctid>", "Get jail details"},
			{"start <ctid|all>", "Start a jail (or all)"},
			{"stop <ctid|all>", "Stop a jail (or all)"},
			{"restart <ctid|all>", "Restart a jail (or all)"},
			{"delete <ctid>", "Delete a jail"},
			{"networks <ctid>", "List networks for a jail"},
			{"rmnet <ctid> <net_id>", "Remove a network from a jail"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch subCmd {
	case "list":
		jailsList(ctx, jsonMode)

	case "get":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: jails get <ctid>"))
			return
		}
		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		jailsGet(ctx, uint(ctID), jsonMode)

	case "start", "stop", "restart":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: jails %s <ctid|all>", subCmd))
			return
		}
		if subArgs[0] == "all" {
			jailsActionAll(ctx, subCmd, jsonMode)
		} else {
			ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
			if err != nil {
				println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
				return
			}
			jailsAction(ctx, uint(ctID), subCmd, jsonMode)
		}

	case "delete":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: jails delete <ctid>"))
			return
		}
		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		jailsDelete(ctx, uint(ctID), jsonMode)

	case "networks":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: jails networks <ctid>"))
			return
		}
		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		jailsNetworksList(ctx, uint(ctID), jsonMode)

	case "rmnet":
		if len(subArgs) < 2 {
			println(ctx, styledErrorf("Usage: jails rmnet <ctid> <net_id>"))
			return
		}
		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid CTID '%s'", subArgs[0]))
			return
		}
		netID, err := strconv.ParseUint(subArgs[1], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid network ID '%s'", subArgs[1]))
			return
		}
		jailsRemoveNetwork(ctx, uint(ctID), uint(netID), jsonMode)

	default:
		println(ctx, styledErrorf("Unknown jails command: '%s'. Type 'jails' for help.", subCmd))
	}
}

func jailsList(ctx *Context, jsonMode bool) {
	jails, err := ctx.Jail.GetJailsSimple()
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching jails: %v", err))
		}
		return
	}

	if jsonMode {
		if jails == nil {
			jails = []jailServiceInterfaces.SimpleList{}
		}
		println(ctx, mustJSON(jails))
		return
	}

	if len(jails) == 0 {
		println(ctx, "No jails found.")
		return
	}

	headers := []string{"CTID", "Name", "State"}
	var rows [][]string
	for _, j := range jails {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(j.CTID), 10),
			j.Name,
			j.State,
		})
	}
	println(ctx, styledTable(headers, rows))
}

func jailsGet(ctx *Context, ctID uint, jsonMode bool) {
	jail, err := ctx.Jail.GetJailByCTID(ctID)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching jail: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(jail))
	} else {
		println(ctx, styledKeyValue("CTID:", strconv.FormatUint(uint64(jail.CTID), 10)))
		println(ctx, styledKeyValue("Name:", jail.Name))
		println(ctx, styledKeyValue("Hostname:", jail.Hostname))
		println(ctx, styledKeyValue("Type:", string(jail.Type)))
		println(ctx, styledKeyValue("Description:", jail.Description))
		println(ctx, styledKeyValue("Cores:", strconv.Itoa(jail.Cores)))
		println(ctx, styledKeyValue("Memory:", strconv.Itoa(jail.Memory)))
		if jail.StartedAt != nil {
			println(ctx, styledKeyValue("Started:", jail.StartedAt.Format("2006-01-02 15:04")))
		}
		if jail.StoppedAt != nil {
			println(ctx, styledKeyValue("Stopped:", jail.StoppedAt.Format("2006-01-02 15:04")))
		}
	}
}

func jailsAction(ctx *Context, ctID uint, action string, jsonMode bool) {
	task, outcome, err := ctx.Lifecycle.RequestAction(
		context.Background(), "jail", ctID, action, "user", "console",
	)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error %s jail: %v", action, err))
		}
		return
	}

	result := jailActionResult{
		CTID:    ctID,
		Action:  action,
		Outcome: outcome,
	}
	if task != nil {
		result.TaskID = task.ID
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Jail %d: %s %s (Task: %d)", ctID, action, outcome, result.TaskID))
	}
}

func jailsActionAll(ctx *Context, action string, jsonMode bool) {
	jails, err := ctx.Jail.GetJailsSimple()
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching jails: %v", err))
		}
		return
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

	if jsonMode {
		println(ctx, mustJSON(results))
	} else if len(results) == 0 {
		println(ctx, "No jails found.")
		return
	} else {
		headers := []string{"CTID", "Name", "Outcome", "Task ID"}
		var rows [][]string
		for _, r := range results {
			taskID := ""
			if r.TaskID > 0 {
				taskID = strconv.FormatUint(uint64(r.TaskID), 10)
			}
			outcome := r.Outcome
			if r.Error != "" {
				outcome = r.Error
			}
			rows = append(rows, []string{
				strconv.FormatUint(uint64(r.CTID), 10),
				r.Name,
				outcome,
				taskID,
			})
		}
		println(ctx, styledTable(headers, rows))
	}
}

func jailsDelete(ctx *Context, ctID uint, jsonMode bool) {
	err := ctx.Jail.DeleteJail(context.Background(), ctID, true, false)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error deleting jail: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(jailDeleteResult{Deleted: true, CTID: ctID}))
	} else {
		println(ctx, styledSuccessf("Jail %d deleted successfully.", ctID))
	}
}

func jailsNetworksList(ctx *Context, ctID uint, jsonMode bool) {
	targetJail, err := ctx.Jail.GetJailByCTID(ctID)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching jail: %v", err))
		}
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

	if len(networks) == 0 {
		println(ctx, styledErrorf("Jail '%s' (CTID: %d) has no networks configured.", targetJail.Name, targetJail.CTID))
		return
	}

	headers := []string{"ID", "Name", "Switch", "Type", "DHCP", "MAC"}
	var rows [][]string
	for _, n := range networks {
		mac := "auto"
		if n.MacAddressObj != nil && len(n.MacAddressObj.Entries) > 0 {
			mac = n.MacAddressObj.Entries[0].Value
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(n.ID), 10),
			n.Name,
			strconv.FormatUint(uint64(n.SwitchID), 10),
			n.SwitchType,
			strconv.FormatBool(n.DHCP),
			mac,
		})
	}
	println(ctx, styledTable(headers, rows))
}

func jailsRemoveNetwork(ctx *Context, ctID, netID uint, jsonMode bool) {
	targetJail, err := ctx.Jail.GetJailByCTID(ctID)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching jail: %v", err))
		}
		return
	}

	found := false
	for _, network := range targetJail.Networks {
		if network.ID == netID {
			found = true
			break
		}
	}

	if !found {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: "Network not found"}))
		} else {
			println(ctx, styledErrorf("Network ID %d not found for jail %s (CTID: %d)", netID, targetJail.Name, ctID))
		}
		return
	}

	err = ctx.Jail.DeleteNetwork(ctID, netID)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error removing network: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(struct {
			Deleted   bool `json:"deleted"`
			CTID      uint `json:"ctId"`
			NetworkID uint `json:"networkId"`
		}{
			Deleted:   true,
			CTID:      ctID,
			NetworkID: netID,
		}))
	} else {
		println(ctx, styledSuccessf("Network %d removed from jail %d.", netID, ctID))
	}
}
