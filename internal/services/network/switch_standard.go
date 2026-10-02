// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
	"gorm.io/gorm"
)

var (
	syncIfaceGet                   = iface.Get
	syncRunCommand                 = utils.RunCommand
	syncRunCommandAllowExitCode    = utils.RunCommandAllowExitCode
	syncRunCommandWithContext      = utils.RunCommandWithContext
	syncSolicitRouterAdvertisement = solicitStandardSwitchRouterAdvertisement
	syncCreateBridge               = createStandardBridge
	syncEditBridge                 = editStandardBridge
	syncEditFilteredBridge         = func(oldSw, newSw networkModels.StandardSwitch, known map[string]struct{}) error {
		return reconcileFilteredStandardBridge(oldSw, newSw, known)
	}
	syncCaptureFilteredPortClaims   = captureFilteredStandardSwitchPortClaims
	syncRestoreFilteredPortClaims   = restoreFilteredStandardSwitchPortClaims
	syncDeleteBridge                = deleteStandardBridge
	syncStopDhclient                = stopDhclient
	syncSetSysctlInt32              = sysctl.SetInt32
	syncInspectBridgeVLAN           = bridgevlan.InspectBridge
	syncPrepareFilteredBridge       = bridgevlan.PrepareManagedFilteredBridge
	syncSetDefaultAccessVLAN        = bridgevlan.SetDefaultAccessVLAN
	syncRemoveFilteredMember        = bridgevlan.RemoveMember
	syncConfigureFilteredMember     = bridgevlan.ConfigureMember
	syncFilteredMemberPolicyMatches = bridgevlan.MemberPolicyMatches
	syncSetBridgeMemberPrivate      = bridgevlan.SetMemberPrivate
	standardSwitchIsDomainShutOff   = func(s *Service, rid uint) (bool, error) {
		if s.LibVirt == nil {
			return false, fmt.Errorf("libvirt_service_unavailable")
		}
		return s.LibVirt.IsDomainShutOff(rid)
	}
)

func optionalVLANEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *Service) requireStoppedVMsForDefaultAccessVLANChange(
	switchID uint,
	before, after *int,
) error {
	if optionalVLANEqual(before, after) {
		return nil
	}

	return s.requireStoppedVMsUsingStandardSwitch(
		switchID, "standard_switch_default_access_vlan_change_requires_stopped_vms",
	)
}

func ensureStandardSwitchIPv6RADefaultRouteSupport() error {
	if err := syncSetSysctlInt32(models.SystemTunableIPv6RFC6204W3OID, 1); err != nil {
		return fmt.Errorf("set %s=1: %w", models.SystemTunableIPv6RFC6204W3OID, err)
	}
	return nil
}

// Capability bits that FreeBSD if_bridge synchronizes across its members,
// plus LRO, which if_bridge always strips.
const (
	ifcapTXCSUM     uint32 = 1 << 1
	ifcapTSO4       uint32 = 1 << 8
	ifcapTSO6       uint32 = 1 << 9
	ifcapLRO        uint32 = 1 << 10
	ifcapTOE4       uint32 = 1 << 14
	ifcapTOE6       uint32 = 1 << 15
	ifcapTXCSUMIPv6 uint32 = 1 << 22
	ifcapMEXTPG     uint32 = 1 << 26
)

func (s *Service) GetStandardSwitches() ([]networkModels.StandardSwitch, error) {
	switches := make([]networkModels.StandardSwitch, 0)
	if err := s.DB.
		Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		Preload("BridgeMACObject.Entries").
		Find(&switches).Error; err != nil {
		return nil, err
	}
	for i := range switches {
		if switches[i].Ports == nil {
			switches[i].Ports = []networkModels.NetworkPort{}
		}
	}
	return switches, nil
}

func (s *Service) GetStandardSwitch(id uint) (networkModels.StandardSwitch, error) {
	return loadStandardSwitch(s.DB, id)
}

func reconcileStandardSwitchAutomaticRouteOwners(db *gorm.DB, ipv4, ipv6 bool) error {
	var switches []networkModels.StandardSwitch
	if err := db.Order("id ASC").Find(&switches).Error; err != nil {
		return fmt.Errorf("load standard switch route owners: %w", err)
	}

	var reconcileErrors []error
	for _, sw := range switches {
		hostInterface := standardSwitchHostInterfaceName(sw)
		if hostInterface == "" {
			continue
		}
		if ipv4 && sw.DHCP && sw.DefaultRoute {
			if err := runDhclient(hostInterface, 10, true); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile IPv4 route owner %s: %w", hostInterface, err))
			}
		}

		if ipv6 && sw.SLAAC && sw.DefaultRoute6 && !sw.DisableIPv6 {
			if err := ensureStandardSwitchIPv6RADefaultRouteSupport(); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile IPv6 route owner %s: %w", hostInterface, err))
				continue
			}
			if _, err := syncRunCommand("/sbin/ifconfig", hostInterface, "inet6", "auto_linklocal", "-ifdisabled", "-no_radr", "accept_rtadv"); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile IPv6 route owner %s flags: %w", hostInterface, err))
				continue
			}
			if err := syncSolicitRouterAdvertisement(hostInterface); err != nil {
				logger.L.Warn().
					Err(err).
					Str("interface", hostInterface).
					Msg("standard_switch_slaac_router_solicitation_failed")
			}
		}
	}
	return errors.Join(reconcileErrors...)
}

func (s *Service) conflictingPortsForVLAN(ports []string, vlan int, excludeSwitchID *uint) ([]networkModels.NetworkPort, error) {
	if len(ports) == 0 {
		return []networkModels.NetworkPort{}, nil
	}

	eps := make([]networkModels.NetworkPort, 0)
	q := s.DB.Preload("Switch").Where("name IN ?", ports)
	if excludeSwitchID != nil {
		q = q.Where("switch_id <> ?", *excludeSwitchID)
	}

	if err := q.Find(&eps).Error; err != nil {
		return nil, fmt.Errorf("db_error_checking_ports: %w", err)
	}

	conflicts := make([]networkModels.NetworkPort, 0, len(eps))
	for _, ep := range eps {
		other := ep.Switch.VLAN
		if vlan == 0 || other == 0 || vlan == other {
			conflicts = append(conflicts, ep)
		}
	}

	return conflicts, nil
}

// validateStandardSwitchManual trims the manually-typed address inputs,
// enforces that no field is supplied both as an object ID and a manual string,
// and validates each manual value for its expected family/shape. It returns the
// trimmed values to be stored on the switch.
func validateStandardSwitchManual(
	network4Id, gateway4Id, network6Id, gateway6Id uint,
	manual networkModels.StandardSwitchManualAddresses,
) (networkModels.StandardSwitchManualAddresses, error) {
	trimmed := networkModels.StandardSwitchManualAddresses{
		Network4: strings.TrimSpace(manual.Network4),
		Gateway4: strings.TrimSpace(manual.Gateway4),
		Network6: strings.TrimSpace(manual.Network6),
		Gateway6: strings.TrimSpace(manual.Gateway6),
	}
	for _, value := range []string{trimmed.Network4, trimmed.Gateway4, trimmed.Network6, trimmed.Gateway6} {
		if len(value) > MaxStandardSwitchManualAddressBytes {
			return trimmed, invalidStandardSwitch("standard_switch_manual_address_too_long", nil)
		}
	}

	if network4Id != 0 && trimmed.Network4 != "" {
		return trimmed, invalidStandardSwitch("standard_switch_network4_source_conflict", nil)
	}
	if gateway4Id != 0 && trimmed.Gateway4 != "" {
		return trimmed, invalidStandardSwitch("standard_switch_gateway4_source_conflict", nil)
	}
	if network6Id != 0 && trimmed.Network6 != "" {
		return trimmed, invalidStandardSwitch("standard_switch_network6_source_conflict", nil)
	}
	if gateway6Id != 0 && trimmed.Gateway6 != "" {
		return trimmed, invalidStandardSwitch("standard_switch_gateway6_source_conflict", nil)
	}

	if trimmed.Network4 != "" && !utils.IsAssignableIPv4CIDR(trimmed.Network4) {
		return trimmed, invalidStandardSwitch("invalid_standard_switch_network4_manual", nil)
	}
	if trimmed.Gateway4 != "" && !utils.IsValidIPv4(trimmed.Gateway4) {
		return trimmed, invalidStandardSwitch("invalid_standard_switch_gateway4_manual", nil)
	}
	if trimmed.Network6 != "" && !utils.IsAssignableIPv6CIDR(trimmed.Network6) {
		return trimmed, invalidStandardSwitch("invalid_standard_switch_network6_manual", nil)
	}
	if trimmed.Gateway6 != "" && !utils.IsValidIPv6(trimmed.Gateway6) {
		return trimmed, invalidStandardSwitch("invalid_standard_switch_gateway6_manual", nil)
	}

	return trimmed, nil
}

type standardSwitchAddressModes struct {
	network4ID  uint
	network6ID  uint
	gateway4ID  uint
	gateway6ID  uint
	dhcp        bool
	disableIPv6 bool
	slaac       bool
	manual      networkModels.StandardSwitchManualAddresses
}

