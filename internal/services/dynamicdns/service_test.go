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

	"github.com/alchemillahq/sylve/internal/db/models"
	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type testProvider struct {
	id                  string
	validated           bool
	validatedRecordType string
	upserts             []string
	upsertErr           error
}

func (p *testProvider) ID() string {
	if p.id != "" {
		return p.id
	}
	return "test"
}

func (p *testProvider) Validate(_ context.Context, _ string, _ string, recordType string, settings map[string]string) (map[string]string, error) {
	p.validated = true
	p.validatedRecordType = recordType
	return cloneSettings(settings), nil
}

func (p *testProvider) Upsert(_ context.Context, _ string, _ map[string]string, _ string, recordType string, address netip.Addr) error {
	p.upserts = append(p.upserts, recordType+":"+address.String())
	return p.upsertErr
}

type testStatusProvider struct {
	testProvider
	matches          bool
	matchErr         error
	checks           int
	checkedAddresses []string
}

func (p *testStatusProvider) AddressMatches(_ context.Context, _ string, _ map[string]string, _ string, recordType string, address netip.Addr) (bool, error) {
	p.checks++
	p.checkedAddresses = append(p.checkedAddresses, recordType+":"+address.String())
	return p.matches, p.matchErr
}

type testResolver struct {
	addresses AddressSet
	err       error
}

func TestManagedCertificateProtectsDynamicDNSEntryIdentity(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{}, &models.Certificate{})
	provider := &testProvider{id: dynamicDNSModels.ProviderSylve}
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{dynamicDNSModels.ProviderSylve: provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}
	entry := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        dynamicDNSModels.ProviderSylve,
		ProviderSecret:  "stored-token",
		Hostname:        "node.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("create Dynamic DNS entry: %v", err)
	}
	entryID := entry.ID
	if err := database.Create(&models.Certificate{
		Name:              "Managed",
		Type:              models.CertificateTypeSylveManaged,
		Domain:            entry.Hostname,
		DynamicDNSEntryID: &entryID,
	}).Error; err != nil {
		t.Fatalf("create managed certificate reference: %v", err)
	}

	_, err := service.UpdateEntry(context.Background(), entry.ID, EntryInput{
		Enabled:         true,
		Provider:        dynamicDNSModels.ProviderSylve,
		Token:           "replacement-token",
		Hostname:        "renamed.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.10"},
	})
	if !errors.Is(err, ErrEntryInUse) {
		t.Fatalf("expected managed hostname update conflict, got %v", err)
	}
	if err := service.DeleteEntry(context.Background(), entry.ID); !errors.Is(err, ErrEntryInUse) {
		t.Fatalf("expected managed Dynamic DNS deletion conflict, got %v", err)
	}

	view, err := service.UpdateEntry(context.Background(), entry.ID, EntryInput{
		Enabled:         false,
		Provider:        dynamicDNSModels.ProviderSylve,
		Token:           "replacement-token",
		Hostname:        entry.Hostname,
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: 15,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.10"},
	})
	if err != nil {
		t.Fatalf("update referenced Dynamic DNS settings and token: %v", err)
	}
	if view.Enabled || view.IntervalMinutes != 15 {
		t.Fatalf("ordinary referenced Dynamic DNS settings were not updated: %#v", view)
	}
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

func TestUpdateEntryRevalidatesRecordType(t *testing.T) {
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
		Enabled:         true,
		Provider:        "test",
		ProviderSecret:  "stored-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	_, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:         true,
		Provider:        "test",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeAAAA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv6: "2001:db8::9"},
	})
	if err != nil {
		t.Fatalf("updating entry record type failed: %v", err)
	}
	if !provider.validated || provider.validatedRecordType != dynamicDNSModels.RecordTypeAAAA {
		t.Fatalf("record type update was not revalidated: %#v", provider)
	}
}

func TestUpdateEntryRevalidatesWhenReenabled(t *testing.T) {
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
		Enabled:         false,
		Provider:        "test",
		ProviderSecret:  "stored-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}
	if err := database.Model(&existing).Update("enabled", false).Error; err != nil {
		t.Fatalf("failed to disable test entry: %v", err)
	}

	_, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:         true,
		Provider:        "test",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	})
	if err != nil {
		t.Fatalf("reenabling entry failed: %v", err)
	}
	if !provider.validated {
		t.Fatal("reenabled entry was not revalidated")
	}
}

