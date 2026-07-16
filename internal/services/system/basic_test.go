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
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
)

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
