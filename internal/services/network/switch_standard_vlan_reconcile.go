// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

type filteredMemberReconcilePlan struct {
	Remove    []networkModels.NetworkPort
	Configure []networkModels.NetworkPort
	Add       []networkModels.NetworkPort
}

func planFilteredStandardBridgeMembers(
	previous []networkModels.NetworkPort,
	desired []networkModels.NetworkPort,
	attached map[string]bool,
	ready map[string]bool,
) filteredMemberReconcilePlan {
	previousByName := make(map[string]networkModels.NetworkPort, len(previous))
	desiredByName := make(map[string]networkModels.NetworkPort, len(desired))
	for _, port := range previous {
		previousByName[port.Name] = port
	}
	for _, port := range desired {
		desiredByName[port.Name] = port
	}

	removeNames := make([]string, 0)
	for name := range previousByName {
		if _, retained := desiredByName[name]; !retained && attached[name] {
			removeNames = append(removeNames, name)
		}
	}
	desiredNames := make([]string, 0, len(desiredByName))
	for name := range desiredByName {
		desiredNames = append(desiredNames, name)
	}
	sort.Strings(removeNames)
	sort.Strings(desiredNames)

	plan := filteredMemberReconcilePlan{
		Remove:    make([]networkModels.NetworkPort, 0, len(removeNames)),
		Configure: make([]networkModels.NetworkPort, 0),
		Add:       make([]networkModels.NetworkPort, 0),
	}
	for _, name := range removeNames {
		plan.Remove = append(plan.Remove, previousByName[name])
	}
	for _, name := range desiredNames {
		port := desiredByName[name]
		switch {
		case !attached[name]:
			plan.Add = append(plan.Add, port)
		case !ready[name]:
			plan.Configure = append(plan.Configure, port)
		}
	}
	return plan
}

func standardSwitchRuntimeMTU(sw networkModels.StandardSwitch) int {
	if sw.MTU > 0 {
		return sw.MTU
	}
	return 1500
}

func filteredMemberRuntimeMatches(port *iface.Interface, mtu int, disableOffloads bool) bool {
	if port == nil || !interfaceIsUp(port) || port.MTU != mtu {
		return false
	}
	if len(port.IPv4) != 0 || len(port.IPv6) != 0 ||
		interfaceFlagNamed(port.ND6.Flags, "ACCEPT_RTADV") ||
		interfaceFlagNamed(port.ND6.Flags, "AUTO_LINKLOCAL") {
		return false
	}
	return !disableOffloads || len(bridgeOffloadDisableArgs(port.Capabilities.Enabled.Raw)) == 0
}

func interfaceFlagNamed(flags iface.Flags, name string) bool {
	for _, flag := range flags.Desc {
		if strings.EqualFold(strings.TrimSpace(flag), name) {
			return true
		}
	}
	return false
}

func interfaceIsUp(interfaceObj *iface.Interface) bool {
	return interfaceObj != nil && (interfaceObj.Flags.Raw&1 != 0 || interfaceFlagNamed(interfaceObj.Flags, "UP"))
}

func filteredBridgeIPv6Disabled(interfaceObj *iface.Interface) bool {
	if interfaceObj == nil {
		return false
	}
	return interfaceFlagNamed(interfaceObj.ND6.Flags, "IFDISABLED") &&
		!interfaceFlagNamed(interfaceObj.ND6.Flags, "ACCEPT_RTADV")
}

func optionalVLANValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func optionalVLANFromPVID(value int) *int {
	if value == 0 {
		return nil
	}
	copy := value
	return &copy
}

func filteredDefaultAccessVLANChanged(previous, desired *int) bool {
	return optionalVLANValue(previous) != optionalVLANValue(desired)
}

func filteredBridgeUnmanagedMembers(
	bridgeInterface *iface.Interface,
	previous []networkModels.NetworkPort,
	desired []networkModels.NetworkPort,
	knownDynamic ...map[string]struct{},
) []string {
	if bridgeInterface == nil {
		return nil
	}

	managed := make(map[string]struct{}, len(previous)+len(desired))
	for _, port := range previous {
		managed[port.Name] = struct{}{}
	}
	for _, port := range desired {
		managed[port.Name] = struct{}{}
	}
	for _, known := range knownDynamic {
		for member := range known {
			managed[member] = struct{}{}
		}
	}

	unmanaged := make([]string, 0)
	for _, member := range bridgeInterface.BridgeMembers {
		if _, ok := managed[member.Name]; !ok {
			unmanaged = append(unmanaged, member.Name)
		}
	}
	sort.Strings(unmanaged)
	return unmanaged
}