func normalizeStandardSwitchAddressModes(modes standardSwitchAddressModes) standardSwitchAddressModes {
	if modes.dhcp {
		modes.network4ID = 0
		modes.gateway4ID = 0
		modes.manual.Network4 = ""
		modes.manual.Gateway4 = ""
	}

	// A disabled IPv6 stack cannot use SLAAC, so it takes precedence when both are requested.
	if modes.disableIPv6 {
		modes.network6ID = 0
		modes.gateway6ID = 0
		modes.manual.Network6 = ""
		modes.manual.Gateway6 = ""
		modes.slaac = false
	} else if modes.slaac {
		modes.network6ID = 0
		modes.gateway6ID = 0
		modes.manual.Network6 = ""
		modes.manual.Gateway6 = ""
	}

	return modes
}

func standardSwitchInputFromConfig(config StandardSwitchConfig) standardSwitchInput {
	return standardSwitchInput{
		mtu:                   config.MTU,
		vlan:                  config.VLAN,
		network4ID:            config.Network4ID,
		network6ID:            config.Network6ID,
		gateway4ID:            config.Gateway4ID,
		gateway6ID:            config.Gateway6ID,
		ports:                 config.Ports,
		macSource:             config.MACSource,
		private:               config.Private,
		dhcp:                  config.DHCP,
		disableIPv6:           config.DisableIPv6,
		slaac:                 config.SLAAC,
		defaultRoute:          config.DefaultRoute,
		defaultRoute6:         config.DefaultRoute6,
		disableBridgeOffloads: config.DisableBridgeOffloads,
		manual:                config.Manual,
		vlanConfig:            config.VLANConfig,
	}
}

func (s *Service) NewStandardSwitch(request CreateStandardSwitchRequest) (switchID uint, retErr error) {
	unlockInterfaceReferences := s.lockInterfaceReferencesWrite()
	defer unlockInterfaceReferences()
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	normalizedName, err := normalizeStandardSwitchName(request.Name)
	if err != nil {
		return 0, err
	}
	bridgeName := utils.ShortHash("vm-" + normalizedName)
	if err := s.checkStandardSwitchCreateConflicts(normalizedName, bridgeName); err != nil {
		return 0, err
	}

	input, err := s.validateStandardSwitchInput(0, bridgeName, standardSwitchInputFromConfig(request.StandardSwitchConfig))
	if err != nil {
		return 0, err
	}
	if err := s.checkStandardSwitchPortsForHostInterfaceL3(input.ports); err != nil {
		return 0, err
	}
	var claimedPortState []standardSwitchPortRuntimeSnapshot
	if input.vlanConfig.Filtering {
		claimedPortState, err = syncCaptureFilteredPortClaims(input.ports)
		if err != nil {
			return 0, err
		}
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return 0, fmt.Errorf("begin standard switch create transaction: %w", tx.Error)
	}
	transactionFinished := false
	defer func() {
		if !transactionFinished {
			retErr = errors.Join(retErr, rollbackStandardSwitchTransaction(tx, "create"))
		}
	}()

	sw := standardSwitchFromInput(normalizedName, bridgeName, input)
	if err := tx.Create(&sw).Error; err != nil {
		if isStandardSwitchDuplicateError(err) {
			return 0, standardSwitchConflict("standard_switch_name_or_bridge_conflict", err)
		}
		return 0, fmt.Errorf("create standard switch: %w", err)
	}

	portRows := standardSwitchPorts(sw.ID, input.ports, input.vlanConfig.PortPolicies)
	if len(portRows) > 0 {
		if err := tx.Create(&portRows).Error; err != nil {
			return 0, fmt.Errorf("create standard switch ports: %w", err)
		}
	}

	fresh, err := loadStandardSwitch(tx, sw.ID)
	if err != nil {
		return 0, fmt.Errorf("reload created standard switch: %w", err)
	}
	warnStandardSwitchMemberRCConflicts(fresh)
	if err := syncCreateBridge(fresh); err != nil {
		restoreErr := syncRestoreFilteredPortClaims(fresh.BridgeName, claimedPortState)
		return 0, errors.Join(fmt.Errorf("apply created standard switch: %w", err), restoreErr)
	}

	if err := tx.Commit().Error; err != nil {
		rollbackErr := rollbackStandardSwitchTransaction(tx, "create_commit")
		transactionFinished = true
		cleanupErr := syncDeleteBridge(fresh)
		if isInterfaceMissingError(cleanupErr) {
			cleanupErr = nil
		} else if cleanupErr != nil {
			logger.L.Error().Err(cleanupErr).Uint("switchID", sw.ID).Msg("standard_switch_create_commit_cleanup_failed")
			cleanupErr = fmt.Errorf("clean runtime after failed standard switch create commit: %w", cleanupErr)
		}
		restoreErr := syncRestoreFilteredPortClaims(fresh.BridgeName, claimedPortState)
		return 0, errors.Join(fmt.Errorf("commit standard switch create: %w", err), rollbackErr, cleanupErr, restoreErr)
	}
	transactionFinished = true

	return sw.ID, nil
}

