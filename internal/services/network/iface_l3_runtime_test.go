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
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
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
			{Family: "inet", Address: "10.0.0.5/24"},
			{Family: "inet", Address: "10.0.1.5/24", Alias: true},
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
		"/sbin/ifconfig em0 mtu 9000",
		"/sbin/ifconfig em0 inet 10.0.0.5/24",
		"/sbin/ifconfig em0 inet 10.0.1.5/24 alias",
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

func TestApplyHostInterfaceL3PlanRaisesMTUBeforeEnablingIPv6(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)
	originalGet := syncIfaceGet
	t.Cleanup(func() { syncIfaceGet = originalGet })
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{
			Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 900,
			Flags: iface.Flags{Desc: []string{"UP"}},
		}, nil
	}

	mtu := uint(1500)
	enabled := false
	_, err := applyHostInterfaceL3Plan(hostInterfaceL3ApplyPlan{
		Interface: "em0", MTU: &mtu, DisableIPv6: &enabled,
		Addresses: []hostInterfaceL3ApplyAddress{{
			Family: "inet6", Address: "2001:db8::5/64",
		}},
	})
	if err != nil {
		t.Fatalf("applyHostInterfaceL3Plan: %v", err)
	}
	expected := []string{
		"/sbin/ifconfig em0 mtu 1500",
		"/sbin/ifconfig em0 inet6 auto_linklocal -ifdisabled",
		"/sbin/ifconfig em0 inet6 2001:db8::5/64",
	}
	if len(*recorded) != len(expected) {
		t.Fatalf("expected %d commands, got %+v", len(expected), *recorded)
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
			Family: "inet6", Address: "2001:db8::5/64",
		}},
	}
	applied := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::5/64",
		}},
	}

	if err := verifyHostInterfaceL3Plan(plan, applied); err == nil ||
		!strings.Contains(err.Error(), "tentative") {
		t.Fatalf("expected tentative DAD verification failure, got %v", err)
	}
}

func TestWaitForHostInterfaceL3DADPollsUntilSettled(t *testing.T) {
	originalGet := syncIfaceGet
	originalWait := hostInterfaceL3DADWait
	t.Cleanup(func() {
		syncIfaceGet = originalGet
		hostInterfaceL3DADWait = originalWait
	})

	getCalls := 0
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		getCalls++
		return &iface.Interface{
			Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500,
			IPv6: []iface.IPv6{{
				IP: mustParseIP(t, "2001:db8::5"), PrefixLength: 64, Tentative: getCalls < 3,
			}},
		}, nil
	}
	hostInterfaceL3DADWait = func(time.Duration) {}
	plan := hostInterfaceL3ApplyPlan{Interface: "em0"}
	applied := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::5/64",
		}},
	}

	if err := waitForHostInterfaceL3DAD(plan, applied); err != nil {
		t.Fatalf("waitForHostInterfaceL3DAD: %v", err)
	}
	if getCalls != 3 {
		t.Fatalf("expected three DAD inspections, got %d", getCalls)
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
	getCalls := 0
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		getCalls++
		if getCalls > 1 {
			return &iface.Interface{Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500}, nil
		}
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
	baselineND6Flags := uint32(0)
	wasUp := false
	snapshot := networkModels.HostInterfaceL3Baseline{
		MTU:      &baselineMTU,
		Metric:   &baselineMetric,
		ND6Flags: &baselineND6Flags,
		Up:       &wasUp,
	}

	appliedMTU := uint(9000)
	appliedMetric := uint(100)
	ipv6Disabled := true
	up := true
	applied := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet", Address: "10.0.0.5/24",
		}},
		MTU:          &appliedMTU,
		Metric:       &appliedMetric,
		IPv6Disabled: &ipv6Disabled,
		Up:           &up,
	}

	if err := revertHostInterfaceL3Runtime("em0", "", snapshot, networkModels.HostInterfaceL3AppliedState{}, applied); err != nil {
		t.Fatalf("revertHostInterfaceL3Runtime: %v", err)
	}

	expected := []string{
		"/sbin/ifconfig em0 inet 10.0.0.5 delete",
		"/sbin/ifconfig em0 mtu 1500",
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

func TestApplyHostInterfaceL3PlanSkipsUnchangedMTUAndMetric(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)
	originalGet := syncIfaceGet
	t.Cleanup(func() { syncIfaceGet = originalGet })
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{
			Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500, Metric: 25,
			Flags: iface.Flags{Desc: []string{"UP"}},
		}, nil
	}

	mtu := uint(1500)
	metric := uint(25)
	applied, err := applyHostInterfaceL3Plan(hostInterfaceL3ApplyPlan{
		Interface: "em0", MTU: &mtu, Metric: &metric,
	})
	if err != nil {
		t.Fatalf("applyHostInterfaceL3Plan: %v", err)
	}
	if len(*recorded) != 0 {
		t.Fatalf("expected no commands for unchanged values, got %+v", *recorded)
	}
	if applied.MTU == nil || *applied.MTU != mtu || applied.Metric == nil || *applied.Metric != metric {
		t.Fatalf("expected unchanged values to remain verified, got %+v", applied)
	}
}

