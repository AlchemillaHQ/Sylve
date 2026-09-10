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
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

var ErrSwitchNotFound = errors.New("network attachment switch not found")

type BridgeInspector func(string) (bridgevlan.BridgeState, error)
type FilteredBridgeValidator func(string, *int) (bridgevlan.BridgeState, error)

type Resolver struct {
	DB                     *gorm.DB
	InspectBridge          BridgeInspector
	ValidateFilteredBridge FilteredBridgeValidator
}

type ResolvedSwitch struct {
	ID                uint
	Name              string
	Type              string
	Bridge            string
	VLANFiltering     bool
	DefaultAccessVLAN *int
	Private           bool
	Standard          *networkModels.StandardSwitch
	Manual            *networkModels.ManualSwitch
}

func NewResolver(db *gorm.DB) Resolver {
	return Resolver{
		DB:                     db,
		InspectBridge:          bridgevlan.InspectBridge,
		ValidateFilteredBridge: bridgevlan.ValidateFilteredBridge,
	}
}

func (r Resolver) resolveDesiredByName(switchType, switchName string) (ResolvedSwitch, error) {
	sw, err := r.ResolveIdentityByName(switchType, switchName)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	return r.observeManual(sw)
}

func (r Resolver) ResolveDesiredByID(switchType string, switchID uint) (ResolvedSwitch, error) {
	sw, err := r.ResolveIdentityByID(switchType, switchID)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	return r.observeManual(sw)
}

func (r Resolver) ResolveIdentityByID(switchType string, switchID uint) (ResolvedSwitch, error) {
	if r.DB == nil {
		return ResolvedSwitch{}, fmt.Errorf("db_not_initialized")
	}
	switchType, err := NormalizeSwitchType(switchType)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	if switchID == 0 {
		return ResolvedSwitch{}, fmt.Errorf("%w: switch_not_found: %s:%d", ErrSwitchNotFound, switchType, switchID)
	}
	return r.resolve(switchType, func(destination any) error {
		return r.DB.First(destination, switchID).Error
	})
}

func (r Resolver) ResolveIdentityByName(switchType, switchName string) (ResolvedSwitch, error) {
	if r.DB == nil {
		return ResolvedSwitch{}, fmt.Errorf("db_not_initialized")
	}
	switchType, err := NormalizeSwitchType(switchType)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	switchName = strings.TrimSpace(switchName)
	if switchName == "" {
		return ResolvedSwitch{}, fmt.Errorf("invalid_switch_name")
	}
	return r.resolve(switchType, func(destination any) error {
		return r.DB.Where("name = ?", switchName).First(destination).Error
	})
}

func (r Resolver) ResolveIdentityByNameAny(switchName string) (ResolvedSwitch, error) {
	switchName = strings.TrimSpace(switchName)
	standard, err := r.ResolveIdentityByName("standard", switchName)
	if err == nil {
		return standard, nil
	}
	if !errors.Is(err, ErrSwitchNotFound) {
		return ResolvedSwitch{}, err
	}
	manual, manualErr := r.ResolveIdentityByName("manual", switchName)
	if manualErr == nil {
		return manual, nil
	}
	if !errors.Is(manualErr, ErrSwitchNotFound) {
		return ResolvedSwitch{}, manualErr
	}
	return ResolvedSwitch{}, fmt.Errorf("%w: switch_not_found: %s", ErrSwitchNotFound, switchName)
}

func (r Resolver) ResolveDesiredByNameAny(switchName string) (ResolvedSwitch, error) {
	sw, err := r.ResolveIdentityByNameAny(switchName)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	return r.observeManual(sw)
}

