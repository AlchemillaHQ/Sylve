// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

func newTunablesTestService(t *testing.T, tunables []sysctl.Tunable, stored []models.SystemTunable) *Service {
	t.Helper()

	db := testutil.NewSQLiteTestDB(t, &models.SystemTunable{})
	if len(stored) > 0 {
		if err := db.Create(&stored).Error; err != nil {
			t.Fatalf("failed to create stored tunables: %v", err)
		}
	}

	byName := make(map[string]sysctl.Tunable, len(tunables))
	for _, tunable := range tunables {
		byName[tunable.Name] = tunable
	}

	return &Service{
		DB:          db,
		tunCache:    tunables,
		tunCachedAt: time.Now(),
		tunDescribe: func(name string) (sysctl.Tunable, bool, error) {
			tunable, found := byName[name]
			return tunable, found, nil
		},
	}
}

func TestListTunablesPaginatedConfiguredOnly(t *testing.T) {
	service := newTunablesTestService(t, []sysctl.Tunable{
		{Name: "kern.alpha", Value: "runtime-alpha", Type: "string", Writable: true},
		{Name: "kern.beta", Value: "same-value", Type: "string", Writable: true},
		{Name: "kern.empty", Value: "runtime-empty", Type: "string", Writable: true},
	}, []models.SystemTunable{
		{Name: "kern.alpha", Value: "configured-alpha"},
		{Name: "kern.beta", Value: "same-value"},
		{Name: "kern.empty", Value: ""},
		{Name: "kern.stale", Value: "configured-stale"},
	})

	all, err := service.ListTunablesPaginated(1, 25, "name", "asc", "", false)
	if err != nil {
		t.Fatalf("listing all tunables failed: %v", err)
	}
	if len(all.Data) != 3 {
		t.Fatalf("expected all 3 current tunables, got %d", len(all.Data))
	}
	if all.Data[0].Value != "configured-alpha" || all.Data[2].Value != "" {
		t.Fatalf("expected stored values to overlay runtime values, got %+v", all.Data)
	}

	configured, err := service.ListTunablesPaginated(1, 25, "name", "asc", "", true)
	if err != nil {
		t.Fatalf("listing configured tunables failed: %v", err)
	}
	if len(configured.Data) != 3 {
		t.Fatalf("expected 3 configured current tunables, got %d", len(configured.Data))
	}
	if configured.Data[1].Name != "kern.beta" || configured.Data[2].Name != "kern.empty" {
		t.Fatalf("expected same-value and empty-value tunables to be included, got %+v", configured.Data)
	}
}

func TestListTunablesPaginatedFiltersBeforePagination(t *testing.T) {
	service := newTunablesTestService(t, []sysctl.Tunable{
		{Name: "kern.alpha", Value: "1"},
		{Name: "kern.beta", Value: "2"},
		{Name: "kern.delta", Value: "3"},
		{Name: "kern.gamma", Value: "4"},
	}, []models.SystemTunable{
		{Name: "kern.alpha", Value: "10"},
		{Name: "kern.delta", Value: "30"},
		{Name: "kern.gamma", Value: "40"},
	})

	response, err := service.ListTunablesPaginated(1, 2, "name", "desc", "a", true)
	if err != nil {
		t.Fatalf("listing filtered tunables failed: %v", err)
	}
	if response.LastPage != 2 {
		t.Fatalf("expected 2 pages after filtering, got %d", response.LastPage)
	}
	if len(response.Data) != 2 || response.Data[0].Name != "kern.gamma" || response.Data[1].Name != "kern.delta" {
		t.Fatalf("unexpected first page: %+v", response.Data)
	}

	empty, err := service.ListTunablesPaginated(1, 2, "name", "asc", "missing", true)
	if err != nil {
		t.Fatalf("listing empty filtered result failed: %v", err)
	}
	if empty.LastPage != 1 || len(empty.Data) != 0 {
		t.Fatalf("expected an empty result with last_page 1, got %+v", empty)
	}
}

func TestListTunablesPaginatedConfiguredOnlyDoesNotWalkMIB(t *testing.T) {
	service := newTunablesTestService(t, nil, []models.SystemTunable{
		{Name: "kern.alpha", Value: "configured-alpha"},
	})
	service.tunList = func() ([]sysctl.Tunable, error) {
		t.Fatal("configured-only listing called the bulk MIB walker")
		return nil, nil
	}
	service.tunDescribe = func(name string) (sysctl.Tunable, bool, error) {
		return sysctl.Tunable{Name: name, Type: "string", Writable: true}, true, nil
	}

	response, err := service.ListTunablesPaginated(1, 25, "name", "asc", "", true)
	if err != nil {
		t.Fatalf("listing configured tunables failed: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].Value != "configured-alpha" {
		t.Fatalf("unexpected configured-only response: %+v", response.Data)
	}
}

func TestListTunablesPaginatedClampsOutOfRangePage(t *testing.T) {
	service := newTunablesTestService(t, []sysctl.Tunable{
		{Name: "kern.alpha", Value: "1"},
	}, nil)

	response, err := service.ListTunablesPaginated(int(^uint(0)>>1), 100, "name", "asc", "", false)
	if err != nil {
		t.Fatalf("listing an out-of-range page failed: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].Name != "kern.alpha" {
		t.Fatalf("expected the out-of-range page to clamp to the last page, got %+v", response.Data)
	}
}
