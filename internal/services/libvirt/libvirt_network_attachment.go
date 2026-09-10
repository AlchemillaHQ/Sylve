// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

var (
	validateVMFilteredBridge = bridgevlan.ValidateFilteredBridge
	inspectVMBridgeVLAN      = bridgevlan.InspectBridge
)

func vmNetworkAttachmentResolver(db *gorm.DB) networkAttachment.Resolver {
	return networkAttachment.Resolver{
		DB:                     db,
		InspectBridge:          inspectVMBridgeVLAN,
		ValidateFilteredBridge: validateVMFilteredBridge,
	}
}

func ResolveDesiredVMNetworkAttachment(
	db *gorm.DB,
	switchType string,
	switchID uint,
) (networkAttachment.Contract, error) {
	contract, _, err := vmNetworkAttachmentResolver(db).DesiredContractByID(
		networkAttachment.KindVM, switchType, switchID, nil,
	)
	return contract, err
}

func ResolveEffectiveVMNetworkAttachment(
	db *gorm.DB,
	switchType string,
	switchID uint,
) (networkAttachment.Contract, error) {
	contract, _, err := vmNetworkAttachmentResolver(db).EffectiveContractByID(
		networkAttachment.KindVM, switchType, switchID, nil,
	)
	return contract, err
}

func ResolveVMNetworkIdentity(
	db *gorm.DB,
	switchType string,
	switchID uint,
) (networkAttachment.Contract, error) {
	sw, err := vmNetworkAttachmentResolver(db).ResolveIdentityByID(switchType, switchID)
	if err != nil {
		return networkAttachment.Contract{}, err
	}
	return networkAttachment.LegacyUnfiltered(networkAttachment.KindVM, sw.Name, sw.Type)
}

func VMNetworkAttachmentFromMetadata(network vmModels.Network) (networkAttachment.Contract, error) {
	if network.Attachment != nil {
		contract, err := networkAttachment.Normalize(*network.Attachment)
		if err != nil {
			return networkAttachment.Contract{}, err
		}
		if contract.Kind != networkAttachment.KindVM {
			return networkAttachment.Contract{}, fmt.Errorf("invalid_vm_network_attachment_kind")
		}
		return contract, nil
	}

	var switchName string
	switch strings.ToLower(strings.TrimSpace(network.SwitchType)) {
	case "manual":
		if network.ManualSwitch == nil {
			return networkAttachment.Contract{}, fmt.Errorf("manual_switch_metadata_missing")
		}
		if network.ManualSwitch.VLANFiltering || network.ManualSwitch.DefaultAccessVLAN != nil {
			return networkAttachment.Contract{}, fmt.Errorf("legacy_filtered_vm_network_attachment_unsupported")
		}
		switchName = network.ManualSwitch.Name
	case "standard", "":
		if network.StandardSwitch == nil {
			return networkAttachment.Contract{}, fmt.Errorf("standard_switch_metadata_missing")
		}
		if network.StandardSwitch.VLANFiltering || network.StandardSwitch.DefaultAccessVLAN != nil {
			return networkAttachment.Contract{}, fmt.Errorf("legacy_filtered_vm_network_attachment_unsupported")
		}
		switchName = network.StandardSwitch.Name
	default:
		return networkAttachment.Contract{}, fmt.Errorf("invalid_switch_type")
	}
	return networkAttachment.LegacyUnfiltered(
		networkAttachment.KindVM, switchName, network.SwitchType,
	)
}

func ResolveDesiredTargetVMNetworkAttachment(
	db *gorm.DB,
	expected networkAttachment.Contract,
) (networkAttachment.ResolvedSwitch, error) {
	if expected.Kind != networkAttachment.KindVM {
		return networkAttachment.ResolvedSwitch{}, fmt.Errorf("invalid_vm_network_attachment_kind")
	}
	return vmNetworkAttachmentResolver(db).ValidateDesiredTarget(expected)
}

func ResolveTargetVMNetworkIdentity(
	db *gorm.DB,
	expected networkAttachment.Contract,
) (networkAttachment.ResolvedSwitch, error) {
	expected, err := networkAttachment.Normalize(expected)
	if err != nil {
		return networkAttachment.ResolvedSwitch{}, err
	}
	if expected.Kind != networkAttachment.KindVM {
		return networkAttachment.ResolvedSwitch{}, fmt.Errorf("invalid_vm_network_attachment_kind")
	}
	return vmNetworkAttachmentResolver(db).ResolveIdentityByName(
		expected.SwitchType, expected.SwitchName,
	)
}

