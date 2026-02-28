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
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
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

	jailName := utils.HashIntToNLetters(int(jail.CTID), 5)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("jail", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	now := time.Now().UTC()

	switch action {
	case "start":
		allowed, leaseErr := s.canStartProtectedJail(uint(ctId))
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
		return nil

	case "restart":
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
		return nil
	}

	return nil
}

func (s *Service) canStartProtectedJail(ctID uint) (bool, error) {
	if ctID == 0 {
		return true, nil
	}

	var policy clusterModels.ReplicationPolicy
	err := s.DB.Where("guest_type = ? AND guest_id = ? AND enabled = ?", clusterModels.ReplicationGuestTypeJail, ctID, true).
		First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		return false, err
	}

	var lease clusterModels.ReplicationLease
	if err := s.DB.Where("policy_id = ?", policy.ID).First(&lease).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}

	if time.Now().UTC().After(lease.ExpiresAt) {
		return false, nil
	}

	return strings.TrimSpace(lease.OwnerNodeID) == strings.TrimSpace(nodeID), nil
}