func TestRestoreHostInterfaceL3IPv6Flags(t *testing.T) {
	tests := []struct {
		name     string
		flags    uint32
		expected string
	}{
		{
			name:     "router advertisements and link-local enabled",
			flags:    hostInterfaceL3ND6AcceptRTAdv | hostInterfaceL3ND6AutoLinkLocal,
			expected: "/sbin/ifconfig em0 inet6 -no_radr accept_rtadv -ifdisabled auto_linklocal",
		},
		{
			name:     "router advertisements and link-local disabled",
			flags:    hostInterfaceL3ND6NoRADR,
			expected: "/sbin/ifconfig em0 inet6 no_radr -accept_rtadv -ifdisabled -auto_linklocal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorded := recordHostInterfaceL3Commands(t)
			if err := restoreHostInterfaceL3IPv6Flags("em0", test.flags); err != nil {
				t.Fatalf("restoreHostInterfaceL3IPv6Flags: %v", err)
			}
			if len(*recorded) != 1 || commandLine((*recorded)[0]) != test.expected {
				t.Fatalf("expected %q, got %+v", test.expected, *recorded)
			}
		})
	}
}

func TestRevertHostInterfaceL3PlanRestoresPrerequisitesBeforeIPv6Address(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)
	originalGet := syncIfaceGet
	t.Cleanup(func() { syncIfaceGet = originalGet })

	baselineMTU := uint(1500)
	baselineFlags := uint32(hostInterfaceL3ND6AcceptRTAdv | hostInterfaceL3ND6AutoLinkLocal | 0x01)
	wasUp := true
	getCalls := 0
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		getCalls++
		if getCalls == 1 {
			return &iface.Interface{
				Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 900,
				ND6: iface.ND6{Flags: iface.Flags{Raw: hostInterfaceL3ND6IfDisabled | hostInterfaceL3ND6NoRADR}},
				IPv6: []iface.IPv6{{
					IP: mustParseIP(t, "2001:db8::6"), PrefixLength: 64,
				}},
			}, nil
		}
		return &iface.Interface{
			Name: name, Ether: "aa:bb:cc:dd:ee:ff", MTU: 1500,
			Flags: iface.Flags{Desc: []string{"UP"}},
			ND6:   iface.ND6{Flags: iface.Flags{Raw: baselineFlags}},
			IPv6: []iface.IPv6{{
				IP: mustParseIP(t, "2001:db8::5"), PrefixLength: 64,
			}},
		}, nil
	}

	enabled := false
	disabled := true
	candidateMTU := uint(900)
	snapshot := networkModels.HostInterfaceL3Baseline{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::5/64",
		}},
		MTU:      &baselineMTU,
		ND6Flags: &baselineFlags,
		Up:       &wasUp,
	}
	target := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::5/64",
		}},
		MTU: &baselineMTU, IPv6Disabled: &enabled, Up: &wasUp,
	}
	candidate := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet6", Address: "2001:db8::6/64",
		}},
		MTU: &candidateMTU, IPv6Disabled: &disabled, Up: &wasUp,
	}
	if err := revertHostInterfaceL3Runtime("em0", "", snapshot, target, candidate); err != nil {
		t.Fatalf("revertHostInterfaceL3Runtime: %v", err)
	}

	expected := []string{
		"/sbin/ifconfig em0 mtu 1500",
		"/sbin/ifconfig em0 inet6 -no_radr accept_rtadv -ifdisabled auto_linklocal",
		"/sbin/ifconfig em0 up",
		"/sbin/ifconfig em0 inet6 2001:db8::6 delete",
		"/sbin/ifconfig em0 inet6 2001:db8::5/64",
	}
	if len(*recorded) != len(expected) {
		t.Fatalf("expected %d commands, got %+v", len(expected), *recorded)
	}
	for index, want := range expected {
		if got := commandLine((*recorded)[index]); got != want {
			t.Fatalf("command %d: expected %q, got %q", index, want, got)
		}
	}
}

func TestRevertHostInterfaceL3PlanRefusesReplacementIdentity(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)
	originalGet := syncIfaceGet
	t.Cleanup(func() { syncIfaceGet = originalGet })
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Ether: "bb:bb:bb:bb:bb:bb", MTU: 1500}, nil
	}

	candidate := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet", Address: "10.0.0.5/24",
		}},
	}
	err := revertHostInterfaceL3Runtime(
		"em0",
		"aa:aa:aa:aa:aa:aa",
		networkModels.HostInterfaceL3Baseline{},
		networkModels.HostInterfaceL3AppliedState{},
		candidate,
	)
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_identity_mismatch" {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
	if len(*recorded) != 0 {
		t.Fatalf("replacement interface was mutated: %+v", *recorded)
	}
}

func TestRevertHostInterfaceL3PlanRefusesRetaggedVLAN(t *testing.T) {
	recorded := recordHostInterfaceL3Commands(t)
	originalGet := syncIfaceGet
	t.Cleanup(func() { syncIfaceGet = originalGet })
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		return &iface.Interface{
			Name: name, Ether: "aa:aa:aa:aa:aa:aa", MTU: 1500, VLANParent: "em0", VLANTag: 200,
		}, nil
	}

	candidate := networkModels.HostInterfaceL3AppliedState{
		Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
			Family: "inet", Address: "10.0.0.5/24",
		}},
	}
	err := revertHostInterfaceL3Runtime(
		"em0.100",
		"aa:aa:aa:aa:aa:aa",
		networkModels.HostInterfaceL3Baseline{VLANParent: "em0", VLANTag: 100},
		networkModels.HostInterfaceL3AppliedState{},
		candidate,
	)
	if err == nil || HostInterfaceL3ErrorCode(err) != networkServiceInterfaces.HostInterfaceL3ConflictVLANIdentity {
		t.Fatalf("expected VLAN identity mismatch, got %v", err)
	}
	if len(*recorded) != 0 {
		t.Fatalf("retagged VLAN was mutated: %+v", *recorded)
	}
}