func (s *Service) DeleteStandardSwitch(id uint) (retErr error) {
	unlockInterfaceReferences := s.lockInterfaceReferencesWrite()
	defer unlockInterfaceReferences()
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	sw, err := loadStandardSwitch(s.DB, id)
	if err != nil {
		return err
	}
	unlockLifecycle := bridgevlan.LockStandardSwitchLifecycle(sw.BridgeName)
	defer unlockLifecycle()

	if err := s.checkStandardSwitchUsage(id, sw.BridgeName); err != nil {
		return err
	}
	if hostInterface := standardSwitchHostInterfaceName(sw); hostInterface != "" && hostInterface != sw.BridgeName {
		if err := s.checkStandardSwitchExternalUsage(hostInterface); err != nil {
			return err
		}
	}
	if err := validateStandardSwitchDeleteMembers(sw); err != nil {
		return err
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin standard switch delete transaction: %w", tx.Error)
	}
	transactionFinished := false
	defer func() {
		if !transactionFinished {
			retErr = errors.Join(retErr, rollbackStandardSwitchTransaction(tx, "delete"))
		}
	}()

	restoreAfterFailure := func(operation string, operationErr error) error {
		rollbackErr := rollbackStandardSwitchTransaction(tx, operation)
		transactionFinished = true
		restoreErr := restoreStandardSwitchRuntime(sw, sw)
		if restoreErr != nil {
			logger.L.Error().
				Err(restoreErr).
				Uint("switchID", sw.ID).
				Str("operation", operation).
				Msg("standard_switch_delete_runtime_restore_failed")
		}
		return errors.Join(operationErr, rollbackErr, restoreErr)
	}

	if err := syncDeleteBridge(sw); err != nil {
		return restoreAfterFailure("delete_runtime", fmt.Errorf("remove standard switch runtime: %w", err))
	}

	if err := tx.Where("switch_id = ?", id).Delete(&networkModels.NetworkPort{}).Error; err != nil {
		return restoreAfterFailure("delete_ports", fmt.Errorf("delete standard switch ports: %w", err))
	}
	if err := tx.Delete(&sw).Error; err != nil {
		return restoreAfterFailure("delete_switch", fmt.Errorf("delete standard switch: %w", err))
	}
	if sw.DefaultRoute || sw.DefaultRoute6 {
		if err := reconcileStandardSwitchAutomaticRouteOwners(tx, sw.DefaultRoute, sw.DefaultRoute6); err != nil {
			return restoreAfterFailure("delete_route_owner_reconcile", err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return restoreAfterFailure("delete_commit", fmt.Errorf("commit standard switch delete: %w", err))
	}
	transactionFinished = true

	return nil
}

func (s *Service) EditStandardSwitch(request UpdateStandardSwitchRequest) (retErr error) {
	unlockInterfaceReferences := s.lockInterfaceReferencesWrite()
	defer unlockInterfaceReferences()
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	before, err := loadStandardSwitch(s.DB, request.ID)
	if err != nil {
		return err
	}
	config := request.StandardSwitchConfig
	if request.PreserveVLANConfig {
		config.VLANConfig = standardSwitchVLANConfigFromModel(before)
	}
	normalizedInput, err := normalizeStandardSwitchInput(standardSwitchInputFromConfig(config))
	if err != nil {
		return err
	}
	if standardSwitchMatchesInput(before, normalizedInput) {
		return nil
	}
	unlockLifecycle := bridgevlan.LockStandardSwitchLifecycle(before.BridgeName)
	defer unlockLifecycle()

	input, err := s.validateStandardSwitchInput(request.ID, before.BridgeName, normalizedInput)
	if err != nil {
		return err
	}
	if err := s.checkStandardSwitchPortsForHostInterfaceL3(input.ports); err != nil {
		return err
	}
	if input.vlanConfig.Filtering &&
		input.vlanConfig.HostVLAN == nil &&
		standardSwitchHasPersistedHostL3(before) &&
		!request.ConfirmHostLayer3Removal {
		return standardSwitchConflict(
			"standard_switch_host_layer3_removal_requires_confirmation",
			nil,
		)
	}
	modeChanged := before.VLANFiltering != input.vlanConfig.Filtering
	if modeChanged {
		if err := requireNoStandardSwitchWorkloadAttachments(s.DB, request.ID); err != nil {
			return err
		}
	}
	desiredHostIdentity := before
	desiredHostIdentity.VLANFiltering = input.vlanConfig.Filtering
	desiredHostIdentity.HostVLAN = input.vlanConfig.HostVLAN
	oldHostInterface := standardSwitchHostInterfaceName(before)
	if oldHostInterface != standardSwitchHostInterfaceName(desiredHostIdentity) {
		if err := s.checkStandardSwitchHostInterfaceUsage(request.ID, oldHostInterface); err != nil {
			return err
		}
	}
	if err := s.requireStoppedVMsForDefaultAccessVLANChange(
		request.ID,
		before.DefaultAccessVLAN,
		input.vlanConfig.DefaultAccessVLAN,
	); err != nil {
		return err
	}
	if before.Private != input.private {
		if err := s.requireStoppedVMsUsingStandardSwitch(
			request.ID,
			"standard_switch_private_change_requires_stopped_vms",
		); err != nil {
			return err
		}
	}
	extraMembers, runtimeExists, err := snapshotStandardSwitchExtraMembers(before)
	if err != nil {
		return err
	}
	if modeChanged && runtimeExists && len(extraMembers) != 0 {
		return standardSwitchConflict(
			"standard_switch_runtime_member_conflict",
			fmt.Errorf(
				"cannot change VLAN-filtering mode while unmanaged bridge members are attached: %s",
				strings.Join(extraMembers, ", "),
			),
		)
	}
	knownJailMembers, err := s.knownStandardSwitchJailMembers(before.ID)
	if err != nil {
		return err
	}
	var claimedPortState []standardSwitchPortRuntimeSnapshot
	if input.vlanConfig.Filtering || modeChanged {
		previousPorts := standardSwitchPortNames(before.Ports)
		claimNames := make([]string, 0)
		for _, port := range input.ports {
			_, alreadySelected := previousPorts[port]
			previouslyManagedAsRawPort := before.VLANFiltering || before.VLAN == 0
			if !alreadySelected || !previouslyManagedAsRawPort {
				claimNames = append(claimNames, port)
			}
		}
		claimedPortState, err = syncCaptureFilteredPortClaims(claimNames)
		if err != nil {
			return err
		}
	}
	if before.VLANFiltering && runtimeExists &&
		filteredDefaultAccessVLANChanged(before.DefaultAccessVLAN, input.vlanConfig.DefaultAccessVLAN) {
		desiredPorts := make(map[string]struct{}, len(input.ports))
		for _, port := range input.ports {
			desiredPorts[port] = struct{}{}
		}
		unmanaged := make([]string, 0, len(extraMembers))
		for _, member := range extraMembers {
			if _, selected := desiredPorts[member]; !selected {
				if _, knownJail := knownJailMembers[member]; !knownJail {
					unmanaged = append(unmanaged, member)
				}
			}
		}
		if len(unmanaged) != 0 {
			return filteredDefaultAccessVLANMemberConflict(unmanaged)
		}
	}

	nullableID := func(value uint) any {
		if value == 0 {
			return nil
		}
		return value
	}
	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin standard switch update transaction: %w", tx.Error)
	}
	transactionFinished := false
	defer func() {
		if !transactionFinished {
			retErr = errors.Join(retErr, rollbackStandardSwitchTransaction(tx, "update"))
		}
	}()

	updates := map[string]any{
		"mtu":                        input.mtu,
		"vlan":                       input.vlan,
		"private":                    input.private,
		"dhcp":                       input.dhcp,
		"disable_ipv6":               input.disableIPv6,
		"sla_ac":                     input.slaac,
		"default_route":              input.defaultRoute,
		"default_route6":             input.defaultRoute6,
		"disable_bridge_offloads":    input.disableBridgeOffloads,
		"vlan_filtering":             input.vlanConfig.Filtering,
		"default_access_vlan":        input.vlanConfig.DefaultAccessVLAN,
		"host_vlan":                  input.vlanConfig.HostVLAN,
		"network_object_id":          nullableID(input.network4ID),
		"gateway_address_object_id":  nullableID(input.gateway4ID),
		"network6_object_id":         nullableID(input.network6ID),
		"gateway6_address_object_id": nullableID(input.gateway6ID),
		"network_manual":             input.manual.Network4,
		"gateway_manual":             input.manual.Gateway4,
		"network6_manual":            input.manual.Network6,
		"gateway6_manual":            input.manual.Gateway6,
		"bridge_mac_mode":            input.macSource.Mode,
		"bridge_mac_source_port":     input.macSource.Port,
		"bridge_mac_object_id":       nullableID(input.macSource.MACObjectID),
	}
	if err := tx.Model(&networkModels.StandardSwitch{}).Where("id = ?", request.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update standard switch: %w", err)
	}
	if modeChanged {
		if err := requireNoStandardSwitchWorkloadAttachments(tx, request.ID); err != nil {
			return err
		}
	}
	if err := tx.Where("switch_id = ?", request.ID).Delete(&networkModels.NetworkPort{}).Error; err != nil {
		return fmt.Errorf("replace standard switch ports: %w", err)
	}
	portRows := standardSwitchPorts(request.ID, input.ports, input.vlanConfig.PortPolicies)
	if len(portRows) > 0 {
		if err := tx.Create(&portRows).Error; err != nil {
			return fmt.Errorf("create updated standard switch ports: %w", err)
		}
	}

	after, err := loadStandardSwitch(tx, request.ID)
	if err != nil {
		return fmt.Errorf("reload updated standard switch: %w", err)
	}
	warnStandardSwitchMemberRCConflicts(after)
	runtimeApplied := false

	restoreAfterFailure := func(operation string, operationErr error) error {
		rollbackErr := rollbackStandardSwitchTransaction(tx, operation)
		transactionFinished = true

		var restoreErr error
		switch {
		case runtimeExists:
			restoreErr = restoreStandardSwitchEditRuntime(before, after, extraMembers, knownJailMembers)
		case runtimeApplied:
			restoreErr = restoreStandardSwitchRuntime(before, after)
		default:
			restoreErr = syncCreateBridge(before)
		}
		if restoreErr != nil {
			logger.L.Error().
				Err(restoreErr).
				Uint("switchID", request.ID).
				Str("operation", operation).
				Msg("standard_switch_update_runtime_restore_failed")
		}
		portRestoreErr := syncRestoreFilteredPortClaims(before.BridgeName, claimedPortState)
		if portRestoreErr != nil {
			logger.L.Error().Err(portRestoreErr).Uint("switchID", request.ID).
				Str("operation", operation).Msg("standard_switch_update_port_restore_failed")
		}
		return errors.Join(operationErr, rollbackErr, restoreErr, portRestoreErr)
	}

	if runtimeExists {
		if modeChanged {
			err = replaceStandardSwitchRuntime(before, after)
		} else if before.VLANFiltering {
			err = syncEditFilteredBridge(before, after, knownJailMembers)
		} else {
			err = syncEditBridge(before, after)
		}
	} else {
		err = syncCreateBridge(after)
	}
	if err != nil {
		return restoreAfterFailure("update_runtime", fmt.Errorf("apply updated standard switch: %w", err))
	}
	runtimeApplied = true
	if err := reconcileStandardSwitchPrivateMembers(after, knownJailMembers); err != nil {
		return restoreAfterFailure("update_private_members", fmt.Errorf("apply updated switch isolation: %w", err))
	}
	reconcileIPv4Owner := before.DefaultRoute && !after.DefaultRoute
	reconcileIPv6Owner := before.DefaultRoute6 && !after.DefaultRoute6
	if reconcileIPv4Owner || reconcileIPv6Owner {
		if err := reconcileStandardSwitchAutomaticRouteOwners(tx, reconcileIPv4Owner, reconcileIPv6Owner); err != nil {
			return restoreAfterFailure("update_route_owner_reconcile", err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return restoreAfterFailure("update_commit", fmt.Errorf("commit standard switch update: %w", err))
	}
	transactionFinished = true

	return nil
}

func (s *Service) SyncStandardSwitches() error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var switches []networkModels.StandardSwitch
	if err := s.DB.Preload("Ports").
		Preload("NetworkObj.Entries").
		Preload("Network6Obj.Entries").
		Preload("GatewayAddressObj.Entries").
		Preload("Gateway6AddressObj.Entries").
		Preload("BridgeMACObject.Entries").
		Find(&switches).Error; err != nil {
		return fmt.Errorf("db_error_checking_switches: %v", err)
	}

	var syncErrors []error
	for _, current := range switches {
		warnStandardSwitchMemberRCConflicts(current)
		knownJailMembers, err := s.knownStandardSwitchJailMembers(current.ID)
		if err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		if err := syncStandardSwitchRuntime(current, knownJailMembers); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}
	if err := reconcileStandardSwitchAutomaticRouteOwners(s.DB, true, true); err != nil {
		syncErrors = append(syncErrors, err)
	}
	return errors.Join(syncErrors...)
}

func syncStandardSwitchRuntime(sw networkModels.StandardSwitch, knownDynamic ...map[string]struct{}) error {
	return syncStandardSwitchRuntimeFrom(sw, sw, knownDynamic...)
}

func syncStandardSwitchRuntimeFrom(
	previous, sw networkModels.StandardSwitch,
	knownDynamic ...map[string]struct{},
) error {
	unlockLifecycle := bridgevlan.LockStandardSwitchLifecycle(sw.BridgeName)
	defer unlockLifecycle()

	dbPorts := make(map[string]bool, len(sw.Ports)*2)
	for _, port := range sw.Ports {
		dbPorts[port.Name] = true
		if sw.VLAN > 0 {
			dbPorts[fmt.Sprintf("%s.%d", port.Name, sw.VLAN)] = true
		}
	}

	preservedMembers := make(map[string]bool)
	bridgeExists := false

	interfaceObj, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		if !isInterfaceMissingError(err) {
			return fmt.Errorf("sync_standard_switches: get %s: %v", sw.BridgeName, err)
		}
	} else if interfaceObj != nil {
		bridgeExists = true
		if !sw.VLANFiltering {
			state, inspectErr := syncInspectBridgeVLAN(sw.BridgeName)
			if inspectErr != nil {
				return fmt.Errorf("sync_standard_switches: inspect %s VLAN mode: %w", sw.BridgeName, inspectErr)
			}
			if state.VLANFiltering {
				return fmt.Errorf("standard_switch_runtime_vlan_mode_mismatch: %s is filtered but its stored switch is unfiltered", sw.BridgeName)
			}
		}
		for _, member := range interfaceObj.BridgeMembers {
			if dbPorts[member.Name] {
				continue
			}
			preservedMembers[member.Name] = true
		}
	}

	if bridgeExists {
		var reconcileErr error
		if sw.VLANFiltering {
			if err := repairFilteredStandardBridgeDefaults(sw, interfaceObj, knownDynamic...); err != nil {
				reconcileErr = err
			} else {
				reconcileErr = reconcileFilteredStandardBridge(previous, sw, knownDynamic...)
			}
		} else {
			reconcileErr = syncEditBridge(previous, sw)
		}
		if reconcileErr != nil {
			return fmt.Errorf("sync_standard_switches: failed_to_reconcile %s: %w", sw.BridgeName, reconcileErr)
		}
	} else if err := syncCreateBridge(sw); err != nil {
		return fmt.Errorf("sync_standard_switches: failed_to_create %s: %v", sw.BridgeName, err)
	}

	if len(preservedMembers) == 0 {
		return reconcileStandardSwitchPrivateMembers(sw, mergeStandardSwitchMemberSets(knownDynamic...))
	}

	freshInterface, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		return fmt.Errorf("sync_standard_switches: get %s after reconcile: %v", sw.BridgeName, err)
	}

	existingMembers := make(map[string]bool)
	if freshInterface != nil {
		for _, member := range freshInterface.BridgeMembers {
			existingMembers[member.Name] = true
		}
	}

	for member := range preservedMembers {
		if _, exists := existingMembers[member]; exists {
			continue
		}
		if sw.VLANFiltering {
			logger.L.Warn().Str("bridge", sw.BridgeName).Str("member", member).
				Msg("filtered_bridge_unknown_member_not_restored")
			continue
		}
		if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "addm", member, "up"); err != nil {
			return fmt.Errorf("sync_standard_switches: add member %s to %s: %v", member, sw.BridgeName, err)
		}
		if _, err := syncRunCommand("/sbin/ifconfig", member, "up"); err != nil {
			return fmt.Errorf("sync_standard_switches: bring up member %s: %v", member, err)
		}
	}
	if _, err := applyStandardSwitchMAC(sw); err != nil {
		return fmt.Errorf("sync_standard_switches: verify %s MAC after preserved members: %v", sw.BridgeName, err)
	}

	return reconcileStandardSwitchPrivateMembers(sw, mergeStandardSwitchMemberSets(knownDynamic...))
}

