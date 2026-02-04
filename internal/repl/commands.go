package repl

import (
	"fmt"
	"os"
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
	{"quit/exit/shutdown", "Shutdown Sylve"},
}

func handle(ctx *Context, line string) bool {
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
		printHelp()

	case "ping":
		fmt.Println("pong")

	case "quit", "exit", "shutdown":
		logger.L.Info().Msg("Shutdown initiated from REPL")
		ctx.QuitChan <- syscall.SIGTERM
		return false

	default:
		fmt.Printf("Unknown command: '%s'. Type 'help'.\n", head)
	}

	return true
}

func printHelp() {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "COMMAND\tDESCRIPTION")
	fmt.Fprintln(w, "-------\t-----------")

	for _, cmd := range commands {
		fmt.Fprintf(w, "  %s\t%s\n", cmd.Name, cmd.Desc)
	}

	fmt.Fprintln(w, "")
	w.Flush()
}

func printSubHelp(title string, cmds []cmdHelp) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\n--- %s ---\n", strings.ToUpper(title))
	for _, cmd := range cmds {
		fmt.Fprintf(w, "  %s\t%s\n", cmd.Name, cmd.Desc)
	}
	fmt.Fprintln(w, "")
	w.Flush()
}
