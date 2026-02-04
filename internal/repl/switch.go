package repl

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

func handleSwitches(ctx *Context, args []string) {
	if len(args) == 0 {
		printSubHelp("switches", []cmdHelp{
			{"list", "List all switches (Standard & Manual)"},
			{"rm <type> <id>", "Delete a switch (type: 'std' or 'manual')"},
		})
		return
	}

	subCmd := args[0]
	subArgs := args[1:]

	switch subCmd {
	case "list":
		switchesList(ctx)

	case "rm":
		if len(subArgs) < 2 {
			fmt.Println("Error: Missing arguments. Usage: switches rm <type> <id>")
			fmt.Println("       <type> must be 'std' or 'manual'")
			return
		}

		swType := subArgs[0]
		idStr := subArgs[1]

		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			fmt.Printf("Error: Invalid ID '%s'\n", idStr)
			return
		}

		if swType != "std" && swType != "manual" {
			fmt.Printf("Error: Unknown switch type '%s'. Use 'std' or 'manual'.\n", swType)
			return
		}

		switchDelete(ctx, swType, uint(id))

	default:
		fmt.Printf("Unknown switches command: '%s'. Type 'switches' for help.\n", subCmd)
	}
}

func switchesList(ctx *Context) {
	stdSwitches, err := ctx.Network.GetStandardSwitches()
	if err != nil {
		fmt.Printf("Error fetching standard switches: %v\n", err)
		return
	}

	manualSwitches, err := ctx.Network.GetManualSwitches()
	if err != nil {
		fmt.Printf("Error fetching manual switches: %v\n", err)
		return
	}

	if len(stdSwitches) == 0 && len(manualSwitches) == 0 {
		fmt.Println("No switches found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)

	fmt.Fprintln(w, "ID\tNAME\tTYPE\tBRIDGE\tVLAN\tPORTS/DETAILS")
	fmt.Fprintln(w, "--\t----\t----\t------\t----\t-------------")

	for _, s := range stdSwitches {
		portNames := []string{}
		for _, p := range s.Ports {
			portNames = append(portNames, p.Name)
		}
		portsStr := strings.Join(portNames, ",")
		if portsStr == "" {
			portsStr = "-"
		}

		vlanStr := "-"
		if s.VLAN > 0 {
			vlanStr = fmt.Sprintf("%d", s.VLAN)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, s.Name, "STD", s.BridgeName, vlanStr, portsStr)
	}

	for _, m := range manualSwitches {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.Name, "MANUAL", m.Bridge, "-", "(External)")
	}

	w.Flush()
	fmt.Println("")
}

func switchDelete(ctx *Context, swType string, id uint) {
	var err error
	if swType == "std" {
		fmt.Printf("Deleting Standard Switch ID %d...\n", id)
		err = ctx.Network.DeleteStandardSwitch(int(id))
	} else {
		fmt.Printf("Deleting Manual Switch ID %d...\n", id)
		err = ctx.Network.DeleteManualSwitch(id)
	}

	if err != nil {
		if strings.Contains(err.Error(), "switch_in_use") {
			fmt.Println("Error: Cannot delete switch because it is currently attached to a VM or Jail.")
			return
		}
		fmt.Printf("Failed to delete switch: %v\n", err)
	} else {
		fmt.Println("Switch deleted successfully.")
	}
}
