// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

type recordedCommand struct {
	command string
	args    []string
}

func recordHostInterfaceL3Commands(t *testing.T) *[]recordedCommand {
	t.Helper()

	recorded := make([]recordedCommand, 0)
	original := syncRunCommand
	t.Cleanup(func() {
		syncRunCommand = original
	})
	syncRunCommand = func(command string, args ...string) (string, error) {
		recorded = append(recorded, recordedCommand{command: command, args: append([]string(nil), args...)})
		return "", nil
	}
	return &recorded
}

func commandLine(command recordedCommand) string {
	return command.command + " " + strings.Join(command.args, " ")
}

func TestApplyHostInterfaceL3PlanRunsExpectedCommands(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)

	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500}, nil
	}

	mtu := uint(9000)
	metric := uint(100)
	disableIPv6 := true
	plan := hostInterfaceL3ApplyPlan{
		Interface: "em0",
		Addresses: []hostInterfaceL3ApplyAddress{
			{Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24, Alias: false},
			{Family: "inet", Address: "10.0.1.5/24", PrefixLength: 24, Alias: true},
		},
		MTU:         &mtu,
		Metric:      &metric,
		DisableIPv6: &disableIPv6,
	}

	applied, err := applyHostInterfaceL3Plan(plan)
	if err != nil {
		t.Fatalf("applyHostInterfaceL3Plan: %v", err)
	}
	if len(applied.Addresses) != 2 || applied.MTU == nil || *applied.MTU != 9000 {
		t.Fatalf("unexpected applied state %+v", applied)
	}
	if applied.Up == nil || !*applied.Up {
		t.Fatalf("expected the interface to be recorded as brought up, got %+v", applied.Up)
	}

	expected := []string{
		"/sbin/ifconfig em0 inet 10.0.0.5/24",
		"/sbin/ifconfig em0 inet 10.0.1.5/24 alias",
		"/sbin/ifconfig em0 mtu 9000",
		"/sbin/ifconfig em0 metric 100",
		"/sbin/ifconfig em0 inet6 no_radr -accept_rtadv ifdisabled",
		"/sbin/ifconfig em0 up",
	}
	if len(*recorded) != len(expected) {
		t.Fatalf("expected %d commands, got %d: %+v", len(expected), len(*recorded), *recorded)
	}
	for index, want := range expected {
		if got := commandLine((*recorded)[index]); got != want {
			t.Fatalf("command %d: expected %q, got %q", index, want, got)
		}
	}
}

func TestVerifyHostInterfaceL3PlanRejectsTentativeIPv6(t *testing.T) {
	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{
			Name:  name,
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   1500,
			IPv6: []iface.IPv6{{
				IP:           mustParseIP(t, "2001:db8::5"),
				PrefixLength: 64,
				Tentative:    true,
			}},
		}, nil
	}

	plan := hostInterfaceL3ApplyPlan{
		Interface: "em0",
		Addresses: []hostInterfaceL3ApplyAddress{{
			Family: "inet6", Address: "2001:db8::5/64", PrefixLength: 64, Alias: false,
		}},
	}
	applied := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::5/64", PrefixLength: 64,
		}},
	}

	if err := verifyHostInterfaceL3Plan(plan, applied); err == nil ||
		!strings.Contains(err.Error(), "tentative") {
		t.Fatalf("expected tentative DAD verification failure, got %v", err)
	}
}

func TestVerifyHostInterfaceL3PlanRejectsMTUMismatch(t *testing.T) {
	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500}, nil
	}

	mtu := uint(9000)
	plan := hostInterfaceL3ApplyPlan{Interface: "em0", MTU: &mtu}
	applied := networkModels.HostInterfaceL3AppliedState{MTU: &mtu}

	if err := verifyHostInterfaceL3Plan(plan, applied); err == nil ||
		!strings.Contains(err.Error(), "MTU") {
		t.Fatalf("expected MTU verification failure, got %v", err)
	}
}

