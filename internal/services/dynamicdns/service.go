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
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var (
	ErrInvalidEntry  = errors.New("invalid dynamic DNS entry")
	ErrEntryNotFound = errors.New("dynamic DNS entry not found")
)

type Service struct {
	DB        *gorm.DB
	providers map[string]DNSProvider
	sources   map[string]IPSourceResolver

	now         func() time.Time
	syncTimeout time.Duration
	syncMu      sync.Mutex
}

func NewService(db *gorm.DB) *Service {
	cloudflare := NewCloudflareProvider()
	namecheap := NewNamecheapProvider()
	interfaceResolver := NewInterfaceResolver()
	manualResolver := ManualResolver{}
	stunResolver := NewSTUNResolver()

	return &Service{
		DB: db,
		providers: map[string]DNSProvider{
			cloudflare.ID(): cloudflare,
			namecheap.ID():  namecheap,
		},
		sources: map[string]IPSourceResolver{
			interfaceResolver.Type(): interfaceResolver,
			manualResolver.Type():    manualResolver,
			stunResolver.Type():      stunResolver,
		},
		now:         time.Now,
		syncTimeout: 20 * time.Second,
	}
}

func (s *Service) ListEntries(ctx context.Context) ([]EntryView, error) {
	var entries []dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).Order("id ASC").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list dynamic DNS entries: %w", err)
	}

	views := make([]EntryView, len(entries))
	for index, entry := range entries {
		views[index] = entryView(entry)
	}
	return views, nil
}

func (s *Service) CreateEntry(ctx context.Context, input EntryInput) (*EntryView, error) {
	entry, err := s.prepareEntry(ctx, input, nil)
	if err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Create(&entry).Error; err != nil {
		return nil, fmt.Errorf("failed to create dynamic DNS entry: %w", err)
	}

	view := entryView(entry)
	return &view, nil
}

func (s *Service) UpdateEntry(ctx context.Context, id uint, input EntryInput) (*EntryView, error) {
	var existing dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).First(&existing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("failed to retrieve dynamic DNS entry: %w", err)
	}

	entry, err := s.prepareEntry(ctx, input, &existing)
	if err != nil {
		return nil, err
	}
	entry.ID = existing.ID
	entry.CreatedAt = existing.CreatedAt

	if err := s.DB.WithContext(ctx).Save(&entry).Error; err != nil {
		return nil, fmt.Errorf("failed to update dynamic DNS entry: %w", err)
	}

	view := entryView(entry)
	return &view, nil
}

func (s *Service) DeleteEntry(ctx context.Context, id uint) error {
	result := s.DB.WithContext(ctx).Delete(&dynamicDNSModels.Entry{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete dynamic DNS entry: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *Service) SyncEntry(ctx context.Context, id uint) (*EntryView, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	var entry dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).First(&entry, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("failed to retrieve dynamic DNS entry: %w", err)
	}

	if err := s.syncEntry(ctx, &entry); err != nil {
		return nil, err
	}

	view := entryView(entry)
	return &view, nil
}

func (s *Service) SyncDue(ctx context.Context) error {
	var entries []dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&entries).Error; err != nil {
		return fmt.Errorf("failed to list enabled dynamic DNS entries: %w", err)
	}

	now := s.currentTime()
	for _, entry := range entries {
		interval := time.Duration(entry.IntervalMinutes) * time.Minute
		if entry.LastSyncAt != nil && now.Sub(*entry.LastSyncAt) < interval {
			continue
		}

		if _, err := s.SyncEntry(ctx, entry.ID); err != nil {
			logger.L.Error().Err(err).Uint("entryID", entry.ID).Msg("dynamic_dns_sync_failed")
		}
	}

	return nil
}

func (s *Service) StartWorker(ctx context.Context) {
	if err := s.SyncDue(ctx); err != nil {
		logger.L.Error().Err(err).Msg("dynamic_dns_initial_sync_failed")
	}

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("stopping_dynamic_dns_worker")
			return
		case <-ticker.C:
			if err := s.SyncDue(ctx); err != nil {
				logger.L.Error().Err(err).Msg("dynamic_dns_worker_failed")
			}
		}
	}
}

