// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type testProvider struct {
	id        string
	validated bool
	upserts   []string
}

func (p *testProvider) ID() string {
	if p.id != "" {
		return p.id
	}
	return "test"
}

func (p *testProvider) Validate(_ context.Context, _ string, _ string, _ string, settings map[string]string) (map[string]string, error) {
	p.validated = true
	return cloneSettings(settings), nil
}

func (p *testProvider) Upsert(_ context.Context, _ string, _ map[string]string, _ string, recordType string, address netip.Addr) error {
	p.upserts = append(p.upserts, recordType+":"+address.String())
	return nil
}

type testResolver struct {
	addresses AddressSet
	err       error
}

func (testResolver) Type() string {
	return "test"
}

func (r testResolver) Resolve(context.Context, map[string]string) (AddressSet, error) {
	return r.addresses, r.err
}

func TestSyncEntryRecordsPartialFamilyResult(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testProvider{}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			"test": testResolver{addresses: AddressSet{IPv4: netip.MustParseAddr("203.0.113.9")}},
		},
		now:         func() time.Time { return now },
		syncTimeout: time.Second,
	}

	entry := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        "test",
		ProviderSecret:  "provider-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeBoth,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      "test",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("syncing entry failed: %v", err)
	}
	if view.LastStatus != "partial" || view.IPv4Status != "success" || view.IPv6Status != "error" {
		t.Fatalf("unexpected partial sync state: %#v", view)
	}
	if view.LastIPv4 != "203.0.113.9" || view.IPv6Error == "" {
		t.Fatalf("unexpected family sync result: %#v", view)
	}
	if len(provider.upserts) != 1 || provider.upserts[0] != "A:203.0.113.9" {
		t.Fatalf("unexpected provider updates: %#v", provider.upserts)
	}
	if view.LastSuccessAt == nil || !view.LastSuccessAt.Equal(now) {
		t.Fatalf("expected partial sync to record a successful family timestamp, got %#v", view.LastSuccessAt)
	}

	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("failed to marshal entry view: %v", err)
	}
	if string(payload) == "" || strings.Contains(string(payload), "providerSecret") || strings.Contains(string(payload), "provider-secret") {
		t.Fatalf("entry view exposed the provider credential: %s", payload)
	}
}

func TestCreateEntryRequiresMatchingManualAddress(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testProvider{}
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	_, err := service.CreateEntry(context.Background(), EntryInput{
		Enabled:    true,
		Provider:   "test",
		Token:      "provider-secret",
		Hostname:   "router.example.com",
		RecordType: dynamicDNSModels.RecordTypeAAAA,
		SourceType: dynamicDNSModels.SourceTypeManual,
		SourceSettings: map[string]string{
			SourceSettingIPv4: "203.0.113.9",
		},
	})
	if err == nil || !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected manual AAAA validation error, got %v", err)
	}
	if provider.validated {
		t.Fatal("provider validation should not run for an invalid source")
	}
}

func TestUpdateEntryPreservesConfiguredCredential(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testProvider{}
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	existing := dynamicDNSModels.Entry{
		Enabled:          true,
		Provider:         "test",
		ProviderSettings: map[string]string{"zoneId": "zone-id"},
		ProviderSecret:   "stored-secret",
		Hostname:         "router.example.com",
		RecordType:       dynamicDNSModels.RecordTypeA,
		IntervalMinutes:  DefaultIntervalMinutes,
		SourceType:       dynamicDNSModels.SourceTypeManual,
		SourceSettings:   map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:          false,
		Provider:         "test",
		ProviderSettings: map[string]string{"zoneId": "zone-id"},
		Hostname:         "router.example.com",
		RecordType:       dynamicDNSModels.RecordTypeA,
		IntervalMinutes:  15,
		SourceType:       dynamicDNSModels.SourceTypeManual,
		SourceSettings:   map[string]string{SourceSettingIPv4: "203.0.113.10"},
	})
	if err != nil {
		t.Fatalf("updating entry failed: %v", err)
	}
	if !view.CredentialConfigured || provider.validated {
		t.Fatalf("expected the stored credential to be retained without revalidation, got %#v", view)
	}

	var updated dynamicDNSModels.Entry
	if err := database.First(&updated, existing.ID).Error; err != nil {
		t.Fatalf("failed to reload updated entry: %v", err)
	}
	if updated.ProviderSecret != "stored-secret" || updated.Enabled || updated.SourceSettings[SourceSettingIPv4] != "203.0.113.10" {
		t.Fatalf("unexpected persisted update: %#v", updated)
	}
}

func TestUpdateEntryRequiresCredentialWhenProviderChanges(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	service := &Service{
		DB: database,
		providers: map[string]DNSProvider{
			"first":  &testProvider{id: "first"},
			"second": &testProvider{id: "second"},
		},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	existing := dynamicDNSModels.Entry{
		Enabled:          true,
		Provider:         "first",
		ProviderSettings: map[string]string{"first": "setting"},
		ProviderSecret:   "first-secret",
		Hostname:         "router.example.com",
		RecordType:       dynamicDNSModels.RecordTypeA,
		IntervalMinutes:  DefaultIntervalMinutes,
		SourceType:       dynamicDNSModels.SourceTypeManual,
		SourceSettings:   map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	_, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:    true,
		Provider:   "second",
		Hostname:   "router.example.com",
		RecordType: dynamicDNSModels.RecordTypeA,
		SourceType: dynamicDNSModels.SourceTypeManual,
		SourceSettings: map[string]string{
			SourceSettingIPv4: "203.0.113.9",
		},
	})
	if err == nil || !errors.Is(err, ErrInvalidEntry) || !strings.Contains(err.Error(), "provider credential is required") {
		t.Fatalf("expected a provider credential error, got %v", err)
	}
}

func TestCreateEntryAcceptsOneMinuteInterval(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": &testProvider{}},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	entry, err := service.CreateEntry(context.Background(), EntryInput{
		Enabled:         true,
		Provider:        "test",
		Token:           "provider-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: 1,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings: map[string]string{
			SourceSettingIPv4: "203.0.113.9",
		},
	})
	if err != nil {
		t.Fatalf("creating a one-minute entry failed: %v", err)
	}
	if entry.IntervalMinutes != 1 {
		t.Fatalf("expected a one-minute interval, got %d", entry.IntervalMinutes)
	}
}
