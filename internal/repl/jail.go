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
)

func handleJails(ctx *Context, args []string) {
	if len(args) == 0 {
		printSubHelp(ctx, "jails", []cmdHelp{
			{"list", "List all Jails"},
			{"networks <ctid>", "List networks for a specific jail (by CTID)"},
			{"rmnet <ctid> <net_id>", "Remove a network from a jail"},
		})
		return
	}

	subCmd := args[0]
	subArgs := args[1:]

	switch subCmd {
	case "list":
		jailsList(ctx)

	case "networks":
		if len(subArgs) < 1 {
			println(ctx, "Error: Missing jail CTID. Usage: jails networks <ctid>")
			return
		}

		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid CTID '%s'\n", subArgs[0])
			return
		}

		jailsNetworksList(ctx, uint(ctID))

	case "rmnet":
		if len(subArgs) < 2 {
			println(ctx, "Error: Missing arguments. Usage: jails rmnet <ctid> <network_id>")
			return
		}

		ctID, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid CTID '%s'\n", subArgs[0])
			return
		}

		netID, err := strconv.ParseUint(subArgs[1], 10, 64)
		if err != nil {
			printf(ctx, "Error: Invalid network ID '%s'\n", subArgs[1])
			return
		}

		jailRemoveNetwork(ctx, uint(ctID), uint(netID))

	default:
		printf(ctx, "Unknown jails command: '%s'. Type 'jails' for help.\n", subCmd)
	}
}

func jailsList(ctx *Context) {
	jails, err := ctx.Jail.GetJails()
	if err != nil {
		printf(ctx, "Error fetching jails: %v\n", err)
		return
	}

	if len(jails) == 0 {
		println(ctx, "No jails found.")
		return
	}

	w := tabwriter.NewWriter(outputWriter(ctx), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "CTID\tNAME\tTYPE\tIP4\tNETWORKS")
	fmt.Fprintln(w, "----\t----\t----\t---\t--------")

	for _, j := range jails {
		ip4Status := "VNET"
		if j.InheritIPv4 {
			ip4Status = "Inherit"
		}

		netCount := fmt.Sprintf("%d", len(j.Networks))
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			j.CTID, j.Name, j.Type, ip4Status, netCount)
	}

	w.Flush()
	println(ctx, "")
}

func jailsNetworksList(ctx *Context, ctID uint) {
	targetJail, err := ctx.Jail.GetJailByCTID(ctID)
	if err != nil {
		printf(ctx, "Error: Could not fetch jail with CTID %d: %v\n", ctID, err)
		return
	}

	if len(targetJail.Networks) == 0 {
		printf(ctx, "Jail '%s' (CTID: %d) has no networks configured.\n", targetJail.Name, targetJail.CTID)
		return
	}

	printf(ctx, "Networks for Jail: %s (CTID: %d)\n", targetJail.Name, targetJail.CTID)

	w := tabwriter.NewWriter(outputWriter(ctx), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NET ID\tNAME\tSWITCH\tTYPE\tDHCP\tMAC")
	fmt.Fprintln(w, "------\t----\t------\t----\t----\t---")

	for _, n := range targetJail.Networks {
		mac := "auto"
		if n.MacAddressObj != nil && len(n.MacAddressObj.Entries) > 0 {
			mac = n.MacAddressObj.Entries[0].Value
		}

		fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%t\t%s\n",
			n.ID, n.Name, n.SwitchID, n.SwitchType, n.DHCP, mac)
	}

	w.Flush()
	println(ctx, "")
}

func jailRemoveNetwork(ctx *Context, ctID uint, netID uint) {
	targetJail, err := ctx.Jail.GetJailByCTID(ctID)
	if err != nil {
		printf(ctx, "Error: Jail CTID %d not found.\n", ctID)
		return
	}

	found := false

	for _, network := range targetJail.Networks {
		if network.ID == netID {
			found = true
		}
	}

	if !found {
		printf(ctx, "Network with ID %d not found for Jail %s (CTID: %d)\n", netID, targetJail.Name, ctID)
		return
	}

	printf(ctx, "Removing Network %d from Jail %s (CTID: %d)\n", netID, targetJail.Name, ctID)

	err = ctx.Jail.DeleteNetwork(ctID, netID)

	if err != nil {
		printf(ctx, "Failed to delete network: %v\n", err)
	} else {
		println(ctx, "Network deleted successfully.")
	}
}