func TestUpdateEntryPreservesRetryStateForSameProviderIdentity(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testProvider{}
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	nextRetry := now.Add(10 * time.Minute)
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         func() time.Time { return now },
		syncTimeout: time.Second,
	}

	existing := dynamicDNSModels.Entry{
		Enabled:             true,
		Provider:            "test",
		ProviderSecret:      "stored-secret",
		Hostname:            "router.example.com",
		RecordType:          dynamicDNSModels.RecordTypeA,
		IntervalMinutes:     DefaultIntervalMinutes,
		SourceType:          dynamicDNSModels.SourceTypeManual,
		SourceSettings:      map[string]string{SourceSettingIPv4: "203.0.113.9"},
		LastStatus:          syncStatusError,
		IPv4Status:          syncStatusError,
		LastIPv4:            "203.0.113.9",
		LastPublishedIPv4:   "203.0.113.8",
		LastSyncAt:          &now,
		ConsecutiveFailures: 2,
		NextRetryAt:         &nextRetry,
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:         true,
		Provider:        "test",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: 30,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.10"},
	})
	if err != nil {
		t.Fatalf("updating retrying entry failed: %v", err)
	}
	if view.IntervalMinutes != 30 || view.SourceSettings[SourceSettingIPv4] != "203.0.113.10" {
		t.Fatalf("configuration update was not applied: %#v", view)
	}
	if view.LastStatus != syncStatusError || view.ConsecutiveFailures != 2 || view.NextRetryAt == nil || !view.NextRetryAt.Equal(nextRetry) || view.LastPublishedIPv4 != "203.0.113.8" {
		t.Fatalf("configuration update erased retry state: %#v", view)
	}
	if provider.validated {
		t.Fatal("source and interval update unexpectedly revalidated provider identity")
	}
}

func TestUpdateEntryRejectsIdentityChangeWhilePending(t *testing.T) {
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
		Enabled:         true,
		Provider:        "test",
		ProviderSecret:  "stored-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
		LastStatus:      syncStatusPending,
		IPv4Status:      syncStatusPending,
		LastIPv4:        "203.0.113.9",
	}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	_, err := service.UpdateEntry(context.Background(), existing.ID, EntryInput{
		Enabled:         true,
		Provider:        "test",
		Hostname:        "other.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	})
	if err == nil || !errors.Is(err, ErrInvalidEntry) || !strings.Contains(err.Error(), "publication is pending") {
		t.Fatalf("expected pending identity change rejection, got %v", err)
	}
}

func TestSyncEntrySkipsPublishedAddressForStatusProvider(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testStatusProvider{}
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
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
		Enabled:           true,
		Provider:          "test",
		ProviderSecret:    "provider-secret",
		Hostname:          "router.example.com",
		RecordType:        dynamicDNSModels.RecordTypeA,
		IntervalMinutes:   DefaultIntervalMinutes,
		SourceType:        "test",
		LastPublishedIPv4: "203.0.113.9",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("syncing unchanged entry failed: %v", err)
	}
	if view.LastStatus != syncStatusSuccess || provider.checks != 0 || len(provider.upserts) != 0 {
		t.Fatalf("unchanged address contacted provider: view=%#v checks=%d upserts=%#v", view, provider.checks, provider.upserts)
	}
}

func TestSyncEntryPersistsTransientRetrySchedule(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testStatusProvider{}
	provider.upsertErr = newProviderError(providerErrorTransient, 10*time.Second, errors.New("temporarily unavailable"))
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
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
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      "test",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("syncing transient failure failed: %v", err)
	}
	if view.LastStatus != syncStatusError || !view.Enabled || view.ConsecutiveFailures != 1 || view.NextRetryAt == nil || !view.NextRetryAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("unexpected transient retry state: %#v", view)
	}
	if len(provider.upserts) != 1 {
		t.Fatalf("expected one provider update, got %#v", provider.upserts)
	}

	if _, err := service.SyncEntry(context.Background(), entry.ID); err != nil {
		t.Fatalf("checking retry gate failed: %v", err)
	}
	if len(provider.upserts) != 1 {
		t.Fatalf("retry gate allowed an early update: %#v", provider.upserts)
	}

	now = now.Add(10 * time.Second)
	provider.upsertErr = newProviderError(providerErrorTransient, 0, errors.New("still unavailable"))
	view, err = service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("running second transient attempt failed: %v", err)
	}
	if view.ConsecutiveFailures != 2 || view.NextRetryAt == nil || !view.NextRetryAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("unexpected exponential retry state: %#v", view)
	}
}

func TestSyncEntryAutoDisablesPermanentProviderFailure(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testStatusProvider{}
	provider.upsertErr = newProviderError(providerErrorPermanent, 0, errors.New("revoked provider-secret"))
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			"test": testResolver{addresses: AddressSet{
				IPv4: netip.MustParseAddr("203.0.113.9"),
				IPv6: netip.MustParseAddr("2001:db8::9"),
			}},
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
		t.Fatalf("syncing permanent failure failed: %v", err)
	}
	if view.Enabled || view.LastStatus != syncStatusError || view.IPv6Status != syncStatusDeferred || view.NextRetryAt != nil {
		t.Fatalf("unexpected permanent failure state: %#v", view)
	}
	if len(provider.upserts) != 1 {
		t.Fatalf("permanent failure did not stop remaining families: %#v", provider.upserts)
	}
	if strings.Contains(view.LastError, "provider-secret") || !strings.Contains(view.LastError, "[REDACTED]") {
		t.Fatalf("permanent failure exposed provider secret: %q", view.LastError)
	}
}

