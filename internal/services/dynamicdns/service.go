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

	"github.com/alchemillahq/sylve/internal/db/models"
	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var (
	ErrInvalidEntry        = errors.New("invalid dynamic DNS entry")
	ErrEntryNotFound       = errors.New("dynamic DNS entry not found")
	ErrEntryInUse          = errors.New("dynamic DNS entry is in use")
	ErrEntryConflict       = errors.New("dynamic DNS entry conflicts with an existing entry")
	ErrProviderUnavailable = errors.New("dynamic DNS provider is unavailable")
)

const (
	syncStatusSuccess  = "success"
	syncStatusPartial  = "partial"
	syncStatusError    = "error"
	syncStatusPending  = "pending"
	syncStatusDeferred = "deferred"

	transientRetryInitialDelay = time.Minute
	transientRetryMaximumDelay = time.Hour
	pendingRetryDelay          = time.Minute
)

type familySyncResult struct {
	status               string
	address              string
	publishedAddress     string
	publicationConfirmed bool
	err                  error
	providerKind         providerErrorKind
	retryAfter           time.Duration
}

type entryOperationLock struct {
	mu   sync.Mutex
	refs uint
}

type Service struct {
	DB        *gorm.DB
	providers map[string]DNSProvider
	sources   map[string]IPSourceResolver

	now          func() time.Time
	syncTimeout  time.Duration
	targetMu     sync.Mutex
	entryLocksMu sync.Mutex
	entryLocks   map[uint]*entryOperationLock
}

func NewService(db *gorm.DB) *Service {
	cloudflare := NewCloudflareProvider()
	namecheap := NewNamecheapProvider()
	sylve := NewSylveProvider()
	interfaceResolver := NewInterfaceResolver()
	manualResolver := ManualResolver{}
	stunResolver := NewSTUNResolver()

	return &Service{
		DB: db,
		providers: map[string]DNSProvider{
			cloudflare.ID(): cloudflare,
			namecheap.ID():  namecheap,
			sylve.ID():      sylve,
		},
		sources: map[string]IPSourceResolver{
			interfaceResolver.Type(): interfaceResolver,
			manualResolver.Type():    manualResolver,
			stunResolver.Type():      stunResolver,
		},
		now:         time.Now,
		syncTimeout: 20 * time.Second,
		entryLocks:  make(map[uint]*entryOperationLock),
	}
}

func (s *Service) lockEntryOperation(id uint) func() {
	s.entryLocksMu.Lock()
	if s.entryLocks == nil {
		s.entryLocks = make(map[uint]*entryOperationLock)
	}
	lock := s.entryLocks[id]
	if lock == nil {
		lock = &entryOperationLock{}
		s.entryLocks[id] = lock
	}
	lock.refs++
	s.entryLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		s.entryLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && s.entryLocks[id] == lock {
			delete(s.entryLocks, id)
		}
		s.entryLocksMu.Unlock()
	}
}

func (s *Service) withTargetMutation(operation func() error) error {
	s.targetMu.Lock()
	defer s.targetMu.Unlock()
	return operation()
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

	err = s.withTargetMutation(func() error {
		return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := ensureTargetAvailable(tx, 0, entry); err != nil {
				return err
			}
			return tx.Create(&entry).Error
		})
	})
	if err != nil {
		if errors.Is(err, ErrEntryConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create dynamic DNS entry: %w", err)
	}

	view := entryView(entry)
	return &view, nil
}

