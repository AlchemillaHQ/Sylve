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
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	ModeAccess = "access"
	ModeTrunk  = "trunk"

	MinVLAN        = 1
	MaxVLAN        = 4094
	MaxTaggedVLANs = MaxVLAN
)

var (
	ErrInvalidMode         = errors.New("invalid VLAN port mode")
	ErrInvalidVLAN         = errors.New("invalid VLAN ID")
	ErrMissingAccessVLAN   = errors.New("access port requires an untagged VLAN")
	ErrAccessTaggedVLANs   = errors.New("access port cannot contain tagged VLANs")
	ErrEmptyTrunk          = errors.New("trunk requires at least one tagged VLAN")
	ErrNativeTaggedOverlap = errors.New("native VLAN cannot also be tagged")
	ErrTooManyTaggedVLANs  = errors.New("too many tagged VLANs")
	ErrInvalidVLANList     = errors.New("invalid VLAN list")
)

type PortPolicy struct {
	Mode         string `json:"mode" gorm:"column:mode"`
	UntaggedVLAN *int   `json:"untaggedVlan,omitempty" gorm:"column:untagged_vlan"`
	TaggedVLANs  []int  `json:"taggedVlans" gorm:"column:tagged_vlans;serializer:json;type:json"`
}

func (p PortPolicy) MarshalJSON() ([]byte, error) {
	type wirePolicy PortPolicy
	copy := wirePolicy(p)
	if copy.TaggedVLANs == nil {
		copy.TaggedVLANs = []int{}
	}
	return json.Marshal(copy)
}

func ValidVLAN(id int) bool {
	return id >= MinVLAN && id <= MaxVLAN
}

func cloneVLAN(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func Normalize(policy PortPolicy) (PortPolicy, error) {
	policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
	policy.UntaggedVLAN = cloneVLAN(policy.UntaggedVLAN)

	if policy.UntaggedVLAN != nil && !ValidVLAN(*policy.UntaggedVLAN) {
		return policy, fmt.Errorf("%w: %d", ErrInvalidVLAN, *policy.UntaggedVLAN)
	}

	if len(policy.TaggedVLANs) > MaxTaggedVLANs {
		return policy, fmt.Errorf("%w: %d", ErrTooManyTaggedVLANs, len(policy.TaggedVLANs))
	}
	tagged := append([]int(nil), policy.TaggedVLANs...)
	sort.Ints(tagged)
	normalizedTagged := make([]int, 0, len(tagged))
	for _, id := range tagged {
		if !ValidVLAN(id) {
			return policy, fmt.Errorf("%w: %d", ErrInvalidVLAN, id)
		}
		if len(normalizedTagged) == 0 || normalizedTagged[len(normalizedTagged)-1] != id {
			normalizedTagged = append(normalizedTagged, id)
		}
	}
	policy.TaggedVLANs = normalizedTagged

	switch policy.Mode {
	case ModeAccess:
		if policy.UntaggedVLAN == nil {
			return policy, ErrMissingAccessVLAN
		}
		if len(policy.TaggedVLANs) != 0 {
			return policy, ErrAccessTaggedVLANs
		}
	case ModeTrunk:
		if len(policy.TaggedVLANs) == 0 {
			return policy, ErrEmptyTrunk
		}
		if policy.UntaggedVLAN != nil {
			index := sort.SearchInts(policy.TaggedVLANs, *policy.UntaggedVLAN)
			if index < len(policy.TaggedVLANs) && policy.TaggedVLANs[index] == *policy.UntaggedVLAN {
				return policy, ErrNativeTaggedOverlap
			}
		}
	default:
		return policy, fmt.Errorf("%w: %q", ErrInvalidMode, policy.Mode)
	}

	return policy, nil
}

func ParseVLANList(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []int{}, nil
	}

	seen := make(map[int]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, ErrInvalidVLANList
		}

		bounds := strings.Split(item, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("%w: %q", ErrInvalidVLANList, item)
		}
		first, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
		if err != nil || !ValidVLAN(first) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidVLANList, item)
		}
		last := first
		if len(bounds) == 2 {
			last, err = strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil || !ValidVLAN(last) || last < first {
				return nil, fmt.Errorf("%w: %q", ErrInvalidVLANList, item)
			}
		}
		for id := first; id <= last; id++ {
			seen[id] = struct{}{}
		}
	}

	result := make([]int, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Ints(result)
	return result, nil
}

func ParsePortPolicyAssignments(value string) (map[string]PortPolicy, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return map[string]PortPolicy{}, nil
	}

	policies := make(map[string]PortPolicy)
	for _, rawAssignment := range strings.Split(value, ";") {
		port, rawPolicy, found := strings.Cut(strings.TrimSpace(rawAssignment), "=")
		port = strings.TrimSpace(port)
		rawPolicy = strings.TrimSpace(rawPolicy)
		if !found || port == "" || rawPolicy == "" {
			return nil, fmt.Errorf("%w: %q", ErrInvalidVLANList, rawAssignment)
		}
		if _, exists := policies[port]; exists {
			return nil, fmt.Errorf("duplicate VLAN policy for port %q", port)
		}

		parts := strings.Split(rawPolicy, ":")
		mode := strings.ToLower(strings.TrimSpace(parts[0]))
		var policy PortPolicy
		switch mode {
		case ModeAccess:
			if len(parts) != 2 {
				return nil, fmt.Errorf("%w: access policy for %q", ErrInvalidVLANList, port)
			}
			vlans, err := ParseVLANList(parts[1])
			if err != nil || len(vlans) != 1 {
				return nil, fmt.Errorf("%w: access policy for %q", ErrInvalidVLANList, port)
			}
			untagged := vlans[0]
			policy = PortPolicy{Mode: ModeAccess, UntaggedVLAN: &untagged, TaggedVLANs: []int{}}

		case ModeTrunk:
			policy = PortPolicy{Mode: ModeTrunk, TaggedVLANs: []int{}}
			seenNative := false
			seenTagged := false
			for _, option := range parts[1:] {
				key, optionValue, found := strings.Cut(strings.TrimSpace(option), "=")
				if !found {
					return nil, fmt.Errorf("%w: trunk policy for %q", ErrInvalidVLANList, port)
				}
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "native":
					vlans, err := ParseVLANList(optionValue)
					if seenNative || err != nil || len(vlans) != 1 {
						return nil, fmt.Errorf("%w: trunk native VLAN for %q", ErrInvalidVLANList, port)
					}
					seenNative = true
					native := vlans[0]
					policy.UntaggedVLAN = &native
				case "tagged":
					vlans, err := ParseVLANList(optionValue)
					if seenTagged || err != nil {
						return nil, fmt.Errorf("%w: trunk tagged VLANs for %q", ErrInvalidVLANList, port)
					}
					seenTagged = true
					policy.TaggedVLANs = vlans
				default:
					return nil, fmt.Errorf("%w: trunk option for %q", ErrInvalidVLANList, port)
				}
			}
			if !seenTagged {
				return nil, fmt.Errorf("%w: trunk tagged VLANs for %q", ErrInvalidVLANList, port)
			}

		default:
			return nil, fmt.Errorf("%w: policy mode for %q", ErrInvalidVLANList, port)
		}

		normalized, err := Normalize(policy)
		if err != nil {
			return nil, fmt.Errorf("invalid VLAN policy for port %q: %w", port, err)
		}
		policies[port] = normalized
	}

	return policies, nil
}