func validateDesiredVMNetworkSwitchCompatibility(sw networkAttachment.ResolvedSwitch) error {
	_, err := sw.Contract(networkAttachment.KindVM, nil)
	return err
}

func validateEffectiveVMNetworkSwitchCompatibility(sw networkAttachment.ResolvedSwitch) error {
	if err := validateDesiredVMNetworkSwitchCompatibility(sw); err != nil {
		return err
	}
	return networkAttachment.Resolver{
		InspectBridge:          inspectVMBridgeVLAN,
		ValidateFilteredBridge: validateVMFilteredBridge,
	}.ValidateRuntime(sw)
}

func (s *Service) validateVMNetworksForStart(vmID uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}

	var networks []vmModels.Network
	if err := s.DB.Session(&gorm.Session{SkipHooks: true}).
		Where("vm_id = ? AND enable = ?", vmID, true).
		Order("id ASC").
		Find(&networks).Error; err != nil {
		return fmt.Errorf("failed_to_list_vm_networks_for_start: %w", err)
	}

	for _, network := range networks {
		sw, err := vmNetworkAttachmentResolver(s.DB).ResolveDesiredByID(network.SwitchType, network.SwitchID)
		if err != nil {
			return fmt.Errorf("failed_to_resolve_network_switch_%d_for_start: %w", network.ID, err)
		}
		if err := validateEffectiveVMNetworkSwitchCompatibility(sw); err != nil {
			return fmt.Errorf("failed_to_validate_network_switch_%d_for_start: %w", network.ID, err)
		}
	}

	return nil
}

func (s *Service) vmStandardSwitchLifecycleBridges(
	vmID uint,
	enabledOnly bool,
	targetNames ...string,
) ([]string, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}

	var networks []vmModels.Network
	query := s.DB.Session(&gorm.Session{SkipHooks: true}).
		Select("switch_id", "switch_type").
		Where("vm_id = ?", vmID).
		Order("id ASC")
	if enabledOnly {
		query = query.Where("enable = ?", true)
	}
	if vmID != 0 {
		if err := query.Find(&networks).Error; err != nil {
			return nil, fmt.Errorf("failed_to_list_vm_networks_for_lifecycle_lock: %w", err)
		}
	}

	bridges := make(map[string]struct{}, len(networks)+len(targetNames))
	resolver := vmNetworkAttachmentResolver(s.DB)
	for _, network := range networks {
		switchType, err := networkAttachment.NormalizeSwitchType(network.SwitchType)
		if err != nil {
			continue
		}
		if switchType != "standard" {
			continue
		}
		sw, err := resolver.ResolveIdentityByID(switchType, network.SwitchID)
		if err != nil {
			if errors.Is(err, networkAttachment.ErrSwitchNotFound) {
				continue
			}
			return nil, fmt.Errorf("failed_to_resolve_vm_network_switch_for_lifecycle_lock: %w", err)
		}
		bridges[sw.Bridge] = struct{}{}
	}
	for _, targetName := range targetNames {
		if strings.TrimSpace(targetName) == "" {
			continue
		}
		sw, err := resolver.ResolveIdentityByNameAny(targetName)
		if err != nil {
			return nil, fmt.Errorf("failed_to_resolve_vm_network_target_for_lifecycle_lock: %w", err)
		}
		if sw.Type == "standard" {
			bridges[sw.Bridge] = struct{}{}
		}
	}

	names := make([]string, 0, len(bridges))
	for bridgeName := range bridges {
		names = append(names, bridgeName)
	}
	sort.Strings(names)
	return names, nil
}

func (s *Service) lockVMStandardSwitchLifecycle(
	vmID uint,
	enabledOnly bool,
	targetNames ...string,
) (func(), error) {
	bridges, err := s.vmStandardSwitchLifecycleBridges(vmID, enabledOnly, targetNames...)
	if err != nil {
		return nil, err
	}
	return bridgevlan.LockStandardSwitchLifecycle(bridges...), nil
}
