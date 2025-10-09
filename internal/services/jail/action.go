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
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
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

	jailName := utils.HashIntToNLetters(jail.CTID, 5)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("jail", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	now := time.Now().UTC()

	switch action {
	case "start":
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
