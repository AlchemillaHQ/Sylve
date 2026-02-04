package repl

import (
	"fmt"
	"os"
	"text/tabwriter"
)

func handleUsers(ctx *Context, args []string) {
	if len(args) == 0 {
		printSubHelp("users", []cmdHelp{
			{"list", "List all registered users"},
		})
		return
	}

	subCmd := args[0]

	switch subCmd {
	case "list":
		usersList(ctx)
	default:
		fmt.Printf("Unknown users command: '%s'\n", subCmd)
	}
}

func usersList(ctx *Context) {
	users, err := ctx.Auth.ListUsers()
	if err != nil {
		fmt.Printf("Error fetching users: %v\n", err)
		return
	}

	if len(users) == 0 {
		fmt.Println("No users found")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUSERNAME\tEMAIL")
	fmt.Fprintln(w, "--\t--------\t-----")

	for _, u := range users {
		if u.Email == "" {
			u.Email = "-"
		}

		fmt.Fprintf(w, "%d\t%s\t%s\n", u.ID, u.Username, u.Email)
	}

	w.Flush()
	fmt.Println("")
}
