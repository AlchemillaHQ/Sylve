// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestNormalizeInitializeRequestAllowsEmptyConfiguration(t *testing.T) {
	normalized, errs := normalizeInitializeRequest(systemServiceInterfaces.InitializeRequest{})
	if len(errs) != 0 {
		t.Fatalf("expected empty configuration to be valid, got %v", errs)
	}
	if len(normalized.Pools) != 0 {
		t.Fatalf("expected no pools, got %v", normalized.Pools)
	}
	if len(normalized.Services) != 0 {
		t.Fatalf("expected no services, got %v", normalized.Services)
	}
}

func TestNormalizeInitializeRequestAcceptsAllServices(t *testing.T) {
	services := []models.AvailableService{
		models.Virtualization,
		models.Jails,
		models.DHCPServer,
		models.SambaServer,
		models.WoLServer,
		models.Firewall,
		models.WireGuard,
		models.ISCSI,
		models.Mdns,
	}

	normalized, errs := normalizeInitializeRequest(systemServiceInterfaces.InitializeRequest{
		Pools:    []string{" tank ", "", "tank", "zroot"},
		Services: services,
	})
	if len(errs) != 0 {
		t.Fatalf("expected all supported services to be valid, got %v", errs)
	}
	if !slices.Equal(normalized.Pools, []string{"tank", "zroot"}) {
		t.Fatalf("expected normalized pools, got %v", normalized.Pools)
	}
	if !slices.Equal(normalized.Services, services) {
		t.Fatalf("expected services to be preserved, got %v", normalized.Services)
	}
}

func TestNormalizeInitializeRequestRejectsDuplicateAndUnknownServices(t *testing.T) {
	normalized, errs := normalizeInitializeRequest(systemServiceInterfaces.InitializeRequest{
		Services: []models.AvailableService{models.Mdns, models.Mdns, "unknown"},
	})
	if len(errs) != 2 {
		t.Fatalf("expected duplicate and unknown service errors, got %v", errs)
	}
	if errs[0].Error() != "duplicate_service_mdns" {
		t.Fatalf("unexpected duplicate service error: %v", errs[0])
	}
	if errs[1].Error() != "unsupported_service_unknown" {
		t.Fatalf("unexpected unknown service error: %v", errs[1])
	}
	if !slices.Equal(normalized.Services, []models.AvailableService{models.Mdns}) {
		t.Fatalf("expected only the first valid service, got %v", normalized.Services)
	}
}

func TestInitializeSerializesConcurrentRequests(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{})
	service := &Service{DB: db}
	request := systemServiceInterfaces.InitializeRequest{
		Pools:    []string{},
		Services: []models.AvailableService{},
	}

	const attempts = 8
	start := make(chan struct{})
	results := make(chan []error, attempts)

	for range attempts {
		go func() {
			<-start
			results <- service.Initialize(context.Background(), request)
		}()
	}

	close(start)

	successes := 0
	alreadyInitialized := 0
	for range attempts {
		errs := <-results
		if len(errs) == 0 {
			successes++
			continue
		}
		if len(errs) == 1 && errs[0].Error() == "system_already_initialized" {
			alreadyInitialized++
			continue
		}
		t.Fatalf("unexpected initialization errors: %v", errs)
	}

	if successes != 1 {
		t.Fatalf("expected one successful initialization, got %d", successes)
	}
	if alreadyInitialized != attempts-1 {
		t.Fatalf("expected %d already-initialized responses, got %d", attempts-1, alreadyInitialized)
	}

	var settings []models.BasicSettings
	if err := db.Find(&settings).Error; err != nil {
		t.Fatalf("failed to read basic settings: %v", err)
	}
	if len(settings) != 1 {
		t.Fatalf("expected one basic settings row, got %d", len(settings))
	}
	if settings[0].ID != 1 {
		t.Fatalf("expected canonical basic settings ID 1, got %d", settings[0].ID)
	}
}
