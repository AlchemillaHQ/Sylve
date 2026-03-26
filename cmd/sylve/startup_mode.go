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

func shouldStartAdvancedStartupWorkers(lookup basicSettingsLookup) (bool, models.BasicSettings, error) {
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

	return settings.Initialized && settings.Restarted, settings, nil
}
