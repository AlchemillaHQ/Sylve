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
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func loadStandardSwitch(db *gorm.DB, id uint) (networkModels.StandardSwitch, error) {
	var sw networkModels.StandardSwitch
	if err := db.
		Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		Preload("BridgeMACObject.Entries").
		First(&sw, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return networkModels.StandardSwitch{}, standardSwitchNotFound(err)
		}
		return networkModels.StandardSwitch{}, fmt.Errorf("load standard switch: %w", err)
	}
	return sw, nil
}

func standardSwitchFromInput(name, bridgeName string, input standardSwitchInput) networkModels.StandardSwitch {
	sw := networkModels.StandardSwitch{
		Name:                  name,
		BridgeName:            bridgeName,
		MTU:                   input.mtu,
		VLAN:                  input.vlan,
		Private:               input.private,
		DHCP:                  input.dhcp,
		DisableIPv6:           input.disableIPv6,
		SLAAC:                 input.slaac,
		DefaultRoute:          input.defaultRoute,
		DefaultRoute6:         input.defaultRoute6,
		DisableBridgeOffloads: input.disableBridgeOffloads,
		VLANFiltering:         input.vlanConfig.Filtering,
		DefaultAccessVLAN:     input.vlanConfig.DefaultAccessVLAN,
		HostVLAN:              input.vlanConfig.HostVLAN,
		NetworkManual:         input.manual.Network4,
		GatewayManual:         input.manual.Gateway4,
		Network6Manual:        input.manual.Network6,
		Gateway6Manual:        input.manual.Gateway6,
		BridgeMACMode:         input.macSource.Mode,
		BridgeMACSourcePort:   input.macSource.Port,
	}
	if input.macSource.MACObjectID != 0 {
		sw.BridgeMACObjectID = &input.macSource.MACObjectID
	}
	if input.network4ID != 0 {
		sw.NetworkID = &input.network4ID
		sw.NetworkManual = ""
	}
	if input.gateway4ID != 0 {
		sw.GatewayAddressID = &input.gateway4ID
		sw.GatewayManual = ""
	}
	if input.network6ID != 0 {
		sw.Network6ID = &input.network6ID
		sw.Network6Manual = ""
	}
	if input.gateway6ID != 0 {
		sw.Gateway6AddressID = &input.gateway6ID
		sw.Gateway6Manual = ""
	}
	return sw
}

func standardSwitchPorts(
	switchID uint,
	names []string,
	policies map[string]bridgevlan.PortPolicy,
) []networkModels.NetworkPort {
	ports := make([]networkModels.NetworkPort, 0, len(names))
	for _, name := range names {
		ports = append(ports, networkModels.NetworkPort{
			Name: name, SwitchID: switchID, VLANPolicy: policies[name],
		})
	}
	return ports
}

func rollbackStandardSwitchTransaction(tx *gorm.DB, operation string) error {
	if tx == nil {
		return nil
	}
	if err := tx.Rollback().Error; err != nil {
		logger.L.Error().Err(err).Str("operation", operation).Msg("standard_switch_transaction_rollback_failed")
		return fmt.Errorf("rollback standard switch transaction during %s: %w", operation, err)
	}
	return nil
}

func restoreStandardSwitchRuntime(previous, current networkModels.StandardSwitch) error {
	var cleanupErr error
	if err := syncDeleteBridge(current); err != nil && !isInterfaceMissingError(err) {
		cleanupErr = fmt.Errorf("clean current standard switch runtime: %w", err)
	}
	if err := syncCreateBridge(previous); err != nil {
		return errors.Join(cleanupErr, fmt.Errorf("restore previous standard switch runtime: %w", err))
	}
	return cleanupErr
}

func replaceStandardSwitchRuntime(previous, current networkModels.StandardSwitch) error {
	if previous.VLANFiltering == current.VLANFiltering {
		return fmt.Errorf("standard switch VLAN-filtering mode did not change")
	}
	if err := syncDeleteBridge(previous); err != nil {
		return fmt.Errorf("remove previous standard switch runtime: %w", err)
	}
	if err := syncCreateBridge(current); err != nil {
		return fmt.Errorf("create replacement standard switch runtime: %w", err)
	}
	return nil
}

