// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func TestNewSwitchesCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newSwitchesCommand()
	want := map[string]bool{"list": false, "create": false, "delete": false, "edit": false}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected switches %s command", name)
		}
	}
}

func TestSwitchCreateCommandBuildsStandardPayload(t *testing.T) {
	command := newSwitchesCommand()
	var got consoleprotocol.SwitchCreatePayload
	for _, subcommand := range command.Commands {
		if subcommand.Name != "create" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			var err error
			got, err = buildSwitchCreatePayload(command)
			return err
		}
	}

	err := command.Run(context.Background(), []string{
		"switches", "create", "--type", "standard", "--name", "private-lan",
		"--network4", "7", "--ports", "igb0, igb1", "--mac-source", "port", "--mac-source-port", "igb0", "--private", "--dhcp=false", "--default-route", "--default-route6", "--disable-bridge-offloads",
	})
	if err != nil {
		t.Fatalf("run switch create command: %v", err)
	}
	if got.Type != "standard" || got.Standard == nil {
		t.Fatalf("unexpected payload: %#v", got)
	}
	if got.Standard.Name != "private-lan" || got.Standard.Network4 != 7 || !got.Standard.Private || got.Standard.DHCP {
		t.Fatalf("unexpected standard payload: %#v", got.Standard)
	}
	if !got.Standard.DisableBridgeOffloads {
		t.Fatalf("expected bridge offloads to be disabled: %#v", got.Standard)
	}
	if !got.Standard.DefaultRoute || !got.Standard.DefaultRoute6 {
		t.Fatalf("expected both route-owner flags: %#v", got.Standard)
	}
	if len(got.Standard.Ports) != 2 || got.Standard.Ports[1] != "igb1" {
		t.Fatalf("unexpected standard ports: %#v", got.Standard.Ports)
	}
	if got.Standard.BridgeMAC.Mode != "port" || got.Standard.BridgeMAC.Port != "igb0" {
		t.Fatalf("unexpected bridge MAC source: %#v", got.Standard.BridgeMAC)
	}
}

func TestSwitchCreateCommandBuildsManualPayload(t *testing.T) {
	command := newSwitchesCommand()
	var got consoleprotocol.SwitchCreatePayload
	for _, subcommand := range command.Commands {
		if subcommand.Name != "create" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			var err error
			got, err = buildSwitchCreatePayload(command)
			return err
		}
	}

	err := command.Run(context.Background(), []string{
		"switches", "create", "--type", "manual", "--name", "uplink", "--bridge", "bridge0",
	})
	if err != nil {
		t.Fatalf("run manual switch create command: %v", err)
	}
	if got.Type != "manual" || got.Manual == nil || got.Manual.Name != "uplink" || got.Manual.Bridge != "bridge0" {
		t.Fatalf("unexpected manual payload: %#v", got)
	}
}

func TestSwitchCreateCommandRejectsStandardOptionsForManualSwitch(t *testing.T) {
	command := newSwitchesCommand()
	for _, subcommand := range command.Commands {
		if subcommand.Name == "create" {
			subcommand.Action = func(_ context.Context, command *cli.Command) error {
				_, err := buildSwitchCreatePayload(command)
				return err
			}
		}
	}

	err := command.Run(context.Background(), []string{
		"switches", "create", "--type", "manual", "--name", "uplink", "--bridge", "bridge0", "--private",
	})
	if err == nil || err.Error() != "standard switch options are not valid for manual switches" {
		t.Fatalf("manual switch create error = %v", err)
	}
}

func TestSwitchEditCommandBuildsPartialStandardPayload(t *testing.T) {
	command := newSwitchesCommand()
	var got consoleprotocol.SwitchEditPayload
	for _, subcommand := range command.Commands {
		if subcommand.Name != "edit" {
			continue
		}
		subcommand.Action = func(_ context.Context, command *cli.Command) error {
			var err error
			got, err = buildSwitchEditPayload(command)
			return err
		}
	}

	err := command.Run(context.Background(), []string{
		"switches", "edit", "--type", "standard", "--id", "7",
		"--mtu", "9000", "--dhcp=false", "--default-route=false", "--default-route6", "--ports", "igb0, igb1", "--disable-bridge-offloads",
	})
	if err != nil {
		t.Fatalf("run switch edit command: %v", err)
	}
	if got.Type != "standard" || got.Standard == nil {
		t.Fatalf("unexpected payload: %#v", got)
	}
	if got.Standard.ID != 7 || got.Standard.MTU == nil || *got.Standard.MTU != 9000 {
		t.Fatalf("unexpected standard payload: %#v", got.Standard)
	}
	if got.Standard.DHCP == nil || *got.Standard.DHCP {
		t.Fatalf("expected explicit false DHCP patch: %#v", got.Standard)
	}
	if got.Standard.DisableBridgeOffloads == nil || !*got.Standard.DisableBridgeOffloads {
		t.Fatalf("expected bridge offload patch: %#v", got.Standard)
	}
	if got.Standard.DefaultRoute == nil || *got.Standard.DefaultRoute {
		t.Fatalf("expected explicit false IPv4 route-owner patch: %#v", got.Standard)
	}
	if got.Standard.DefaultRoute6 == nil || !*got.Standard.DefaultRoute6 {
		t.Fatalf("expected IPv6 route-owner patch: %#v", got.Standard)
	}
	if got.Standard.Ports == nil || len(*got.Standard.Ports) != 2 || (*got.Standard.Ports)[1] != "igb1" {
		t.Fatalf("unexpected ports patch: %#v", got.Standard.Ports)
	}
	if got.Standard.VLAN != nil || got.Standard.Private != nil {
		t.Fatalf("expected unset fields to remain absent: %#v", got.Standard)
	}
}