func (r Resolver) resolve(
	switchType string,
	load func(any) error,
) (ResolvedSwitch, error) {
	switch switchType {
	case "standard":
		var sw networkModels.StandardSwitch
		if err := load(&sw); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ResolvedSwitch{}, fmt.Errorf("%w: switch_not_found: standard", ErrSwitchNotFound)
			}
			return ResolvedSwitch{}, fmt.Errorf("failed_to_find_standard_switch: %w", err)
		}
		return ResolvedSwitch{
			ID: sw.ID, Name: sw.Name, Type: "standard", Bridge: sw.BridgeName,
			VLANFiltering: sw.VLANFiltering, DefaultAccessVLAN: cloneVLAN(sw.DefaultAccessVLAN),
			Private:  sw.Private,
			Standard: &sw,
		}, nil

	case "manual":
		var sw networkModels.ManualSwitch
		if err := load(&sw); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ResolvedSwitch{}, fmt.Errorf("%w: switch_not_found: manual", ErrSwitchNotFound)
			}
			return ResolvedSwitch{}, fmt.Errorf("failed_to_find_manual_switch: %w", err)
		}
		return ResolvedSwitch{
			ID: sw.ID, Name: sw.Name, Type: "manual", Bridge: sw.Bridge,
			Manual: &sw,
		}, nil
	}

	return ResolvedSwitch{}, fmt.Errorf("invalid_switch_type")
}

func (r Resolver) observeManual(sw ResolvedSwitch) (ResolvedSwitch, error) {
	if sw.Type != "manual" {
		return sw, nil
	}
	if r.InspectBridge == nil {
		r.InspectBridge = bridgevlan.InspectBridge
	}
	state, err := r.InspectBridge(sw.Bridge)
	if err != nil {
		return ResolvedSwitch{}, fmt.Errorf("failed_to_inspect_manual_switch_vlan_state: %s: %w", sw.Name, err)
	}
	if state.VLANFiltering && state.DefaultQinQ {
		return ResolvedSwitch{}, fmt.Errorf("filtered_switch_qinq_unsupported: %s", sw.Name)
	}
	var defaultAccessVLAN *int
	if state.VLANFiltering && state.DefaultPVID != 0 {
		if !bridgevlan.ValidVLAN(state.DefaultPVID) {
			return ResolvedSwitch{}, fmt.Errorf("invalid_manual_switch_default_pvid: %d", state.DefaultPVID)
		}
		defaultAccessVLAN = cloneVLAN(&state.DefaultPVID)
	}
	sw.VLANFiltering = state.VLANFiltering
	sw.DefaultAccessVLAN = defaultAccessVLAN
	sw.Manual.VLANFiltering = state.VLANFiltering
	sw.Manual.DefaultAccessVLAN = cloneVLAN(defaultAccessVLAN)
	return sw, nil
}

func (r Resolver) ValidateRuntime(sw ResolvedSwitch) error {
	if sw.Type != "standard" {
		return nil
	}
	if r.InspectBridge == nil {
		r.InspectBridge = bridgevlan.InspectBridge
	}
	if r.ValidateFilteredBridge == nil {
		r.ValidateFilteredBridge = bridgevlan.ValidateFilteredBridge
	}
	if sw.VLANFiltering {
		if _, err := r.ValidateFilteredBridge(sw.Bridge, sw.DefaultAccessVLAN); err != nil {
			return fmt.Errorf("filtered_switch_runtime_mismatch: %s: %w", sw.Name, err)
		}
		return nil
	}
	state, err := r.InspectBridge(sw.Bridge)
	if err != nil {
		return fmt.Errorf("unfiltered_switch_runtime_inspection_failed: %s: %w", sw.Name, err)
	}
	if state.VLANFiltering {
		return fmt.Errorf("unfiltered_switch_runtime_vlan_mode_mismatch: %s", sw.Name)
	}
	return nil
}

func (sw ResolvedSwitch) Contract(kind Kind, policy *bridgevlan.PortPolicy) (Contract, error) {
	contract := Contract{
		Version:           CurrentVersion,
		Kind:              kind,
		SwitchName:        sw.Name,
		SwitchType:        sw.Type,
		VLANFiltering:     sw.VLANFiltering,
		DefaultAccessVLAN: cloneVLAN(sw.DefaultAccessVLAN),
	}
	if kind == KindJail {
		contract.DefaultAccessVLAN = nil
		contract.VLANPolicy = clonePolicy(policy)
	}
	return Normalize(contract)
}

func (r Resolver) DesiredContractByID(
	kind Kind,
	switchType string,
	switchID uint,
	policy *bridgevlan.PortPolicy,
) (Contract, ResolvedSwitch, error) {
	sw, err := r.ResolveDesiredByID(switchType, switchID)
	if err != nil {
		return Contract{}, ResolvedSwitch{}, err
	}
	contract, err := sw.Contract(kind, policy)
	if err != nil {
		return Contract{}, ResolvedSwitch{}, err
	}
	return contract, sw, nil
}

