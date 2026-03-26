// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"fmt"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/alchemillahq/sylve/internal/logger"
)

type cmdHelp struct {
	Name string
	Desc string
}

var commands = []cmdHelp{
	{"help", "Show this help message"},
	{"ping", "Check server connectivity"},
	{"users", "Manage system users (list)"},
	{"jails", "Manage Jails"},
	{"vms", "Manage Virtual Machines"},
	{"switches", "Manage manual/standard switches"},
	{"quit/exit", "Exit console session"},
	{"shutdown", "Shutdown Sylve"},
}

func ExecuteLine(ctx *Context, line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}

	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}

	head := parts[0]
	args := parts[1:]

	switch head {
	case "users":
		handleUsers(ctx, args)

	case "jails":
		handleJails(ctx, args)

	case "vms":
		handleVms(ctx, args)

	case "switches":
		handleSwitches(ctx, args)

	case "help":
		printHelp(ctx)

	case "ping":
		println(ctx, "pong")

	case "quit", "exit":
		return false

	case "shutdown":
		logger.L.Info().Msg("Shutdown initiated from REPL")
		if ctx != nil && ctx.QuitChan != nil {
			ctx.QuitChan <- syscall.SIGTERM
		}
		return false

	default:
		printf(ctx, "Unknown command: '%s'. Type 'help'.\n", head)
	}

	return true
}

func printHelp(ctx *Context) {
	w := tabwriter.NewWriter(outputWriter(ctx), 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "COMMAND\tDESCRIPTION")
	fmt.Fprintln(w, "-------\t-----------")

	for _, cmd := range commands {
		fmt.Fprintf(w, "  %s\t%s\n", cmd.Name, cmd.Desc)
	}

	fmt.Fprintln(w, "")
	w.Flush()
}

func printSubHelp(ctx *Context, title string, cmds []cmdHelp) {
	w := tabwriter.NewWriter(outputWriter(ctx), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\n--- %s ---\n", strings.ToUpper(title))
	for _, cmd := range cmds {
		fmt.Fprintf(w, "  %s\t%s\n", cmd.Name, cmd.Desc)
	}
	fmt.Fprintln(w, "")
	w.Flush()
}
