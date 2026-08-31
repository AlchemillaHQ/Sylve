// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/rs/zerolog"
)

func TestParseStandardSwitchMemberRCModes(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   standardSwitchMemberRCModes
	}{
		{
			name:   "neither",
			output: standardSwitchRCModesMarker + " dhcp=0 slaac=0\n",
			want:   standardSwitchMemberRCModes{},
		},
		{
			name:   "dhcp",
			output: "unrelated rc output\n" + standardSwitchRCModesMarker + " dhcp=1 slaac=0\n",
			want:   standardSwitchMemberRCModes{DHCP: true},
		},
		{
			name:   "slaac",
			output: standardSwitchRCModesMarker + " slaac=1 dhcp=0\n",
			want:   standardSwitchMemberRCModes{SLAAC: true},
		},
		{
			name:   "both",
			output: standardSwitchRCModesMarker + " dhcp=1 slaac=1\n",
			want:   standardSwitchMemberRCModes{DHCP: true, SLAAC: true},
		},
		{
			name:   "static addresses and aliases",
			output: standardSwitchRCModesMarker + " dhcp=0 slaac=0 static4=1 static6=1 alias4=1 alias6=1\n",
			want: standardSwitchMemberRCModes{
				StaticIPv4: true, StaticIPv6: true, AliasesIPv4: true, AliasesIPv6: true,
			},
		},
		{
			name:   "configured bridges are ordered and deduplicated",
			output: standardSwitchRCModesMarker + " dhcp=0 bridge=bridge0 slaac=0 bridge=bridge1 bridge=bridge0\n",
			want: standardSwitchMemberRCModes{
				ConfiguredBridges: []string{"bridge0", "bridge1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStandardSwitchMemberRCModes(tt.output)
			if err != nil {
				t.Fatalf("parse rc modes: %v", err)
			}
			if got.DHCP != tt.want.DHCP ||
				got.SLAAC != tt.want.SLAAC ||
				got.StaticIPv4 != tt.want.StaticIPv4 ||
				got.StaticIPv6 != tt.want.StaticIPv6 ||
				got.AliasesIPv4 != tt.want.AliasesIPv4 ||
				got.AliasesIPv6 != tt.want.AliasesIPv6 ||
				!slices.Equal(got.ConfiguredBridges, tt.want.ConfiguredBridges) {
				t.Fatalf("modes = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseStandardSwitchMemberRCModesRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{
		"",
		standardSwitchRCModesMarker + " dhcp=1\n",
		standardSwitchRCModesMarker + " dhcp=yes slaac=0\n",
	} {
		if _, err := parseStandardSwitchMemberRCModes(output); err == nil {
			t.Fatalf("expected invalid output %q to fail", output)
		}
	}
}

func TestInspectStandardSwitchMemberRCModesUsesPositionalInterfaceArgument(t *testing.T) {
	original := runStandardSwitchRCInspection
	t.Cleanup(func() { runStandardSwitchRCInspection = original })

	const member = "igb0;not-shell-syntax"
	runStandardSwitchRCInspection = func(_ context.Context, command string, args ...string) (string, error) {
		if command != "/bin/sh" {
			t.Fatalf("command = %q, want /bin/sh", command)
		}
		if len(args) != 4 {
			t.Fatalf("args = %#v, want four arguments", args)
		}
		if args[0] != "-c" || args[2] != "sylve-standard-switch-rc-inspection" || args[3] != member {
			t.Fatalf("unexpected shell arguments: %#v", args)
		}
		return standardSwitchRCModesMarker + " dhcp=1 slaac=0 bridge=bridge0\n", nil
	}

	modes, err := inspectStandardSwitchMemberRCModes(member)
	if err != nil {
		t.Fatalf("inspect rc modes: %v", err)
	}
	if !modes.DHCP || modes.SLAAC || !slices.Equal(modes.ConfiguredBridges, []string{"bridge0"}) {
		t.Fatalf("modes = %#v, want DHCP and bridge0", modes)
	}
}

func TestStandardSwitchRCBridgeScanScriptDetectsAddmMember(t *testing.T) {
	script := `
. /etc/rc.subr || exit 1
. /etc/network.subr || exit 1

_sylve_if=em0
cloned_interfaces='bridge0 bridge1'
ifconfig_bridge0='addm em0 SYNCDHCP'
create_args_bridge0='inet6 auto_linklocal -ifdisabled'
ifconfig_bridge1='addm ix0 up'
` + standardSwitchRCBridgeScanScript + `
printf '__sylve_standard_switch_rc_modes__ dhcp=0 slaac=0'
for _sylve_bridge in $_sylve_rc_bridges; do
	printf ' bridge=%s' "$_sylve_bridge"
done
printf '\n'
`

	output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("run rc bridge scan: %v: %s", err, output)
	}
	modes, err := parseStandardSwitchMemberRCModes(string(output))
	if err != nil {
		t.Fatalf("parse rc bridge scan: %v: %s", err, output)
	}
	if modes.DHCP || modes.SLAAC || !slices.Equal(modes.ConfiguredBridges, []string{"bridge0"}) {
		t.Fatalf("modes = %#v, want bridge0 only", modes)
	}
}

func TestWarnStandardSwitchMemberRCConflictsLogsConfiguredVLANMember(t *testing.T) {
	originalInspect := syncInspectStandardSwitchRCModes
	originalList := syncListStandardSwitchInterfaces
	originalLogger := logger.L
	t.Cleanup(func() {
		syncInspectStandardSwitchRCModes = originalInspect
		syncListStandardSwitchInterfaces = originalList
		logger.L = originalLogger
	})

	syncListStandardSwitchInterfaces = func() ([]*iface.Interface, error) {
		return nil, nil
	}
	inspected := make([]string, 0, 2)
	syncInspectStandardSwitchRCModes = func(member string) (standardSwitchMemberRCModes, error) {
		inspected = append(inspected, member)
		if member == "igb0.100" {
			return standardSwitchMemberRCModes{DHCP: true, SLAAC: true}, nil
		}
		return standardSwitchMemberRCModes{}, nil
	}

	var logs bytes.Buffer
	logger.L = zerolog.New(&logs).Level(zerolog.WarnLevel)
	warnStandardSwitchMemberRCConflicts(networkModels.StandardSwitch{
		ID:         42,
		Name:       "wan",
		BridgeName: "vm-wan",
		VLAN:       100,
		Ports: []networkModels.NetworkPort{
			{Name: "igb0"},
			{Name: "ix0"},
		},
	})

	if got, want := strings.Join(inspected, ","), "igb0.100,ix0.100"; got != want {
		t.Fatalf("inspected members = %q, want %q", got, want)
	}

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("warning log lines = %d, want 1: %q", len(lines), logs.String())
	}
	var event struct {
		Level    string `json:"level"`
		Message  string `json:"message"`
		SwitchID uint   `json:"switch_id"`
		Switch   string `json:"switch"`
		Bridge   string `json:"bridge"`
		Port     string `json:"port"`
		Member   string `json:"member"`
		DHCP     bool   `json:"dhcp"`
		SLAAC    bool   `json:"slaac"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode warning log: %v", err)
	}
	if event.Level != "warn" || event.Message != "standard_switch_member_rc_l3_conflict" ||
		event.SwitchID != 42 || event.Switch != "wan" || event.Bridge != "vm-wan" ||
		event.Port != "igb0" || event.Member != "igb0.100" || !event.DHCP || !event.SLAAC {
		t.Fatalf("unexpected warning event: %#v", event)
	}
}

func TestWarnStandardSwitchMemberRCConflictsLogsBridgeOwnershipOnce(t *testing.T) {
	originalInspect := syncInspectStandardSwitchRCModes
	originalList := syncListStandardSwitchInterfaces
	originalLogger := logger.L
	t.Cleanup(func() {
		syncInspectStandardSwitchRCModes = originalInspect
		syncListStandardSwitchInterfaces = originalList
		logger.L = originalLogger
	})

	syncInspectStandardSwitchRCModes = func(member string) (standardSwitchMemberRCModes, error) {
		if member != "em0" {
			t.Fatalf("inspected member = %q, want em0", member)
		}
		return standardSwitchMemberRCModes{ConfiguredBridges: []string{"bridge0"}}, nil
	}
	syncListStandardSwitchInterfaces = func() ([]*iface.Interface, error) {
		return []*iface.Interface{
			{
				Name:          "vm-wan",
				BridgeMembers: []iface.BridgeMember{{Name: "em0"}},
			},
			{
				Name:          "bridge0",
				BridgeMembers: []iface.BridgeMember{{Name: "em0"}},
			},
		}, nil
	}

	var logs bytes.Buffer
	logger.L = zerolog.New(&logs).Level(zerolog.WarnLevel)
	warnStandardSwitchMemberRCConflicts(networkModels.StandardSwitch{
		ID:         42,
		Name:       "wan",
		BridgeName: "vm-wan",
		Ports:      []networkModels.NetworkPort{{Name: "em0"}},
	})

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("warning log lines = %d, want 1: %q", len(lines), logs.String())
	}
	var event struct {
		Level             string `json:"level"`
		Message           string `json:"message"`
		SwitchID          uint   `json:"switch_id"`
		Switch            string `json:"switch"`
		Bridge            string `json:"bridge"`
		Port              string `json:"port"`
		Member            string `json:"member"`
		ConflictingBridge string `json:"conflicting_bridge"`
		RCConfigured      bool   `json:"rc_configured"`
		RuntimeAttached   bool   `json:"runtime_attached"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode warning log: %v", err)
	}
	if event.Level != "warn" || event.Message != "standard_switch_member_bridge_ownership_conflict" ||
		event.SwitchID != 42 || event.Switch != "wan" || event.Bridge != "vm-wan" ||
		event.Port != "em0" || event.Member != "em0" || event.ConflictingBridge != "bridge0" ||
		!event.RCConfigured || !event.RuntimeAttached {
		t.Fatalf("unexpected warning event: %#v", event)
	}
}

func TestWarnStandardSwitchMemberRCConflictsUsesRuntimeWhenRCInspectionFails(t *testing.T) {
	originalInspect := syncInspectStandardSwitchRCModes
	originalList := syncListStandardSwitchInterfaces
	originalLogger := logger.L
	t.Cleanup(func() {
		syncInspectStandardSwitchRCModes = originalInspect
		syncListStandardSwitchInterfaces = originalList
		logger.L = originalLogger
	})

	syncInspectStandardSwitchRCModes = func(string) (standardSwitchMemberRCModes, error) {
		return standardSwitchMemberRCModes{}, context.DeadlineExceeded
	}
	syncListStandardSwitchInterfaces = func() ([]*iface.Interface, error) {
		return []*iface.Interface{{
			Name:          "bridge0",
			BridgeMembers: []iface.BridgeMember{{Name: "em0"}},
		}}, nil
	}

	var logs bytes.Buffer
	logger.L = zerolog.New(&logs).Level(zerolog.WarnLevel)
	warnStandardSwitchMemberRCConflicts(networkModels.StandardSwitch{
		ID:         42,
		Name:       "wan",
		BridgeName: "vm-wan",
		Ports:      []networkModels.NetworkPort{{Name: "em0"}},
	})

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("warning log lines = %d, want 1: %q", len(lines), logs.String())
	}
	var event struct {
		Message           string `json:"message"`
		ConflictingBridge string `json:"conflicting_bridge"`
		RCConfigured      bool   `json:"rc_configured"`
		RuntimeAttached   bool   `json:"runtime_attached"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode warning log: %v", err)
	}
	if event.Message != "standard_switch_member_bridge_ownership_conflict" ||
		event.ConflictingBridge != "bridge0" || event.RCConfigured || !event.RuntimeAttached {
		t.Fatalf("unexpected warning event: %#v", event)
	}
}

func TestInspectStandardSwitchRCConflictsReturnsStructuredL3AndBridgeWarnings(t *testing.T) {
	originalInspect := syncInspectStandardSwitchRCModes
	originalList := syncListStandardSwitchInterfaces
	t.Cleanup(func() {
		syncInspectStandardSwitchRCModes = originalInspect
		syncListStandardSwitchInterfaces = originalList
	})

	syncInspectStandardSwitchRCModes = func(member string) (standardSwitchMemberRCModes, error) {
		if member != "igb0.200" {
			t.Fatalf("inspected member = %q, want igb0.200", member)
		}
		return standardSwitchMemberRCModes{
			DHCP: true, StaticIPv6: true, AliasesIPv4: true,
			ConfiguredBridges: []string{"bridge9"},
		}, nil
	}
	syncListStandardSwitchInterfaces = func() ([]*iface.Interface, error) {
		return []*iface.Interface{{
			Name: "bridge9", BridgeMembers: []iface.BridgeMember{{Name: "igb0.200"}},
		}}, nil
	}

	warnings := inspectStandardSwitchRCConflicts(networkModels.StandardSwitch{
		Name: "wan", BridgeName: "vm-wan", VLAN: 200,
		Ports: []networkModels.NetworkPort{{Name: "igb0"}},
	})
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v, want two", warnings)
	}
	if l3 := warnings[0]; l3.Code != standardSwitchRCConflictL3 || l3.Member != "igb0.200" ||
		!l3.DHCP || !l3.StaticIPv6 || !l3.AliasesIPv4 {
		t.Fatalf("unexpected L3 warning: %#v", l3)
	}
	if bridge := warnings[1]; bridge.Code != standardSwitchRCConflictBridge ||
		bridge.ConflictingBridge != "bridge9" || !bridge.RCConfigured || !bridge.RuntimeAttached {
		t.Fatalf("unexpected bridge warning: %#v", bridge)
	}
}

func TestInspectStandardSwitchRCConflictsReportsInspectionFailures(t *testing.T) {
	originalInspect := syncInspectStandardSwitchRCModes
	originalList := syncListStandardSwitchInterfaces
	t.Cleanup(func() {
		syncInspectStandardSwitchRCModes = originalInspect
		syncListStandardSwitchInterfaces = originalList
	})
	syncInspectStandardSwitchRCModes = func(string) (standardSwitchMemberRCModes, error) {
		return standardSwitchMemberRCModes{}, context.DeadlineExceeded
	}
	syncListStandardSwitchInterfaces = func() ([]*iface.Interface, error) {
		return nil, context.DeadlineExceeded
	}

	warnings := inspectStandardSwitchRCConflicts(networkModels.StandardSwitch{
		Ports: []networkModels.NetworkPort{{Name: "em0"}},
	})
	if len(warnings) != 2 || warnings[0].Code != standardSwitchRuntimeInspectionUnavailable ||
		warnings[1].Code != standardSwitchRCInspectionUnavailable {
		t.Fatalf("inspection warnings = %#v", warnings)
	}
}
