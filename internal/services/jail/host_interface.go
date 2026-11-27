// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"sort"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"gorm.io/gorm"
)

// ensureHostInterfaceUnclaimed only touches db, so it is safe inside a
// transaction. ensureHostInterfaceEligible queries through the network
// service's own handle and must run outside one: the pool has one connection.
func ensureHostInterfaceUnclaimed(db *gorm.DB, name string, excludeNetworkID, excludeJailID uint) error {
	var count int64
	query := db.Model(&jailModels.Network{}).
		Where("switch_type = ? AND host_interface = ?", networkAttachment.SwitchTypeHostInterface, name)
	if excludeNetworkID > 0 {
		query = query.Where("id <> ?", excludeNetworkID)
	}
	if excludeJailID > 0 {
		query = query.Where("jid <> ?", excludeJailID)
	}
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_host_interface_usage: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("host_interface_used_by_jail: %s", name)
	}
	return nil
}

func (s *Service) ensureHostInterfaceEligible(name string) error {
	if !networkAttachment.ValidHostInterfaceName(name) {
		return fmt.Errorf("invalid_host_interface_name")
	}
	if s.NetworkService == nil {
		return fmt.Errorf("network_service_unavailable")
	}
	targets, err := s.NetworkService.JailHostInterfaceTargets()
	if err != nil {
		return fmt.Errorf("failed_to_inspect_host_interfaces: %w", err)
	}
	for _, target := range targets {
		if target.Interface != name {
			continue
		}
		if !target.Eligible {
			return fmt.Errorf("%s: %s", target.Reason, name)
		}
		return nil
	}
	return fmt.Errorf("host_interface_not_found: %s", name)
}

func (s *Service) keepAvailableHostInterfaceNetworks(
	jailID uint,
	networks []jailModels.Network,
) ([]jailModels.Network, []string) {
	kept := make([]jailModels.Network, 0, len(networks))
	var warnings []string
	for _, network := range networks {
		if !isHostInterfaceNetwork(network) {
			kept = append(kept, network)
			continue
		}
		_, dropped := SplitHostInterfaceNetworks([]jailModels.Network{network})
		name := dropped[0]
		err := s.ensureHostInterfaceEligible(name)
		if err == nil {
			err = ensureHostInterfaceUnclaimed(s.DB, name, 0, jailID)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("host_interface_%s_not_available_on_host; skipped", name))
			continue
		}
		network.HostInterface = name
		kept = append(kept, network)
	}
	return kept, warnings
}

var jailDyingIDs = func(s *Service, ctID uint) ([]int, error) {
	return s.DyingJailIDs(ctID)
}

func (s *Service) ensureJailHostInterfacesReady(jail jailModels.Jail) error {
	for _, network := range jail.Networks {
		if !isHostInterfaceNetwork(network) {
			continue
		}
		err := s.ensureHostInterfaceEligible(network.HostInterface)
		if err == nil {
			continue
		}
		if strings.HasPrefix(err.Error(), "host_interface_not_found") {
			if jids, dyingErr := jailDyingIDs(s, jail.CTID); dyingErr == nil && len(jids) > 0 {
				return fmt.Errorf("host_interface_held_by_dying_jail: %s", network.HostInterface)
			}
		}
		return err
	}
	return nil
}

func (s *Service) ListHostInterfaces() ([]jailServiceInterfaces.HostInterfaceTarget, error) {
	if s.NetworkService == nil {
		return nil, fmt.Errorf("network_service_unavailable")
	}
	live, err := s.NetworkService.JailHostInterfaceTargets()
	if err != nil {
		return nil, fmt.Errorf("failed_to_inspect_host_interfaces: %w", err)
	}
	byName := make(map[string]*jailServiceInterfaces.HostInterfaceTarget, len(live))
	for _, target := range live {
		byName[target.Interface] = &jailServiceInterfaces.HostInterfaceTarget{
			Interface: target.Interface,
			Eligible:  target.Eligible,
			Reason:    target.Reason,
		}
	}

	var claims []struct {
		HostInterface string
		CTID          uint
		Name          string
	}
	if err := s.DB.Table("jail_networks").
		Select("jail_networks.host_interface, jails.ct_id AS ct_id, jails.name").
		Joins("JOIN jails ON jails.id = jail_networks.jid").
		Where("jail_networks.switch_type = ?", networkAttachment.SwitchTypeHostInterface).
		Scan(&claims).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_host_interface_claims: %w", err)
	}
	for _, claim := range claims {
		target, ok := byName[claim.HostInterface]
		if !ok {
			target = &jailServiceInterfaces.HostInterfaceTarget{Interface: claim.HostInterface}
			byName[claim.HostInterface] = target
		}
		target.Eligible = false
		target.Reason = "host_interface_used_by_jail"
		target.JailCTID = claim.CTID
		target.JailName = claim.Name
	}

	targets := make([]jailServiceInterfaces.HostInterfaceTarget, 0, len(byName))
	for _, target := range byName {
		targets = append(targets, *target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Interface < targets[j].Interface })
	return targets, nil
}