func (s *Service) UpdateEntry(ctx context.Context, id uint, input EntryInput) (*EntryView, error) {
	unlockEntry := s.lockEntryOperation(id)
	defer unlockEntry()

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
	sameTarget := sameProviderTarget(entry, existing)
	sameIdentity := sameTarget && entry.ProviderSecret == existing.ProviderSecret
	if existing.LastStatus == syncStatusPending && !sameTarget {
		return nil, invalidEntry("provider identity cannot be changed while DNS publication is pending")
	}
	if sameIdentity || (existing.LastStatus == syncStatusPending && sameTarget) {
		preserveSyncState(&entry, existing)
	}
	entry.ID = existing.ID
	entry.CreatedAt = existing.CreatedAt

	err = s.withTargetMutation(func() error {
		return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := ensureTargetAvailable(tx, existing.ID, entry); err != nil {
				return err
			}
			if entry.Provider != existing.Provider || entry.Hostname != existing.Hostname {
				inUse, err := entryReferencedByCertificate(tx, existing.ID)
				if err != nil {
					return err
				}
				if inUse {
					return fmt.Errorf("%w: provider and hostname cannot be changed while a managed certificate uses this entry", ErrEntryInUse)
				}
			}
			return tx.Save(&entry).Error
		})
	})
	if err != nil {
		if errors.Is(err, ErrEntryInUse) || errors.Is(err, ErrEntryConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to update dynamic DNS entry: %w", err)
	}

	view := entryView(entry)
	return &view, nil
}

func ensureTargetAvailable(db *gorm.DB, excludeID uint, entry dynamicDNSModels.Entry) error {
	query := db.Model(&dynamicDNSModels.Entry{}).
		Select("id", "record_type").
		Where(
			"provider = ? AND hostname = ? AND record_type IN ?",
			entry.Provider,
			entry.Hostname,
			overlappingRecordTypes(entry.RecordType),
		)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}

	var conflicts []dynamicDNSModels.Entry
	if err := query.Order("id ASC").Limit(1).Find(&conflicts).Error; err != nil {
		return fmt.Errorf("failed to check dynamic DNS target availability: %w", err)
	}
	if len(conflicts) == 0 {
		return nil
	}

	conflict := conflicts[0]
	return fmt.Errorf(
		"%w: entry %d already manages %s records for %q with provider %q",
		ErrEntryConflict,
		conflict.ID,
		conflict.RecordType,
		entry.Hostname,
		entry.Provider,
	)
}

func overlappingRecordTypes(recordType string) []string {
	switch recordType {
	case dynamicDNSModels.RecordTypeA:
		return []string{dynamicDNSModels.RecordTypeA, dynamicDNSModels.RecordTypeBoth}
	case dynamicDNSModels.RecordTypeAAAA:
		return []string{dynamicDNSModels.RecordTypeAAAA, dynamicDNSModels.RecordTypeBoth}
	case dynamicDNSModels.RecordTypeBoth:
		return []string{
			dynamicDNSModels.RecordTypeA,
			dynamicDNSModels.RecordTypeAAAA,
			dynamicDNSModels.RecordTypeBoth,
		}
	default:
		return []string{recordType}
	}
}

func (s *Service) DeleteEntry(ctx context.Context, id uint) error {
	unlockEntry := s.lockEntryOperation(id)
	defer unlockEntry()

	err := s.withTargetMutation(func() error {
		return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			inUse, err := entryReferencedByCertificate(tx, id)
			if err != nil {
				return err
			}
			if inUse {
				return fmt.Errorf("%w: delete the managed certificate before deleting this entry", ErrEntryInUse)
			}
			result := tx.Delete(&dynamicDNSModels.Entry{}, id)
			if result.Error != nil {
				return fmt.Errorf("failed to delete dynamic DNS entry: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				return ErrEntryNotFound
			}
			return nil
		})
	})
	return err
}

func entryReferencedByCertificate(db *gorm.DB, id uint) (bool, error) {
	var count int64
	if err := db.Model(&models.Certificate{}).
		Where("dynamic_dns_entry_id = ?", id).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check Dynamic DNS certificate references: %w", err)
	}
	return count > 0, nil
}

func (s *Service) SyncEntry(ctx context.Context, id uint) (*EntryView, error) {
	return s.syncEntryByID(ctx, id, false)
}

