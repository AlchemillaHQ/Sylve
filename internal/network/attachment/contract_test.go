// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package attachment

import (
	"errors"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func vlanPointer(value int) *int { return &value }

func TestNormalizeAttachmentContract(t *testing.T) {
	native := 10
	contract, err := Normalize(Contract{
		Version:       CurrentVersion,
		Kind:          KindJail,
		SwitchName:    " LAN ",
		SwitchType:    " STANDARD ",
		VLANFiltering: true,
		VLANPolicy: &bridgevlan.PortPolicy{
			Mode: bridgevlan.ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{30, 20, 30},
		},
	})
	if err != nil {
		t.Fatalf("normalize contract: %v", err)
	}
	if contract.SwitchName != "LAN" || contract.SwitchType != "standard" ||
		contract.VLANPolicy == nil || len(contract.VLANPolicy.TaggedVLANs) != 2 ||
		contract.VLANPolicy.TaggedVLANs[0] != 20 || contract.VLANPolicy.TaggedVLANs[1] != 30 {
		t.Fatalf("unexpected normalized contract: %#v", contract)
	}

	if _, err := Normalize(Contract{Version: 2, Kind: KindVM, SwitchName: "LAN"}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("unsupported version error = %v", err)
	}
	if _, err := Normalize(Contract{
		Version: CurrentVersion, Kind: KindVM, SwitchName: "LAN", VLANFiltering: true,
	}); err == nil || !strings.Contains(err.Error(), "filtered_switch_vm_default_access_vlan_required") {
		t.Fatalf("missing VM access VLAN error = %v", err)
	}
}

func TestCompareAttachmentContract(t *testing.T) {
	vlan10, vlan20 := 10, 20
	expected := Contract{
		Version: CurrentVersion, Kind: KindVM, SwitchName: "LAN", SwitchType: "standard",
		VLANFiltering: true, DefaultAccessVLAN: &vlan10,
	}
	if err := Compare(expected, expected); err != nil {
		t.Fatalf("matching attachment: %v", err)
	}

	actual := expected
	actual.DefaultAccessVLAN = &vlan20
	if err := Compare(expected, actual); !errors.Is(err, ErrIncompatible) ||
		!strings.Contains(err.Error(), "vm_network_default_access_vlan_mismatch") {
		t.Fatalf("default VLAN mismatch = %v", err)
	}

	actual = expected
	actual.VLANFiltering = false
	actual.DefaultAccessVLAN = nil
	if err := Compare(expected, actual); !errors.Is(err, ErrIncompatible) ||
		!strings.Contains(err.Error(), "vm_network_vlan_mode_mismatch") {
		t.Fatalf("mode mismatch = %v", err)
	}
}

func TestLegacyUnfilteredIsNarrow(t *testing.T) {
	contract, err := LegacyUnfiltered(KindVM, "LAN", "")
	if err != nil {
		t.Fatalf("legacy contract: %v", err)
	}
	if contract.Version != CurrentVersion || contract.VLANFiltering || contract.SwitchType != "standard" {
		t.Fatalf("unexpected legacy contract: %#v", contract)
	}
}

func TestJailContractPolicyComparison(t *testing.T) {
	access10, access20 := 10, 20
	expected, err := Normalize(Contract{
		Version: CurrentVersion, Kind: KindJail, SwitchName: "LAN", SwitchType: "manual",
		VLANFiltering: true,
		VLANPolicy:    &bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access10},
	})
	if err != nil {
		t.Fatalf("normalize expected policy: %v", err)
	}
	actual := expected
	actual.VLANPolicy = &bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access20}
	if err := Compare(expected, actual); !errors.Is(err, ErrIncompatible) ||
		!strings.Contains(err.Error(), "jail_network_vlan_policy_mismatch") {
		t.Fatalf("policy mismatch = %v", err)
	}
}
