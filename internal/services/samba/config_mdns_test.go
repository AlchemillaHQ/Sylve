// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

func stubGlobalConfigDependencies(t *testing.T) *int {
	t.Helper()

	originalSupportedCharsets := sambaSupportedCharsets
	sambaSupportedCharsets = func() []string { return []string{"UTF-8"} }
	t.Cleanup(func() { sambaSupportedCharsets = originalSupportedCharsets })

	originalGetInterface := sambaGetInterface
	sambaGetInterface = func(string) (*iface.Interface, error) { return nil, nil }
	t.Cleanup(func() { sambaGetInterface = originalGetInterface })

	writeCalls := 0
	originalWriteConfig := sambaWriteConfig
	sambaWriteConfig = func(*Service, context.Context, bool) error {
		writeCalls++
		return nil
	}
	t.Cleanup(func() { sambaWriteConfig = originalWriteConfig })

	return &writeCalls
}

func newAppleMdnsTestService(
	t *testing.T,
	appleExtensions bool,
	services []models.AvailableService,
) (*Service, *systemService.Service) {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&sambaModels.SambaSettings{},
	)
	if err := db.Create(&models.BasicSettings{Services: services}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaSettings{
		UnixCharset:        "UTF-8",
		Workgroup:          "WORKGROUP",
		ServerString:       "Sylve SMB Server",
		Interfaces:         "lo0",
		BindInterfacesOnly: true,
		AppleExtensions:    appleExtensions,
	}).Error; err != nil {
		t.Fatalf("failed to create samba settings: %v", err)
	}

	systemSvc := &systemService.Service{DB: db}
	sambaSvc := &Service{
		DB:                      db,
		EnsureMdnsEnabled:       systemSvc.EnsureMdnsEnabled,
		WithServiceSettingsLock: systemSvc.WithServiceSettingsLock,
	}
	return sambaSvc, systemSvc
}

func setAppleExtensions(t *testing.T, service *Service, enabled bool) error {
	t.Helper()
	return service.SetGlobalConfig(
		context.Background(),
		"UTF-8",
		"WORKGROUP",
		"Sylve SMB Server",
		"lo0",
		true,
		enabled,
	)
}

func mdnsServiceCount(t *testing.T, service *Service) int {
	t.Helper()

	var settings models.BasicSettings
	if err := service.DB.First(&settings).Error; err != nil {
		t.Fatalf("failed to load basic settings: %v", err)
	}

	count := 0
	for _, enabledService := range settings.Services {
		if enabledService == models.Mdns {
			count++
		}
	}
	return count
}

func appleExtensionsEnabled(t *testing.T, service *Service) bool {
	t.Helper()

	var settings sambaModels.SambaSettings
	if err := service.DB.First(&settings).Error; err != nil {
		t.Fatalf("failed to load samba settings: %v", err)
	}
	return settings.AppleExtensions
}

func TestSetGlobalConfigEnablesMdnsForAppleTransition(t *testing.T) {
	writeCalls := stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(t, false, []models.AvailableService{models.SambaServer})
	rebuildCalls := 0
	service.OnConfigChange = func() error {
		rebuildCalls++
		return nil
	}

	if err := setAppleExtensions(t, service, true); err != nil {
		t.Fatalf("failed to enable Apple extensions: %v", err)
	}

	if !appleExtensionsEnabled(t, service) {
		t.Fatal("Apple extensions were not enabled")
	}
	if count := mdnsServiceCount(t, service); count != 1 {
		t.Fatalf("expected mDNS to be enabled exactly once, got %d entries", count)
	}
	if *writeCalls != 1 {
		t.Fatalf("expected one Samba config write, got %d", *writeCalls)
	}
	if rebuildCalls != 1 {
		t.Fatalf("expected one mDNS rebuild after commit, got %d", rebuildCalls)
	}
}

func TestSetGlobalConfigPreservesManualMdnsDisable(t *testing.T) {
	stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(t, true, []models.AvailableService{models.SambaServer})

	if err := setAppleExtensions(t, service, true); err != nil {
		t.Fatalf("failed to save unchanged Apple extensions: %v", err)
	}

	if count := mdnsServiceCount(t, service); count != 0 {
		t.Fatalf("expected a manual mDNS disable to be preserved, got %d entries", count)
	}
}

func TestSetGlobalConfigDoesNotDisableMdnsWithAppleExtensions(t *testing.T) {
	stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(
		t,
		true,
		[]models.AvailableService{models.SambaServer, models.Mdns},
	)

	if err := setAppleExtensions(t, service, false); err != nil {
		t.Fatalf("failed to disable Apple extensions: %v", err)
	}

	if appleExtensionsEnabled(t, service) {
		t.Fatal("Apple extensions remained enabled")
	}
	if count := mdnsServiceCount(t, service); count != 1 {
		t.Fatalf("expected mDNS to remain enabled, got %d entries", count)
	}
}

func TestSetGlobalConfigRollsBackWhenMdnsEnableFails(t *testing.T) {
	writeCalls := stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(t, false, []models.AvailableService{models.SambaServer})
	service.EnsureMdnsEnabled = func(_ *gorm.DB) error {
		return errors.New("database unavailable")
	}

	if err := setAppleExtensions(t, service, true); err == nil {
		t.Fatal("expected mDNS dependency failure")
	}

	if appleExtensionsEnabled(t, service) {
		t.Fatal("Apple extensions were not rolled back")
	}
	if count := mdnsServiceCount(t, service); count != 0 {
		t.Fatalf("expected mDNS to remain disabled, got %d entries", count)
	}
	if *writeCalls != 0 {
		t.Fatalf("expected Samba config writing to be skipped, got %d calls", *writeCalls)
	}
}

func TestSetGlobalConfigRollsBackWhenConfigWriteFails(t *testing.T) {
	stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(t, false, []models.AvailableService{models.SambaServer})
	sambaWriteConfig = func(*Service, context.Context, bool) error {
		return errors.New("testparm failed")
	}

	if err := setAppleExtensions(t, service, true); err == nil {
		t.Fatal("expected Samba config write failure")
	}

	if appleExtensionsEnabled(t, service) {
		t.Fatal("Apple extensions were not rolled back")
	}
	if count := mdnsServiceCount(t, service); count != 0 {
		t.Fatalf("expected mDNS enablement to be rolled back, got %d entries", count)
	}
}

func TestSetGlobalConfigReportsMdnsRebuildFailure(t *testing.T) {
	writeCalls := stubGlobalConfigDependencies(t)
	service, _ := newAppleMdnsTestService(t, false, []models.AvailableService{models.SambaServer})
	service.OnConfigChange = func() error {
		return errors.New("responder unavailable")
	}

	if err := setAppleExtensions(t, service, true); err == nil {
		t.Fatal("expected mDNS rebuild failure")
	}

	if !appleExtensionsEnabled(t, service) {
		t.Fatal("Apple extensions should remain committed after Samba configuration succeeds")
	}
	if count := mdnsServiceCount(t, service); count != 1 {
		t.Fatalf("expected mDNS to remain enabled for a retry, got %d entries", count)
	}
	if *writeCalls != 1 {
		t.Fatalf("expected one successful Samba config write, got %d", *writeCalls)
	}
}
