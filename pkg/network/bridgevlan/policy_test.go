// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bridgevlan

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func vlanPointer(value int) *int { return &value }

func TestNormalizePortPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  PortPolicy
		want    PortPolicy
		wantErr error
	}{
		{
			name:   "access",
			policy: PortPolicy{Mode: " ACCESS ", UntaggedVLAN: vlanPointer(10)},
			want:   PortPolicy{Mode: ModeAccess, UntaggedVLAN: vlanPointer(10), TaggedVLANs: []int{}},
		},
		{
			name:   "trunk canonicalizes",
			policy: PortPolicy{Mode: ModeTrunk, UntaggedVLAN: vlanPointer(10), TaggedVLANs: []int{30, 20, 30}},
			want:   PortPolicy{Mode: ModeTrunk, UntaggedVLAN: vlanPointer(10), TaggedVLANs: []int{20, 30}},
		},
		{name: "bad mode", policy: PortPolicy{Mode: "auto"}, wantErr: ErrInvalidMode},
		{name: "missing access VLAN", policy: PortPolicy{Mode: ModeAccess}, wantErr: ErrMissingAccessVLAN},
		{name: "tagged access", policy: PortPolicy{Mode: ModeAccess, UntaggedVLAN: vlanPointer(10), TaggedVLANs: []int{20}}, wantErr: ErrAccessTaggedVLANs},
		{name: "empty trunk", policy: PortPolicy{Mode: ModeTrunk}, wantErr: ErrEmptyTrunk},
		{name: "overlap", policy: PortPolicy{Mode: ModeTrunk, UntaggedVLAN: vlanPointer(20), TaggedVLANs: []int{20}}, wantErr: ErrNativeTaggedOverlap},
		{name: "reserved zero", policy: PortPolicy{Mode: ModeAccess, UntaggedVLAN: vlanPointer(0)}, wantErr: ErrInvalidVLAN},
		{name: "reserved 4095", policy: PortPolicy{Mode: ModeTrunk, TaggedVLANs: []int{4095}}, wantErr: ErrInvalidVLAN},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize(test.policy)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Normalize() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize() error: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Normalize() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeRejectsOversizedTaggedInput(t *testing.T) {
	policy := PortPolicy{
		Mode:        ModeTrunk,
		TaggedVLANs: make([]int, MaxTaggedVLANs+1),
	}
	for index := range policy.TaggedVLANs {
		policy.TaggedVLANs[index] = MinVLAN
	}

	if _, err := Normalize(policy); !errors.Is(err, ErrTooManyTaggedVLANs) {
		t.Fatalf("oversized tagged VLAN input error = %v", err)
	}
}

func TestParsePortPolicyAssignments(t *testing.T) {
	policies, err := ParsePortPolicyAssignments(
		"igb0=access:10;igb1=trunk:native=20:tagged=30,40-41;igb2=trunk:tagged=50",
	)
	if err != nil {
		t.Fatalf("parse port policies: %v", err)
	}
	if len(policies) != 3 || policies["igb0"].Mode != ModeAccess ||
		policies["igb0"].UntaggedVLAN == nil || *policies["igb0"].UntaggedVLAN != 10 {
		t.Fatalf("unexpected access policy: %#v", policies["igb0"])
	}
	if got := policies["igb1"]; got.UntaggedVLAN == nil || *got.UntaggedVLAN != 20 ||
		!slices.Equal(got.TaggedVLANs, []int{30, 40, 41}) {
		t.Fatalf("unexpected native trunk policy: %#v", got)
	}
	if got := policies["igb2"]; got.UntaggedVLAN != nil || !slices.Equal(got.TaggedVLANs, []int{50}) {
		t.Fatalf("unexpected tagged trunk policy: %#v", got)
	}
}

func TestParsePortPolicyAssignmentsRejectsUnsafeForms(t *testing.T) {
	for _, value := range []string{
		"igb0=access:10,20",
		"igb0=trunk:native=10",
		"igb0=trunk:native=10:tagged=10",
		"igb0=access:10;igb0=access:20",
		"igb0=trunk:all=true",
	} {
		if _, err := ParsePortPolicyAssignments(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestParseVLANList(t *testing.T) {
	got, err := ParseVLANList("30, 10-12,11,20")
	if err != nil {
		t.Fatalf("ParseVLANList(): %v", err)
	}
	want := []int{10, 11, 12, 20, 30}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseVLANList() = %v, want %v", got, want)
	}

	for _, value := range []string{"0", "4095", "20-10", "1-2-3", "10,,20", "x"} {
		if _, err := ParseVLANList(value); !errors.Is(err, ErrInvalidVLANList) {
			t.Errorf("ParseVLANList(%q) error = %v", value, err)
		}
	}
}

func TestPortPolicyJSONUsesEmptyArray(t *testing.T) {
	encoded, err := json.Marshal(PortPolicy{Mode: ModeAccess, UntaggedVLAN: vlanPointer(10)})
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	if string(encoded) != `{"mode":"access","untaggedVlan":10,"taggedVlans":[]}` {
		t.Fatalf("Marshal() = %s", encoded)
	}
}
