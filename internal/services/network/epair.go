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

			isRunning := j.StartedAt != nil && (j.StoppedAt == nil || j.StartedAt.After(*j.StoppedAt))
			if isRunning {
				if hasA {
					continue
				}

				// A is missing while jail is running; log and skip, don't auto-destruct/recreate.
				logger.L.Warn().Msgf("jail %d is running but epair %s is missing on host; not recreating automatically", j.CTID, epairA)
				continue
			}

			// Jail is stopped: now it is safe to "repair" things.

			// Both ends present? Good.
			if hasA && hasB {
				continue
			}

			// Exactly one side present (half-broken) → destroy whatever exists.
			if hasA {
				_, _ = utils.RunCommand("ifconfig", epairA, "destroy")
			}
			if hasB {
				_, _ = utils.RunCommand("ifconfig", epairB, "destroy")
			}

			// Now create a fresh, clean pair.
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.SwitchID, err)
			}

			ifaces, _ = iface.List()
		}
	}

	return nil
}