func filteredDefaultAccessVLANMemberConflict(members []string) error {
	return standardSwitchConflict(
		"standard_switch_default_access_vlan_member_conflict",
		fmt.Errorf(
			"cannot change the default access VLAN while unmanaged bridge members are attached: %s",
			strings.Join(members, ", "),
		),
	)
}

func inspectFilteredBridgeTransition(
	bridge string,
	previousDefault *int,
	desiredDefault *int,
) (bridgevlan.BridgeState, error) {
	state, err := syncInspectBridgeVLAN(bridge)
	if err != nil {
		return state, err
	}
	if !state.VLANFiltering {
		return state, fmt.Errorf("bridge VLAN filtering is disabled")
	}
	if state.DefaultQinQ {
		return state, fmt.Errorf("bridge default Q-in-Q is enabled")
	}
	previous := optionalVLANValue(previousDefault)
	desired := optionalVLANValue(desiredDefault)
	if state.DefaultPVID != previous && state.DefaultPVID != desired {
		return state, fmt.Errorf(
			"bridge default PVID is %d; expected current %d or desired %d",
			state.DefaultPVID,
			previous,
			desired,
		)
	}
	return state, nil
}

func repairFilteredStandardBridgeDefaults(
	sw networkModels.StandardSwitch,
	bridgeInterface *iface.Interface,
	knownDynamic ...map[string]struct{},
) error {
	state, err := syncInspectBridgeVLAN(sw.BridgeName)
	if err != nil {
		return err
	}
	if !state.VLANFiltering {
		return fmt.Errorf("bridge VLAN filtering is disabled")
	}
	if !state.DefaultQinQ && state.DefaultPVID == optionalVLANValue(sw.DefaultAccessVLAN) {
		return nil
	}

	managedMembers := make(map[string]struct{}, len(sw.Ports))
	for _, port := range sw.Ports {
		managedMembers[port.Name] = struct{}{}
	}
	for _, known := range knownDynamic {
		for member := range known {
			managedMembers[member] = struct{}{}
		}
	}
	if bridgeInterface != nil {
		for _, member := range bridgeInterface.BridgeMembers {
			if _, managed := managedMembers[member.Name]; managed {
				continue
			}
			return fmt.Errorf(
				"bridge VLAN defaults drifted while unmanaged member %s is attached",
				member.Name,
			)
		}
	}
	if err := syncSetDefaultAccessVLAN(sw.BridgeName, sw.DefaultAccessVLAN); err != nil {
		return fmt.Errorf("repair bridge VLAN defaults: %w", err)
	}
	return nil
}

func filteredBridgeMemberDetached(bridge, member string) (bool, error) {
	bridgeInterface, err := syncIfaceGet(bridge)
	if err != nil {
		if isInterfaceMissingError(err) {
			return true, nil
		}
		return false, err
	}
	if bridgeInterface == nil {
		return true, nil
	}
	for _, candidate := range bridgeInterface.BridgeMembers {
		if candidate.Name == member {
			return false, nil
		}
	}
	return true, nil
}

func removeFilteredStandardBridgeMember(bridge, member string) (retErr error) {
	if _, err := syncRunCommand("/sbin/ifconfig", member, "down"); err != nil {
		return fmt.Errorf("hold %s down: %w", member, err)
	}
	restoreLink := false
	defer func() {
		if !restoreLink {
			return
		}
		if _, err := syncRunCommand("/sbin/ifconfig", member, "up"); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("restore %s link state: %w", member, err))
		}
	}()
	if err := syncRemoveFilteredMember(bridge, member); err != nil {
		removeErr := fmt.Errorf("remove %s: %w", member, err)
		detached, inspectErr := filteredBridgeMemberDetached(bridge, member)
		if inspectErr != nil {
			return errors.Join(removeErr, fmt.Errorf("verify %s detachment: %w", member, inspectErr))
		}
		restoreLink = detached
		return removeErr
	}
	restoreLink = true
	return nil
}