func (s *Service) prepareEntry(ctx context.Context, input EntryInput, existing *dynamicDNSModels.Entry) (dynamicDNSModels.Entry, error) {
	providerID := strings.ToLower(strings.TrimSpace(input.Provider))
	provider, ok := s.providers[providerID]
	if !ok {
		return dynamicDNSModels.Entry{}, invalidEntry("unsupported DNS provider %q", input.Provider)
	}

	hostname, err := normalizeHostname(input.Hostname)
	if err != nil {
		return dynamicDNSModels.Entry{}, invalidEntry("%v", err)
	}

	recordType := strings.ToUpper(strings.TrimSpace(input.RecordType))
	if !isRecordType(recordType) {
		return dynamicDNSModels.Entry{}, invalidEntry("record type must be A, AAAA, or BOTH")
	}

	interval := input.IntervalMinutes
	if interval == 0 {
		interval = DefaultIntervalMinutes
	}
	if interval < MinimumIntervalMinutes || interval > MaximumIntervalMinutes {
		return dynamicDNSModels.Entry{}, invalidEntry("update interval must be between %d and %d minutes", MinimumIntervalMinutes, MaximumIntervalMinutes)
	}

	sourceType, sourceSettings, err := s.normalizeSource(input.SourceType, input.SourceSettings, recordType)
	if err != nil {
		return dynamicDNSModels.Entry{}, err
	}

	secret := strings.TrimSpace(input.Token)
	providerSettings := cloneSettings(input.ProviderSettings)
	if existing != nil && providerID == existing.Provider {
		if secret == "" {
			secret = existing.ProviderSecret
		}
		if len(providerSettings) == 0 {
			providerSettings = cloneSettings(existing.ProviderSettings)
		}
	}
	if secret == "" {
		return dynamicDNSModels.Entry{}, invalidEntry("provider credential is required")
	}

	needsValidation := existing == nil ||
		strings.TrimSpace(input.Token) != "" ||
		providerID != existing.Provider ||
		hostname != existing.Hostname ||
		!sameSettings(providerSettings, existing.ProviderSettings)
	if needsValidation {
		providerSettings, err = provider.Validate(ctx, secret, hostname, recordType, providerSettings)
		if err != nil {
			return dynamicDNSModels.Entry{}, invalidEntry("provider validation failed: %v", redactSecret(err.Error(), secret))
		}
	}

	return dynamicDNSModels.Entry{
		Enabled:          input.Enabled,
		Provider:         providerID,
		ProviderSettings: providerSettings,
		ProviderSecret:   secret,
		Hostname:         hostname,
		RecordType:       recordType,
		IntervalMinutes:  interval,
		SourceType:       sourceType,
		SourceSettings:   sourceSettings,
	}, nil
}

func (s *Service) normalizeSource(rawType string, rawSettings map[string]string, recordType string) (string, map[string]string, error) {
	sourceType := strings.ToLower(strings.TrimSpace(rawType))
	if _, ok := s.sources[sourceType]; !ok {
		return "", nil, invalidEntry("unsupported IP source %q", rawType)
	}

	settings := cloneSettings(rawSettings)
	switch sourceType {
	case dynamicDNSModels.SourceTypeInterface:
		name := strings.TrimSpace(settings[SourceSettingInterface])
		if name == "" {
			return "", nil, invalidEntry("network interface is required")
		}
		return sourceType, map[string]string{SourceSettingInterface: name}, nil
	case dynamicDNSModels.SourceTypeManual:
		manual := map[string]string{}
		if raw := strings.TrimSpace(settings[SourceSettingIPv4]); raw != "" {
			address, err := netip.ParseAddr(raw)
			if err != nil || !address.Is4() {
				return "", nil, invalidEntry("manual IPv4 address is invalid")
			}
			manual[SourceSettingIPv4] = address.Unmap().String()
		}
		if raw := strings.TrimSpace(settings[SourceSettingIPv6]); raw != "" {
			address, err := netip.ParseAddr(raw)
			if err != nil || !address.Is6() {
				return "", nil, invalidEntry("manual IPv6 address is invalid")
			}
			manual[SourceSettingIPv6] = address.String()
		}
		if (recordType == dynamicDNSModels.RecordTypeA && manual[SourceSettingIPv4] == "") ||
			(recordType == dynamicDNSModels.RecordTypeAAAA && manual[SourceSettingIPv6] == "") ||
			(recordType == dynamicDNSModels.RecordTypeBoth && len(manual) == 0) {
			return "", nil, invalidEntry("manual source does not provide an address for the selected record type")
		}
		return sourceType, manual, nil
	case dynamicDNSModels.SourceTypeSTUN:
		server, err := normalizeSTUNServer(settings[SourceSettingSTUNServer])
		if err != nil {
			return "", nil, invalidEntry("%v", err)
		}
		return sourceType, map[string]string{SourceSettingSTUNServer: server}, nil
	default:
		return "", nil, invalidEntry("unsupported IP source %q", rawType)
	}
}

