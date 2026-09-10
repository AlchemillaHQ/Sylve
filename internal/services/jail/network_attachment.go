// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

func jailNetworkAttachmentResolver(db *gorm.DB) networkAttachment.Resolver {
	return networkAttachment.Resolver{
		DB:                     db,
		InspectBridge:          jailInspectBridgeVLAN,
		ValidateFilteredBridge: jailValidateFilteredBridge,
	}
}

func (s *Service) lockJailStandardSwitchLifecycle(jailID uint, targetNames ...string) (func(), error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("jail_network_service_unavailable")
	}

	var networks []jailModels.Network
	if jailID != 0 {
		if err := s.DB.Select("switch_id", "switch_type").
			Where("jid = ?", jailID).
			Order("id ASC").
			Find(&networks).Error; err != nil {
			return nil, fmt.Errorf("failed_to_list_jail_networks_for_lifecycle_lock: %w", err)
		}
	}

	bridges := make(map[string]struct{}, len(networks)+len(targetNames))
	resolver := jailNetworkAttachmentResolver(s.DB)
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
			return nil, fmt.Errorf("failed_to_resolve_jail_network_switch_for_lifecycle_lock: %w", err)
		}
		bridges[sw.Bridge] = struct{}{}
	}
	for _, targetName := range targetNames {
		sw, err := resolver.ResolveIdentityByNameAny(targetName)
		if err != nil {
			return nil, fmt.Errorf("failed_to_resolve_jail_network_target_for_lifecycle_lock: %w", err)
		}
		if sw.Type == "standard" {
			bridges[sw.Bridge] = struct{}{}
		}
	}

	names := make([]string, 0, len(bridges))
	for bridgeName := range bridges {
		names = append(names, bridgeName)
	}
	return bridgevlan.LockStandardSwitchLifecycle(names...), nil
}

func jailNetworkPolicy(network jailModels.Network) *bridgevlan.PortPolicy {
	if network.VLANPolicy.Mode == "" && network.VLANPolicy.UntaggedVLAN == nil &&
		len(network.VLANPolicy.TaggedVLANs) == 0 {
		return nil
	}
	policy := network.VLANPolicy
	return &policy
}

func ResolveDesiredNetworkAttachment(
	db *gorm.DB,
	network *jailModels.Network,
) (networkAttachment.Contract, networkAttachment.ResolvedSwitch, error) {
	if network == nil {
		return networkAttachment.Contract{}, networkAttachment.ResolvedSwitch{}, fmt.Errorf("jail_network_required")
	}
	contract, sw, err := jailNetworkAttachmentResolver(db).DesiredContractByID(
		networkAttachment.KindJail, network.SwitchType, network.SwitchID, jailNetworkPolicy(*network),
	)
	if err != nil {
		return networkAttachment.Contract{}, networkAttachment.ResolvedSwitch{}, err
	}
	normalizeJailNetworkPolicy(network, contract)
	return contract, sw, nil
}

func ResolveEffectiveNetworkAttachment(
	db *gorm.DB,
	network *jailModels.Network,
) (networkAttachment.Contract, networkAttachment.ResolvedSwitch, error) {
	if network == nil {
		return networkAttachment.Contract{}, networkAttachment.ResolvedSwitch{}, fmt.Errorf("jail_network_required")
	}
	contract, sw, err := jailNetworkAttachmentResolver(db).EffectiveContractByID(
		networkAttachment.KindJail, network.SwitchType, network.SwitchID, jailNetworkPolicy(*network),
	)
	if err != nil {
		return networkAttachment.Contract{}, networkAttachment.ResolvedSwitch{}, err
	}
	normalizeJailNetworkPolicy(network, contract)
	return contract, sw, nil
}

func normalizeJailNetworkPolicy(network *jailModels.Network, contract networkAttachment.Contract) {
	if contract.VLANPolicy == nil {
		network.VLANPolicy = bridgevlan.PortPolicy{}
	} else {
		network.VLANPolicy = *contract.VLANPolicy
	}
}

func NetworkAttachmentFromMetadata(network jailModels.Network) (networkAttachment.Contract, error) {
	if network.Attachment != nil {
		contract, err := networkAttachment.Normalize(*network.Attachment)
		if err != nil {
			return networkAttachment.Contract{}, err
		}
		if contract.Kind != networkAttachment.KindJail {
			return networkAttachment.Contract{}, fmt.Errorf("invalid_jail_network_attachment_kind")
		}
		return contract, nil
	}

	if jailNetworkPolicy(network) != nil {
		return networkAttachment.Contract{}, fmt.Errorf("legacy_filtered_jail_network_attachment_unsupported")
	}

	switchType, err := networkAttachment.NormalizeSwitchType(network.SwitchType)
	if err != nil {
		return networkAttachment.Contract{}, err
	}
	var switchName string
	switch switchType {
	case "manual":
		if network.ManualSwitch == nil {
			return networkAttachment.Contract{}, fmt.Errorf("manual_switch_metadata_missing")
		}
		if network.ManualSwitch.VLANFiltering {
			return networkAttachment.Contract{}, fmt.Errorf("legacy_filtered_jail_network_attachment_unsupported")
		}
		switchName = network.ManualSwitch.Name
	case "standard":
		if network.StandardSwitch == nil {
			return networkAttachment.Contract{}, fmt.Errorf("standard_switch_metadata_missing")
		}
		if network.StandardSwitch.VLANFiltering {
			return networkAttachment.Contract{}, fmt.Errorf("legacy_filtered_jail_network_attachment_unsupported")
		}
		switchName = network.StandardSwitch.Name
	}

	return networkAttachment.LegacyUnfiltered(
		networkAttachment.KindJail, switchName, switchType,
	)
}

func ResolveDesiredTargetNetworkAttachment(
	db *gorm.DB,
	expected networkAttachment.Contract,
) (networkAttachment.ResolvedSwitch, error) {
	if expected.Kind != networkAttachment.KindJail {
		return networkAttachment.ResolvedSwitch{}, fmt.Errorf("invalid_jail_network_attachment_kind")
	}
	return jailNetworkAttachmentResolver(db).ValidateDesiredTarget(expected)
}

func (s *Service) prepareJailNetworkMetadata(jail *jailModels.Jail) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("jail_network_service_unavailable")
	}
	if jail == nil {
		return fmt.Errorf("jail_metadata_required")
	}
	for i := range jail.Networks {
		network := &jail.Networks[i]
		network.Attachment = nil
		contract, _, err := ResolveDesiredNetworkAttachment(s.DB, network)
		if err != nil {
			return fmt.Errorf("failed_to_resolve_jail_network_switch_for_metadata: network=%d: %w", i+1, err)
		}
		network.Attachment = &contract
		network.StandardSwitch = nil
		network.ManualSwitch = nil
	}
	return nil
}
