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
	utils "github.com/alchemillahq/sylve/pkg/utils"

	iface "github.com/alchemillahq/sylve/pkg/network/iface"
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

func (s *Service) SyncEpairs() error {
	var jails []jailModels.Jail
	if err := s.DB.Preload("Networks").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed to find jails: %w", err)
	}

	ifaces, err := iface.List()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	for _, j := range jails {
		hash := utils.HashIntToNLetters(int(j.CTID), 5)

		for _, network := range j.Networks {
			networkId := fmt.Sprintf("%d", network.SwitchID)
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

			// Healthy: both sides exist
			if hasA && hasB {
				continue
			}

			// Half-broken: A exists but B is gone (common after jail kill).
			// Destroy A so we can recreate a clean pair with CreateEpair.
			if hasA && !hasB {
				if _, err := utils.RunCommand("ifconfig", epairA, "destroy"); err != nil {
					return fmt.Errorf("failed to destroy orphaned epair %s: %w", epairA, err)
				}
			}

			// Either both missing, or we just destroyed A: create fresh pair.
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.SwitchID, err)
			}

			// Refresh iface list so later iterations see the new epairs
			ifaces, _ = iface.List()
		}
	}

	return nil
}