func (s *Service) knownStandardSwitchJailMembers(switchID uint) (map[string]struct{}, error) {
	members := make(map[string]struct{})
	if s == nil || s.DB == nil || switchID == 0 {
		return members, nil
	}

	var rows []struct {
		NetworkID uint `gorm:"column:network_id"`
		CTID      uint `gorm:"column:ct_id"`
	}
	if err := s.DB.Table("jail_networks AS network").
		Select("network.id AS network_id, jail.ct_id AS ct_id").
		Joins("JOIN jails AS jail ON jail.id = network.jid").
		Where("network.switch_id = ? AND network.switch_type = ?", switchID, "standard").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load filtered Standard Switch jail members: %w", err)
	}
	for _, row := range rows {
		if row.NetworkID == 0 || row.CTID == 0 {
			continue
		}
		name := fmt.Sprintf("%s_net%da", utils.HashIntToNLetters(int(row.CTID), 5), row.NetworkID)
		members[name] = struct{}{}
	}
	return members, nil
}

func standardSwitchPortNames(ports []networkModels.NetworkPort) map[string]struct{} {
	names := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		names[port.Name] = struct{}{}
	}
	return names
}

func mergeStandardSwitchMemberSets(sets ...map[string]struct{}) map[string]struct{} {
	merged := make(map[string]struct{})
	for _, set := range sets {
		for name := range set {
			merged[name] = struct{}{}
		}
	}
	return merged
}

func standardSwitchManagedMembers(sw networkModels.StandardSwitch) map[string]struct{} {
	members := make(map[string]struct{}, len(sw.Ports))
	for _, port := range sw.Ports {
		member := port.Name
		if sw.VLAN > 0 {
			member = fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
		}
		members[member] = struct{}{}
	}
	return members
}

func snapshotStandardSwitchExtraMembers(sw networkModels.StandardSwitch) ([]string, bool, error) {
	interfaceObj, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		if isInterfaceMissingError(err) {
			return []string{}, false, nil
		}
		return nil, false, fmt.Errorf("inspect standard switch bridge %q: %w", sw.BridgeName, err)
	}
	if interfaceObj == nil {
		return []string{}, false, nil
	}

	managedMembers := standardSwitchManagedMembers(sw)
	extraMembers := make([]string, 0, len(interfaceObj.BridgeMembers))
	for _, member := range interfaceObj.BridgeMembers {
		if _, managed := managedMembers[member.Name]; !managed {
			extraMembers = append(extraMembers, member.Name)
		}
	}
	return extraMembers, true, nil
}

func reattachStandardSwitchMembers(bridgeName string, members []string) error {
	if len(members) == 0 {
		return nil
	}

	bridge, err := syncIfaceGet(bridgeName)
	if err != nil {
		return fmt.Errorf("inspect restored standard switch bridge %q: %w", bridgeName, err)
	}
	if bridge == nil {
		return fmt.Errorf("restored standard switch bridge %q not found", bridgeName)
	}

	existing := make(map[string]struct{}, len(bridge.BridgeMembers))
	for _, member := range bridge.BridgeMembers {
		existing[member.Name] = struct{}{}
	}

	var attachErrors []error
	for _, member := range members {
		if _, attached := existing[member]; attached {
			continue
		}
		memberObj, inspectErr := syncIfaceGet(member)
		if inspectErr != nil {
			if isInterfaceMissingError(inspectErr) {
				continue
			}
			attachErrors = append(attachErrors, fmt.Errorf("inspect bridge member %s: %w", member, inspectErr))
			continue
		}
		if memberObj == nil {
			continue
		}

		if _, attachErr := syncRunCommand("/sbin/ifconfig", bridgeName, "addm", member, "up"); attachErr != nil &&
			!strings.Contains(strings.ToLower(attachErr.Error()), "file exists") {
			attachErrors = append(attachErrors, fmt.Errorf("reattach bridge member %s: %w", member, attachErr))
			continue
		}
		if _, upErr := syncRunCommand("/sbin/ifconfig", member, "up"); upErr != nil {
			attachErrors = append(attachErrors, fmt.Errorf("bring up restored bridge member %s: %w", member, upErr))
		}
	}

	return errors.Join(attachErrors...)
}