func TestSyncEntryConfirmsPendingWriteWithoutResubmitting(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testStatusProvider{}
	provider.upsertErr = newProviderError(providerErrorPending, 0, errors.New("publication delayed"))
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
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
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      "test",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("syncing delayed publication failed: %v", err)
	}
	if view.LastStatus != syncStatusPending || view.NextRetryAt == nil || !view.NextRetryAt.Equal(now.Add(time.Minute)) || view.LastError != "" {
		t.Fatalf("unexpected pending state: %#v", view)
	}
	if len(provider.upserts) != 1 {
		t.Fatalf("expected one accepted update, got %#v", provider.upserts)
	}

	now = now.Add(time.Minute)
	service.sources["test"] = testResolver{addresses: AddressSet{IPv4: netip.MustParseAddr("203.0.113.10")}}
	provider.matches = false
	view, err = service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("checking pending publication failed: %v", err)
	}
	if view.LastStatus != syncStatusPending || view.LastIPv4 != "203.0.113.9" || len(provider.upserts) != 1 {
		t.Fatalf("pending check repeated the write: view=%#v upserts=%#v", view, provider.upserts)
	}
	if got := provider.checkedAddresses[len(provider.checkedAddresses)-1]; got != "A:203.0.113.9" {
		t.Fatalf("pending check used current WAN address instead of submitted address: %q", got)
	}

	now = now.Add(time.Minute)
	provider.matches = true
	view, err = service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("confirming pending publication failed: %v", err)
	}
	if view.LastStatus != syncStatusPending || view.LastIPv4 != "203.0.113.10" || view.LastPublishedIPv4 != "203.0.113.9" || len(provider.upserts) != 2 {
		t.Fatalf("confirmed publication did not submit changed WAN address: view=%#v upserts=%#v", view, provider.upserts)
	}

	now = now.Add(time.Minute)
	view, err = service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("confirming replacement publication failed: %v", err)
	}
	if view.LastStatus != syncStatusSuccess || view.NextRetryAt != nil || view.LastPublishedIPv4 != "203.0.113.10" || len(provider.upserts) != 2 {
		t.Fatalf("unexpected replacement publication state: view=%#v upserts=%#v", view, provider.upserts)
	}
}

func TestSyncEntryPreservesPendingDeferredFamily(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testStatusProvider{}
	provider.upsertErr = newProviderError(providerErrorTransient, 0, errors.New("temporarily unavailable"))
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			"test": testResolver{addresses: AddressSet{
				IPv4: netip.MustParseAddr("203.0.113.10"),
				IPv6: netip.MustParseAddr("2001:db8::10"),
			}},
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
		LastStatus:      syncStatusPending,
		IPv6Status:      syncStatusPending,
		LastIPv6:        "2001:db8::9",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}

	view, err := service.SyncEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("syncing dual-stack pending entry failed: %v", err)
	}
	if view.LastStatus != syncStatusPending || view.IPv6Status != syncStatusPending || view.LastIPv6 != "2001:db8::9" {
		t.Fatalf("deferred family lost pending target: %#v", view)
	}
	if len(provider.upserts) != 1 || provider.upserts[0] != "A:203.0.113.10" {
		t.Fatalf("unexpected provider updates: %#v", provider.upserts)
	}
}

func TestScheduledSyncSkipsEntryDisabledAfterSelection(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := &testProvider{}
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{"test": provider},
		sources: map[string]IPSourceResolver{
			"test": testResolver{addresses: AddressSet{IPv4: netip.MustParseAddr("203.0.113.9")}},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	entry := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        "test",
		ProviderSecret:  "provider-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      "test",
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatalf("failed to create test entry: %v", err)
	}
	if err := database.Model(&entry).Update("enabled", false).Error; err != nil {
		t.Fatalf("failed to disable test entry: %v", err)
	}

	view, err := service.syncEntryByID(context.Background(), entry.ID, true)
	if err != nil {
		t.Fatalf("checking disabled scheduled entry failed: %v", err)
	}
	if view.Enabled || view.LastSyncAt != nil || len(provider.upserts) != 0 {
		t.Fatalf("scheduled sync ran disabled entry: view=%#v upserts=%#v", view, provider.upserts)
	}
}

func TestTransientRetryDelayCapsAtOneHour(t *testing.T) {
	wants := []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		32 * time.Minute,
		time.Hour,
		time.Hour,
	}
	for index, want := range wants {
		if got := transientRetryDelay(uint(index + 1)); got != want {
			t.Fatalf("retry %d delay = %s, want %s", index+1, got, want)
		}
	}
}
