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

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

func TestResolverUsesPersistedStandardAndLiveManualState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	defaultVLAN := 10
	standard := networkModels.StandardSwitch{
		Name: "standard-lan", BridgeName: "vm-lan", VLANFiltering: true,
		DefaultAccessVLAN: &defaultVLAN, Private: true,
	}
	manual := networkModels.ManualSwitch{Name: "manual-lan", Bridge: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("create standard switch: %v", err)
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual switch: %v", err)
	}

	var inspected, validated []string
	resolver := Resolver{
		DB: db,
		InspectBridge: func(bridge string) (bridgevlan.BridgeState, error) {
			inspected = append(inspected, bridge)
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 20}, nil
		},
		ValidateFilteredBridge: func(bridge string, expected *int) (bridgevlan.BridgeState, error) {
			validated = append(validated, bridge)
			if bridge != "vm-lan" || expected == nil || *expected != defaultVLAN {
				t.Fatalf("unexpected standard validation: bridge=%s expected=%v", bridge, expected)
			}
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
		},
	}

	for _, test := range []struct {
		name       string
		switchType string
		switchID   uint
		wantVLAN   int
	}{
		{standard.Name, "standard", standard.ID, defaultVLAN},
		{manual.Name, "manual", manual.ID, 20},
	} {
		t.Run(test.switchType, func(t *testing.T) {
			for _, resolve := range []struct {
				name string
				fn   func() (ResolvedSwitch, error)
			}{
				{"id", func() (ResolvedSwitch, error) { return resolver.ResolveDesiredByID(test.switchType, test.switchID) }},
				{"name", func() (ResolvedSwitch, error) { return resolver.resolveDesiredByName(test.switchType, test.name) }},
				{"untyped name", func() (ResolvedSwitch, error) { return resolver.ResolveDesiredByNameAny(test.name) }},
			} {
				t.Run(resolve.name, func(t *testing.T) {
					inspected, validated = nil, nil
					sw, err := resolve.fn()
					if err != nil {
						t.Fatalf("resolve desired switch: %v", err)
					}
					contract, err := sw.Contract(KindVM, nil)
					if err != nil || contract.DefaultAccessVLAN == nil || *contract.DefaultAccessVLAN != test.wantVLAN {
						t.Fatalf("contract = %#v, err = %v", contract, err)
					}
					if test.switchType == "standard" {
						if !sw.Private || len(inspected) != 0 || len(validated) != 0 {
							t.Fatalf("desired Standard state must use only its declaration: %#v, inspected=%v validated=%v", sw, inspected, validated)
						}
					} else if len(inspected) != 1 || inspected[0] != manual.Bridge || !sw.Manual.VLANFiltering {
						t.Fatalf("desired Manual state must be observed: %#v, inspected=%v", sw, inspected)
					}
				})
			}

			inspected, validated = nil, nil
			if _, _, err := resolver.EffectiveContractByID(KindVM, test.switchType, test.switchID, nil); err != nil {
				t.Fatalf("effective contract: %v", err)
			}
			if test.switchType == "standard" && (len(validated) != 1 || len(inspected) != 0) ||
				test.switchType == "manual" && (len(inspected) != 1 || len(validated) != 0) {
				t.Fatalf("effective state inspected=%v validated=%v", inspected, validated)
			}
		})
	}
}

func TestResolverValidateTargetUsesOneContractPath(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	defaultVLAN := 20
	sw := networkModels.StandardSwitch{
		Name: "LAN", BridgeName: "vm-lan", VLANFiltering: true,
		DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("create switch: %v", err)
	}
	resolver := Resolver{DB: db}
	expected := Contract{
		Version: CurrentVersion, Kind: KindVM, SwitchName: "LAN", SwitchType: "standard",
		VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN,
	}
	if _, err := resolver.ValidateDesiredTarget(expected); err != nil {
		t.Fatalf("validate matching target: %v", err)
	}

	wrong := 30
	expected.DefaultAccessVLAN = &wrong
	if _, err := resolver.ValidateDesiredTarget(expected); err == nil ||
		!strings.Contains(err.Error(), "vm_network_default_access_vlan_mismatch") {
		t.Fatalf("target mismatch = %v", err)
	}
}