func reconcileFilteredStandardBridge(
	oldSw, newSw networkModels.StandardSwitch,
	knownDynamic ...map[string]struct{},
) error {
	if !oldSw.VLANFiltering || !newSw.VLANFiltering {
		return fmt.Errorf("edit_filtered_standard_bridge: VLAN filtering mode changes require bridge replacement")
	}
	bridge := oldSw.BridgeName
	if bridge == "" || bridge != newSw.BridgeName {
		return fmt.Errorf("edit_filtered_standard_bridge: bridge identity cannot change")
	}

	bridgeVLAN, err := inspectFilteredBridgeTransition(
		bridge,
		oldSw.DefaultAccessVLAN,
		newSw.DefaultAccessVLAN,
	)
	if err != nil {
		return fmt.Errorf("edit_filtered_standard_bridge: validate bridge: %w", err)
	}
	bridgeInterface, err := syncIfaceGet(bridge)
	if err != nil {
		return fmt.Errorf("edit_filtered_standard_bridge: inspect bridge: %w", err)
	}
	if bridgeInterface == nil {
		return fmt.Errorf("edit_filtered_standard_bridge: interface %s not found", bridge)
	}
	if filteredDefaultAccessVLANChanged(oldSw.DefaultAccessVLAN, newSw.DefaultAccessVLAN) {
		if unmanaged := filteredBridgeUnmanagedMembers(
			bridgeInterface,
			oldSw.Ports,
			newSw.Ports,
			knownDynamic...,
		); len(unmanaged) != 0 {
			return filteredDefaultAccessVLANMemberConflict(unmanaged)
		}
	}

	attached := make(map[string]bool, len(bridgeInterface.BridgeMembers))
	for _, member := range bridgeInterface.BridgeMembers {
		attached[member.Name] = true
	}
	ready := make(map[string]bool, len(newSw.Ports))
	mtu := standardSwitchRuntimeMTU(newSw)
	for _, port := range newSw.Ports {
		if !attached[port.Name] {
			continue
		}
		policyMatches, err := syncFilteredMemberPolicyMatches(bridge, port.Name, port.VLANPolicy)
		if err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: inspect %s VLAN policy: %w", port.Name, err)
		}
		portInterface, err := syncIfaceGet(port.Name)
		if err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: inspect %s runtime: %w", port.Name, err)
		}
		ready[port.Name] = policyMatches && filteredMemberRuntimeMatches(
			portInterface,
			mtu,
			newSw.DisableBridgeOffloads,
		)
	}
	plan := planFilteredStandardBridgeMembers(oldSw.Ports, newSw.Ports, attached, ready)

	if bridgeInterface.Description != newSw.Name {
		if _, err := syncRunCommand("/sbin/ifconfig", bridge, "descr", newSw.Name); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: set description: %w", err)
		}
	}
	if bridgeInterface.MTU != mtu {
		if _, err := syncRunCommand("/sbin/ifconfig", bridge, "mtu", fmt.Sprint(mtu)); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: set MTU: %w", err)
		}
	}
	if _, err := applyStandardSwitchMAC(newSw); err != nil {
		return fmt.Errorf("edit_filtered_standard_bridge: set bridge MAC: %w", err)
	}
	if !filteredBridgeIPv6Disabled(bridgeInterface) {
		if _, err := syncRunCommand("/sbin/ifconfig", bridge, "inet6", "no_radr", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: disable base bridge IPv6: %w", err)
		}
	}
	if !interfaceIsUp(bridgeInterface) {
		if _, err := syncRunCommand("/sbin/ifconfig", bridge, "up"); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: bring up bridge: %w", err)
		}
	}

	for _, port := range plan.Remove {
		if err := removeFilteredStandardBridgeMember(bridge, port.Name); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: %w", err)
		}
	}
	currentDefault := optionalVLANFromPVID(bridgeVLAN.DefaultPVID)
	for _, port := range append(plan.Configure, plan.Add...) {
		if err := addFilteredBridgeMember(
			bridge,
			port.Name,
			mtu,
			newSw.DisableBridgeOffloads,
			currentDefault,
			port.VLANPolicy,
		); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: configure %s: %w", port.Name, err)
		}
	}
	if bridgeVLAN.DefaultPVID != optionalVLANValue(newSw.DefaultAccessVLAN) {
		if err := syncSetDefaultAccessVLAN(bridge, newSw.DefaultAccessVLAN); err != nil {
			return fmt.Errorf("edit_filtered_standard_bridge: set default access VLAN: %w", err)
		}
	}
	if _, err := applyStandardSwitchMAC(newSw); err != nil {
		return fmt.Errorf("edit_filtered_standard_bridge: verify bridge MAC: %w", err)
	}
	if err := reconcileFilteredStandardSwitchHost(oldSw, newSw); err != nil {
		return fmt.Errorf("edit_filtered_standard_bridge: reconcile host VLAN: %w", err)
	}
	return nil
}