func (s *Service) syncStandardSwitchTransitions(
	previous, current []networkModels.StandardSwitch,
	reverse bool,
) error {
	if len(previous) != len(current) {
		return fmt.Errorf("standard switch transition count mismatch: %d != %d", len(previous), len(current))
	}

	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var syncErrors []error
	for offset := range current {
		index := offset
		if reverse {
			index = len(current) - 1 - offset
		}
		from, to := previous[index], current[index]
		if reverse {
			from, to = to, from
		}
		knownJailMembers, err := s.knownStandardSwitchJailMembers(to.ID)
		if err != nil {
			syncErrors = append(syncErrors, err)
			continue
		}
		if err := syncStandardSwitchRuntimeFrom(from, to, knownJailMembers); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}
	if err := reconcileStandardSwitchAutomaticRouteOwners(s.DB, true, true); err != nil {
		syncErrors = append(syncErrors, err)
	}
	return errors.Join(syncErrors...)
}

func isInterfaceMissingError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "no such interface")
}

func normalizeIPv6GatewayForRoute(gateway, ifaceName string) string {
	gateway = strings.TrimSpace(gateway)
	if gateway == "" {
		return ""
	}

	if strings.HasPrefix(strings.ToLower(gateway), "fe80:") &&
		!strings.Contains(gateway, "%") &&
		ifaceName != "" {
		return gateway + "%" + ifaceName
	}

	return gateway
}

func createStandardBridge(sw networkModels.StandardSwitch) (retErr error) {
	raw, err := syncRunCommand("/sbin/ifconfig", "bridge", "create")
	if err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_create: %v", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("create_standard_bridge: empty_created_interface")
	}

	renamed := false
	addedNetwork4Route := false
	addedDefault4Route := false
	addedNetwork6Route := false
	addedDefault6Route := false
	defer func() {
		if retErr == nil {
			return
		}

		if addedDefault4Route {
			_ = deleteRouteIfPresent("delete", "default", sw.Gateway(4))
		}
		if addedDefault6Route {
			_ = deleteRouteIfPresent(
				"-6",
				"delete",
				"default",
				normalizeIPv6GatewayForRoute(sw.Gateway(6), sw.BridgeName),
			)
		}
		if addedNetwork4Route {
			_ = deleteRouteIfPresent("delete", "-net", sw.Network(4), sw.Gateway(4))
		}
		if addedNetwork6Route {
			_ = deleteRouteIfPresent(
				"-6",
				"delete",
				"-net",
				sw.Network(6),
				normalizeIPv6GatewayForRoute(sw.Gateway(6), sw.BridgeName),
			)
		}

		if sw.DHCP {
			if hostInterface := standardSwitchHostInterfaceName(sw); hostInterface != "" {
				if err := stopDhclient(hostInterface); err != nil {
					logger.L.Error().Err(err).Str("interface", hostInterface).Msg("standard_switch_create_dhclient_cleanup_failed")
				}
			}
		}
		if renamed {
			if err := destroyStandardSwitchRuntimeInterfaces(sw); err != nil {
				logger.L.Error().Err(err).Str("bridge", sw.BridgeName).Msg("standard_switch_create_interface_cleanup_failed")
			}
		} else if _, err := syncRunCommand("/sbin/ifconfig", raw, "destroy"); err != nil && !isInterfaceMissingError(err) {
			logger.L.Error().Err(err).Str("interface", raw).Msg("standard_switch_create_raw_interface_cleanup_failed")
		}
	}()

	if _, err := syncRunCommand("/sbin/ifconfig", raw, "name", sw.BridgeName); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_rename: %v", err)
	}
	renamed = true
	if sw.VLANFiltering {
		if err := syncPrepareFilteredBridge(sw.BridgeName); err != nil {
			return fmt.Errorf("create_standard_bridge: prepare VLAN filtering: %w", err)
		}
	}

	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "descr", sw.Name); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_set_descr: %v", err)
	}
	mtu := sw.MTU
	if mtu == 0 {
		mtu = 1500
	}
	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "mtu", strconv.Itoa(mtu)); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_mtu: %v", err)
	}
	if _, err := applyStandardSwitchMAC(sw); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_mac: %v", err)
	}

	network4, gateway4 := sw.Network(4), sw.Gateway(4)
	assignableNetwork4 := utils.IsAssignableIPv4CIDR(network4)
	if assignableNetwork4 && !sw.VLANFiltering {
		if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet", network4); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_network: %v", err)
		}
	}
	network6, gateway6 := sw.Network(6), sw.Gateway(6)
	if sw.VLANFiltering || sw.DisableIPv6 {
		if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet6", "no_radr", "-accept_rtadv", "ifdisabled"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_disable_ipv6_flags: %v", err)
		}
	} else {
		if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet6", "auto_linklocal", "-ifdisabled"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_enable_linklocal: %v", err)
		}
		if sw.SLAAC {
			routerPolicy := "no_radr"
			if sw.DefaultRoute6 {
				if err := ensureStandardSwitchIPv6RADefaultRouteSupport(); err != nil {
					return fmt.Errorf("create_standard_bridge: enable IPv6 RA default route: %v", err)
				}
				routerPolicy = "-no_radr"
			}
			if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet6", "auto_linklocal", "-ifdisabled", routerPolicy, "accept_rtadv"); err != nil {
				return fmt.Errorf("create_standard_bridge: failed_to_enable_slaac: %v", err)
			}
		} else if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet6", "no_radr", "-accept_rtadv"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_disable_slaac: %v", err)
		}
	}
	assignableNetwork6 := utils.IsAssignableIPv6CIDR(network6)
	if assignableNetwork6 && !sw.DisableIPv6 && !sw.VLANFiltering {
		if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "inet6", network6, "-no_dad"); err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_set_bridge_address6: %v", err)
		}
	}
	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "up"); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_bring_up_bridge: %v", err)
	}
	for _, port := range sw.Ports {
		var memberErr error
		if sw.VLANFiltering {
			memberErr = addFilteredBridgeMember(
				sw.BridgeName, port.Name, mtu, sw.DisableBridgeOffloads, nil, port.VLANPolicy,
			)
		} else {
			memberErr = addBridgeMember(sw.BridgeName, port.Name, mtu, sw.VLAN, sw.DisableBridgeOffloads)
		}
		if memberErr != nil {
			return fmt.Errorf("create_standard_bridge: %w", memberErr)
		}
	}
	if sw.VLANFiltering {
		if err := syncSetDefaultAccessVLAN(sw.BridgeName, sw.DefaultAccessVLAN); err != nil {
			return fmt.Errorf("create_standard_bridge: set default access VLAN: %w", err)
		}
	}
	if _, err := applyStandardSwitchMAC(sw); err != nil {
		return fmt.Errorf("create_standard_bridge: failed_to_verify_bridge_mac_after_members: %v", err)
	}
	if sw.VLANFiltering {
		empty := networkModels.StandardSwitch{BridgeName: sw.BridgeName, VLANFiltering: true}
		if err := reconcileFilteredStandardSwitchHost(empty, sw); err != nil {
			return fmt.Errorf("create_standard_bridge: configure host VLAN: %w", err)
		}
	}
	if sw.SLAAC && !sw.DisableIPv6 && !sw.VLANFiltering {
		if err := syncSolicitRouterAdvertisement(sw.BridgeName); err != nil {
			logger.L.Warn().
				Err(err).
				Str("bridge", sw.BridgeName).
				Msg("standard_switch_slaac_router_solicitation_failed")
		}
	}

	if assignableNetwork4 && gateway4 != "" && !sw.VLANFiltering {
		addedNetwork4Route, err = addRouteIfMissing("add", "-net", network4, gateway4)
		if err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_add_network_route: %v", err)
		}

		if sw.DefaultRoute {
			addedDefault4Route, err = addDefaultRouteIfMissing(gateway4, sw.BridgeName)
			if err != nil {
				return fmt.Errorf("create_standard_bridge: failed_to_add_default_route: %v", err)
			}
		}
	}
	if assignableNetwork6 && gateway6 != "" && !sw.DisableIPv6 && !sw.VLANFiltering {
		routeGateway6 := normalizeIPv6GatewayForRoute(gateway6, sw.BridgeName)
		addedNetwork6Route, err = addRouteIfMissing("-6", "add", "-net", network6, routeGateway6)
		if err != nil {
			return fmt.Errorf("create_standard_bridge: failed_to_add_network6_route: %v", err)
		}
		if sw.DefaultRoute6 {
			addedDefault6Route, err = addDefaultRoute6IfMissing(routeGateway6, sw.BridgeName)
			if err != nil {
				return fmt.Errorf("create_standard_bridge: failed_to_add_default6_route: %v", err)
			}
		}
	}

	if sw.DHCP && !sw.VLANFiltering {
		if err := runDhclient(sw.BridgeName, 10, sw.DefaultRoute); err != nil {
			return fmt.Errorf("create_standard_bridge: %v", err)
		}
	}

	return nil
}

