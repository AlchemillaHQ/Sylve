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
	"fmt"
	"net/netip"
	"strings"
	"sync"
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
	validateErr         error
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
	if p.validateErr != nil {
		return nil, p.validateErr
	}
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

type blockingSyncProvider struct {
	blockHostname string
	started       chan struct{}
	release       chan struct{}
	startedOnce   sync.Once
	releaseOnce   sync.Once
}

func newBlockingSyncProvider(hostname string) *blockingSyncProvider {
	return &blockingSyncProvider{
		blockHostname: hostname,
		started:       make(chan struct{}),
		release:       make(chan struct{}),
	}
}

func (*blockingSyncProvider) ID() string {
	return "blocking"
}

func (*blockingSyncProvider) Validate(_ context.Context, _ string, _ string, _ string, settings map[string]string) (map[string]string, error) {
	return cloneSettings(settings), nil
}

func (p *blockingSyncProvider) Upsert(ctx context.Context, _ string, _ map[string]string, hostname, _ string, _ netip.Addr) error {
	if hostname != p.blockHostname {
		return nil
	}

	p.startedOnce.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *blockingSyncProvider) unblock() {
	p.releaseOnce.Do(func() { close(p.release) })
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

func newTargetConflictTestService(t *testing.T) *Service {
	t.Helper()

	return &Service{
		DB: testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{}),
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
}

func targetConflictInput(provider, hostname, recordType string, enabled bool) EntryInput {
	return EntryInput{
		Enabled:         enabled,
		Provider:        provider,
		Token:           "provider-secret",
		Hostname:        hostname,
		RecordType:      recordType,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings: map[string]string{
			SourceSettingIPv4: "203.0.113.9",
			SourceSettingIPv6: "2001:db8::9",
		},
	}
}

func newEntryLockTestService(t *testing.T) (*Service, *blockingSyncProvider, dynamicDNSModels.Entry, dynamicDNSModels.Entry) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := newBlockingSyncProvider("slow.example.com")
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{provider.ID(): provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: 5 * time.Second,
	}

	slow := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        provider.ID(),
		ProviderSecret:  "slow-secret",
		Hostname:        "slow.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.8"},
	}
	fast := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        provider.ID(),
		ProviderSecret:  "fast-secret",
		Hostname:        "fast.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.9"},
	}
	if err := database.Create(&slow).Error; err != nil {
		t.Fatalf("seed slow entry: %v", err)
	}
	if err := database.Create(&fast).Error; err != nil {
		t.Fatalf("seed fast entry: %v", err)
	}

	return service, provider, slow, fast
}