func (s *Service) syncEntry(ctx context.Context, entry *dynamicDNSModels.Entry) error {
	now := s.currentTime()
	entry.LastStatus = ""
	entry.LastError = ""
	entry.IPv4Status = ""
	entry.IPv4Error = ""
	entry.IPv6Status = ""
	entry.IPv6Error = ""
	entry.LastIPv4 = ""
	entry.LastIPv6 = ""
	entry.LastSyncAt = cloneTime(&now)

	runCtx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()

	resolver, sourceKnown := s.sources[entry.SourceType]
	var addresses AddressSet
	var resolveErr error
	if sourceKnown {
		addresses, resolveErr = resolver.Resolve(runCtx, entry.SourceSettings)
	} else {
		resolveErr = fmt.Errorf("unsupported IP source %q", entry.SourceType)
	}

	provider, providerKnown := s.providers[entry.Provider]
	if !providerKnown {
		resolveErr = firstError(resolveErr, fmt.Errorf("unsupported DNS provider %q", entry.Provider))
	}

	requested := 0
	succeeded := 0
	var failures []string

	if entry.RecordType == dynamicDNSModels.RecordTypeA || entry.RecordType == dynamicDNSModels.RecordTypeBoth {
		requested++
		status, address, err := syncFamily(runCtx, provider, providerKnown, entry, dynamicDNSModels.RecordTypeA, addresses.IPv4, resolveErr)
		entry.IPv4Status = status
		entry.LastIPv4 = address
		if err != nil {
			entry.IPv4Error = redactSecret(err.Error(), entry.ProviderSecret)
			failures = append(failures, "IPv4: "+entry.IPv4Error)
		} else {
			succeeded++
		}
	}
	if entry.RecordType == dynamicDNSModels.RecordTypeAAAA || entry.RecordType == dynamicDNSModels.RecordTypeBoth {
		requested++
		status, address, err := syncFamily(runCtx, provider, providerKnown, entry, dynamicDNSModels.RecordTypeAAAA, addresses.IPv6, resolveErr)
		entry.IPv6Status = status
		entry.LastIPv6 = address
		if err != nil {
			entry.IPv6Error = redactSecret(err.Error(), entry.ProviderSecret)
			failures = append(failures, "IPv6: "+entry.IPv6Error)
		} else {
			succeeded++
		}
	}

	switch {
	case succeeded == requested:
		entry.LastStatus = "success"
		entry.LastSuccessAt = cloneTime(&now)
	case succeeded > 0:
		entry.LastStatus = "partial"
		entry.LastSuccessAt = cloneTime(&now)
	default:
		entry.LastStatus = "error"
	}
	entry.LastError = strings.Join(failures, "; ")

	if err := s.DB.WithContext(ctx).Save(entry).Error; err != nil {
		return fmt.Errorf("failed to save dynamic DNS sync status: %w", err)
	}
	return nil
}

func syncFamily(ctx context.Context, provider DNSProvider, providerKnown bool, entry *dynamicDNSModels.Entry, recordType string, address netip.Addr, resolveErr error) (string, string, error) {
	family := "IPv4"
	if recordType == dynamicDNSModels.RecordTypeAAAA {
		family = "IPv6"
	}
	if resolveErr != nil {
		return "error", "", fmt.Errorf("failed to resolve %s address: %w", family, resolveErr)
	}
	if !address.IsValid() {
		return "error", "", fmt.Errorf("no %s address resolved", family)
	}
	if !providerKnown {
		return "error", address.String(), fmt.Errorf("DNS provider is unavailable")
	}
	if err := provider.Upsert(ctx, entry.ProviderSecret, entry.ProviderSettings, entry.Hostname, recordType, address); err != nil {
		return "error", address.String(), err
	}
	return "success", address.String(), nil
}

func (s *Service) currentTime() time.Time {
	now := s.now
	if now == nil {
		now = time.Now
	}
	return now().UTC()
}

func normalizeHostname(raw string) (string, error) {
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if hostname == "" || len(hostname) > 253 {
		return "", fmt.Errorf("hostname is invalid")
	}
	if address, err := netip.ParseAddr(hostname); err == nil && address.IsValid() {
		return "", fmt.Errorf("hostname must be a DNS name")
	}

	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("hostname must include a DNS zone")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("hostname is invalid")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("hostname is invalid")
			}
		}
	}

	return hostname, nil
}

func isRecordType(recordType string) bool {
	return recordType == dynamicDNSModels.RecordTypeA ||
		recordType == dynamicDNSModels.RecordTypeAAAA ||
		recordType == dynamicDNSModels.RecordTypeBoth
}

func invalidEntry(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEntry, fmt.Sprintf(format, args...))
}

func sameSettings(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}

func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func redactSecret(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