type standardSwitchMemberMutation struct {
	portName string
	vlan     int
}

func standardSwitchMemberName(portName string, vlan int) string {
	if vlan > 0 {
		return fmt.Sprintf("%s.%d", portName, vlan)
	}
	return portName
}

func standardSwitchEffectiveMTU(sw networkModels.StandardSwitch) int {
	if sw.MTU == 0 {
		return 1500
	}
	return sw.MTU
}

func standardSwitchPortSet(sw networkModels.StandardSwitch) map[string]struct{} {
	ports := make(map[string]struct{}, len(sw.Ports))
	for _, port := range sw.Ports {
		ports[port.Name] = struct{}{}
	}
	return ports
}

// standardSwitchMemberMutations leaves present, unchanged members attached while
// still repairing missing members. Besides avoiding an unnecessary link flap,
// the live-state check makes a reverse edit safe after a partial forward edit.
func standardSwitchMemberMutations(
	oldSw, newSw networkModels.StandardSwitch,
	interfaceObj *iface.Interface,
) (removals, additions []standardSwitchMemberMutation) {
	liveMembers := make(map[string]struct{}, len(interfaceObj.BridgeMembers))
	for _, member := range interfaceObj.BridgeMembers {
		liveMembers[member.Name] = struct{}{}
	}

	oldPorts := standardSwitchPortSet(oldSw)
	newPorts := standardSwitchPortSet(newSw)
	refreshRetained := oldSw.VLAN != newSw.VLAN ||
		standardSwitchEffectiveMTU(oldSw) != standardSwitchEffectiveMTU(newSw) ||
		oldSw.DisableBridgeOffloads != newSw.DisableBridgeOffloads
	removedTargets := make(map[string]struct{})

	appendRemoval := func(portName string, vlan int) {
		target := standardSwitchMemberName(portName, vlan)
		if _, live := liveMembers[target]; !live {
			return
		}
		if _, alreadyRemoved := removedTargets[target]; alreadyRemoved {
			return
		}
		removals = append(removals, standardSwitchMemberMutation{portName: portName, vlan: vlan})
		removedTargets[target] = struct{}{}
		delete(liveMembers, target)
	}

	for _, oldPort := range oldSw.Ports {
		_, retained := newPorts[oldPort.Name]
		oldTarget := standardSwitchMemberName(oldPort.Name, oldSw.VLAN)
		newTarget := standardSwitchMemberName(oldPort.Name, newSw.VLAN)
		if !retained || oldTarget != newTarget || refreshRetained {
			appendRemoval(oldPort.Name, oldSw.VLAN)
		}
	}

	for _, newPort := range newSw.Ports {
		_, retained := oldPorts[newPort.Name]
		oldTarget := standardSwitchMemberName(newPort.Name, oldSw.VLAN)
		newTarget := standardSwitchMemberName(newPort.Name, newSw.VLAN)
		needsConfiguration := !retained || oldTarget != newTarget || refreshRetained

		if needsConfiguration {
			// A newly managed port may already be attached as an unmanaged member.
			// Detach it before clearing layer-3 state and applying member settings.
			appendRemoval(newPort.Name, newSw.VLAN)
		}
		if _, live := liveMembers[newTarget]; needsConfiguration || !live {
			additions = append(additions, standardSwitchMemberMutation{portName: newPort.Name, vlan: newSw.VLAN})
			liveMembers[newTarget] = struct{}{}
		}
	}

	return removals, additions
}

