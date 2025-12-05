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
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	utils "github.com/alchemillahq/sylve/pkg/utils"

	"github.com/alchemillahq/sylve/pkg/network/iface"
)

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

func (s *Service) SyncEpairs(forceStart bool) error {
	var jails []jailModels.Jail
	if err := s.DB.Preload("Networks").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed to find jails: %w", err)
	}

	ifaces, err := iface.List()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	runningJails := make(map[uint]bool)
	output, err := utils.RunCommand("jls", "--libxo", "json")
	if err != nil {
		logger.L.Warn().Msgf("Failed to get running jails list: %v", err)
	} else {
		jlsOutput := string(output)
		for _, j := range jails {
			jailPath := fmt.Sprintf("/sylve/jails/%d", j.CTID)
			runningJails[j.CTID] = strings.Contains(jlsOutput, jailPath)
		}
	}

	for _, j := range jails {
		hash := utils.HashIntToNLetters(int(j.CTID), 5)
		isRunning := runningJails[j.CTID]

		for _, network := range j.Networks {
			networkId := fmt.Sprintf("net%d", network.ID)
			base := hash + "_" + networkId

			epairA := base + "a"
			epairB := base + "b"

			hasA, hasB := false, false
			for _, ifc := range ifaces {
				switch ifc.Name {
				case epairA:
					hasA = true
				case epairB:
					hasB = true
				}
			}

			shouldCreateEpairs := isRunning || forceStart

			if !shouldCreateEpairs {
				if hasA {
					logger.L.Debug().Msgf("Cleaning up epair %s for non-running jail %d", epairA, j.CTID)
					_, _ = utils.RunCommand("ifconfig", epairA, "destroy")
				}
				if hasB {
					logger.L.Debug().Msgf("Cleaning up epair %s for non-running jail %d", epairB, j.CTID)
					_, _ = utils.RunCommand("ifconfig", epairB, "destroy")
				}
				continue
			}

			if isRunning {
				// For running jails, only check if 'a' side exists (b side is in jail namespace)
				if hasA {
					continue // Epair exists and jail is running, don't touch it
				}
				// Missing epair for running jail - this shouldn't happen normally
				logger.L.Warn().Msgf("Running jail %d missing epair %s - not recreating to avoid disruption", j.CTID, epairA)
				continue
			}

			// Jail not running but force flag is set - ensure complete epair pair exists
			if hasA && hasB {
				continue
			}

			// Clean up partial epairs for non-running jails
			if hasA {
				logger.L.Debug().Msgf("Destroying partial epair %s for non-running jail %d", epairA, j.CTID)
				_, _ = utils.RunCommand("ifconfig", epairA, "destroy")
			}
			if hasB {
				logger.L.Debug().Msgf("Destroying partial epair %s for non-running jail %d", epairB, j.CTID)
				_, _ = utils.RunCommand("ifconfig", epairB, "destroy")
			}

			logger.L.Debug().Msgf("Creating epair %s for jail %d", base, j.CTID)
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.ID, err)
			}

			// Refresh interface list after creating epairs, to avoid stale data
			ifaces, _ = iface.List()
		}
	}

	return nil
}
