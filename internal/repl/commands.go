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
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/alchemillahq/sylve/internal/logger"
)

type cmdHelp struct {
	Name string
	Desc string
}

func parsePositiveUint(value string) (uint, error) {
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || parsed == 0 || parsed > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid_positive_integer")
	}
	return uint(parsed), nil
}

var commands = []cmdHelp{
	{"help", "Show this help message"},
	{"ping", "Check server connectivity"},
	{"datacenter", "Manage replicated datacenter state"},
	{"notes", "Manage notes"},
	{"jails", "Manage jails"},
	{"vms", "Manage virtual machines"},
	{"tasks", "Inspect lifecycle tasks"},
	{"switches", "Manage network switches"},
	{"objects", "Manage network objects"},
	{"downloads", "Manage downloads"},
	{"quit/exit", "Exit console session"},
	{"shutdown", "Shutdown Sylve"},
}

func ExecuteLine(ctx *Context, line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}

	parts, err := splitCommandLine(line)
	if err != nil {
		println(ctx, styledErrorf("Invalid command: %v", err))
		return true
	}
	if len(parts) == 0 {
		return true
	}

	head := parts[0]
	args := parts[1:]
	if rawCommandRequiresMutation(parts) && ctx != nil && ctx.Cluster != nil {
		admittedCtx, release, err := ctx.Cluster.EnterMutation(operationContext(ctx))
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return true
		}
		previous := ctx.mutationContext
		ctx.mutationContext = admittedCtx
		defer func() {
			ctx.mutationContext = previous
			release()
		}()
	}

	switch head {
	case "datacenter":
		handleDatacenter(ctx, args)

	case "notes":
		handleNotes(ctx, args)

	case "jails":
		handleJails(ctx, args)

	case "vms":
		handleVms(ctx, args)

	case "tasks":
		handleTasks(ctx, args)

	case "switches":
		handleSwitches(ctx, args)

	case "objects":
		handleObjects(ctx, args)

	case "downloads":
		handleDownloads(ctx, args)

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

func rawCommandRequiresMutation(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	head := strings.ToLower(strings.TrimSpace(parts[0]))
	args := parts[1:]
	if head == "help" || head == "ping" || head == "quit" || head == "exit" || head == "shutdown" {
		return false
	}
	if len(args) == 0 {
		switch head {
		case "datacenter", "notes", "jails", "vms", "tasks", "switches", "objects", "downloads":
			return false
		}
	}
	sub := ""
	if len(args) != 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch head {
	case "datacenter":
		if len(args) < 2 {
			return false
		}
		group := strings.ToLower(strings.TrimSpace(args[0]))
		action := strings.ToLower(strings.TrimSpace(args[1]))
		return !((group == "notes" && (action == "list" || action == "get")) ||
			(group == "cluster" && (action == "status" || action == "members")))
	case "notes":
		return sub != "list" && sub != "get"
	case "jails":
		return sub != "list" && sub != "get" && sub != "networks" &&
			!(sub == "bootstrap" && len(args) > 1 && strings.EqualFold(args[1], "list"))
	case "vms":
		if sub == "list" || sub == "get" {
			return false
		}
		if _, err := parsePositiveUint(sub); err == nil && len(args) == 1 {
			return false
		}
		if len(args) > 1 {
			action := strings.ToLower(strings.TrimSpace(args[1]))
			return !((sub == "storage" || sub == "snapshots" || sub == "network") && action == "list") &&
				!(sub == "templates" && (action == "list" || action == "get")) &&
				!(sub == "qga" && action == "info")
		}
	case "tasks":
		return sub != "active" && sub != "recent" && sub != "get"
	case "switches", "objects", "downloads":
		return sub != "list"
	}
	return true
}

// splitCommandLine supports quoting and escaping without invoking a shell.
func splitCommandLine(line string) ([]string, error) {
	var args []string
	var token strings.Builder
	var quote rune
	escaped := false
	tokenStarted := false

	appendToken := func() {
		if tokenStarted {
			args = append(args, token.String())
			token.Reset()
			tokenStarted = false
		}
	}

	for _, r := range line {
		if escaped {
			token.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}

		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case quote == '"' && r == '\\':
				escaped = true
			default:
				token.WriteRune(r)
			}
			continue
		}

		switch {
		case r == '\\':
			escaped = true
			tokenStarted = true
		case r == '\'' || r == '"':
			quote = r
			tokenStarted = true
		case unicode.IsSpace(r):
			appendToken()
		default:
			token.WriteRune(r)
			tokenStarted = true
		}
	}

	if escaped {
		return nil, fmt.Errorf("unterminated escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}

	appendToken()
	return args, nil
}

func printHelp(ctx *Context) {
	println(ctx, styledHelpList(commands))
}

func printSubHelp(ctx *Context, title string, cmds []cmdHelp) {
	println(ctx, "")
	println(ctx, keyStyle.Render(strings.ToUpper(title)))
	println(ctx, styledHelpList(cmds))
}