func restoreStandardSwitchExtraMembers(sw networkModels.StandardSwitch, members []string) error {
	if !sw.VLANFiltering {
		return reattachStandardSwitchMembers(sw.BridgeName, members)
	}
	if len(members) == 0 {
		return nil
	}

	bridge, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		return fmt.Errorf("inspect restored filtered standard switch bridge %q: %w", sw.BridgeName, err)
	}
	if bridge == nil {
		return fmt.Errorf("restored filtered standard switch bridge %q not found", sw.BridgeName)
	}

	existing := make(map[string]struct{}, len(bridge.BridgeMembers))
	for _, member := range bridge.BridgeMembers {
		existing[member.Name] = struct{}{}
	}

	missing := make([]string, 0)
	var inspectErrors []error
	for _, member := range members {
		if _, attached := existing[member]; attached {
			continue
		}
		memberObj, inspectErr := syncIfaceGet(member)
		if inspectErr != nil {
			if isInterfaceMissingError(inspectErr) {
				continue
			}
			inspectErrors = append(inspectErrors, fmt.Errorf("inspect filtered bridge member %s: %w", member, inspectErr))
			continue
		}
		if memberObj != nil {
			missing = append(missing, member)
		}
	}
	if len(missing) > 0 {
		inspectErrors = append(inspectErrors, fmt.Errorf(
			"filtered_standard_switch_rollback_member_policy_required: detached members %s were not reattached because their VLAN policies are owned by their workloads; restart the affected workloads",
			strings.Join(missing, ", "),
		))
	}
	return errors.Join(inspectErrors...)
}

func restoreStandardSwitchEditRuntime(
	previous, current networkModels.StandardSwitch,
	extraMembers []string,
	knownDynamic ...map[string]struct{},
) error {
	if previous.VLANFiltering != current.VLANFiltering {
		return restoreStandardSwitchRuntime(previous, current)
	}
	var reconcileErr error
	if previous.VLANFiltering || current.VLANFiltering {
		known := make(map[string]struct{})
		for _, group := range knownDynamic {
			for member := range group {
				known[member] = struct{}{}
			}
		}
		reconcileErr = syncEditFilteredBridge(current, previous, known)
	} else {
		reconcileErr = syncEditBridge(current, previous)
	}
	if reconcileErr == nil {
		return errors.Join(
			restoreStandardSwitchExtraMembers(previous, extraMembers),
			reconcileStandardSwitchPrivateMembers(previous, mergeStandardSwitchMemberSets(knownDynamic...)),
		)
	}
	if previous.VLANFiltering || current.VLANFiltering {
		return fmt.Errorf("restore filtered standard switch in place: %w", reconcileErr)
	}
	restoreErr := restoreStandardSwitchRuntime(previous, current)
	reattachErr := restoreStandardSwitchExtraMembers(previous, extraMembers)
	return errors.Join(restoreErr, reattachErr)
}

func validateStandardSwitchDeleteMembers(sw networkModels.StandardSwitch) error {
	interfaceObj, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		if isInterfaceMissingError(err) {
			return nil
		}
		return fmt.Errorf("inspect standard switch bridge %q: %w", sw.BridgeName, err)
	}
	if interfaceObj == nil {
		return nil
	}

	managedMembers := standardSwitchManagedMembers(sw)

	unexpected := make([]string, 0)
	for _, member := range interfaceObj.BridgeMembers {
		if _, managed := managedMembers[member.Name]; !managed {
			unexpected = append(unexpected, member.Name)
		}
	}
	if len(unexpected) > 0 {
		return standardSwitchConflict(
			"standard_switch_runtime_member_conflict",
			fmt.Errorf("unmanaged bridge members: %s", strings.Join(unexpected, ", ")),
		)
	}

	return nil
}
