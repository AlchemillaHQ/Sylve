// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
)

func TestBuildConsoleSwitchCreateRequestStandard(t *testing.T) {
	request, err := buildConsoleSwitchCreateRequest([]string{
		"standard", "private-lan", "--network4", "7", "--ports", "igb0, igb1", "--private", "--dhcp=false",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Type != "standard" || request.Standard == nil {
		t.Fatalf("unexpected request: %#v", request)
	}
	if request.Standard.Name != "private-lan" || request.Standard.Network4 != 7 || !request.Standard.Private || request.Standard.DHCP {
		t.Fatalf("unexpected standard request: %#v", request.Standard)
	}
	if len(request.Standard.Ports) != 2 || request.Standard.Ports[1] != "igb1" {
		t.Fatalf("unexpected standard ports: %#v", request.Standard.Ports)
	}
}

func TestBuildConsoleSwitchCreateRequestManual(t *testing.T) {
	request, err := buildConsoleSwitchCreateRequest([]string{"manual", "uplink", "bridge0"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Type != "manual" || request.Manual == nil || request.Manual.Name != "uplink" || request.Manual.Bridge != "bridge0" {
		t.Fatalf("unexpected manual request: %#v", request)
	}
}

func TestBuildConsoleSwitchCreateRequestAllowsPortlessStandardSwitch(t *testing.T) {
	request, err := buildConsoleSwitchCreateRequest([]string{"standard", "isolated", "--private"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Standard == nil || !request.Standard.Private || len(request.Standard.Ports) != 0 {
		t.Fatalf("unexpected portless standard request: %#v", request.Standard)
	}
}

func TestBuildConsoleSwitchEditRequestManual(t *testing.T) {
	request, err := buildConsoleSwitchEditRequest([]string{"manual", "7", "--name", "WAN", "--bridge", "bridge0"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Type != "manual" || request.Manual == nil {
		t.Fatalf("unexpected request: %#v", request)
	}
	if request.Manual.ID != 7 || request.Manual.Name == nil || *request.Manual.Name != "WAN" || request.Manual.Bridge == nil || *request.Manual.Bridge != "bridge0" {
		t.Fatalf("unexpected manual request: %#v", request.Manual)
	}
}

func TestBuildConsoleSwitchEditRequestStandard(t *testing.T) {
	request, err := buildConsoleSwitchEditRequest([]string{
		"standard", "7", "--mtu", "9000", "--private", "false", "--ports", "igb0, igb1",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Type != "standard" || request.Standard == nil {
		t.Fatalf("unexpected request: %#v", request)
	}
	if request.Standard.ID != 7 || request.Standard.MTU == nil || *request.Standard.MTU != 9000 {
		t.Fatalf("unexpected standard request: %#v", request.Standard)
	}
	if request.Standard.Private == nil || *request.Standard.Private || request.Standard.Ports == nil || len(*request.Standard.Ports) != 2 {
		t.Fatalf("unexpected standard request: %#v", request.Standard)
	}
}

func TestBuildConsoleSwitchEditRequestAcceptsFlagStyleBooleans(t *testing.T) {
	request, err := buildConsoleSwitchEditRequest([]string{
		"standard", "7", "--dhcp", "--private=false", "--slaac", "false",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if request.Standard == nil || request.Standard.DHCP == nil || !*request.Standard.DHCP {
		t.Fatalf("DHCP shorthand was not parsed: %#v", request.Standard)
	}
	if request.Standard.Private == nil || *request.Standard.Private {
		t.Fatalf("private=false was not parsed: %#v", request.Standard)
	}
	if request.Standard.SLAAC == nil || *request.Standard.SLAAC {
		t.Fatalf("legacy explicit false was not retained: %#v", request.Standard)
	}
}

func TestSwitchesUseDeleteInsteadOfRM(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	handleSwitches(ctx, []string{"delete", "standard", "1"})
	if !strings.Contains(out.String(), "Error deleting switch: network_service_unavailable") {
		t.Fatalf("unexpected delete output: %q", out.String())
	}

	out.Reset()
	handleSwitches(ctx, []string{"rm", "standard", "1"})
	if !strings.Contains(out.String(), "Unknown switches command: 'rm'") {
		t.Fatalf("unexpected rm output: %q", out.String())
	}
}

func TestApplyStandardSwitchEditRequestPreservesAndNormalizesState(t *testing.T) {
	config := standardSwitchEditConfig{
		MTU:      1500,
		VLAN:     10,
		Network4: 1,
		DHCP:     true,
		Ports:    []string{"igb0"},
	}
	network4 := uint(9)
	request := consoleprotocol.StandardSwitchEditRequest{
		ID:       7,
		Network4: &network4,
	}

	if err := applyStandardSwitchEditRequest(&config, request); err != nil {
		t.Fatalf("apply request: %v", err)
	}
	if config.Network4 != 9 || config.DHCP {
		t.Fatalf("unexpected normalized config: %#v", config)
	}
	if config.MTU != 1500 || config.VLAN != 10 || len(config.Ports) != 1 {
		t.Fatalf("expected untouched fields to be preserved: %#v", config)
	}
}

func TestApplyStandardSwitchEditRequestClearsInheritedIPv6ModeForAddressPatch(t *testing.T) {
	config := standardSwitchEditConfig{
		MTU:   1500,
		SLAAC: true,
		Ports: []string{"igb0"},
	}
	network6 := uint(9)
	disableIPv6 := false
	request := consoleprotocol.StandardSwitchEditRequest{
		ID:          7,
		Network6:    &network6,
		DisableIPv6: &disableIPv6,
	}

	if err := applyStandardSwitchEditRequest(&config, request); err != nil {
		t.Fatalf("apply request: %v", err)
	}
	if config.Network6 != 9 || config.DisableIPv6 || config.SLAAC {
		t.Fatalf("unexpected normalized config: %#v", config)
	}
}
