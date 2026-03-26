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
	"text/tabwriter"
)

func handleUsers(ctx *Context, args []string) {
	if len(args) == 0 {
		printSubHelp(ctx, "users", []cmdHelp{
			{"list", "List all registered users"},
		})
		return
	}

	subCmd := args[0]

	switch subCmd {
	case "list":
		usersList(ctx)
	default:
		printf(ctx, "Unknown users command: '%s'\n", subCmd)
	}
}

func usersList(ctx *Context) {
	users, err := ctx.Auth.ListUsers()
	if err != nil {
		printf(ctx, "Error fetching users: %v\n", err)
		return
	}

	if len(users) == 0 {
		println(ctx, "No users found")
		return
	}

	w := tabwriter.NewWriter(outputWriter(ctx), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUSERNAME\tEMAIL")
	fmt.Fprintln(w, "--\t--------\t-----")

	for _, u := range users {
		if u.Email == "" {
			u.Email = "-"
		}

		fmt.Fprintf(w, "%d\t%s\t%s\n", u.ID, u.Username, u.Email)
	}

	w.Flush()
	println(ctx, "")
}
