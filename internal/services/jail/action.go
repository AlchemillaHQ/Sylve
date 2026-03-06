// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) JailAction(ctId int, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed to get jails path: %w", err)
	}

	jailConf := fmt.Sprintf("%s/%d/%d.conf", jailsPath, ctId, ctId)

	var jail jailModels.Jail
	if err := s.DB.First(&jail, "ct_id = ?", ctId).Error; err != nil {
		return fmt.Errorf("failed to find jail with ct_id %d: %w", ctId, err)
	}

	jailName := s.GetCTIDHash(jail.CTID)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("jail", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	now := time.Now().UTC()

	switch action {
	case "start":
		allowed, leaseErr := s.canMutateProtectedJail(uint(ctId))
		if leaseErr != nil {
			return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
		}
		if !allowed {
			return fmt.Errorf("replication_lease_not_owned")
		}

		active, err := s.IsJailActive(uint(ctId))
		if err != nil {
			return fmt.Errorf("failed to check if jail is active: %w", err)
		}

		if active {
			return nil
		}

		err = s.NetworkService.SyncEpairs(true)
		if err != nil {
			return fmt.Errorf("failed to sync epairs: %w", err)
		}

		if out, err := run("-f", jailConf, "-c", jailName); err != nil {
			return fmt.Errorf("failed to start jail %s: %v\n%s", jailName, err, out)
		}
		jail.StartedAt = &now
		jail.StoppedAt = nil
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		s.emitLeftPanelRefresh(fmt.Sprintf("jail_start_%d", ctId))
		return nil

	case "stop":
		if out, err := run("-f", jailConf, "-r", jailName); err != nil {
			if !strings.Contains(out, "not found") && !strings.Contains(out, "No such process") {
				return fmt.Errorf("failed to stop jail %s: %v\n%s", jailName, err, out)
			}
		}
		jail.StoppedAt = &now
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		s.emitLeftPanelRefresh(fmt.Sprintf("jail_stop_%d", ctId))
		return nil

	case "restart":
		allowed, leaseErr := s.canMutateProtectedJail(uint(ctId))
		if leaseErr != nil {
			return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
		}
		if !allowed {
			return fmt.Errorf("replication_lease_not_owned")
		}

		if out, err := run("-f", jailConf, "-r", jailName); err != nil {
			if !strings.Contains(out, "not found") && !strings.Contains(out, "No such process") {
				return fmt.Errorf("failed to stop jail %s: %v\n%s", jailName, err, out)
			}
		}

		if out, err := run("-f", jailConf, "-c", jailName); err != nil {
			return fmt.Errorf("failed to start jail %s: %v\n%s", jailName, err, out)
		}
		jail.StartedAt = &now
		jail.StoppedAt = nil
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		s.emitLeftPanelRefresh(fmt.Sprintf("jail_restart_%d", ctId))
		return nil
	}

	return nil
}

func (s *Service) canMutateProtectedJail(ctID uint) (bool, error) {
	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		return false, err
	}
	return clusterService.CanNodeMutateProtectedGuest(
		s.DB,
		clusterModels.ReplicationGuestTypeJail,
		ctID,
		strings.TrimSpace(nodeID),
	)
}

func (s *Service) CanMutateProtectedJail(ctID uint) (bool, error) {
	return s.canMutateProtectedJail(ctID)
}

func (s *Service) canStartProtectedJail(ctID uint) (bool, error) {
	return s.canMutateProtectedJail(ctID)
}
