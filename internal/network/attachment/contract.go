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
	"fmt"
	"slices"
	"strings"

	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
)

const CurrentVersion = 1

type Kind string

const (
	KindVM   Kind = "vm"
	KindJail Kind = "jail"
)

var (
	ErrUnsupportedVersion = errors.New("unsupported network attachment contract version")
	ErrInvalidKind        = errors.New("invalid network attachment kind")
	ErrInvalidSwitch      = errors.New("invalid network attachment switch")
	ErrIncompatible       = errors.New("incompatible network attachment")
)

type Contract struct {
	Version           int                    `json:"version"`
	Kind              Kind                   `json:"kind"`
	SwitchName        string                 `json:"switchName"`
	SwitchType        string                 `json:"switchType"`
	VLANFiltering     bool                   `json:"vlanFiltering"`
	DefaultAccessVLAN *int                   `json:"defaultAccessVlan,omitempty"`
	VLANPolicy        *bridgevlan.PortPolicy `json:"vlanPolicy,omitempty"`
}

type NamedContract struct {
	Name         string   `json:"name,omitempty"`
	Attachment   Contract `json:"attachment"`
	IdentityOnly bool     `json:"identityOnly,omitempty"`
}

type TargetResult struct {
	MissingSwitches             []string `json:"missingSwitches"`
	IncompatibleSwitches        []string `json:"incompatibleSwitches"`
	NetworkCompatibilityChecked bool     `json:"networkCompatibilityChecked"`
}

func cloneVLAN(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func clonePolicy(value *bridgevlan.PortPolicy) *bridgevlan.PortPolicy {
	if value == nil {
		return nil
	}
	copy := *value
	copy.UntaggedVLAN = cloneVLAN(value.UntaggedVLAN)
	copy.TaggedVLANs = append([]int(nil), value.TaggedVLANs...)
	return &copy
}

func NormalizeSwitchType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "standard"
	}
	if value != "standard" && value != "manual" {
		return "", fmt.Errorf("%w: %q", ErrInvalidSwitch, value)
	}
	return value, nil
}

func Normalize(value Contract) (Contract, error) {
	if value.Version != CurrentVersion {
		return value, fmt.Errorf("%w: %d", ErrUnsupportedVersion, value.Version)
	}
	if value.Kind != KindVM && value.Kind != KindJail {
		return value, fmt.Errorf("%w: %q", ErrInvalidKind, value.Kind)
	}
	value.SwitchName = strings.TrimSpace(value.SwitchName)
	if value.SwitchName == "" {
		return value, fmt.Errorf("%w: switch name is empty", ErrInvalidSwitch)
	}
	switchType, err := NormalizeSwitchType(value.SwitchType)
	if err != nil {
		return value, err
	}
	value.SwitchType = switchType
	value.DefaultAccessVLAN = cloneVLAN(value.DefaultAccessVLAN)
	value.VLANPolicy = clonePolicy(value.VLANPolicy)

	switch value.Kind {
	case KindVM:
		if value.VLANPolicy != nil {
			return value, fmt.Errorf("vm_network_vlan_policy_unsupported")
		}
		if !value.VLANFiltering {
			if value.DefaultAccessVLAN != nil {
				return value, fmt.Errorf("vm_network_default_access_vlan_requires_filtering")
			}
			return value, nil
		}
		if value.DefaultAccessVLAN == nil || !bridgevlan.ValidVLAN(*value.DefaultAccessVLAN) {
			return value, fmt.Errorf("filtered_switch_vm_default_access_vlan_required: %s", value.SwitchName)
		}

	case KindJail:
		if value.DefaultAccessVLAN != nil {
			return value, fmt.Errorf("jail_network_default_access_vlan_unsupported")
		}
		if !value.VLANFiltering {
			if value.VLANPolicy != nil {
				return value, fmt.Errorf("vlan_policy_requires_filtered_switch")
			}
			return value, nil
		}
		if value.VLANPolicy == nil {
			return value, fmt.Errorf("filtered_switch_vlan_policy_required")
		}
		normalized, err := bridgevlan.Normalize(*value.VLANPolicy)
		if err != nil {
			return value, fmt.Errorf("invalid_vlan_policy: %w", err)
		}
		value.VLANPolicy = &normalized
	}

	return value, nil
}

func LegacyUnfiltered(kind Kind, switchName, switchType string) (Contract, error) {
	return Normalize(Contract{
		Version:    CurrentVersion,
		Kind:       kind,
		SwitchName: switchName,
		SwitchType: switchType,
	})
}

func mismatchCode(kind Kind, suffix string) string {
	return fmt.Sprintf("%s_network_%s", kind, suffix)
}

func Compare(expected, actual Contract) error {
	expected, err := Normalize(expected)
	if err != nil {
		return fmt.Errorf("normalize expected attachment: %w", err)
	}
	actual, err = Normalize(actual)
	if err != nil {
		return fmt.Errorf("normalize actual attachment: %w", err)
	}
	if expected.Kind != actual.Kind {
		return fmt.Errorf("%w: network_attachment_kind_mismatch", ErrIncompatible)
	}
	if expected.SwitchType != actual.SwitchType {
		return fmt.Errorf("%w: %s", ErrIncompatible, mismatchCode(expected.Kind, "switch_type_mismatch"))
	}
	if expected.SwitchName != actual.SwitchName {
		return fmt.Errorf("%w: %s", ErrIncompatible, mismatchCode(expected.Kind, "switch_name_mismatch"))
	}
	if expected.VLANFiltering != actual.VLANFiltering {
		return fmt.Errorf(
			"%w: %s: expected_filtered=%t actual_filtered=%t",
			ErrIncompatible,
			mismatchCode(expected.Kind, "vlan_mode_mismatch"),
			expected.VLANFiltering,
			actual.VLANFiltering,
		)
	}
	if !expected.VLANFiltering {
		return nil
	}

	if expected.Kind == KindVM {
		if expected.DefaultAccessVLAN == nil || actual.DefaultAccessVLAN == nil ||
			*expected.DefaultAccessVLAN != *actual.DefaultAccessVLAN {
			return fmt.Errorf("%w: vm_network_default_access_vlan_mismatch", ErrIncompatible)
		}
		return nil
	}

	if expected.VLANPolicy == nil || actual.VLANPolicy == nil ||
		expected.VLANPolicy.Mode != actual.VLANPolicy.Mode ||
		!optionalVLANEqual(expected.VLANPolicy.UntaggedVLAN, actual.VLANPolicy.UntaggedVLAN) ||
		!slices.Equal(expected.VLANPolicy.TaggedVLANs, actual.VLANPolicy.TaggedVLANs) {
		return fmt.Errorf("%w: jail_network_vlan_policy_mismatch", ErrIncompatible)
	}
	return nil
}

func optionalVLANEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