func TestRevertHostInterfaceL3PlanRestoresBaseline(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)

	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{
			Name:  name,
			Ether: "aa:bb:cc:dd:ee:ff",
			MTU:   9000,
			IPv4: []iface.IPv4{{
				IP:      mustParseIP(t, "10.0.0.5"),
				Netmask: "255.255.255.0",
			}},
		}, nil
	}

	baselineMTU := uint(1500)
	baselineMetric := uint(0)
	baselineIPv6Disabled := false
	wasUp := false
	snapshot := networkModels.HostInterfaceL3Baseline{
		MTU:          &baselineMTU,
		Metric:       &baselineMetric,
		IPv6Disabled: &baselineIPv6Disabled,
		Up:           &wasUp,
	}

	appliedMTU := uint(9000)
	appliedMetric := uint(100)
	ipv6Disabled := true
	up := true
	applied := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24,
		}},
		MTU:          &appliedMTU,
		Metric:       &appliedMetric,
		IPv6Disabled: &ipv6Disabled,
		Up:           &up,
	}

	if err := revertHostInterfaceL3Runtime("em0", snapshot, networkModels.HostInterfaceL3AppliedState{}, applied); err != nil {
		t.Fatalf("revertHostInterfaceL3Runtime: %v", err)
	}

	expected := []string{
		"/sbin/ifconfig em0 inet 10.0.0.5 delete",
		"/sbin/ifconfig em0 mtu 1500",
		"/sbin/ifconfig em0 metric 0",
		"/sbin/ifconfig em0 inet6 auto_linklocal -ifdisabled",
		"/sbin/ifconfig em0 down",
	}
	if len(*recorded) != len(expected) {
		t.Fatalf("expected %d commands, got %d: %+v", len(expected), len(*recorded), *recorded)
	}
	for index, want := range expected {
		if got := commandLine((*recorded)[index]); got != want {
			t.Fatalf("command %d: expected %q, got %q", index, want, got)
		}
	}
}

func TestRevertHostInterfaceL3PlanRestoresND6Flags(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)

	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500}, nil
	}

	baselineFlags := uint32(hostInterfaceL3ND6AcceptRTAdv | hostInterfaceL3ND6AutoLinkLocal | 0x01)
	ipv6Disabled := true
	snapshot := networkModels.HostInterfaceL3Baseline{
		IPv6Disabled: &ipv6Disabled,
		ND6Flags:     &baselineFlags,
	}
	applied := networkModels.HostInterfaceL3AppliedState{
		IPv6Disabled: &ipv6Disabled,
	}

	if err := revertHostInterfaceL3Runtime("em0", snapshot, networkModels.HostInterfaceL3AppliedState{}, applied); err != nil {
		t.Fatalf("revertHostInterfaceL3Runtime: %v", err)
	}

	expected := "/sbin/ifconfig em0 inet6 -no_radr accept_rtadv -ifdisabled auto_linklocal"
	if len(*recorded) != 1 || commandLine((*recorded)[0]) != expected {
		t.Fatalf("expected %q, got %+v", expected, *recorded)
	}
}

func TestRevertHostInterfaceL3PlanClearsAutoLinkLocalWhenBaselineLackedIt(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)

	originalGet := syncIfaceGet
	t.Cleanup(func() {
		syncIfaceGet = originalGet
	})
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500}, nil
	}

	baselineFlags := uint32(hostInterfaceL3ND6NoRADR | 0x01) // no AUTO_LINKLOCAL, no RTADV
	ipv6Disabled := true
	snapshot := networkModels.HostInterfaceL3Baseline{
		IPv6Disabled: &ipv6Disabled,
		ND6Flags:     &baselineFlags,
	}
	applied := networkModels.HostInterfaceL3AppliedState{IPv6Disabled: &ipv6Disabled}

	if err := revertHostInterfaceL3Runtime("em0", snapshot, networkModels.HostInterfaceL3AppliedState{}, applied); err != nil {
		t.Fatalf("revertHostInterfaceL3Runtime: %v", err)
	}

	expected := "/sbin/ifconfig em0 inet6 no_radr -accept_rtadv -ifdisabled -auto_linklocal"
	if len(*recorded) != 1 || commandLine((*recorded)[0]) != expected {
		t.Fatalf("expected %q, got %+v", expected, *recorded)
	}
}