func TestResolverIdentityDoesNotInspectManualRuntime(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	standard := networkModels.StandardSwitch{Name: "standard-lan", BridgeName: "vm-lan"}
	manual := networkModels.ManualSwitch{Name: "manual-lan", Bridge: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("create standard switch: %v", err)
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual switch: %v", err)
	}

	inspectCalls := 0
	resolver := Resolver{
		DB: db,
		InspectBridge: func(string) (bridgevlan.BridgeState, error) {
			inspectCalls++
			return bridgevlan.BridgeState{}, errors.New("runtime unavailable")
		},
		ValidateFilteredBridge: func(string, *int) (bridgevlan.BridgeState, error) {
			t.Fatal("identity resolution must not validate Standard Switch runtime")
			return bridgevlan.BridgeState{}, nil
		},
	}

	byID, err := resolver.ResolveIdentityByID("manual", manual.ID)
	if err != nil {
		t.Fatalf("resolve manual identity by ID: %v", err)
	}
	byName, err := resolver.ResolveIdentityByName("manual", manual.Name)
	if err != nil {
		t.Fatalf("resolve manual identity by name: %v", err)
	}
	if _, err := resolver.ResolveIdentityByID("standard", standard.ID); err != nil {
		t.Fatalf("resolve standard identity: %v", err)
	}
	byNameAny, err := resolver.ResolveIdentityByNameAny(manual.Name)
	if err != nil {
		t.Fatalf("resolve manual identity by globally unique name: %v", err)
	}
	if inspectCalls != 0 {
		t.Fatalf("identity resolution inspected live bridge %d times", inspectCalls)
	}
	if byID.Name != manual.Name || byID.Type != "manual" || byID.Bridge != manual.Bridge {
		t.Fatalf("unexpected identity by ID: %#v", byID)
	}
	if byName.ID != manual.ID || byName.VLANFiltering || byName.DefaultAccessVLAN != nil {
		t.Fatalf("unexpected identity by name: %#v", byName)
	}
	if byNameAny.ID != manual.ID || byNameAny.Type != "manual" {
		t.Fatalf("unexpected identity by untyped name: %#v", byNameAny)
	}
}

func TestResolverByIDDistinguishesInvalidTypeFromMissingSwitch(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
	)
	resolver := Resolver{DB: db}

	for _, resolve := range []struct {
		name string
		fn   func(string, uint) (ResolvedSwitch, error)
	}{
		{name: "desired", fn: resolver.ResolveDesiredByID},
		{name: "identity", fn: resolver.ResolveIdentityByID},
	} {
		t.Run(resolve.name, func(t *testing.T) {
			if _, err := resolve.fn("unsupported", 1); !errors.Is(err, ErrInvalidSwitch) {
				t.Fatalf("invalid switch type error = %v, want ErrInvalidSwitch", err)
			} else if errors.Is(err, ErrSwitchNotFound) {
				t.Fatalf("invalid switch type was misclassified as missing: %v", err)
			}

			if _, err := resolve.fn("standard", 0); !errors.Is(err, ErrSwitchNotFound) {
				t.Fatalf("zero switch ID error = %v, want ErrSwitchNotFound", err)
			}
		})
	}
}

func TestResolverValidateTargetsClassifiesMissingAndIncompatible(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	defaultVLAN := 20
	switches := []networkModels.StandardSwitch{
		{Name: "LAN", BridgeName: "vm-lan"},
		{Name: "tenant", BridgeName: "vm-tenant", VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN},
	}
	if err := db.Create(&switches).Error; err != nil {
		t.Fatalf("create switches: %v", err)
	}

	expectedVLAN := 30
	resolver := Resolver{
		DB: db,
		InspectBridge: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{}, nil
		},
		ValidateFilteredBridge: func(string, *int) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: defaultVLAN}, nil
		},
	}
	result := resolver.ValidateEffectiveTargets(KindVM, []NamedContract{
		{
			Name: "vtnet0",
			Attachment: Contract{
				Version: CurrentVersion, Kind: KindVM, SwitchName: "LAN", SwitchType: "standard",
			},
		},
		{
			Name: "vtnet1",
			Attachment: Contract{
				Version: CurrentVersion, Kind: KindVM, SwitchName: "missing", SwitchType: "standard",
			},
		},
		{
			Name: "vtnet2",
			Attachment: Contract{
				Version: CurrentVersion, Kind: KindVM, SwitchName: "tenant", SwitchType: "standard",
				VLANFiltering: true, DefaultAccessVLAN: &expectedVLAN,
			},
		},
	})

	if !result.NetworkCompatibilityChecked {
		t.Fatal("target did not acknowledge network compatibility validation")
	}
	if len(result.MissingSwitches) != 1 || result.MissingSwitches[0] != "vtnet1" {
		t.Fatalf("unexpected missing switches: %#v", result.MissingSwitches)
	}
	if len(result.IncompatibleSwitches) != 1 ||
		!strings.Contains(result.IncompatibleSwitches[0], "vtnet2") ||
		!strings.Contains(result.IncompatibleSwitches[0], "vm_network_default_access_vlan_mismatch") {
		t.Fatalf("unexpected incompatible switches: %#v", result.IncompatibleSwitches)
	}
}

