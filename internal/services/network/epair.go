// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	utils "github.com/alchemillahq/sylve/pkg/utils"

	"github.com/alchemillahq/sylve/pkg/network/iface"
)

const sylveEpairGroup = "sylve"

var (
	epairRe            = regexp.MustCompile(`^([a-z0-9]{5})_net([0-9]+)(a|b)$`)
	epairInterfaceList = iface.List
	epairRunCommand    = utils.RunCommand
)

func (s *Service) CreateEpair(name string) error {
	output, err := epairRunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		return fmt.Errorf("failed to create epair: %w", err)
	}

	epairA := strings.TrimSpace(string(output))
	if epairA == "" {
		return fmt.Errorf("failed to get epair name")
	}

	epairB := strings.TrimSuffix(epairA, "a") + "b"

	_, err = epairRunCommand("/sbin/ifconfig", epairA, "name", name+"a")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairA, name+"a", err)
	}

	_, err = epairRunCommand("/sbin/ifconfig", epairB, "name", name+"b")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairB, name+"b", err)
	}
	for _, epair := range []string{name + "a", name + "b"} {
		if _, err = epairRunCommand("/sbin/ifconfig", epair, "group", sylveEpairGroup); err != nil {
			return fmt.Errorf("failed to mark epair %s as Sylve-managed: %w", epair, err)
		}
	}

	return nil
}

func (s *Service) DeleteEpair(name string) error {
	ifaces, err := epairInterfaceList()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	var epairA string
	for _, iface := range ifaces {
		if iface.Name != name+"a" {
			continue
		}
		if !slices.Contains(iface.Groups, sylveEpairGroup) {
			return fmt.Errorf("%w: refusing to delete unmanaged epair %s", networkServiceInterfaces.ErrEpairOwnershipConflict, name)
		}
		// The VNET transfer drops custom groups from the jail-side b interface.
		// The host-visible a side is therefore the ownership sentinel.
		epairA = iface.Name
	}

	if epairA == "" {
		return fmt.Errorf("epair %s not found", name)
	}

	_, err = epairRunCommand("/sbin/ifconfig", epairA, "destroy")

	if err != nil {
		return fmt.Errorf("failed to delete epair %s: %w", epairA, err)
	}

	return nil
}

func (s *Service) SyncEpairs(_ bool) error {
	s.epairSyncMutex.Lock()
	defer s.epairSyncMutex.Unlock()

	var jails []jailModels.Jail
	if err := s.DB.Preload("Networks").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed to find jails: %w", err)
	}

	ifaces, err := epairInterfaceList()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	activePaths := []string{}
	jls, err := epairRunCommand("/usr/sbin/jls", "path")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(jls), "\n")
		for _, line := range lines {
			path := strings.TrimSpace(line)
			if strings.Contains(path, "/sylve/jails/") {
				activePaths = append(activePaths, path)
			}
		}
	}

	interfaceByName := func(name string) *iface.Interface {
		for _, ifc := range ifaces {
			if ifc.Name == name {
				return ifc
			}
		}
		return nil
	}

	ownedPairs := make(map[string]struct{})

	for _, j := range jails {
		hash := utils.HashIntToNLetters(int(j.CTID), 5)
		jailSuffix := fmt.Sprintf("/sylve/jails/%d", j.CTID)
		isActive := false

		for _, p := range activePaths {
			if strings.HasSuffix(p, jailSuffix) {
				isActive = true
				break
			}
		}

		for _, network := range j.Networks {
			networkId := fmt.Sprintf("net%d", network.ID)
			base := hash + "_" + networkId
			ownedPairs[base] = struct{}{}

			epairA := base + "a"
			epairB := base + "b"

			if existingA := interfaceByName(epairA); existingA != nil {
				if !slices.Contains(existingA.Groups, sylveEpairGroup) {
					return fmt.Errorf("%w: refusing to adopt unmanaged epair %s", networkServiceInterfaces.ErrEpairOwnershipConflict, epairA)
				}
				if existingB := interfaceByName(epairB); existingB == nil {
					// VNET Logic: If the jail is active, the 'b' side is inside the jail
					// and will NOT appear in the host's iface.List(), we if don't skip deletion here the jail will lose its network!!
					if isActive {
						logger.L.Debug().Msgf("Jail %d is active; skipping existing VNET pair %s", j.CTID, base)
						continue
					}

					// If the jail is NOT active but 'b' is missing, it's a dirty state, how do we end up here exactly?
					logger.L.Warn().Msgf("Cleaning up orphaned epair %s for inactive jail %d", base, j.CTID)
					_ = s.DeleteEpair(base)
				} else {
					continue
				}
			} else if interfaceByName(epairB) != nil {
				return fmt.Errorf("%w: refusing to create epair %s while %s already exists", networkServiceInterfaces.ErrEpairOwnershipConflict, base, epairB)
			}

			logger.L.Debug().Msgf("Creating epair %s for jail %d", base, j.CTID)
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.ID, err)
			}

			// Refresh interface list so the next iteration sees the new 'a' side
			ifaces, _ = epairInterfaceList()
		}
	}

	for _, ifc := range ifaces {
		if !slices.Contains(ifc.Groups, sylveEpairGroup) {
			continue
		}
		m := epairRe.FindStringSubmatch(ifc.Name)
		if m == nil {
			continue
		}

		suffix := m[3]
		base := m[1] + "_net" + m[2]
		if _, owned := ownedPairs[base]; !owned {
			if suffix == "a" {
				logger.L.Debug().Msgf("Deleting unused epair %s", base)
				_ = s.DeleteEpair(base)
			}
		}
	}

	return nil
}
