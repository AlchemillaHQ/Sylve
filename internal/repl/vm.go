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
	"text/tabwriter"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func handleVms(ctx *Context, args []string) {
	if len(args) == 0 {
		printSubHelp(ctx, "vms", []cmdHelp{
			{"list", "List all VMs"},
			{"networks <rid>", "List networks for a specific VM (by RID)"},
			{"rmnet <rid> <net_id>", "Remove a network from a VM"},
		})
		return
	}

	subCmd := args[0]
	subArgs := args[1:]

	switch subCmd {
	case "list":
		vmsList(ctx)

	case "networks":
		if len(subArgs) < 1 {
			println(ctx, "Error: Missing VM RID. Usage: vms networks <rid>")
			return
		}

		rid, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid RID '%s'\n", subArgs[0])
			return
		}

		vmsNetworksList(ctx, uint(rid))

	case "rmnet":
		if len(subArgs) < 2 {
			println(ctx, "Error: Missing arguments. Usage: vms rmnet <rid> <network_id>")
			return
		}

		rid, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid RID '%s'\n", subArgs[0])
			return
		}

		netID, err := strconv.ParseUint(subArgs[1], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid network ID '%s'\n", subArgs[1])
			return
		}

		vmRemoveNetwork(ctx, uint(rid), uint(netID))

	default:
		printf(ctx, "Unknown vms command: '%s'. Type 'vms' for help.\n", subCmd)
	}
}

func vmsList(ctx *Context) {
	vms, err := ctx.VirtualMachine.ListVMs()
	if err != nil {
		printf(ctx, "Error fetching VMs: %v\n", err)
		return
	}

	if len(vms) == 0 {
		println(ctx, "No VMs found.")
		return
	}

	w := tabwriter.NewWriter(outputWriter(ctx), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "RID\tNAME\tNETWORKS")
	fmt.Fprintln(w, "---\t----\t--------")

	for _, v := range vms {
		netCount := fmt.Sprintf("%d", len(v.Networks))
		fmt.Fprintf(w, "%d\t%s\t%s\n", v.RID, v.Name, netCount)
	}

	w.Flush()
	println(ctx, "")
}

func vmsNetworksList(ctx *Context, rid uint) {
	vms, err := ctx.VirtualMachine.ListVMs()
	if err != nil {
		printf(ctx, "Error fetching VMs: %v\n", err)
		return
	}

	var targetVM *vmModels.VM
	for i := range vms {
		if vms[i].RID == rid {
			targetVM = &vms[i]
			break
		}
	}

	if targetVM == nil {
		printf(ctx, "Error: VM with RID %d not found.\n", rid)
		return
	}

	if len(targetVM.Networks) == 0 {
		printf(ctx, "VM '%s' (RID: %d) has no networks configured.\n", targetVM.Name, targetVM.RID)
		return
	}

	printf(ctx, "Networks for VM: %s (RID: %d)\n", targetVM.Name, targetVM.RID)

	w := tabwriter.NewWriter(outputWriter(ctx), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NET ID\tSWITCH\tTYPE\tEMUL\tMAC")
	fmt.Fprintln(w, "------\t------\t----\t----\t---")

	for _, n := range targetVM.Networks {
		mac := "auto"
		if n.AddressObj != nil && len(n.AddressObj.Entries) > 0 {
			mac = n.AddressObj.Entries[0].Value
		}

		switchName := fmt.Sprintf("%d", n.SwitchID)
		if n.SwitchType == "standard" && n.StandardSwitch != nil {
			switchName = n.StandardSwitch.Name
		} else if n.SwitchType == "manual" && n.ManualSwitch != nil {
			switchName = n.ManualSwitch.Name
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			n.ID, switchName, n.SwitchType, n.Emulation, mac)
	}

	w.Flush()
	println(ctx, "")
}

func vmRemoveNetwork(ctx *Context, rid uint, netID uint) {
	inactive, err := ctx.VirtualMachine.IsDomainInactive(rid)
	if err != nil {
		printf(ctx, "Error checking VM status: %v\n", err)
		return
	}
	if !inactive {
		println(ctx, "Error: VM must be powered off to remove networks.")
		return
	}

	printf(ctx, "Removing Network %d from VM RID %d...\n", netID, rid)

	err = ctx.VirtualMachine.NetworkDetach(rid, netID)

	if err != nil {
		printf(ctx, "Failed to delete network: %v\n", err)
	} else {
		println(ctx, "Network deleted successfully.")
	}
}
