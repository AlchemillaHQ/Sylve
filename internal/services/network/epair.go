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
	"strconv"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	utils "github.com/alchemillahq/sylve/pkg/utils"

	"github.com/alchemillahq/sylve/pkg/network/iface"
)

var epairRe = regexp.MustCompile(`^([a-z0-9]{5})_net([0-9]+)(a|b)$`)

func (s *Service) CreateEpair(name string) error {
	output, err := utils.RunCommand("ifconfig", "epair", "create")
	if err != nil {
		return fmt.Errorf("failed to create epair: %w", err)
	}

	epairA := strings.TrimSpace(string(output))
	if epairA == "" {
		return fmt.Errorf("failed to get epair name")
	}

	epairB := strings.TrimSuffix(epairA, "a") + "b"

	_, err = utils.RunCommand("ifconfig", epairA, "name", name+"a")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairA, name+"a", err)
	}

	_, err = utils.RunCommand("ifconfig", epairB, "name", name+"b")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairB, name+"b", err)
	}

	return nil
}

func (s *Service) DeleteEpair(name string) error {
	ifaces, err := iface.List()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	var epairA string
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, name) {
			if strings.HasSuffix(iface.Name, "a") {
				epairA = iface.Name
			}
		}
	}

	if epairA == "" {
		return fmt.Errorf("epair %s not found", name)
	}

	_, err = utils.RunCommand("ifconfig", epairA, "destroy")

	if err != nil {
		return fmt.Errorf("failed to delete epair %s: %w", epairA, err)
	}

	return nil
}

func (s *Service) SyncEpairs(_ bool) error {
	var jails []jailModels.Jail
	if err := s.DB.Preload("Networks").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed to find jails: %w", err)
	}

	ifaces, err := iface.List()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	activePaths := []string{}
	jls, err := utils.RunCommand("jls", "path")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(jls), "\n")
		for _, line := range lines {
			path := strings.TrimSpace(line)
			if strings.Contains(path, "/sylve/jails/") {
				activePaths = append(activePaths, path)
			}
		}
	}

	ifaceExists := func(name string) bool {
		for _, ifc := range ifaces {
			if ifc.Name == name {
				return true
			}
		}
		return false
	}

	existingIds := []uint{}

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
			existingIds = append(existingIds, network.ID)

			networkId := fmt.Sprintf("net%d", network.ID)
			base := hash + "_" + networkId

			epairA := base + "a"
			epairB := base + "b"

			if ifaceExists(epairA) {
				if !ifaceExists(epairB) {
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
			}

			logger.L.Debug().Msgf("Creating epair %s for jail %d", base, j.CTID)
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.ID, err)
			}

			// Refresh interface list so the next iteration sees the new 'a' side
			ifaces, _ = iface.List()
		}
	}

	for _, ifc := range ifaces {
		m := epairRe.FindStringSubmatch(ifc.Name)
		if m == nil {
			continue
		}

		hash := m[1]
		netIDNum, _ := strconv.Atoi(m[2])
		suffix := m[3]

		if !slices.Contains(existingIds, uint(netIDNum)) {
			base := fmt.Sprintf("%s_net%d", hash, netIDNum)
			if suffix == "a" {
				logger.L.Debug().Msgf("Deleting unused epair %s", base)
				_ = s.DeleteEpair(base)
			}
		}
	}

	return nil
}