func editStandardBridge(oldSw, newSw networkModels.StandardSwitch) error {
	if oldSw.VLANFiltering || newSw.VLANFiltering {
		return reconcileFilteredStandardBridge(oldSw, newSw)
	}

	br := oldSw.BridgeName

	// 1) snapshot existing members
	ifaceObj, err := syncIfaceGet(br)
	if err != nil {
		return fmt.Errorf("edit_standard_bridge: get %s: %v", br, err)
	}
	if ifaceObj == nil {
		return fmt.Errorf("edit_standard_bridge: interface %s not found", br)
	}
	fullReconcile := standardSwitchModelsMatch(oldSw, newSw)
	desiredMAC, err := desiredStandardSwitchMAC(newSw)
	if err != nil {
		return fmt.Errorf("edit_standard_bridge: resolve bridge MAC: %v", err)
	}
	currentMAC, currentMACErr := currentInterfaceMAC(ifaceObj)
	macWillChange := currentMACErr != nil || currentMAC != desiredMAC
	memberRemovals, memberAdditions := standardSwitchMemberMutations(oldSw, newSw, ifaceObj)
	membersWillChange := len(memberRemovals) > 0 || len(memberAdditions) > 0
	oldManagedMembers := standardSwitchManagedMembers(oldSw)
	newManagedMembers := standardSwitchManagedMembers(newSw)
	extraMembers := make([]string, 0, len(ifaceObj.BridgeMembers))
	for _, member := range ifaceObj.BridgeMembers {
		if _, managed := oldManagedMembers[member.Name]; managed {
			continue
		}
		if _, managed := newManagedMembers[member.Name]; managed {
			continue
		}
		extraMembers = append(extraMembers, member.Name)
	}
	if (macWillChange && (oldSw.DHCP || newSw.DHCP)) || (oldSw.DHCP && newSw.DHCP && membersWillChange) {
		if err := syncStopDhclient(br); err != nil {
			return fmt.Errorf("edit_standard_bridge: stop DHCP before bridge reconfiguration: %v", err)
		}
	}
	if macWillChange {
		if _, err := applyStandardSwitchMAC(newSw); err != nil {
			return fmt.Errorf("edit_standard_bridge: set bridge MAC: %v", err)
		}
	}
	if oldSw.DHCP && !newSw.DHCP {
		if err := syncDeleteBridge(oldSw); err != nil {
			return fmt.Errorf("edit_standard_bridge: remove DHCP runtime: %v", err)
		}
		if err := syncCreateBridge(newSw); err != nil {
			return fmt.Errorf("edit_standard_bridge: create non-DHCP runtime: %v", err)
		}
		if err := reattachStandardSwitchMembers(newSw.BridgeName, extraMembers); err != nil {
			return fmt.Errorf("edit_standard_bridge: restore extra members: %v", err)
		}
		return nil
	}

	// 3) remove only ports whose membership or member-level settings changed.
	for _, member := range memberRemovals {
		if err := removeBridgeMember(br, member.portName, member.vlan); err != nil {
			return fmt.Errorf("edit_standard_bridge: remove old port %s: %v", member.portName, err)
		}
	}

	// 4) reconfigure bridge in place
	if ifaceObj.Description != newSw.Name {
		if _, err := syncRunCommand("/sbin/ifconfig", br, "descr", newSw.Name); err != nil {
			return fmt.Errorf("edit_standard_bridge: set descr: %v", err)
		}
	}

	newMTU := standardSwitchEffectiveMTU(newSw)
	if ifaceObj.MTU != newMTU {
		if _, err := syncRunCommand("/sbin/ifconfig", br, "mtu", strconv.Itoa(newMTU)); err != nil {
			return fmt.Errorf("edit_standard_bridge: set mtu: %v", err)
		}
	}

	old4Network, new4Network := oldSw.Network(4), newSw.Network(4)
	old4Gateway, new4Gateway := oldSw.Gateway(4), newSw.Gateway(4)
	ipv4AddressChanged := oldSw.DHCP != newSw.DHCP || old4Network != new4Network
	ipv4NetworkRouteChanged := ipv4AddressChanged || old4Gateway != new4Gateway
	ipv4DefaultRouteChanged := oldSw.DHCP != newSw.DHCP ||
		oldSw.DefaultRoute != newSw.DefaultRoute || old4Gateway != new4Gateway

	if ipv4NetworkRouteChanged && old4Gateway != "" && old4Network != "" {
		if err := deleteRouteIfPresent("delete", "-net", old4Network, old4Gateway); err != nil {
			return fmt.Errorf("edit_standard_bridge: delete route %s via %s: %v", old4Network, old4Gateway, err)
		}
	}
	if ipv4DefaultRouteChanged && oldSw.DefaultRoute && !oldSw.DHCP {
		if _, err := removeDefaultRouteForInterface("", br); err != nil {
			return fmt.Errorf("edit_standard_bridge: delete IPv4 default route on %s: %v", br, err)
		}
	}
	if fullReconcile || ipv4AddressChanged {
		switch {
		case newSw.DHCP:
			if !oldSw.DHCP {
				if err := deleteStandardSwitchHostIPv4(br, ifaceObj, ""); err != nil {
					return fmt.Errorf("edit_standard_bridge: prepare IPv4 DHCP: %v", err)
				}
			}
		default:
			if old4Network != "" && old4Network != new4Network &&
				interfaceHasIPv4Prefix(ifaceObj, old4Network) {
				if _, err := syncRunCommand("/sbin/ifconfig", br, "inet", old4Network, "delete"); err != nil {
					logger.L.Warn().Msgf("edit_standard_bridge: del old inet %s: %v", old4Network, err)
				}
			}
			if new4Network == "" {
				if err := deleteStandardSwitchHostIPv4(br, ifaceObj, ""); err != nil {
					return fmt.Errorf("edit_standard_bridge: reconcile IPv4 addresses: %v", err)
				}
			}
			if utils.IsAssignableIPv4CIDR(new4Network) && !interfaceHasIPv4Prefix(ifaceObj, new4Network) {
				if _, err := syncRunCommand("/sbin/ifconfig", br, "inet", new4Network); err != nil {
					return fmt.Errorf("edit_standard_bridge: set inet %s: %v", new4Network, err)
				}
			}
		}
	}

	old6Network, new6Network := oldSw.Network(6), newSw.Network(6)
	old6Gateway, new6Gateway := oldSw.Gateway(6), newSw.Gateway(6)
	ipv6ModeChanged := oldSw.DisableIPv6 != newSw.DisableIPv6 ||
		oldSw.SLAAC != newSw.SLAAC || oldSw.DefaultRoute6 != newSw.DefaultRoute6
	ipv6AddressChanged := oldSw.DisableIPv6 != newSw.DisableIPv6 ||
		oldSw.SLAAC != newSw.SLAAC || old6Network != new6Network
	ipv6NetworkRouteChanged := ipv6AddressChanged || old6Gateway != new6Gateway
	ipv6DefaultRouteChanged := ipv6ModeChanged || old6Gateway != new6Gateway

	if ipv6NetworkRouteChanged && old6Gateway != "" && old6Network != "" {
		oldRouteGateway := normalizeIPv6GatewayForRoute(old6Gateway, br)
		if err := deleteRouteIfPresent("-6", "delete", "-net", old6Network, oldRouteGateway); err != nil {
			return fmt.Errorf("edit_standard_bridge: delete IPv6 route %s via %s: %v", old6Network, old6Gateway, err)
		}
	}
	removeIPv6Default := ipv6DefaultRouteChanged && oldSw.DefaultRoute6
	removeIPv6Default = removeIPv6Default ||
		((fullReconcile || ipv6DefaultRouteChanged) && newSw.SLAAC && !newSw.DefaultRoute6)
	if removeIPv6Default {
		if _, err := removeDefaultRouteForInterface("-6", br); err != nil {
			return fmt.Errorf("edit_standard_bridge: remove IPv6 default route: %v", err)
		}
	}

	if fullReconcile || ipv6ModeChanged {
		switch {
		case newSw.DisableIPv6:
			if !filteredBridgeIPv6Disabled(ifaceObj) {
				if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", "no_radr", "-accept_rtadv", "ifdisabled"); err != nil {
					return fmt.Errorf("edit_standard_bridge: disable IPv6: %v", err)
				}
			}
		case newSw.SLAAC:
			routerPolicy := "no_radr"
			if newSw.DefaultRoute6 {
				if err := ensureStandardSwitchIPv6RADefaultRouteSupport(); err != nil {
					return fmt.Errorf("edit_standard_bridge: enable IPv6 RA default route: %v", err)
				}
				routerPolicy = "-no_radr"
			}
			if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", "auto_linklocal", "-ifdisabled", routerPolicy, "accept_rtadv"); err != nil {
				return fmt.Errorf("edit_standard_bridge: enable SLAAC: %v", err)
			}
		default:
			if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", "auto_linklocal", "-ifdisabled", "no_radr", "-accept_rtadv"); err != nil {
				return fmt.Errorf("edit_standard_bridge: disable SLAAC: %v", err)
			}
		}
	}

	if fullReconcile || ipv6AddressChanged {
		switch {
		case newSw.DisableIPv6:
			if err := deleteStandardSwitchHostIPv6(br, ifaceObj, "", false, false); err != nil {
				return fmt.Errorf("edit_standard_bridge: remove disabled IPv6 addresses: %v", err)
			}
		default:
			if old6Network != "" && old6Network != new6Network &&
				interfaceHasIPv6Prefix(ifaceObj, old6Network) {
				if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", old6Network, "delete"); err != nil {
					logger.L.Warn().Msgf("edit_standard_bridge: del old inet6 %s: %v", old6Network, err)
				}
			}
			if !newSw.SLAAC && (fullReconcile || oldSw.SLAAC != newSw.SLAAC) {
				for _, address := range ifaceObj.IPv6 {
					if !address.AutoConf {
						continue
					}
					ip := address.IP.String()
					if address.IP.IsLinkLocalUnicast() {
						ip += "%" + br
					}
					if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", ip, "delete"); err != nil &&
						!ignorableBridgeMemberIPv6CleanupError(err) {
						return fmt.Errorf("edit_standard_bridge: delete SLAAC address %s: %v", ip, err)
					}
				}
			}
			if !newSw.SLAAC && utils.IsAssignableIPv6CIDR(new6Network) &&
				!interfaceHasIPv6Prefix(ifaceObj, new6Network) {
				if _, err := syncRunCommand("/sbin/ifconfig", br, "inet6", new6Network); err != nil {
					return fmt.Errorf("edit_standard_bridge: set inet6 %s: %v", new6Network, err)
				}
			}
		}
	}

	// 5) add new, changed, or unexpectedly missing DB ports.
	for _, member := range memberAdditions {
		if err := addBridgeMember(br, member.portName, newMTU, member.vlan, newSw.DisableBridgeOffloads); err != nil {
			return fmt.Errorf("edit_standard_bridge: add port %s: %v", member.portName, err)
		}
	}
	if err := reattachStandardSwitchMembers(br, extraMembers); err != nil {
		return fmt.Errorf("edit_standard_bridge: restore extra members: %v", err)
	}
	if membersWillChange || len(extraMembers) > 0 {
		if _, err := applyStandardSwitchMAC(newSw); err != nil {
			return fmt.Errorf("edit_standard_bridge: verify bridge MAC after members: %v", err)
		}
	}

	if (fullReconcile || ipv4NetworkRouteChanged) &&
		utils.IsAssignableIPv4CIDR(new4Network) && new4Gateway != "" && !newSw.DHCP {
		if _, err := addRouteIfMissing("add", "-net", new4Network, new4Gateway); err != nil {
			return fmt.Errorf("edit_standard_bridge: add route %s via %s: %v", new4Network, new4Gateway, err)
		}
	}
	if newSw.DefaultRoute && !newSw.DHCP &&
		(fullReconcile || ipv4DefaultRouteChanged || ipv4NetworkRouteChanged) &&
		utils.IsAssignableIPv4CIDR(new4Network) && new4Gateway != "" {
		if _, err := addDefaultRouteIfMissing(new4Gateway, br); err != nil {
			return fmt.Errorf("edit_standard_bridge: add default route via %s: %v", new4Gateway, err)
		}
	}
	if (fullReconcile || ipv6NetworkRouteChanged) && new6Gateway != "" &&
		utils.IsAssignableIPv6CIDR(new6Network) && !newSw.DisableIPv6 && !newSw.SLAAC {
		newRouteGateway := normalizeIPv6GatewayForRoute(new6Gateway, br)
		if _, err := addRouteIfMissing("-6", "add", "-net", new6Network, newRouteGateway); err != nil {
			return fmt.Errorf("edit_standard_bridge: add IPv6 route %s via %s: %v", new6Network, new6Gateway, err)
		}
	}
	if newSw.DefaultRoute6 && !newSw.DisableIPv6 && !newSw.SLAAC &&
		(fullReconcile || ipv6DefaultRouteChanged || ipv6NetworkRouteChanged) &&
		new6Gateway != "" && utils.IsAssignableIPv6CIDR(new6Network) {
		newRouteGateway := normalizeIPv6GatewayForRoute(new6Gateway, br)
		if _, err := addDefaultRoute6IfMissing(newRouteGateway, br); err != nil {
			return fmt.Errorf("edit_standard_bridge: add IPv6 default route via %s: %v", new6Gateway, err)
		}
	}

	bridgeWasUp := interfaceIsUp(ifaceObj)
	if !bridgeWasUp {
		if _, err := syncRunCommand("/sbin/ifconfig", br, "up"); err != nil {
			return fmt.Errorf("edit_standard_bridge: failed to bring up bridge: %v", err)
		}
	}
	connectivityChanged := macWillChange || membersWillChange || ifaceObj.MTU != newMTU || !bridgeWasUp
	if newSw.SLAAC && !newSw.DisableIPv6 &&
		(fullReconcile || ipv6ModeChanged || connectivityChanged) {
		if err := syncSolicitRouterAdvertisement(br); err != nil {
			logger.L.Warn().
				Err(err).
				Str("bridge", br).
				Msg("standard_switch_slaac_router_solicitation_failed")
		}
	}
	if newSw.DHCP &&
		(fullReconcile || !oldSw.DHCP || oldSw.DefaultRoute != newSw.DefaultRoute || connectivityChanged) {
		if err := runDhclient(newSw.BridgeName, 10, newSw.DefaultRoute); err != nil {
			return fmt.Errorf("edit_standard_bridge: %v", err)
		}
	}

	return nil
}

