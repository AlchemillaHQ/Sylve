// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"strings"
	"syscall"

	"github.com/alchemillahq/sylve/internal/logger"
)

type cmdHelp struct {
	Name string
	Desc string
}

var commands = []cmdHelp{
	{"help", "Show this help message"},
	{"ping", "Check server connectivity"},
	{"notes", "Manage notes"},
	{"jails", "Manage jails"},
	{"vms", "Manage virtual machines"},
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
	case "notes":
		handleNotes(ctx, args)

	case "jails":
		handleJails(ctx, args)

	case "vms":
		handleVms(ctx, args)

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
	println(ctx, styledHelpList(commands))
}

func printSubHelp(ctx *Context, title string, cmds []cmdHelp) {
	println(ctx, "")
	println(ctx, keyStyle.Render(strings.ToUpper(title)))
	println(ctx, styledHelpList(cmds))
}