func TestSyncEntryDoesNotBlockDifferentEntry(t *testing.T) {
	service, provider, slow, fast := newEntryLockTestService(t)
	defer provider.unblock()

	type result struct {
		view *EntryView
		err  error
	}
	slowDone := make(chan result, 1)
	go func() {
		view, err := service.SyncEntry(context.Background(), slow.ID)
		slowDone <- result{view: view, err: err}
	}()

	select {
	case <-provider.started:
	case completed := <-slowDone:
		t.Fatalf("slow sync completed before blocking: view=%+v err=%v", completed.view, completed.err)
	case <-time.After(time.Second):
		t.Fatal("slow sync did not reach the provider")
	}

	fastDone := make(chan result, 1)
	go func() {
		view, err := service.SyncEntry(context.Background(), fast.ID)
		fastDone <- result{view: view, err: err}
	}()

	select {
	case completed := <-fastDone:
		if completed.err != nil || completed.view == nil || completed.view.LastStatus != syncStatusSuccess {
			t.Fatalf("different-entry sync failed while slow entry was blocked: view=%+v err=%v", completed.view, completed.err)
		}
	case <-time.After(time.Second):
		provider.unblock()
		<-slowDone
		<-fastDone
		t.Fatal("slow provider call blocked a different Dynamic DNS entry")
	}

	provider.unblock()
	select {
	case completed := <-slowDone:
		if completed.err != nil || completed.view == nil || completed.view.LastStatus != syncStatusSuccess {
			t.Fatalf("slow sync failed after release: view=%+v err=%v", completed.view, completed.err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow sync did not finish after provider release")
	}

	service.entryLocksMu.Lock()
	remainingLocks := len(service.entryLocks)
	service.entryLocksMu.Unlock()
	if remainingLocks != 0 {
		t.Fatalf("entry lock registry retained %d unused locks", remainingLocks)
	}
}

func TestSyncEntrySerializesUpdateForSameEntry(t *testing.T) {
	service, provider, slow, _ := newEntryLockTestService(t)
	defer provider.unblock()

	slowDone := make(chan error, 1)
	go func() {
		_, err := service.SyncEntry(context.Background(), slow.ID)
		slowDone <- err
	}()

	select {
	case <-provider.started:
	case err := <-slowDone:
		t.Fatalf("slow sync completed before blocking: %v", err)
	case <-time.After(time.Second):
		t.Fatal("slow sync did not reach the provider")
	}

	type updateResult struct {
		view *EntryView
		err  error
	}
	updateDone := make(chan updateResult, 1)
	input := targetConflictInput(provider.ID(), slow.Hostname, dynamicDNSModels.RecordTypeA, true)
	input.IntervalMinutes = 15
	go func() {
		view, err := service.UpdateEntry(context.Background(), slow.ID, input)
		updateDone <- updateResult{view: view, err: err}
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	waiting := false
	for !waiting {
		service.entryLocksMu.Lock()
		lock := service.entryLocks[slow.ID]
		waiting = lock != nil && lock.refs == 2
		service.entryLocksMu.Unlock()
		if waiting {
			break
		}

		select {
		case completed := <-updateDone:
			provider.unblock()
			<-slowDone
			t.Fatalf("same-entry update completed during sync: view=%+v err=%v", completed.view, completed.err)
		case <-deadline.C:
			provider.unblock()
			<-slowDone
			t.Fatal("same-entry update did not wait on the entry lock")
		case <-time.After(time.Millisecond):
		}
	}

	select {
	case completed := <-updateDone:
		provider.unblock()
		<-slowDone
		t.Fatalf("same-entry update bypassed the active sync: view=%+v err=%v", completed.view, completed.err)
	default:
	}

	provider.unblock()
	select {
	case err := <-slowDone:
		if err != nil {
			t.Fatalf("slow sync failed after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow sync did not finish after provider release")
	}

	select {
	case completed := <-updateDone:
		if completed.err != nil || completed.view == nil || completed.view.IntervalMinutes != 15 {
			t.Fatalf("same-entry update failed after sync: view=%+v err=%v", completed.view, completed.err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-entry update did not resume after sync")
	}

	service.entryLocksMu.Lock()
	remainingLocks := len(service.entryLocks)
	service.entryLocksMu.Unlock()
	if remainingLocks != 0 {
		t.Fatalf("entry lock registry retained %d unused locks", remainingLocks)
	}
}

func TestConcurrentCreateEntryPreservesTargetUniqueness(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{})
	provider := newBlockingSyncProvider("")
	service := &Service{
		DB:        database,
		providers: map[string]DNSProvider{provider.ID(): provider},
		sources: map[string]IPSourceResolver{
			dynamicDNSModels.SourceTypeManual: ManualResolver{},
		},
		now:         time.Now,
		syncTimeout: time.Second,
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := service.CreateEntry(
				context.Background(),
				targetConflictInput(provider.ID(), "router.example.com", dynamicDNSModels.RecordTypeA, true),
			)
			results <- err
		}()
	}
	close(start)

	succeeded := 0
	conflicted := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrEntryConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent create error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent creates succeeded=%d conflicted=%d", succeeded, conflicted)
	}

	var count int64
	if err := database.Model(&dynamicDNSModels.Entry{}).Count(&count).Error; err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent creates persisted %d entries", count)
	}
}

func TestCreateEntryClassifiesProviderValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{
			name:    "untyped provider rejection is invalid input",
			err:     errors.New("provider-secret is invalid"),
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "permanent provider rejection is invalid input",
			err:     newProviderError(providerErrorPermanent, 0, errors.New("provider-secret is invalid")),
			wantErr: ErrInvalidEntry,
		},
		{
			name:    "transient provider failure is upstream failure",
			err:     newProviderError(providerErrorTransient, 0, errors.New("provider-secret service unavailable")),
			wantErr: ErrProviderUnavailable,
		},
		{
			name:    "pending provider response is upstream failure",
			err:     newProviderError(providerErrorPending, 0, errors.New("provider-secret publication pending")),
			wantErr: ErrProviderUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &testProvider{id: "test", validateErr: test.err}
			service := &Service{
				DB:        testutil.NewSQLiteTestDB(t, &dynamicDNSModels.Entry{}),
				providers: map[string]DNSProvider{"test": provider},
				sources: map[string]IPSourceResolver{
					dynamicDNSModels.SourceTypeManual: ManualResolver{},
				},
				now:         time.Now,
				syncTimeout: time.Second,
			}

			_, err := service.CreateEntry(
				context.Background(),
				targetConflictInput("test", "router.example.com", dynamicDNSModels.RecordTypeA, true),
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "provider-secret") {
				t.Fatalf("validation error exposed provider credential: %v", err)
			}

			var count int64
			if err := service.DB.Model(&dynamicDNSModels.Entry{}).Count(&count).Error; err != nil {
				t.Fatalf("count entries: %v", err)
			}
			if count != 0 {
				t.Fatalf("failed validation persisted %d entries", count)
			}
		})
	}
}