func deleteStandardBridge(sw networkModels.StandardSwitch) error {
	hostInterface := standardSwitchHostInterfaceName(sw)
	if err := removeStandardSwitchRoutes(sw); err != nil {
		return fmt.Errorf("delete_standard_bridge: remove routes: %v", err)
	}
	if err := destroyStandardSwitchRuntimeInterfaces(sw); err != nil {
		return fmt.Errorf("delete_standard_bridge: %v", err)
	}
	if sw.DHCP && hostInterface != "" {
		if err := stopDhclient(hostInterface); err != nil {
			return fmt.Errorf("delete_standard_bridge: %v", err)
		}
	}
	return nil
}

func bridgeOffloadDisableArgs(enabled uint32) []string {
	args := make([]string, 0, 6)
	if enabled&ifcapTXCSUM != 0 {
		args = append(args, "-txcsum")
	}
	if enabled&ifcapTXCSUMIPv6 != 0 {
		args = append(args, "-txcsum6")
	}
	if enabled&(ifcapTSO4|ifcapTSO6) != 0 {
		args = append(args, "-tso")
	}
	if enabled&ifcapLRO != 0 {
		args = append(args, "-lro")
	}
	if enabled&(ifcapTOE4|ifcapTOE6) != 0 {
		args = append(args, "-toe")
	}
	if enabled&ifcapMEXTPG != 0 {
		args = append(args, "-mextpg")
	}
	return args
}

func disableBridgeMemberOffloads(name string) error {
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		return fmt.Errorf("inspect interface %s: %v", name, err)
	}
	if interfaceObj == nil {
		return fmt.Errorf("inspect interface %s: interface not found", name)
	}

	interfaceName := strings.ToLower(name)
	driver := strings.ToLower(interfaceObj.Driver)
	if strings.HasPrefix(interfaceName, "tap") || strings.HasPrefix(interfaceName, "epair") ||
		strings.HasPrefix(interfaceName, "vnet") || strings.HasPrefix(interfaceName, "bridge") ||
		strings.Contains(driver, "tap") || strings.Contains(driver, "epair") || strings.Contains(driver, "bridge") ||
		utils.Contains(interfaceObj.Groups, "tap") || utils.Contains(interfaceObj.Groups, "epair") ||
		utils.Contains(interfaceObj.Groups, "vnet") || utils.Contains(interfaceObj.Groups, "bridge") {
		return nil
	}

	disableArgs := bridgeOffloadDisableArgs(interfaceObj.Capabilities.Enabled.Raw)
	if len(disableArgs) == 0 {
		return nil
	}

	logger.L.Info().Msgf("disabling bridge offloads on %s: %s", name, strings.Join(disableArgs, " "))
	args := append([]string{name}, disableArgs...)
	if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
		return fmt.Errorf("disable bridge offloads on %s: %v", name, err)
	}
	return nil
}

func ignorableBridgeMemberIPv6CleanupError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "can't assign requested address") ||
		strings.Contains(message, "address not available") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "permission denied")
}

func clearBridgeMemberLayer3(name string) error {
	if err := syncStopDhclient(name); err != nil {
		return fmt.Errorf("stop DHCP client on %s: %v", name, err)
	}

	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		return fmt.Errorf("inspect addresses on %s: %v", name, err)
	}
	if interfaceObj == nil {
		return fmt.Errorf("inspect addresses on %s: interface not found", name)
	}

	var cleanupErrors []error
	if _, err := syncRunCommand("/sbin/ifconfig", name, "inet6", "-auto_linklocal", "-accept_rtadv"); err != nil &&
		!ignorableBridgeMemberIPv6CleanupError(err) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("disable IPv6 autoconfiguration on %s: %w", name, err))
	}
	for _, address := range interfaceObj.IPv4 {
		if _, err := syncRunCommand("/sbin/ifconfig", name, "inet", address.IP.String(), "delete"); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete IPv4 address %s from %s: %w", address.IP, name, err))
		}
	}
	for _, address := range interfaceObj.IPv6 {
		ip := address.IP.String()
		if strings.HasPrefix(strings.ToLower(ip), "fe80:") {
			ip += "%" + name
		}
		if _, err := syncRunCommand("/sbin/ifconfig", name, "inet6", ip, "delete"); err != nil &&
			!ignorableBridgeMemberIPv6CleanupError(err) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete IPv6 address %s from %s: %w", ip, name, err))
		}
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		return err
	}

	freshInterface, err := syncIfaceGet(name)
	if err != nil {
		return fmt.Errorf("verify addresses on %s: %v", name, err)
	}
	if freshInterface == nil {
		return fmt.Errorf("verify addresses on %s: interface not found", name)
	}
	if len(freshInterface.IPv4) > 0 || len(freshInterface.IPv6) > 0 {
		return fmt.Errorf(
			"verify addresses on %s: %d IPv4 and %d IPv6 addresses remain",
			name,
			len(freshInterface.IPv4),
			len(freshInterface.IPv6),
		)
	}

	return nil
}

