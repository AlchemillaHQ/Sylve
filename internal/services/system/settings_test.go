// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func hasService(services []models.AvailableService, wanted models.AvailableService) bool {
	for _, service := range services {
		if service == wanted {
			return true
		}
	}

	return false
}

func TestEnsureMdnsEnabledIsIdempotent(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := db.Create(&models.BasicSettings{
		Services: []models.AvailableService{models.SambaServer},
	}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	service := &Service{DB: db}
	if err := service.WithServiceSettingsLock(func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := service.EnsureMdnsEnabled(tx); err != nil {
				return err
			}
			return service.EnsureMdnsEnabled(tx)
		})
	}); err != nil {
		t.Fatalf("failed to ensure mDNS is enabled: %v", err)
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}

	mdnsCount := 0
	for _, enabledService := range current.Services {
		if enabledService == models.Mdns {
			mdnsCount++
		}
	}
	if mdnsCount != 1 {
		t.Fatalf("expected one mDNS service entry, got %d", mdnsCount)
	}
}

func TestServiceToggleMdnsRebuildsAfterPersistingState(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	var rebuildStates []bool
	service := &Service{
		DB: db,
		MdnsRebuild: func() error {
			var current models.BasicSettings
			if err := db.First(&current).Error; err != nil {
				return err
			}
			rebuildStates = append(rebuildStates, hasService(current.Services, models.Mdns))
			return nil
		},
	}

	if err := service.ServiceToggle(models.Mdns); err != nil {
		t.Fatalf("enabling mdns failed: %v", err)
	}
	if err := service.ServiceToggle(models.Mdns); err != nil {
		t.Fatalf("disabling mdns failed: %v", err)
	}

	if len(rebuildStates) != 2 || !rebuildStates[0] || rebuildStates[1] {
		t.Fatalf("expected rebuilds after enable and disable persistence, got %v", rebuildStates)
	}
}

func TestServiceToggleMdnsRestoresStateAfterRebuildFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	settings := models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}

	calls := 0
	var rebuildStates []bool
	service := &Service{
		DB: db,
		MdnsRebuild: func() error {
			var current models.BasicSettings
			if err := db.First(&current).Error; err != nil {
				return err
			}
			rebuildStates = append(rebuildStates, hasService(current.Services, models.Mdns))
			calls++
			if calls == 1 {
				return errors.New("responder unavailable")
			}
			return nil
		},
	}

	if err := service.ServiceToggle(models.Mdns); err == nil {
		t.Fatal("expected mdns rebuild failure")
	}

	var current models.BasicSettings
	if err := db.First(&current).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}
	if hasService(current.Services, models.Mdns) {
		t.Fatalf("mdns remained enabled after failed rebuild: %v", current.Services)
	}
	if len(rebuildStates) != 2 || !rebuildStates[0] || rebuildStates[1] {
		t.Fatalf("expected failed enable then restored disabled state, got %v", rebuildStates)
	}
}