func TestCreateEntryRejectsOverlappingTargets(t *testing.T) {
	tests := []struct {
		name                string
		existingProvider    string
		existingHostname    string
		existingRecordType  string
		disableExisting     bool
		candidateProvider   string
		candidateHostname   string
		candidateRecordType string
		candidateEnabled    bool
		wantConflict        bool
	}{
		{
			name:             "same A target",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "A overlaps BOTH",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeBoth, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "BOTH overlaps A",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeBoth,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "AAAA overlaps BOTH",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeAAAA,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeBoth, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "BOTH overlaps AAAA",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeBoth,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeAAAA, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "normalized target conflicts",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: " FIRST ", candidateHostname: "ROUTER.EXAMPLE.COM.", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "disabled existing entry still conflicts",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA, disableExisting: true,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
			wantConflict: true,
		},
		{
			name:             "disabled candidate still conflicts",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: false,
			wantConflict: true,
		},
		{
			name:             "A and AAAA can coexist",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "first", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeAAAA, candidateEnabled: true,
		},
		{
			name:             "different hostname can coexist",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "first", candidateHostname: "other.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
		},
		{
			name:             "different provider can coexist",
			existingProvider: "first", existingHostname: "router.example.com", existingRecordType: dynamicDNSModels.RecordTypeA,
			candidateProvider: "second", candidateHostname: "router.example.com", candidateRecordType: dynamicDNSModels.RecordTypeA, candidateEnabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTargetConflictTestService(t)
			existing := dynamicDNSModels.Entry{
				Enabled:         true,
				Provider:        test.existingProvider,
				ProviderSecret:  "existing-secret",
				Hostname:        test.existingHostname,
				RecordType:      test.existingRecordType,
				IntervalMinutes: DefaultIntervalMinutes,
				SourceType:      dynamicDNSModels.SourceTypeManual,
				SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.8"},
			}
			if err := service.DB.Create(&existing).Error; err != nil {
				t.Fatalf("seed existing entry: %v", err)
			}
			if test.disableExisting {
				if err := service.DB.Model(&existing).Update("enabled", false).Error; err != nil {
					t.Fatalf("disable existing entry: %v", err)
				}
			}

			_, err := service.CreateEntry(
				context.Background(),
				targetConflictInput(
					test.candidateProvider,
					test.candidateHostname,
					test.candidateRecordType,
					test.candidateEnabled,
				),
			)
			if test.wantConflict {
				if !errors.Is(err, ErrEntryConflict) {
					t.Fatalf("expected target conflict, got %v", err)
				}
				if !strings.Contains(err.Error(), fmt.Sprintf("entry %d", existing.ID)) {
					t.Fatalf("conflict does not identify the existing entry: %v", err)
				}
			} else if err != nil {
				t.Fatalf("expected target combination to be allowed, got %v", err)
			}

			var count int64
			if err := service.DB.Model(&dynamicDNSModels.Entry{}).Count(&count).Error; err != nil {
				t.Fatalf("count entries: %v", err)
			}
			wantCount := int64(2)
			if test.wantConflict {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("entry count = %d, want %d", count, wantCount)
			}
		})
	}
}

func TestUpdateEntryRejectsOverlappingTarget(t *testing.T) {
	service := newTargetConflictTestService(t)
	first := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        "first",
		ProviderSecret:  "first-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv4: "203.0.113.8"},
	}
	second := dynamicDNSModels.Entry{
		Enabled:         true,
		Provider:        "first",
		ProviderSecret:  "second-secret",
		Hostname:        "router.example.com",
		RecordType:      dynamicDNSModels.RecordTypeAAAA,
		IntervalMinutes: DefaultIntervalMinutes,
		SourceType:      dynamicDNSModels.SourceTypeManual,
		SourceSettings:  map[string]string{SourceSettingIPv6: "2001:db8::8"},
	}
	if err := service.DB.Create(&first).Error; err != nil {
		t.Fatalf("seed A entry: %v", err)
	}
	if err := service.DB.Create(&second).Error; err != nil {
		t.Fatalf("seed AAAA entry: %v", err)
	}

	_, err := service.UpdateEntry(
		context.Background(),
		second.ID,
		targetConflictInput("first", second.Hostname, dynamicDNSModels.RecordTypeBoth, true),
	)
	if !errors.Is(err, ErrEntryConflict) {
		t.Fatalf("expected overlapping update conflict, got %v", err)
	}

	var unchanged dynamicDNSModels.Entry
	if err := service.DB.First(&unchanged, second.ID).Error; err != nil {
		t.Fatalf("reload conflicting entry: %v", err)
	}
	if unchanged.RecordType != dynamicDNSModels.RecordTypeAAAA {
		t.Fatalf("conflicting update changed the entry: %+v", unchanged)
	}

	view, err := service.UpdateEntry(
		context.Background(),
		first.ID,
		targetConflictInput("first", first.Hostname, dynamicDNSModels.RecordTypeA, true),
	)
	if err != nil {
		t.Fatalf("entry conflicted with itself: %v", err)
	}
	if view.ID != first.ID || view.RecordType != dynamicDNSModels.RecordTypeA {
		t.Fatalf("unexpected self-update result: %+v", view)
	}
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