func (r Resolver) EffectiveContractByID(
	kind Kind,
	switchType string,
	switchID uint,
	policy *bridgevlan.PortPolicy,
) (Contract, ResolvedSwitch, error) {
	contract, sw, err := r.DesiredContractByID(kind, switchType, switchID, policy)
	if err != nil {
		return Contract{}, ResolvedSwitch{}, err
	}
	if err := r.ValidateRuntime(sw); err != nil {
		return Contract{}, ResolvedSwitch{}, err
	}
	return contract, sw, nil
}

func validateResolvedTarget(expected Contract, actualSwitch ResolvedSwitch) error {
	if expected.VLANFiltering != actualSwitch.VLANFiltering {
		return fmt.Errorf(
			"%w: %s: expected_filtered=%t actual_filtered=%t",
			ErrIncompatible,
			mismatchCode(expected.Kind, "vlan_mode_mismatch"),
			expected.VLANFiltering,
			actualSwitch.VLANFiltering,
		)
	}
	actual, err := actualSwitch.Contract(expected.Kind, expected.VLANPolicy)
	if err != nil {
		return err
	}
	return Compare(expected, actual)
}

func (r Resolver) ValidateDesiredTarget(expected Contract) (ResolvedSwitch, error) {
	expected, err := Normalize(expected)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	actualSwitch, err := r.resolveDesiredByName(expected.SwitchType, expected.SwitchName)
	if err != nil {
		return ResolvedSwitch{}, err
	}
	if err := validateResolvedTarget(expected, actualSwitch); err != nil {
		return ResolvedSwitch{}, err
	}
	return actualSwitch, nil
}

func (r Resolver) ValidateEffectiveTargets(expectedKind Kind, entries []NamedContract) TargetResult {
	result := TargetResult{
		MissingSwitches:             []string{},
		IncompatibleSwitches:        []string{},
		NetworkCompatibilityChecked: true,
	}
	type cachedResolution struct {
		switchState ResolvedSwitch
		err         error
	}
	resolved := make(map[string]cachedResolution)
	for index, entry := range entries {
		label := strings.TrimSpace(entry.Name)
		if label == "" {
			label = strings.TrimSpace(entry.Attachment.SwitchName)
		}
		if label == "" {
			label = fmt.Sprintf("network-%d", index+1)
		}
		var err error
		if entry.IdentityOnly && expectedKind != KindVM {
			err = fmt.Errorf("%w: identity-only validation is only valid for VM networks", ErrInvalidKind)
		} else if entry.Attachment.Kind != expectedKind {
			err = fmt.Errorf(
				"%w: expected %q, got %q",
				ErrInvalidKind,
				expectedKind,
				entry.Attachment.Kind,
			)
		}
		expected := entry.Attachment
		if err == nil {
			expected, err = Normalize(entry.Attachment)
		}
		if err == nil {
			key := expected.SwitchType + "\x00" + expected.SwitchName
			if entry.IdentityOnly {
				key += "\x00identity"
			}
			cached, ok := resolved[key]
			if !ok {
				if entry.IdentityOnly {
					cached.switchState, cached.err = r.ResolveIdentityByName(
						expected.SwitchType, expected.SwitchName,
					)
				} else {
					cached.switchState, cached.err = r.resolveDesiredByName(
						expected.SwitchType, expected.SwitchName,
					)
					if cached.err == nil {
						cached.err = r.ValidateRuntime(cached.switchState)
					}
				}
				resolved[key] = cached
			}
			err = cached.err
			if err == nil && !entry.IdentityOnly {
				err = validateResolvedTarget(expected, cached.switchState)
			}
		}
		if err != nil {
			if errors.Is(err, ErrSwitchNotFound) {
				result.MissingSwitches = append(result.MissingSwitches, label)
			} else {
				result.IncompatibleSwitches = append(result.IncompatibleSwitches, fmt.Sprintf("%s: %v", label, err))
			}
		}
	}
	return result
}
