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
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"gorm.io/gorm"
)

func TestShouldStartAdvancedStartupWorkersMissingLookup(t *testing.T) {
	_, _, err := shouldStartAdvancedStartupWorkers(nil)
	if err == nil {
		t.Fatalf("expected error when lookup is nil")
	}

	if !strings.Contains(err.Error(), "basic_settings_lookup_required") {
		t.Fatalf("expected missing lookup error, got: %v", err)
	}
}

func TestShouldStartAdvancedStartupWorkersNoBasicSettingsYet(t *testing.T) {
	enabled, settings, err := shouldStartAdvancedStartupWorkers(func() (models.BasicSettings, error) {
		return models.BasicSettings{}, gorm.ErrRecordNotFound
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if enabled {
		t.Fatalf("expected advanced startup workers to stay disabled when settings are missing")
	}
	if settings.Initialized || settings.Restarted {
		t.Fatalf("expected zero-value settings when basic settings are missing, got %+v", settings)
	}
}

func TestShouldStartAdvancedStartupWorkersRequiresBothFlags(t *testing.T) {
	cases := []struct {
		name     string
		settings models.BasicSettings
		enabled  bool
	}{
		{
			name: "initialized false restarted false",
			settings: models.BasicSettings{
				Initialized: false,
				Restarted:   false,
			},
			enabled: false,
		},
		{
			name: "initialized true restarted false",
			settings: models.BasicSettings{
				Initialized: true,
				Restarted:   false,
			},
			enabled: false,
		},
		{
			name: "initialized false restarted true",
			settings: models.BasicSettings{
				Initialized: false,
				Restarted:   true,
			},
			enabled: false,
		},
		{
			name: "initialized true restarted true",
			settings: models.BasicSettings{
				Initialized: true,
				Restarted:   true,
			},
			enabled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enabled, settings, err := shouldStartAdvancedStartupWorkers(func() (models.BasicSettings, error) {
				return tc.settings, nil
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if enabled != tc.enabled {
				t.Fatalf("expected enabled=%t, got %t", tc.enabled, enabled)
			}
			if settings.Initialized != tc.settings.Initialized || settings.Restarted != tc.settings.Restarted {
				t.Fatalf("expected settings %+v, got %+v", tc.settings, settings)
			}
		})
	}
}

func TestShouldStartAdvancedStartupWorkersLookupFailure(t *testing.T) {
	lookupErr := errors.New("db_timeout")

	enabled, _, err := shouldStartAdvancedStartupWorkers(func() (models.BasicSettings, error) {
		return models.BasicSettings{}, lookupErr
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if enabled {
		t.Fatalf("expected advanced startup workers disabled on lookup failure")
	}
	if !strings.Contains(err.Error(), "failed_to_fetch_basic_settings") {
		t.Fatalf("expected wrapped lookup error, got: %v", err)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("expected wrapped error %v, got %v", lookupErr, err)
	}
}
