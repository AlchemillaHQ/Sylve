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

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetJailLogs(id uint) (string, error) {
	var jail jailModels.Jail

	if err := s.DB.First(&jail, "id = ?", id).Error; err != nil {
		return "", fmt.Errorf("failed to find jail with id %d: %w", id, err)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed to get jails path: %w", err)
	}

	logFilePath := fmt.Sprintf("%s/%d/%d.log", jailsPath, jail.CTID, jail.CTID)
	logs, err := utils.ReadFile(logFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read jail logs: %w", err)
	}

	return string(logs), nil
}