func addBridgeMember(br, portName string, mtu, vlan int, disableOffloads bool) error {
	if disableOffloads {
		if err := disableBridgeMemberOffloads(portName); err != nil {
			return err
		}
	}

	if mtu > 0 {
		if _, err := syncRunCommand("/sbin/ifconfig", portName, "mtu", strconv.Itoa(mtu)); err != nil {
			return fmt.Errorf("set mtu for %s: %v", portName, err)
		}
	}

	targetPort := portName
	if vlan > 0 {
		vif := fmt.Sprintf("%s.%d", portName, vlan)
		targetPort = vif

		if _, err := syncRunCommand("/sbin/ifconfig", vif); err != nil {
			args := []string{
				"vlan", "create",
				"vlandev", portName,
				"vlan", strconv.Itoa(vlan),
				"descr", fmt.Sprintf("svm-vlan/%s/%s", br, vif),
				"name", vif,
				"group", "svm-vlan",
				"up",
			}
			if _, err := syncRunCommand("/sbin/ifconfig", args...); err != nil {
				return fmt.Errorf("create vlan %s: %v", vif, err)
			}
		}

		if disableOffloads {
			if err := disableBridgeMemberOffloads(targetPort); err != nil {
				return err
			}
		}
	}

	if err := clearBridgeMemberLayer3(targetPort); err != nil {
		return fmt.Errorf("clear layer-3 configuration on %s: %v", targetPort, err)
	}

	if _, err := syncRunCommand("/sbin/ifconfig", br, "addm", targetPort, "up"); err != nil {
		return fmt.Errorf("add %s to bridge %s: %v", targetPort, br, err)
	}
	if err := syncSetBridgeMemberPrivate(br, targetPort, false); err != nil {
		return fmt.Errorf("clear private state on physical member %s: %v", targetPort, err)
	}
	if _, err := syncRunCommand("/sbin/ifconfig", targetPort, "up"); err != nil {
		return fmt.Errorf("bring up %s: %v", targetPort, err)
	}

	return nil
}

func addFilteredBridgeMember(
	bridge string,
	portName string,
	mtu int,
	disableOffloads bool,
	expectedDefaultAccessVLAN *int,
	policy bridgevlan.PortPolicy,
) (retErr error) {
	bridgeState, err := syncIfaceGet(bridge)
	if err != nil {
		return fmt.Errorf("inspect filtered bridge %s members: %w", bridge, err)
	}
	if bridgeState == nil {
		return fmt.Errorf("inspect filtered bridge %s members: interface not found", bridge)
	}
	portState, err := syncIfaceGet(portName)
	if err != nil {
		return fmt.Errorf("inspect filtered member %s: %w", portName, err)
	}
	if portState == nil {
		return fmt.Errorf("inspect filtered member %s: interface not found", portName)
	}
	wasAttached := false
	for _, member := range bridgeState.BridgeMembers {
		if member.Name == portName {
			wasAttached = true
			break
		}
	}

	if _, err := syncRunCommand("/sbin/ifconfig", portName, "down"); err != nil {
		return fmt.Errorf("hold filtered member %s down: %w", portName, err)
	}
	restoreAttachedLink := wasAttached && interfaceIsUp(portState)
	defer func() {
		if retErr != nil && restoreAttachedLink {
			if _, err := syncRunCommand("/sbin/ifconfig", portName, "up"); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("restore filtered member %s link state: %w", portName, err))
			}
		}
		retErr = cleanupFailedFilteredBridgeMember(bridge, portName, wasAttached, retErr)
	}()

	if disableOffloads {
		if err := disableBridgeMemberOffloads(portName); err != nil {
			return err
		}
	}
	if mtu > 0 && portState.MTU != mtu {
		if _, err := syncRunCommand("/sbin/ifconfig", portName, "mtu", strconv.Itoa(mtu)); err != nil {
			return fmt.Errorf("set mtu for %s: %w", portName, err)
		}
	}

	if err := clearBridgeMemberLayer3(portName); err != nil {
		return fmt.Errorf("clear layer-3 configuration on %s: %w", portName, err)
	}
	restoreAttachedLink = false
	if err := syncConfigureFilteredMember(bridge, portName, expectedDefaultAccessVLAN, policy); err != nil {
		return fmt.Errorf("configure %s on filtered bridge %s: %w", portName, bridge, err)
	}
	if err := syncSetBridgeMemberPrivate(bridge, portName, false); err != nil {
		return fmt.Errorf("clear private state on physical member %s: %w", portName, err)
	}
	if _, err := syncRunCommand("/sbin/ifconfig", portName, "up"); err != nil {
		return fmt.Errorf("bring up filtered member %s: %w", portName, err)
	}
	return nil
}

func cleanupFailedFilteredBridgeMember(
	bridge string,
	member string,
	wasAttached bool,
	operationErr error,
) error {
	if operationErr == nil || wasAttached {
		return operationErr
	}

	detached, inspectErr := filteredBridgeMemberDetached(bridge, member)
	if inspectErr != nil {
		logger.L.Error().Err(inspectErr).Str("bridge", bridge).Str("member", member).
			Msg("filtered_bridge_member_cleanup_inspection_failed")
		return errors.Join(
			operationErr,
			fmt.Errorf("verify failed new filtered member %s detachment: %w", member, inspectErr),
		)
	}

	if !detached {
		removeErr := syncRemoveFilteredMember(bridge, member)
		if removeErr == nil || isInterfaceMissingError(removeErr) {
			detached = true
		} else {
			logger.L.Error().Err(removeErr).Str("bridge", bridge).Str("member", member).
				Msg("filtered_bridge_member_cleanup_failed")
			operationErr = errors.Join(
				operationErr,
				fmt.Errorf("remove failed new filtered member %s: %w", member, removeErr),
			)

			detached, inspectErr = filteredBridgeMemberDetached(bridge, member)
			if inspectErr != nil {
				return errors.Join(
					operationErr,
					fmt.Errorf("verify failed new filtered member %s detachment: %w", member, inspectErr),
				)
			}
		}
	}
	if !detached {
		return operationErr
	}

	if _, err := syncRunCommand("/sbin/ifconfig", member, "up"); err != nil {
		logger.L.Error().Err(err).Str("bridge", bridge).Str("member", member).
			Msg("filtered_bridge_member_link_restore_failed")
		return errors.Join(operationErr, fmt.Errorf("restore filtered member %s link state: %w", member, err))
	}
	return operationErr
}

func removeBridgeMember(br, portName string, vlan int) error {
	if vlan > 0 {
		vif := fmt.Sprintf("%s.%d", portName, vlan)
		if _, err := syncRunCommand("/sbin/ifconfig", br, "deletem", vif); err != nil {
			return fmt.Errorf("remove vlan member %s: %v", vif, err)
		}

		if _, err := syncRunCommand("/sbin/ifconfig", vif, "destroy"); err != nil {
			return fmt.Errorf("destroy vlan iface %s: %v", vif, err)
		}

	} else {
		if _, err := syncRunCommand("/sbin/ifconfig", br, "deletem", portName); err != nil {
			return fmt.Errorf("remove port member %s: %v", portName, err)
		}
	}
	return nil
}

const standardSwitchRouterSolicitationTimeout = 5 * time.Second

func solicitStandardSwitchRouterAdvertisement(br string) error {
	ctx, cancel := context.WithTimeout(context.Background(), standardSwitchRouterSolicitationTimeout)
	defer cancel()

	// Do not use rtsol -F here: that option disables IPv6 forwarding.
	if _, err := syncRunCommandWithContext(ctx, "/sbin/rtsol", "-i", br); err != nil {
		return fmt.Errorf("solicit IPv6 router advertisement on %s: %w", br, err)
	}
	return nil
}

func runDhclient(br string, timeout int, useDefaultRoute bool) error {
	interfaceObj, err := syncIfaceGet(br)
	if err != nil {
		return fmt.Errorf("dhclient: failed to get interface %s: %v", br, err)
	}
	if interfaceObj == nil {
		return fmt.Errorf("dhclient: interface %s not found", br)
	}
	if err := ensureDhclientRuntimeDir(); err != nil {
		return fmt.Errorf("dhclient: prepare runtime directory: %v", err)
	}
	running, _, err := dhclientRunning(br)
	if err != nil {
		return fmt.Errorf("dhclient: inspect existing client for %s: %v", br, err)
	}
	policyMatches, err := dhclientRoutePolicyMatches(br, useDefaultRoute)
	if err != nil {
		return fmt.Errorf("dhclient: inspect route policy for %s: %v", br, err)
	}
	if !useDefaultRoute {
		if _, err := removeDefaultRouteForInterface("", br); err != nil {
			return fmt.Errorf("dhclient: remove default route for %s: %v", br, err)
		}
	}
	if running && len(interfaceObj.IPv4) > 0 && policyMatches {
		if !useDefaultRoute {
			return nil
		}
		_, routeExists, err := defaultRouteInterface("")
		if err != nil {
			return fmt.Errorf("dhclient: inspect default route for %s: %v", br, err)
		}
		if routeExists {
			// Routes on other interfaces are outside this managed switch's ownership.
			return nil
		}
	}
	if running {
		if err := stopDhclient(br); err != nil {
			return fmt.Errorf("dhclient: restart client for %s: %v", br, err)
		}
	}
	if err := configureDhclientRoutePolicy(br, useDefaultRoute); err != nil {
		return fmt.Errorf("dhclient: configure route policy for %s: %v", br, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeout))
	defer cancel()

	args := []string{"-b", "-p", dhclientPIDPath(br)}
	if !useDefaultRoute {
		args = append(args, "-c", dhclientConfigPath(br))
	}
	args = append(args, br)
	_, err = syncRunCommandWithContext(ctx, "/sbin/dhclient", args...)
	if err != nil {
		logger.L.Debug().Msgf("dhclient: failed to run dhclient for %s: %v", br, err)
		if strings.Contains(err.Error(), "dhclient already running") {
			return nil
		}

		return fmt.Errorf("dhclient: failed to run dhclient for %s: %v", br, err)
	}

	return nil
}
