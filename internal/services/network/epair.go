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

		for _, network := range j.Networks {
			existingIds = append(existingIds, network.ID)

			networkId := fmt.Sprintf("net%d", network.ID)
			base := hash + "_" + networkId

			epairA := base + "a"
			// epairB := base + "b" // we don't actually need to check B; CreateEpair will create both
			// If A side exists, assume the pair is already there/in use and do nothing
			if ifaceExists(epairA) {
				continue
			}

			logger.L.Debug().Msgf("Creating epair %s for jail %d", base, j.CTID)
			if err := s.CreateEpair(base); err != nil {
				return fmt.Errorf("failed to create epair for jail %d network %d: %w",
					j.CTID, network.ID, err)
			}

			// refresh list after creating, so ifaceExists sees new stuff
			ifaces, _ = iface.List()
		}
	}

	for _, ifc := range ifaces {
		// aaadx_net64b regex match <anystring>_net<number><a||b>
		m := epairRe.FindStringSubmatch(ifc.Name)
		if m == nil {
			continue
		}

		hash := m[1]
		netID := m[2]
		netIDNum, err := strconv.Atoi(netID)
		if err != nil {
			continue
		}

		suffix := m[3]

		base := fmt.Sprintf("%s_net%s", hash, netID)
		_ = base
		_ = suffix

		if !slices.Contains(existingIds, uint(netIDNum)) {
			logger.L.Debug().Msgf("Deleting unused epair %s", ifc.Name)
			// if err := s.DeleteEpair(base); err != nil {
			// 	return fmt.Errorf("failed to delete unused epair %s: %w", ifc.Name, err)
			// }
		}
	}

	ifaces, _ = iface.List()

	return nil
}