func (s *Service) syncEntryByID(ctx context.Context, id uint, requireEnabled bool) (*EntryView, error) {
	unlockEntry := s.lockEntryOperation(id)
	defer unlockEntry()

	var entry dynamicDNSModels.Entry
	if err := s.DB.WithContext(ctx).First(&entry, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("failed to retrieve dynamic DNS entry: %w", err)
	}
	if requireEnabled && !entry.Enabled {
		view := entryView(entry)
		return &view, nil
	}
	if entry.NextRetryAt != nil && s.currentTime().Before(*entry.NextRetryAt) {
		view := entryView(entry)
		return &view, nil
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
		if entry.NextRetryAt != nil {
			if now.Before(*entry.NextRetryAt) {
				continue
			}
		} else {
			interval := time.Duration(entry.IntervalMinutes) * time.Minute
			if entry.LastSyncAt != nil && now.Sub(*entry.LastSyncAt) < interval {
				continue
			}
		}

		if _, err := s.syncEntryByID(ctx, entry.ID, true); err != nil {
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
		recordType != existing.RecordType ||
		(input.Enabled && !existing.Enabled) ||
		!sameSettings(providerSettings, existing.ProviderSettings)
	if needsValidation {
		providerSettings, err = provider.Validate(ctx, secret, hostname, recordType, providerSettings)
		if err != nil {
			return dynamicDNSModels.Entry{}, providerValidationError(err, secret)
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
	previousIPv4Status := entry.IPv4Status
	previousIPv6Status := entry.IPv6Status
	previousIPv4Address := entry.LastIPv4
	previousIPv6Address := entry.LastIPv6
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

	succeeded := 0
	pending := 0
	publicationConfirmed := false
	var failures []string
	var controlResult *familySyncResult
	stopRemainingFamilies := false

	if entry.RecordType == dynamicDNSModels.RecordTypeA || entry.RecordType == dynamicDNSModels.RecordTypeBoth {
		result := syncFamily(runCtx, provider, providerKnown, entry, dynamicDNSModels.RecordTypeA, addresses.IPv4, resolveErr, previousIPv4Status, previousIPv4Address, entry.LastPublishedIPv4)
		entry.IPv4Status = result.status
		entry.LastIPv4 = result.address
		if result.status == syncStatusSuccess {
			succeeded++
		} else if result.status == syncStatusPending {
			pending++
		}
		if result.publishedAddress != "" {
			entry.LastPublishedIPv4 = result.publishedAddress
		}
		publicationConfirmed = publicationConfirmed || result.publicationConfirmed
		if result.err != nil && result.providerKind != providerErrorPending {
			entry.IPv4Error = redactSecret(result.err.Error(), entry.ProviderSecret)
			failures = append(failures, "IPv4: "+entry.IPv4Error)
		}
		if result.providerKind != 0 {
			resultCopy := result
			controlResult = &resultCopy
			stopRemainingFamilies = true
		}
	}
	if entry.RecordType == dynamicDNSModels.RecordTypeAAAA || entry.RecordType == dynamicDNSModels.RecordTypeBoth {
		result := familySyncResult{status: syncStatusDeferred}
		if stopRemainingFamilies && previousIPv6Status == syncStatusPending {
			result.status = syncStatusPending
			result.address = previousIPv6Address
		} else if addresses.IPv6.IsValid() {
			result.address = addresses.IPv6.String()
		}
		if !stopRemainingFamilies {
			result = syncFamily(runCtx, provider, providerKnown, entry, dynamicDNSModels.RecordTypeAAAA, addresses.IPv6, resolveErr, previousIPv6Status, previousIPv6Address, entry.LastPublishedIPv6)
		}
		entry.IPv6Status = result.status
		entry.LastIPv6 = result.address
		if result.status == syncStatusSuccess {
			succeeded++
		} else if result.status == syncStatusPending {
			pending++
		}
		if result.publishedAddress != "" {
			entry.LastPublishedIPv6 = result.publishedAddress
		}
		publicationConfirmed = publicationConfirmed || result.publicationConfirmed
		if result.err != nil && result.providerKind != providerErrorPending {
			entry.IPv6Error = redactSecret(result.err.Error(), entry.ProviderSecret)
			failures = append(failures, "IPv6: "+entry.IPv6Error)
		}
		if result.providerKind != 0 {
			resultCopy := result
			controlResult = &resultCopy
			stopRemainingFamilies = true
		}
	}

	requested := 1
	if entry.RecordType == dynamicDNSModels.RecordTypeBoth {
		requested = 2
	}
	switch {
	case succeeded == requested:
		entry.LastStatus = syncStatusSuccess
		entry.LastSuccessAt = cloneTime(&now)
	case pending > 0:
		entry.LastStatus = syncStatusPending
		if succeeded > 0 {
			entry.LastSuccessAt = cloneTime(&now)
		}
	case succeeded > 0:
		entry.LastStatus = syncStatusPartial
		entry.LastSuccessAt = cloneTime(&now)
	default:
		entry.LastStatus = syncStatusError
	}
	if publicationConfirmed {
		entry.LastSuccessAt = cloneTime(&now)
	}
	entry.LastError = strings.Join(failures, "; ")

	s.applyRetryPolicy(entry, controlResult, now)

	if err := s.DB.WithContext(ctx).Save(entry).Error; err != nil {
		return fmt.Errorf("failed to save dynamic DNS sync status: %w", err)
	}
	return nil
}

func (s *Service) applyRetryPolicy(entry *dynamicDNSModels.Entry, result *familySyncResult, now time.Time) {
	if result == nil {
		entry.ConsecutiveFailures = 0
		entry.NextRetryAt = nil
		return
	}

	switch result.providerKind {
	case providerErrorPermanent:
		entry.Enabled = false
		entry.ConsecutiveFailures++
		entry.NextRetryAt = nil
		logger.L.Warn().Uint("entryID", entry.ID).Str("hostname", entry.Hostname).Msg("dynamic_dns_entry_auto_disabled")
	case providerErrorTransient:
		entry.ConsecutiveFailures++
		delay := result.retryAfter
		if delay <= 0 {
			delay = transientRetryDelay(entry.ConsecutiveFailures)
		}
		nextRetry := now.Add(delay)
		entry.NextRetryAt = cloneTime(&nextRetry)
		if entry.ConsecutiveFailures >= 3 {
			logger.L.Warn().Uint("entryID", entry.ID).Uint("failures", entry.ConsecutiveFailures).Str("hostname", entry.Hostname).Msg("dynamic_dns_provider_failure_persistent")
		}
	case providerErrorPending:
		entry.ConsecutiveFailures = 0
		nextRetry := now.Add(pendingRetryDelay)
		entry.NextRetryAt = cloneTime(&nextRetry)
	}
}

func transientRetryDelay(failures uint) time.Duration {
	if failures == 0 {
		failures = 1
	}
	delay := transientRetryInitialDelay
	for attempt := uint(1); attempt < failures && delay < transientRetryMaximumDelay; attempt++ {
		delay *= 2
		if delay > transientRetryMaximumDelay {
			return transientRetryMaximumDelay
		}
	}
	return delay
}

func syncFamily(ctx context.Context, provider DNSProvider, providerKnown bool, entry *dynamicDNSModels.Entry, recordType string, address netip.Addr, resolveErr error, previousStatus, previousAddress, publishedAddress string) familySyncResult {
	family := "IPv4"
	if recordType == dynamicDNSModels.RecordTypeAAAA {
		family = "IPv6"
	}

	statusProvider, hasStatus := provider.(DNSStatusProvider)
	confirmedAddress := ""
	if providerKnown && hasStatus && previousStatus == syncStatusPending {
		pendingAddress, err := netip.ParseAddr(previousAddress)
		if err != nil || (recordType == dynamicDNSModels.RecordTypeA && !pendingAddress.Is4()) || (recordType == dynamicDNSModels.RecordTypeAAAA && !pendingAddress.Is6()) {
			result := familySyncFailure(previousAddress, newProviderError(providerErrorTransient, 0, fmt.Errorf("pending %s address is invalid", family)))
			result.status = syncStatusPending
			return result
		}
		matches, err := statusProvider.AddressMatches(ctx, entry.ProviderSecret, entry.ProviderSettings, entry.Hostname, recordType, pendingAddress)
		if err != nil {
			result := familySyncFailure(previousAddress, err)
			if result.providerKind == providerErrorTransient {
				result.status = syncStatusPending
			}
			return result
		}
		if !matches {
			return familySyncResult{status: syncStatusPending, address: previousAddress, providerKind: providerErrorPending}
		}
		confirmedAddress = pendingAddress.Unmap().String()
	}

	if resolveErr != nil {
		result := familySyncFailure("", fmt.Errorf("failed to resolve %s address: %w", family, resolveErr))
		result.publishedAddress = confirmedAddress
		result.publicationConfirmed = confirmedAddress != ""
		return result
	}
	if !address.IsValid() {
		result := familySyncFailure("", fmt.Errorf("no %s address resolved", family))
		result.publishedAddress = confirmedAddress
		result.publicationConfirmed = confirmedAddress != ""
		return result
	}
	addressValue := address.String()
	if recordType == dynamicDNSModels.RecordTypeA {
		addressValue = address.Unmap().String()
	}
	if !providerKnown {
		return familySyncFailure(addressValue, fmt.Errorf("DNS provider is unavailable"))
	}

	if confirmedAddress != "" && confirmedAddress == addressValue {
		return familySyncResult{status: syncStatusSuccess, address: addressValue, publishedAddress: addressValue, publicationConfirmed: true}
	}

	if hasStatus && confirmedAddress == "" {
		if published, err := netip.ParseAddr(publishedAddress); err == nil && published.Unmap() == address.Unmap() {
			return familySyncResult{status: syncStatusSuccess, address: addressValue, publishedAddress: addressValue}
		}

		matches, err := statusProvider.AddressMatches(ctx, entry.ProviderSecret, entry.ProviderSettings, entry.Hostname, recordType, address)
		if err != nil {
			return familySyncFailure(addressValue, err)
		}
		if matches {
			return familySyncResult{status: syncStatusSuccess, address: addressValue, publishedAddress: addressValue, publicationConfirmed: true}
		}
	}

	if err := provider.Upsert(ctx, entry.ProviderSecret, entry.ProviderSettings, entry.Hostname, recordType, address); err != nil {
		result := familySyncFailure(addressValue, err)
		result.publishedAddress = confirmedAddress
		result.publicationConfirmed = confirmedAddress != ""
		if result.providerKind == providerErrorPending {
			result.status = syncStatusPending
		}
		return result
	}
	return familySyncResult{status: syncStatusSuccess, address: addressValue, publishedAddress: addressValue, publicationConfirmed: true}
}

func familySyncFailure(address string, err error) familySyncResult {
	result := familySyncResult{status: syncStatusError, address: address, err: err}
	if kind, retryAfter, ok := providerErrorDetails(err); ok {
		result.providerKind = kind
		result.retryAfter = retryAfter
	}
	return result
}

func sameProviderTarget(first, second dynamicDNSModels.Entry) bool {
	return first.Provider == second.Provider &&
		first.Hostname == second.Hostname &&
		first.RecordType == second.RecordType &&
		sameSettings(first.ProviderSettings, second.ProviderSettings)
}

func preserveSyncState(target *dynamicDNSModels.Entry, existing dynamicDNSModels.Entry) {
	target.LastStatus = existing.LastStatus
	target.LastError = existing.LastError
	target.IPv4Status = existing.IPv4Status
	target.IPv4Error = existing.IPv4Error
	target.IPv6Status = existing.IPv6Status
	target.IPv6Error = existing.IPv6Error
	target.LastIPv4 = existing.LastIPv4
	target.LastIPv6 = existing.LastIPv6
	target.LastSyncAt = cloneTime(existing.LastSyncAt)
	target.LastSuccessAt = cloneTime(existing.LastSuccessAt)
	target.LastPublishedIPv4 = existing.LastPublishedIPv4
	target.LastPublishedIPv6 = existing.LastPublishedIPv6
	target.ConsecutiveFailures = existing.ConsecutiveFailures
	target.NextRetryAt = cloneTime(existing.NextRetryAt)
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

func providerValidationError(err error, secret string) error {
	detail := redactSecret(err.Error(), secret)
	if kind, _, ok := providerErrorDetails(err); ok && kind != providerErrorPermanent {
		return fmt.Errorf("%w: provider validation failed: %s", ErrProviderUnavailable, detail)
	}
	return invalidEntry("provider validation failed: %s", detail)
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
