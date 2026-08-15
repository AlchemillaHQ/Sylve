// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"errors"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"

	"gorm.io/gorm"
)

type basicSettingsLookup func() (models.BasicSettings, error)

func shouldStartOperationalRuntime(lookup basicSettingsLookup) (bool, models.BasicSettings, error) {
	if lookup == nil {
		return false, models.BasicSettings{}, fmt.Errorf("basic_settings_lookup_required")
	}

	settings, err := lookup()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, models.BasicSettings{}, nil
		}

		return false, models.BasicSettings{}, fmt.Errorf("failed_to_fetch_basic_settings: %w", err)
	}

	return settings.Initialized, settings, nil
}

func markOperationalStartupComplete(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("database_required")
	}

	result := database.Model(&models.BasicSettings{}).
		Where("id = ? AND initialized = ?", 1, true).
		Update("restarted", true)
	if result.Error != nil {
		return fmt.Errorf("failed_to_mark_sylve_restarted: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("initialized_basic_settings_not_found")
	}

	return nil
}