func TestResolverValidateTargetsInspectsEachManualSwitchOnce(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.ManualSwitch{})
	manual := networkModels.ManualSwitch{Name: "tenant", Bridge: "bridge-tenant"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create Manual Switch: %v", err)
	}

	inspectCalls := 0
	resolver := Resolver{
		DB: db,
		InspectBridge: func(bridge string) (bridgevlan.BridgeState, error) {
			inspectCalls++
			if bridge != manual.Bridge {
				t.Fatalf("inspected bridge %q, want %q", bridge, manual.Bridge)
			}
			return bridgevlan.BridgeState{VLANFiltering: true}, nil
		},
	}
	access10, access20 := 10, 20
	entries := []NamedContract{
		{
			Name: "vnet0",
			Attachment: Contract{
				Version: CurrentVersion, Kind: KindJail,
				SwitchName: manual.Name, SwitchType: "manual", VLANFiltering: true,
				VLANPolicy: &bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access10},
			},
		},
		{
			Name: "vnet1",
			Attachment: Contract{
				Version: CurrentVersion, Kind: KindJail,
				SwitchName: manual.Name, SwitchType: "manual", VLANFiltering: true,
				VLANPolicy: &bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &access20},
			},
		},
	}

	result := resolver.ValidateEffectiveTargets(KindJail, entries)
	if len(result.MissingSwitches) != 0 || len(result.IncompatibleSwitches) != 0 {
		t.Fatalf("matching Manual Switch rejected: %#v", result)
	}
	if inspectCalls != 1 {
		t.Fatalf("Manual Switch inspected %d times, want once", inspectCalls)
	}
}

func TestResolverRejectsUnsupportedManualVLANStateForWorkload(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.ManualSwitch{})
	manual := networkModels.ManualSwitch{Name: "tenant", Bridge: "bridge-tenant"}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create Manual Switch: %v", err)
	}

	resolver := Resolver{
		DB: db,
		InspectBridge: func(string) (bridgevlan.BridgeState, error) {
			return bridgevlan.BridgeState{
				VLANFiltering: true,
				DefaultPVID:   10,
				DefaultQinQ:   true,
			}, nil
		},
	}
	accessVLAN := 10
	_, _, err := resolver.DesiredContractByID(KindJail, "manual", manual.ID, &bridgevlan.PortPolicy{
		Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN,
	})
	if err == nil || !strings.Contains(err.Error(), "filtered_switch_qinq_unsupported") {
		t.Fatalf("unsupported Manual Switch workload error = %v", err)
	}
}

func TestResolverValidateTargetsRejectsUnexpectedAttachmentKind(t *testing.T) {
	result := (Resolver{}).ValidateEffectiveTargets(KindVM, []NamedContract{{
		Name: "vtnet0",
		Attachment: Contract{
			Version:    CurrentVersion,
			Kind:       KindJail,
			SwitchName: "LAN",
			SwitchType: "standard",
		},
	}})

	if len(result.MissingSwitches) != 0 || len(result.IncompatibleSwitches) != 1 ||
		!strings.Contains(result.IncompatibleSwitches[0], "expected \"vm\", got \"jail\"") {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestResolverValidateTargetsUsesIdentityOnlyForDisabledVMNetwork(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	defaultVLAN := 20
	sw := networkModels.StandardSwitch{
		Name: "tenant", BridgeName: "vm-tenant", VLANFiltering: true,
		DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("create switch: %v", err)
	}

	inspectCalls := 0
	resolver := Resolver{
		DB: db,
		InspectBridge: func(string) (bridgevlan.BridgeState, error) {
			inspectCalls++
			return bridgevlan.BridgeState{}, errors.New("must not inspect identity-only attachment")
		},
		ValidateFilteredBridge: func(string, *int) (bridgevlan.BridgeState, error) {
			inspectCalls++
			return bridgevlan.BridgeState{}, errors.New("must not validate identity-only attachment")
		},
	}
	identity, err := LegacyUnfiltered(KindVM, sw.Name, "standard")
	if err != nil {
		t.Fatalf("create identity contract: %v", err)
	}

	result := resolver.ValidateEffectiveTargets(KindVM, []NamedContract{{
		Name: "vtnet0", Attachment: identity, IdentityOnly: true,
	}})
	if len(result.MissingSwitches) != 0 || len(result.IncompatibleSwitches) != 0 {
		t.Fatalf("identity-only attachment rejected: %#v", result)
	}
	if inspectCalls != 0 {
		t.Fatalf("identity-only attachment performed %d runtime inspections", inspectCalls)
	}
}

func TestResolverValidateTargetsRejectsIdentityOnlyJailNetwork(t *testing.T) {
	contract, err := LegacyUnfiltered(KindJail, "LAN", "standard")
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	result := (Resolver{}).ValidateEffectiveTargets(KindJail, []NamedContract{{
		Name: "vnet0", Attachment: contract, IdentityOnly: true,
	}})
	if len(result.MissingSwitches) != 0 || len(result.IncompatibleSwitches) != 1 ||
		!strings.Contains(result.IncompatibleSwitches[0], "identity-only validation is only valid for VM networks") {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}
