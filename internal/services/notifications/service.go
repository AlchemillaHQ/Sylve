// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	hub "github.com/alchemillahq/sylve/internal/events"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultNtfyBaseURL = "https://ntfy.sh"
	defaultSMTPPort    = 587
	defaultListLimit   = 50
	maxListLimit       = 500
)

const (
	TransportTypeNtfy    = "ntfy"
	TransportTypeSMTP    = "smtp"
	TransportTypeDiscord = "discord"
)

const (
	RuleTemplateZFSPoolState   = "system.zfs.pool_state"
	ruleTemplateTargetTypePool = "pool"

	RuleTemplateDiskSmartTemperature = "system.disk.smart.temperature"
	RuleTemplateDiskSmartWearout     = "system.disk.smart.wearout"
	RuleTemplateDiskSmartHealth      = "system.disk.smart.health"
	RuleTemplateDiskSmartNvme        = "system.disk.smart.nvme"
	RuleTemplateDiskSmartSelfTest    = "system.disk.smart.selftest"
	ruleTemplateTargetTypeDisk       = "disk"

	diskSmartConfigTemperatureWarningCelsius  = "warningCelsius"
	diskSmartConfigTemperatureCriticalCelsius = "criticalCelsius"
	diskSmartConfigWearoutWarningPercent      = "warningPercent"
	diskSmartConfigWearoutCriticalPercent     = "criticalPercent"

	defaultTemperatureWarningCelsius  = 55.0
	defaultTemperatureCriticalCelsius = 65.0
	defaultWearoutWarningPercent      = 80.0
	defaultWearoutCriticalPercent     = 90.0
)

type ruleTemplateDefinition struct {
	View            RuleTemplateView
	AutoCreateRules bool
	ActiveTargets   map[string]struct{}
	TargetDevices   map[string]string
	DefaultConfig   string
}

type diskSmartRuleConfig struct {
	WarningCelsius  float64 `json:"warningCelsius"`
	CriticalCelsius float64 `json:"criticalCelsius"`
	WarningPercent  float64 `json:"warningPercent"`
	CriticalPercent float64 `json:"criticalPercent"`
}

type NtfySender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error

type EmailSender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error

type DiscordSender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, webhookURL string) error

type Service struct {
	DB          *gorm.DB
	DiskService diskServiceInterfaces.DiskServiceInterface
	httpClient  *http.Client
	now         func() time.Time

	ntfySender    NtfySender
	emailSender   EmailSender
	discordSender DiscordSender

	legacyDiskSmartMigrationMu   sync.Mutex
	legacyDiskSmartMigrationDone bool
	diskInventoryMu              sync.Mutex
	diskInventoryCache           []diskServiceInterfaces.Disk
	diskInventoryExpiresAt       time.Time
}

type diskInventoryProvider interface {
	GetDiskDevicesInventory(ctx context.Context) ([]diskServiceInterfaces.Disk, error)
}

type diskSmartIdentityAlias struct {
	device string
	key    string
}

type ListScope string

const (
	ListScopeActive ListScope = "active"
	ListScopeAll    ListScope = "all"
)

type TransportConfigView struct {
	Transports []TransportConfigEntryView `json:"transports"`
}

type TransportConfigEntryView struct {
	ID      uint                        `json:"id"`
	Name    string                      `json:"name"`
	Type    string                      `json:"type"`
	Enabled bool                        `json:"enabled"`
	Ntfy    *NtfyTransportConfigView    `json:"ntfy,omitempty"`
	Email   *EmailTransportConfigView   `json:"email,omitempty"`
	Discord *DiscordTransportConfigView `json:"discord,omitempty"`
}

type NtfyTransportConfigView struct {
	BaseURL      string `json:"baseUrl"`
	Topic        string `json:"topic"`
	HasAuthToken bool   `json:"hasAuthToken"`
}

type EmailTransportConfigView struct {
	SMTPHost     string   `json:"smtpHost"`
	SMTPPort     int      `json:"smtpPort"`
	SMTPUsername string   `json:"smtpUsername"`
	SMTPFrom     string   `json:"smtpFrom"`
	SMTPUseTLS   bool     `json:"smtpUseTls"`
	Recipients   []string `json:"recipients"`
	HasPassword  bool     `json:"hasPassword"`
}

type DiscordTransportConfigView struct {
	WebhookURL string `json:"webhookUrl"`
}

type TransportConfigUpdate struct {
	Transports []TransportConfigEntryUpdate `json:"transports"`
}

type TransportConfigEntryUpdate struct {
	ID      uint                          `json:"id"`
	Name    string                        `json:"name"`
	Type    string                        `json:"type"`
	Enabled bool                          `json:"enabled"`
	Ntfy    *NtfyTransportConfigUpdate    `json:"ntfy,omitempty"`
	Email   *EmailTransportConfigUpdate   `json:"email,omitempty"`
	Discord *DiscordTransportConfigUpdate `json:"discord,omitempty"`
}

type NtfyTransportConfigUpdate struct {
	BaseURL   string  `json:"baseUrl"`
	Topic     string  `json:"topic"`
	AuthToken *string `json:"authToken,omitempty"`
}

type EmailTransportConfigUpdate struct {
	SMTPHost     string   `json:"smtpHost"`
	SMTPPort     int      `json:"smtpPort"`
	SMTPUsername string   `json:"smtpUsername"`
	SMTPFrom     string   `json:"smtpFrom"`
	SMTPUseTLS   bool     `json:"smtpUseTls"`
	Recipients   []string `json:"recipients"`
	SMTPPassword *string  `json:"smtpPassword,omitempty"`
}

type DiscordTransportConfigUpdate struct {
	WebhookURL *string `json:"webhookUrl,omitempty"`
}

type RuleConfigView struct {
	Rules     []RuleConfigEntryView `json:"rules"`
	Templates []RuleTemplateView    `json:"templates"`
}

type RuleConfigEntryView struct {
	ID             uint   `json:"id"`
	Kind           string `json:"kind"`
	TemplateKey    string `json:"templateKey"`
	TemplateLabel  string `json:"templateLabel"`
	TargetKey      string `json:"targetKey"`
	TargetLabel    string `json:"targetLabel"`
	Active         bool   `json:"active"`
	UIEnabled      bool   `json:"uiEnabled"`
	NtfyEnabled    bool   `json:"ntfyEnabled"`
	EmailEnabled   bool   `json:"emailEnabled"`
	DiscordEnabled bool   `json:"discordEnabled"`
	Config         string `json:"config"`
}

type RuleTemplateView struct {
	Key           string                   `json:"key"`
	Label         string                   `json:"label"`
	Description   string                   `json:"description"`
	TargetType    string                   `json:"targetType"`
	Targets       []RuleTemplateTargetView `json:"targets"`
	DefaultConfig string                   `json:"defaultConfig,omitempty"`
}

type RuleTemplateTargetView struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type RuleConfigUpdate struct {
	Rules []RuleConfigEntryUpdate `json:"rules"`
}

type RuleConfigEntryUpdate struct {
	ID             uint   `json:"id"`
	Kind           string `json:"kind"`
	Pool           string `json:"pool"`
	TemplateKey    string `json:"templateKey"`
	TargetKey      string `json:"targetKey"`
	UIEnabled      bool   `json:"uiEnabled"`
	NtfyEnabled    bool   `json:"ntfyEnabled"`
	EmailEnabled   bool   `json:"emailEnabled"`
	DiscordEnabled bool   `json:"discordEnabled"`
}

type RuleCreateInput struct {
	TemplateKey    string `json:"templateKey"`
	TargetKey      string `json:"targetKey"`
	UIEnabled      bool   `json:"uiEnabled"`
	NtfyEnabled    bool   `json:"ntfyEnabled"`
	EmailEnabled   bool   `json:"emailEnabled"`
	DiscordEnabled bool   `json:"discordEnabled"`
}

type RuleUpdateInput struct {
	UIEnabled      bool   `json:"uiEnabled"`
	NtfyEnabled    bool   `json:"ntfyEnabled"`
	EmailEnabled   bool   `json:"emailEnabled"`
	DiscordEnabled bool   `json:"discordEnabled"`
	Config         string `json:"config"`
}

type TestRuleInput struct {
	TemplateKey string `json:"templateKey"`
	TargetKey   string `json:"targetKey"`
	Condition   string `json:"condition"`
	Severity    string `json:"severity"`
}

func NewService(db *gorm.DB) *Service {
	s := &Service{
		DB: db,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		now: time.Now,
	}

	s.ntfySender = s.sendNtfy
	s.emailSender = s.sendEmail
	s.discordSender = s.sendDiscord

	return s
}

func (s *Service) SetDiskService(ds diskServiceInterfaces.DiskServiceInterface) {
	s.DiskService = ds
	s.diskInventoryMu.Lock()
	s.diskInventoryCache = nil
	s.diskInventoryExpiresAt = time.Time{}
	s.diskInventoryMu.Unlock()
}

func (s *Service) MigrateLegacyDiskSmartRecords(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("notifications_service_not_initialized")
	}
	aliases := s.diskSmartIdentityAliases(ctx)
	s.legacyDiskSmartMigrationMu.Lock()
	defer s.legacyDiskSmartMigrationMu.Unlock()
	if s.legacyDiskSmartMigrationDone {
		return nil
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.migrateLegacyDiskSmartSelfTestKinds(tx); err != nil {
			return err
		}
		if err := s.migrateDiskSmartIdentityAliases(tx, aliases); err != nil {
			return err
		}
		return s.migrateLegacyDiskSmartNotificationConditions(tx, aliases)
	})
	if err == nil {
		s.legacyDiskSmartMigrationDone = true
	}
	return err
}

func (s *Service) diskSmartIdentityAliases(ctx context.Context) []diskSmartIdentityAlias {
	if s == nil || s.DiskService == nil {
		return nil
	}
	disks, err := s.loadDiskInventory(ctx)
	if err != nil {
		return nil
	}
	aliases := make([]diskSmartIdentityAlias, 0, len(disks))
	for _, disk := range disks {
		device := normalizeRuleTargetKey(disk.Device)
		key := normalizeRuleTargetKey(disk.UUID)
		if !disk.IdentityStable || device == "" || key == "" || device == key {
			continue
		}
		aliases = append(aliases, diskSmartIdentityAlias{device: device, key: key})
	}
	return aliases
}

func (s *Service) loadDiskInventory(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	if s == nil || s.DiskService == nil {
		return nil, nil
	}
	s.diskInventoryMu.Lock()
	defer s.diskInventoryMu.Unlock()
	now := time.Now()
	if now.Before(s.diskInventoryExpiresAt) {
		return append([]diskServiceInterfaces.Disk(nil), s.diskInventoryCache...), nil
	}
	var disks []diskServiceInterfaces.Disk
	var err error
	if inventory, ok := s.DiskService.(diskInventoryProvider); ok {
		disks, err = inventory.GetDiskDevicesInventory(ctx)
	} else {
		disks, err = s.DiskService.GetDiskDevices(ctx)
	}
	if err != nil {
		return nil, err
	}
	s.diskInventoryCache = append([]diskServiceInterfaces.Disk(nil), disks...)
	s.diskInventoryExpiresAt = now.Add(30 * time.Second)
	return append([]diskServiceInterfaces.Disk(nil), disks...), nil
}

func (s *Service) SetNtfySender(sender NtfySender) {
	if sender == nil {
		s.ntfySender = s.sendNtfy
		return
	}

	s.ntfySender = sender
}

func (s *Service) SetEmailSender(sender EmailSender) {
	if sender == nil {
		s.emailSender = s.sendEmail
		return
	}

	s.emailSender = sender
}

func (s *Service) SetDiscordSender(sender DiscordSender) {
	if sender == nil {
		s.discordSender = s.sendDiscord
		return
	}

	s.discordSender = sender
}

func (s *Service) Emit(ctx context.Context, input notifier.EventInput) (notifier.EmitResult, error) {
	if s == nil || s.DB == nil {
		return notifier.EmitResult{}, fmt.Errorf("notifications_service_not_initialized")
	}

	normalized := normalizeInput(input)
	if normalized.Kind == "" {
		return notifier.EmitResult{}, fmt.Errorf("notification_kind_required")
	}
	if normalized.Title == "" {
		return notifier.EmitResult{}, fmt.Errorf("notification_title_required")
	}
	if normalized.Fingerprint == "" {
		normalized.Fingerprint = makeFingerprint(normalized)
	}

	now := s.now().UTC()

	result := notifier.EmitResult{}
	var kindRule models.NotificationKindRule
	canSuppress := shouldPersistSuppressionForKind(normalized.Kind)
	uiSelected := notificationChannelSelected(normalized.Channels, notifier.ChannelUI)
	ntfySelected := notificationChannelSelected(normalized.Channels, notifier.ChannelNtfy)
	emailSelected := notificationChannelSelected(normalized.Channels, notifier.ChannelEmail)
	discordSelected := notificationChannelSelected(normalized.Channels, notifier.ChannelDiscord)

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if prefix, target, ok := notifier.DiskNameFromSmartKind(normalized.Kind); ok {
			device := normalizeRuleTargetKey(normalized.Metadata["device"])
			diskKey := normalizeRuleTargetKey(normalized.Metadata["disk_key"])
			if device != "" && diskKey != "" && device != diskKey && normalizeRuleTargetKey(target) == diskKey {
				if err := s.migrateDiskSmartKindAlias(tx, prefix, diskSmartIdentityAlias{device: device, key: diskKey}); err != nil {
					return err
				}
			}
		}

		kindRule, err = s.ensureKindRule(tx, normalized.Kind, "")
		if err != nil {
			return err
		}
		result.UIHandled = uiSelected
		if kindRule.UserDisabled {
			return nil
		}

		if canSuppress {
			suppressionFingerprint := suppressionKey(normalized.Kind, normalized.Fingerprint)

			var suppression models.NotificationSuppression
			err = tx.
				Where("kind = ?", normalized.Kind).
				Where("fingerprint = ?", suppressionFingerprint).
				First(&suppression).Error
			if err == nil {
				result.Suppressed = true
				return nil
			}
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
		}

		if uiSelected && kindRule.UIEnabled {
			var existing models.Notification
			err = tx.Where("fingerprint = ?", normalized.Fingerprint).First(&existing).Error
			if err == nil {
				existing.Kind = normalized.Kind
				existing.Title = normalized.Title
				existing.Body = normalized.Body
				existing.Severity = models.NotificationSeverity(normalized.Severity)
				existing.Source = normalized.Source
				existing.Metadata = normalized.Metadata
				existing.LastOccurredAt = now
				existing.OccurrenceCount++
				existing.DismissedAt = nil
				existing.UpdatedAt = now

				if updateErr := tx.Save(&existing).Error; updateErr != nil {
					return updateErr
				}

				result.NotificationID = existing.ID
			} else if err == gorm.ErrRecordNotFound {
				rec := models.Notification{
					Kind:            normalized.Kind,
					Title:           normalized.Title,
					Body:            normalized.Body,
					Severity:        models.NotificationSeverity(normalized.Severity),
					Source:          normalized.Source,
					Fingerprint:     normalized.Fingerprint,
					Metadata:        normalized.Metadata,
					OccurrenceCount: 1,
					FirstOccurredAt: now,
					LastOccurredAt:  now,
				}

				if createErr := tx.Create(&rec).Error; createErr != nil {
					return createErr
				}

				result.NotificationID = rec.ID
			} else {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return notifier.EmitResult{}, err
	}
	if kindRule.UserDisabled {
		return result, nil
	}

	if result.Suppressed {
		return result, nil
	}

	if uiSelected && kindRule.UIEnabled {
		s.publishRefresh()
	}
	if !ntfySelected && !emailSelected && !discordSelected {
		return result, nil
	}

	transportConfigs, err := s.listDeliveryTransportConfigs(ctx, normalized.TransportID)
	if err != nil {
		return result, err
	}
	result.TransportConfigLoaded = true

	var transportErr error
	for _, cfg := range transportConfigs {
		if normalized.TransportID != 0 && cfg.ID != normalized.TransportID {
			continue
		}
		switch normalizeTransportType(cfg.Type) {
		case TransportTypeNtfy:
			if !ntfySelected || !cfg.NtfyEnabled || !kindRule.NtfyEnabled {
				continue
			}
			token := strings.TrimSpace(cfg.NtfyAuthToken)
			result.AttemptedNtfy = true
			if err := s.ntfySender(ctx, cfg, normalized, token); err == nil {
				result.SentNtfy = true
			} else {
				result.FailedNtfy = true
				transportErr = err
			}
		case TransportTypeSMTP:
			if !emailSelected || !cfg.EmailEnabled || !kindRule.EmailEnabled || len(cfg.EmailRecipients) == 0 {
				continue
			}
			password := strings.TrimSpace(cfg.SMTPPassword)
			result.AttemptedEmail = true
			if err := s.emailSender(ctx, cfg, normalized, password); err == nil {
				result.SentEmail = true
			} else {
				result.FailedEmail = true
				transportErr = err
			}
		case TransportTypeDiscord:
			if !discordSelected || !cfg.DiscordEnabled || !kindRule.DiscordEnabled {
				continue
			}
			webhookURL := strings.TrimSpace(cfg.DiscordWebhookURL)
			if webhookURL == "" {
				continue
			}
			result.AttemptedDiscord = true
			if err := s.discordSender(ctx, cfg, normalized, webhookURL); err == nil {
				result.SentDiscord = true
			} else {
				result.FailedDiscord = true
				transportErr = err
			}
		}
	}
	if result.FailedNtfy || result.FailedEmail || result.FailedDiscord {
		return result, fmt.Errorf("notification_delivery_failed: %w", transportErr)
	}

	return result, nil
}

func (s *Service) DeliveryTargets(ctx context.Context, input notifier.EventInput) ([]string, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("notifications_service_not_initialized")
	}

	normalized := normalizeInput(input)
	if normalized.Kind == "" {
		return nil, fmt.Errorf("notification_kind_required")
	}

	var kindRule models.NotificationKindRule
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if prefix, target, ok := notifier.DiskNameFromSmartKind(normalized.Kind); ok {
			device := normalizeRuleTargetKey(normalized.Metadata["device"])
			diskKey := normalizeRuleTargetKey(normalized.Metadata["disk_key"])
			if device != "" && diskKey != "" && device != diskKey && normalizeRuleTargetKey(target) == diskKey {
				if err := s.migrateDiskSmartKindAlias(tx, prefix, diskSmartIdentityAlias{device: device, key: diskKey}); err != nil {
					return err
				}
			}
		}

		var err error
		kindRule, err = s.ensureKindRule(tx, normalized.Kind, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	if kindRule.UserDisabled {
		return []string{}, nil
	}

	targets := make([]string, 0, 4)
	if kindRule.UIEnabled {
		targets = append(targets, notifier.ChannelUI)
	}
	if !kindRule.NtfyEnabled && !kindRule.EmailEnabled && !kindRule.DiscordEnabled {
		return targets, nil
	}

	configs, err := s.listTransportConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, cfg := range configs {
		switch normalizeTransportType(cfg.Type) {
		case TransportTypeNtfy:
			if kindRule.NtfyEnabled && cfg.NtfyEnabled {
				targets = append(targets, notificationDeliveryTarget(notifier.ChannelNtfy, cfg.ID))
			}
		case TransportTypeSMTP:
			if kindRule.EmailEnabled && cfg.EmailEnabled && len(cfg.EmailRecipients) > 0 {
				targets = append(targets, notificationDeliveryTarget(notifier.ChannelEmail, cfg.ID))
			}
		case TransportTypeDiscord:
			if kindRule.DiscordEnabled && cfg.DiscordEnabled && strings.TrimSpace(cfg.DiscordWebhookURL) != "" {
				targets = append(targets, notificationDeliveryTarget(notifier.ChannelDiscord, cfg.ID))
			}
		}
	}

	return targets, nil
}

func (s *Service) EmitTarget(ctx context.Context, input notifier.EventInput, target string) (notifier.EmitResult, error) {
	channel, transportID, err := parseNotificationDeliveryTarget(target)
	if err != nil {
		return notifier.EmitResult{}, err
	}
	if channel == "all" {
		input.Channels = nil
		input.TransportID = 0
		return s.Emit(ctx, input)
	}
	input.Channels = []string{channel}
	input.TransportID = transportID
	return s.Emit(ctx, input)
}

func notificationDeliveryTarget(channel string, transportID uint) string {
	return channel + ":" + strconv.FormatUint(uint64(transportID), 10)
}

func parseNotificationDeliveryTarget(target string) (string, uint, error) {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "all" || target == notifier.ChannelUI {
		return target, 0, nil
	}
	channel, idValue, ok := strings.Cut(target, ":")
	if !ok || channel != notifier.ChannelNtfy && channel != notifier.ChannelEmail && channel != notifier.ChannelDiscord {
		return "", 0, fmt.Errorf("invalid_notification_delivery_target")
	}
	id, err := strconv.ParseUint(idValue, 10, 64)
	if err != nil || id == 0 {
		return "", 0, fmt.Errorf("invalid_notification_delivery_target")
	}
	return channel, uint(id), nil
}

func (s *Service) List(ctx context.Context, scope ListScope, limit, offset int) ([]models.Notification, int64, error) {
	if s == nil || s.DB == nil {
		return nil, 0, fmt.Errorf("notifications_service_not_initialized")
	}

	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	if offset < 0 {
		offset = 0
	}

	q := s.DB.WithContext(ctx).Model(&models.Notification{})
	if scope != ListScopeAll {
		q = q.Where("dismissed_at IS NULL")
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []models.Notification
	if err := q.Order("last_occurred_at DESC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

func (s *Service) CountActive(ctx context.Context) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("notifications_service_not_initialized")
	}

	var count int64
	err := s.DB.WithContext(ctx).
		Model(&models.Notification{}).
		Where("dismissed_at IS NULL").
		Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (s *Service) Dismiss(ctx context.Context, id uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("notifications_service_not_initialized")
	}

	now := s.now().UTC()

	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var notif models.Notification
		if err := tx.First(&notif, id).Error; err != nil {
			return err
		}

		return s.dismissNotification(tx, notif, now)
	}); err != nil {
		return err
	}

	s.publishRefresh()
	return nil
}

func (s *Service) DismissAll(ctx context.Context) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("notifications_service_not_initialized")
	}

	now := s.now().UTC()
	var dismissed int64

	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var notifications []models.Notification
		if err := tx.Where("dismissed_at IS NULL").Find(&notifications).Error; err != nil {
			return err
		}

		for _, notification := range notifications {
			if err := s.dismissNotification(tx, notification, now); err != nil {
				return err
			}
			dismissed++
		}

		return nil
	}); err != nil {
		return 0, err
	}

	if dismissed > 0 {
		s.publishRefresh()
	}

	return dismissed, nil
}

func (s *Service) dismissNotification(tx *gorm.DB, notification models.Notification, now time.Time) error {
	if notification.DismissedAt == nil {
		if err := tx.Model(&models.Notification{}).Where("id = ?", notification.ID).Updates(map[string]any{
			"dismissed_at": now,
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}
	}

	if shouldPersistSuppressionForKind(notification.Kind) {
		suppression := models.NotificationSuppression{
			Fingerprint: suppressionKey(notification.Kind, notification.Fingerprint),
			Kind:        notification.Kind,
		}

		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&suppression).Error; err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) DeleteTransport(ctx context.Context, id uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("notifications_service_not_initialized")
	}
	if id == 0 {
		return fmt.Errorf("invalid_transport_id")
	}

	result := s.DB.WithContext(ctx).Delete(&models.NotificationTransportConfig{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

func (s *Service) TestTransport(ctx context.Context, id uint) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("notifications_service_not_initialized")
	}
	if id == 0 {
		return fmt.Errorf("invalid_transport_id")
	}

	cfg, err := s.resolveTransportForUpdate(s.DB.WithContext(ctx), id)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	input := notifier.EventInput{
		Kind:        "system.notifications.test",
		Title:       "Sylve Notification Test",
		Body:        fmt.Sprintf("This is a test notification sent at %s.", now.Format(time.RFC3339)),
		Severity:    string(models.NotificationSeverityInfo),
		Source:      "settings.notifications",
		Fingerprint: fmt.Sprintf("transport-test-%d-%d", id, now.UnixNano()),
		Metadata: map[string]string{
			"transportId": strconv.FormatUint(uint64(id), 10),
		},
	}

	switch normalizeTransportType(cfg.Type) {
	case TransportTypeNtfy:
		token := strings.TrimSpace(cfg.NtfyAuthToken)
		return s.ntfySender(ctx, cfg, input, token)
	case TransportTypeSMTP:
		password := strings.TrimSpace(cfg.SMTPPassword)
		return s.emailSender(ctx, cfg, input, password)
	case TransportTypeDiscord:
		webhookURL := strings.TrimSpace(cfg.DiscordWebhookURL)
		if webhookURL == "" {
			return fmt.Errorf("discord_webhook_url_required")
		}
		return s.discordSender(ctx, cfg, input, webhookURL)
	default:
		return fmt.Errorf("invalid_transport_type")
	}
}

func (s *Service) TestRule(ctx context.Context, input TestRuleInput) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("notifications_service_not_initialized")
	}

	templateKey := normalizeRuleTemplateKey(input.TemplateKey)
	if templateKey == "" {
		return fmt.Errorf("notification_rule_template_required")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	definitions, _, err := s.loadRuleTemplateDefinitions(s.DB.WithContext(ctx), diskDefinitions)
	if err != nil {
		return err
	}

	var definition *ruleTemplateDefinition
	for _, def := range definitions {
		if def.View.Key == templateKey {
			definition = def
			break
		}
	}
	if definition == nil {
		return fmt.Errorf("notification_rule_template_not_found")
	}
	if len(definition.View.Targets) == 0 {
		return fmt.Errorf("notification_rule_no_targets")
	}

	targetKey := normalizeRuleTargetKey(input.TargetKey)
	if targetKey == "" {
		targetKey = definition.View.Targets[0].Key
	}
	if _, active := definition.ActiveTargets[targetKey]; !active {
		return fmt.Errorf("notification_rule_target_not_found")
	}

	now := s.now().UTC()
	condition := strings.TrimSpace(input.Condition)
	if condition == "" {
		condition = defaultTestConditionForTemplate(templateKey)
	}

	kind, err := ruleKindForTemplateTarget(templateKey, targetKey)
	if err != nil {
		return err
	}

	event := buildTestEventInput(templateKey, targetKey, kind, condition, now)
	if device := definition.TargetDevices[targetKey]; device != "" && device != targetKey {
		event.Title = strings.ReplaceAll(event.Title, targetKey, device)
		event.Body = strings.ReplaceAll(event.Body, targetKey, device)
		if event.Metadata == nil {
			event.Metadata = make(map[string]string)
		}
		event.Metadata["device"] = device
		event.Metadata["disk_key"] = targetKey
	}
	if input.Severity != "" {
		event.Severity = normalizeSeverity(input.Severity)
	}

	_, err = notifier.Emit(ctx, event)
	return err
}

func buildTestEventInput(templateKey, targetKey, kind, condition string, now time.Time) notifier.EventInput {
	switch condition {
	case "temperature_warning":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s temperature high: 60 C", targetKey),
			Body:     fmt.Sprintf("Temperature 60 C exceeds warning threshold configured for disk %s.", targetKey),
			Severity: string(models.NotificationSeverityWarning), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "temperature": "60"},
		}
	case "temperature_critical":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s temperature critical: 70 C", targetKey),
			Body:     fmt.Sprintf("Temperature 70 C exceeds critical threshold configured for disk %s.", targetKey),
			Severity: string(models.NotificationSeverityCritical), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "temperature": "70"},
		}
	case "wearout_warning":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s wear-out high: 85.0%%", targetKey),
			Body:     fmt.Sprintf("Wear-out of 85.0%% exceeds warning threshold configured for disk %s.", targetKey),
			Severity: string(models.NotificationSeverityWarning), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "wearout": "85.0"},
		}
	case "wearout_critical":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s wear-out critical: 95.0%%", targetKey),
			Body:     fmt.Sprintf("Wear-out of 95.0%% exceeds critical threshold configured for disk %s.", targetKey),
			Severity: string(models.NotificationSeverityCritical), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "wearout": "95.0"},
		}
	case "health_failed":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s S.M.A.R.T health check FAILED", targetKey),
			Body:     fmt.Sprintf("The overall S.M.A.R.T health assessment for disk %s indicates failure.", targetKey),
			Severity: string(models.NotificationSeverityCritical), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true"},
		}
	case "sector_issues":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s has sector issues", targetKey),
			Body:     fmt.Sprintf("Sector anomalies detected on disk %s: reallocated=5, pending=2.", targetKey),
			Severity: string(models.NotificationSeverityWarning), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "reallocated": "5", "pending": "2"},
		}
	case "nvme_warning":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s NVMe S.M.A.R.T warning", targetKey),
			Body:     fmt.Sprintf("NVMe S.M.A.R.T issues on disk %s: critical_warning=0x08; available_spare=5%%, threshold=10%%.", targetKey),
			Severity: string(models.NotificationSeverityWarning), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true", "critical_warning": "0x08"},
		}
	case "self_test_failed":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s self-test failed", targetKey),
			Body:     fmt.Sprintf("The most recent self-test on disk %s reported a failure.", targetKey),
			Severity: string(models.NotificationSeverityCritical), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true"},
		}
	case "self_test_passed":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("Disk %s self-test passed", targetKey),
			Body:     fmt.Sprintf("The most recent self-test on disk %s completed successfully.", targetKey),
			Severity: string(models.NotificationSeverityInfo), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"device": targetKey, "condition": condition, "test": "true"},
		}
	case "pool_degraded":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("ZFS pool %s vdev pool is DEGRADED", targetKey),
			Body:     fmt.Sprintf("ZFS state-change detected for pool %s: vdev pool is now DEGRADED.", targetKey),
			Severity: string(models.NotificationSeverityWarning), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"pool": targetKey, "state": "DEGRADED", "test": "true"},
		}
	case "pool_faulted":
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("ZFS pool %s vdev pool is FAULTED", targetKey),
			Body:     fmt.Sprintf("ZFS state-change detected for pool %s: vdev pool is now FAULTED.", targetKey),
			Severity: string(models.NotificationSeverityCritical), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"pool": targetKey, "state": "FAULTED", "test": "true"},
		}
	default:
		return notifier.EventInput{
			Kind: kind, Title: fmt.Sprintf("[TEST] %s / %s", templateKey, targetKey),
			Body:     fmt.Sprintf("This is a test notification for template %s on target %s sent at %s.", templateKey, targetKey, now.Format(time.RFC3339)),
			Severity: string(models.NotificationSeverityInfo), Source: "settings.notifications.test",
			Fingerprint: fmt.Sprintf("test-%s-%s-%d", targetKey, condition, now.UnixNano()),
			Metadata:    map[string]string{"test": "true"},
		}
	}
}

func defaultTestConditionForTemplate(templateKey string) string {
	switch templateKey {
	case RuleTemplateDiskSmartTemperature:
		return "temperature_warning"
	case RuleTemplateDiskSmartWearout:
		return "wearout_warning"
	case RuleTemplateDiskSmartHealth:
		return "health_failed"
	case RuleTemplateDiskSmartNvme:
		return "nvme_warning"
	case RuleTemplateDiskSmartSelfTest:
		return "self_test_failed"
	case RuleTemplateZFSPoolState:
		return "pool_degraded"
	default:
		return ""
	}
}

func (s *Service) GetTransportConfig(ctx context.Context) (TransportConfigView, error) {
	if s == nil || s.DB == nil {
		return TransportConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}

	configs, err := s.ensureTransportConfigsDB(ctx)
	if err != nil {
		return TransportConfigView{}, err
	}

	return s.toTransportConfigView(configs), nil
}

func (s *Service) UpdateTransportConfig(ctx context.Context, input TransportConfigUpdate) (TransportConfigView, error) {
	if s == nil || s.DB == nil {
		return TransportConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}

	entries := append([]TransportConfigEntryUpdate{}, input.Transports...)

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		keepIDs := make(map[uint]struct{}, len(entries))

		for _, entry := range entries {
			transportType := normalizeTransportType(entry.Type)
			if transportType != TransportTypeNtfy && transportType != TransportTypeSMTP && transportType != TransportTypeDiscord {
				return fmt.Errorf("invalid_transport_type")
			}

			cfg, err := s.resolveTransportForUpdate(tx, entry.ID)
			if err != nil {
				return err
			}

			cfg.Name = strings.TrimSpace(entry.Name)
			if cfg.Name == "" {
				return fmt.Errorf("transport_name_required")
			}
			cfg.Type = transportType

			if cfg.ID == 0 {
				if err := tx.Create(&cfg).Error; err != nil {
					return err
				}
			}

			switch transportType {
			case TransportTypeNtfy:
				if entry.Ntfy == nil {
					return fmt.Errorf("ntfy_config_required")
				}

				cfg.NtfyEnabled = entry.Enabled
				cfg.NtfyBaseURL = normalizeNtfyBaseURL(entry.Ntfy.BaseURL)
				cfg.NtfyTopic = strings.TrimSpace(entry.Ntfy.Topic)
				cfg.EmailEnabled = false
				cfg.SMTPHost = ""
				cfg.SMTPPort = defaultSMTPPort
				cfg.SMTPUsername = ""
				cfg.SMTPFrom = ""
				cfg.SMTPUseTLS = true
				cfg.EmailRecipients = []string{}
				cfg.SMTPPassword = ""
				cfg.DiscordEnabled = false
				cfg.DiscordWebhookURL = ""

				if entry.Ntfy.AuthToken != nil {
					cfg.NtfyAuthToken = strings.TrimSpace(*entry.Ntfy.AuthToken)
				}
			case TransportTypeSMTP:
				if entry.Email == nil {
					return fmt.Errorf("smtp_config_required")
				}

				normalizedRecipients, err := normalizeRecipients(entry.Email.Recipients)
				if err != nil {
					return err
				}
				cfg.EmailEnabled = entry.Enabled
				cfg.SMTPHost = strings.TrimSpace(entry.Email.SMTPHost)
				cfg.SMTPPort = entry.Email.SMTPPort
				if cfg.SMTPPort <= 0 {
					cfg.SMTPPort = defaultSMTPPort
				}
				cfg.SMTPUsername = strings.TrimSpace(entry.Email.SMTPUsername)
				cfg.SMTPFrom = strings.TrimSpace(entry.Email.SMTPFrom)
				if cfg.SMTPFrom != "" && !utils.IsValidEmail(cfg.SMTPFrom) {
					return fmt.Errorf("invalid_smtp_from_email")
				}
				cfg.SMTPUseTLS = entry.Email.SMTPUseTLS
				cfg.EmailRecipients = normalizedRecipients
				cfg.NtfyEnabled = false
				cfg.NtfyBaseURL = defaultNtfyBaseURL
				cfg.NtfyTopic = ""
				cfg.NtfyAuthToken = ""
				cfg.DiscordEnabled = false
				cfg.DiscordWebhookURL = ""

				if entry.Email.SMTPPassword != nil {
					cfg.SMTPPassword = strings.TrimSpace(*entry.Email.SMTPPassword)
				}
			case TransportTypeDiscord:
				if entry.Discord == nil {
					return fmt.Errorf("discord_config_required")
				}

				cfg.DiscordEnabled = entry.Enabled
				cfg.NtfyEnabled = false
				cfg.NtfyBaseURL = defaultNtfyBaseURL
				cfg.NtfyTopic = ""
				cfg.NtfyAuthToken = ""
				cfg.EmailEnabled = false
				cfg.SMTPHost = ""
				cfg.SMTPPort = defaultSMTPPort
				cfg.SMTPUsername = ""
				cfg.SMTPFrom = ""
				cfg.SMTPUseTLS = true
				cfg.EmailRecipients = []string{}
				cfg.SMTPPassword = ""

				if entry.Discord.WebhookURL != nil {
					webhookURL := strings.TrimSpace(*entry.Discord.WebhookURL)
					if webhookURL != "" && !strings.HasPrefix(webhookURL, "https://discord.com/api/webhooks/") {
						return fmt.Errorf("invalid_discord_webhook_url")
					}
					cfg.DiscordWebhookURL = webhookURL
				}
			default:
				return fmt.Errorf("invalid_transport_type")
			}

			if err := tx.Save(&cfg).Error; err != nil {
				return err
			}

			keepIDs[cfg.ID] = struct{}{}
		}

		var existing []models.NotificationTransportConfig
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		for _, cfg := range existing {
			if _, keep := keepIDs[cfg.ID]; keep {
				continue
			}
			if err := tx.Delete(&cfg).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return TransportConfigView{}, err
	}

	updated, err := s.listTransportConfigs(ctx)
	if err != nil {
		return TransportConfigView{}, err
	}

	return s.toTransportConfigView(updated), nil
}

func (s *Service) GetRuleConfig(ctx context.Context) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}

	var view RuleConfigView
	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, definitionsByKey, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		rules, err := s.listManagedRuleRows(tx)
		if err != nil {
			return err
		}

		view = s.buildRuleConfigView(definitions, definitionsByKey, rules)
		return nil
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return view, nil
}

func (s *Service) UpdateRuleConfig(ctx context.Context, input RuleConfigUpdate) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}

	entries := append([]RuleConfigEntryUpdate{}, input.Rules...)
	var view RuleConfigView
	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, definitionsByKey, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		storedRules, err := s.listManagedRuleRows(tx)
		if err != nil {
			return err
		}

		rulesByID := make(map[uint]*models.NotificationKindRule, len(storedRules))
		rulesByKind := make(map[string]*models.NotificationKindRule, len(storedRules))
		for idx := range storedRules {
			rule := &storedRules[idx]
			rulesByID[rule.ID] = rule
			rulesByKind[strings.TrimSpace(strings.ToLower(rule.Kind))] = rule
		}

		seenIDs := make(map[uint]struct{}, len(entries))
		seenKinds := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			var rule *models.NotificationKindRule

			if entry.ID > 0 {
				if _, exists := seenIDs[entry.ID]; exists {
					return fmt.Errorf("duplicate_notification_rule_id")
				}
				seenIDs[entry.ID] = struct{}{}

				matched, ok := rulesByID[entry.ID]
				if !ok {
					return fmt.Errorf("notification_rule_not_found")
				}
				rule = matched
			} else {
				kind, err := s.resolveRuleUpdateKind(entry)
				if err != nil {
					return err
				}
				if _, exists := seenKinds[kind]; exists {
					return fmt.Errorf("duplicate_notification_rule_kind")
				}
				seenKinds[kind] = struct{}{}

				matched, ok := rulesByKind[kind]
				if !ok {
					return fmt.Errorf("notification_rule_not_found")
				}
				rule = matched
			}

			rule.UIEnabled = entry.UIEnabled
			rule.NtfyEnabled = entry.NtfyEnabled
			rule.EmailEnabled = entry.EmailEnabled
			rule.DiscordEnabled = entry.DiscordEnabled
			if err := tx.Save(rule).Error; err != nil {
				return err
			}
		}

		updatedRules, err := s.listManagedRuleRows(tx)
		if err != nil {
			return err
		}
		view = s.buildRuleConfigView(definitions, definitionsByKey, updatedRules)

		return nil
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return view, nil
}

func (s *Service) CreateRule(ctx context.Context, input RuleCreateInput) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}

	templateKey := normalizeRuleTemplateKey(input.TemplateKey)
	targetKey := normalizeRuleTargetKey(input.TargetKey)
	if templateKey == "" {
		return RuleConfigView{}, fmt.Errorf("notification_rule_template_required")
	}
	if targetKey == "" {
		return RuleConfigView{}, fmt.Errorf("notification_rule_target_required")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, definitionsByKey, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		definition, exists := definitionsByKey[templateKey]
		if !exists {
			return fmt.Errorf("notification_rule_template_not_found")
		}
		if _, exists := definition.ActiveTargets[targetKey]; !exists {
			return fmt.Errorf("notification_rule_target_not_found")
		}

		kind, err := ruleKindForTemplateTarget(templateKey, targetKey)
		if err != nil {
			return err
		}

		var existing models.NotificationKindRule
		err = tx.Where("kind = ?", kind).First(&existing).Error
		if err == nil {
			if existing.UserDisabled {
				existing.UserDisabled = false
				existing.UIEnabled = input.UIEnabled
				existing.NtfyEnabled = input.NtfyEnabled
				existing.EmailEnabled = input.EmailEnabled
				existing.DiscordEnabled = input.DiscordEnabled
				if existing.Config == "" && definition.DefaultConfig != "" {
					existing.Config = definition.DefaultConfig
				}
				return tx.Save(&existing).Error
			}
			return fmt.Errorf("notification_rule_already_exists")
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}

		record := models.NotificationKindRule{
			Kind:           kind,
			UIEnabled:      input.UIEnabled,
			NtfyEnabled:    input.NtfyEnabled,
			EmailEnabled:   input.EmailEnabled,
			DiscordEnabled: input.DiscordEnabled,
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return s.GetRuleConfig(ctx)
}

func (s *Service) UpdateRule(ctx context.Context, id uint, input RuleUpdateInput) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}
	if id == 0 {
		return RuleConfigView{}, fmt.Errorf("invalid_notification_rule_id")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, _, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		var rule models.NotificationKindRule
		if err := tx.First(&rule, id).Error; err != nil {
			return err
		}

		if _, _, ok := resolveTemplateTargetFromKind(rule.Kind); !ok {
			return fmt.Errorf("notification_rule_not_found")
		}

		rule.UIEnabled = input.UIEnabled
		rule.NtfyEnabled = input.NtfyEnabled
		rule.EmailEnabled = input.EmailEnabled
		rule.DiscordEnabled = input.DiscordEnabled
		if input.Config != "" {
			if !json.Valid([]byte(input.Config)) {
				return fmt.Errorf("invalid_notification_rule_config_json")
			}
			templateKey, _, _ := resolveTemplateTargetFromKind(rule.Kind)
			if err := validateDiskSmartRuleConfig(templateKey, input.Config); err != nil {
				return err
			}
			rule.Config = input.Config
		}
		return tx.Save(&rule).Error
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return s.GetRuleConfig(ctx)
}

func validateDiskSmartRuleConfig(templateKey, config string) error {
	switch templateKey {
	case RuleTemplateDiskSmartTemperature:
		var value diskSmartRuleConfig
		if err := json.Unmarshal([]byte(config), &value); err != nil {
			return fmt.Errorf("invalid_notification_rule_config_json")
		}
		if value.WarningCelsius < 0 || value.CriticalCelsius <= value.WarningCelsius || value.CriticalCelsius > 200 {
			return fmt.Errorf("invalid_notification_rule_temperature_thresholds")
		}
	case RuleTemplateDiskSmartWearout:
		var value diskSmartRuleConfig
		if err := json.Unmarshal([]byte(config), &value); err != nil {
			return fmt.Errorf("invalid_notification_rule_config_json")
		}
		if value.WarningPercent < 0 || value.CriticalPercent <= value.WarningPercent || value.CriticalPercent > 100 {
			return fmt.Errorf("invalid_notification_rule_wearout_thresholds")
		}
	}
	return nil
}

func (s *Service) DeleteRule(ctx context.Context, id uint) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}
	if id == 0 {
		return RuleConfigView{}, fmt.Errorf("invalid_notification_rule_id")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, _, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		var rule models.NotificationKindRule
		if err := tx.First(&rule, id).Error; err != nil {
			return err
		}

		rule.UserDisabled = true
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return s.GetRuleConfig(ctx)
}

func (s *Service) BulkDeleteRules(ctx context.Context, ids []uint) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}
	if len(ids) == 0 {
		return RuleConfigView{}, fmt.Errorf("invalid_notification_rule_ids")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, _, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		var rules []models.NotificationKindRule
		if err := tx.Find(&rules, ids).Error; err != nil {
			return err
		}

		for _, rule := range rules {
			rule.UserDisabled = true
			if err := tx.Save(&rule).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return s.GetRuleConfig(ctx)
}

func (s *Service) BulkUpdateRules(ctx context.Context, ids []uint, uiEnabled, ntfyEnabled, emailEnabled, discordEnabled *bool) (RuleConfigView, error) {
	if s == nil || s.DB == nil {
		return RuleConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}
	if err := s.MigrateLegacyDiskSmartRecords(ctx); err != nil {
		return RuleConfigView{}, err
	}
	if len(ids) == 0 {
		return RuleConfigView{}, fmt.Errorf("invalid_notification_rule_ids")
	}

	diskDefinitions := s.buildDiskSmartTemplateDefinitions(ctx)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		definitions, _, err := s.loadRuleTemplateDefinitions(tx, diskDefinitions)
		if err != nil {
			return err
		}
		if err := s.syncAutoManagedRules(tx, definitions); err != nil {
			return err
		}

		var rules []models.NotificationKindRule
		if err := tx.Find(&rules, ids).Error; err != nil {
			return err
		}

		for _, rule := range rules {
			if uiEnabled != nil {
				rule.UIEnabled = *uiEnabled
			}
			if ntfyEnabled != nil {
				rule.NtfyEnabled = *ntfyEnabled
			}
			if emailEnabled != nil {
				rule.EmailEnabled = *emailEnabled
			}
			if discordEnabled != nil {
				rule.DiscordEnabled = *discordEnabled
			}
			if err := tx.Save(&rule).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return RuleConfigView{}, err
	}

	return s.GetRuleConfig(ctx)
}

func (s *Service) ensureKindRule(tx *gorm.DB, kind string, defaultConfig string) (models.NotificationKindRule, error) {
	kind = strings.TrimSpace(kind)
	var rule models.NotificationKindRule
	err := tx.Where("kind = ?", kind).First(&rule).Error
	if err == nil {
		if rule.UserDisabled {
			return rule, nil
		}
		if rule.Config == "" && defaultConfig != "" {
			rule.Config = defaultConfig
			if saveErr := tx.Save(&rule).Error; saveErr != nil {
				return models.NotificationKindRule{}, saveErr
			}
		}
		return rule, nil
	}

	if err != gorm.ErrRecordNotFound {
		return models.NotificationKindRule{}, err
	}

	rule = models.NotificationKindRule{
		Kind:           kind,
		UIEnabled:      true,
		NtfyEnabled:    true,
		EmailEnabled:   true,
		DiscordEnabled: false,
		Config:         defaultConfig,
	}
	if err := tx.Create(&rule).Error; err != nil {
		return models.NotificationKindRule{}, err
	}

	return rule, nil
}

func (s *Service) listActivePools(tx *gorm.DB) ([]string, error) {
	var settings models.BasicSettings
	err := tx.Limit(1).First(&settings).Error
	if err == gorm.ErrRecordNotFound {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	return normalizePoolNames(settings.Pools), nil
}

func (s *Service) loadRuleTemplateDefinitions(tx *gorm.DB, diskDefinitions []*ruleTemplateDefinition) ([]*ruleTemplateDefinition, map[string]*ruleTemplateDefinition, error) {
	pools, err := s.listActivePools(tx)
	if err != nil {
		return nil, nil, err
	}

	targets := make([]RuleTemplateTargetView, 0, len(pools))
	activeTargets := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		targets = append(targets, RuleTemplateTargetView{
			Key:   pool,
			Label: pool,
		})
		activeTargets[pool] = struct{}{}
	}

	definitions := []*ruleTemplateDefinition{
		{
			View: RuleTemplateView{
				Key:         RuleTemplateZFSPoolState,
				Label:       "ZFS Pool State",
				Description: "ZFS pool/vdev state-change notifications.",
				TargetType:  ruleTemplateTargetTypePool,
				Targets:     targets,
			},
			AutoCreateRules: true,
			ActiveTargets:   activeTargets,
		},
	}

	definitions = append(definitions, diskDefinitions...)

	definitionsByKey := make(map[string]*ruleTemplateDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByKey[definition.View.Key] = definition
	}

	return definitions, definitionsByKey, nil
}

func (s *Service) buildDiskSmartTemplateDefinitions(ctx context.Context) []*ruleTemplateDefinition {
	if s.DiskService == nil {
		return nil
	}

	disks, err := s.loadDiskInventory(ctx)
	if err != nil {
		return nil
	}

	type deviceInfo struct{ key, label, device string }
	allDisks := make([]deviceInfo, 0)
	ssdDisks := make([]deviceInfo, 0)
	nvmeDisks := make([]deviceInfo, 0)
	for _, disk := range disks {
		diskType := strings.ToUpper(strings.TrimSpace(disk.Type))
		if diskType != "HDD" && diskType != "SSD" && diskType != "NVME" && diskType != "VIRTUAL" {
			continue
		}
		label := disk.Device
		if disk.Model != "" {
			label = fmt.Sprintf("%s (%s)", disk.Device, disk.Model)
		}
		key := disk.Device
		if disk.IdentityStable && strings.TrimSpace(disk.UUID) != "" {
			key = strings.TrimSpace(strings.ToLower(disk.UUID))
		}
		info := deviceInfo{key: key, label: label, device: disk.Device}
		allDisks = append(allDisks, info)
		if diskType == "NVME" {
			ssdDisks = append(ssdDisks, info)
			nvmeDisks = append(nvmeDisks, info)
		} else if diskType == "SSD" {
			ssdDisks = append(ssdDisks, info)
		}
	}

	targetViews := func(devs []deviceInfo) ([]RuleTemplateTargetView, map[string]struct{}, map[string]string) {
		views := make([]RuleTemplateTargetView, 0, len(devs))
		active := make(map[string]struct{}, len(devs))
		devices := make(map[string]string, len(devs))
		for _, dev := range devs {
			views = append(views, RuleTemplateTargetView{
				Key:   dev.key,
				Label: dev.label,
			})
			active[dev.key] = struct{}{}
			devices[dev.key] = dev.device
		}
		return views, active, devices
	}

	tempViews, tempActive, tempDevices := targetViews(allDisks)
	healthViews, healthActive, healthDevices := targetViews(allDisks)
	selfTestViews, selfTestActive, selfTestDevices := targetViews(allDisks)
	wearViews, wearActive, wearDevices := targetViews(ssdDisks)
	nvmeViews, nvmeActive, nvmeDevices := targetViews(nvmeDisks)

	tempCfg, _ := json.Marshal(map[string]float64{
		diskSmartConfigTemperatureWarningCelsius:  defaultTemperatureWarningCelsius,
		diskSmartConfigTemperatureCriticalCelsius: defaultTemperatureCriticalCelsius,
	})

	wearCfg, _ := json.Marshal(map[string]float64{
		diskSmartConfigWearoutWarningPercent:  defaultWearoutWarningPercent,
		diskSmartConfigWearoutCriticalPercent: defaultWearoutCriticalPercent,
	})

	return []*ruleTemplateDefinition{
		{
			View: RuleTemplateView{
				Key:           RuleTemplateDiskSmartTemperature,
				Label:         "Disk S.M.A.R.T Temperature",
				Description:   "Disk S.M.A.R.T temperature threshold alerts.",
				TargetType:    ruleTemplateTargetTypeDisk,
				Targets:       tempViews,
				DefaultConfig: string(tempCfg),
			},
			AutoCreateRules: true,
			ActiveTargets:   tempActive,
			TargetDevices:   tempDevices,
			DefaultConfig:   string(tempCfg),
		},
		{
			View: RuleTemplateView{
				Key:           RuleTemplateDiskSmartWearout,
				Label:         "Disk S.M.A.R.T Wear-Out",
				Description:   "Disk S.M.A.R.T wear-out threshold alerts (SSD/NVMe).",
				TargetType:    ruleTemplateTargetTypeDisk,
				Targets:       wearViews,
				DefaultConfig: string(wearCfg),
			},
			AutoCreateRules: true,
			ActiveTargets:   wearActive,
			TargetDevices:   wearDevices,
			DefaultConfig:   string(wearCfg),
		},
		{
			View: RuleTemplateView{
				Key:         RuleTemplateDiskSmartHealth,
				Label:       "Disk S.M.A.R.T Health",
				Description: "Disk S.M.A.R.T health status and reallocated/pending sector alerts.",
				TargetType:  ruleTemplateTargetTypeDisk,
				Targets:     healthViews,
			},
			AutoCreateRules: true,
			ActiveTargets:   healthActive,
			TargetDevices:   healthDevices,
		},
		{
			View: RuleTemplateView{
				Key:         RuleTemplateDiskSmartNvme,
				Label:       "NVMe S.M.A.R.T",
				Description: "NVMe-specific critical warnings, available spare, and media error alerts.",
				TargetType:  ruleTemplateTargetTypeDisk,
				Targets:     nvmeViews,
			},
			AutoCreateRules: true,
			ActiveTargets:   nvmeActive,
			TargetDevices:   nvmeDevices,
		},
		{
			View: RuleTemplateView{
				Key:         RuleTemplateDiskSmartSelfTest,
				Label:       "Disk S.M.A.R.T Self-Test",
				Description: "Disk S.M.A.R.T self-test lifecycle and result alerts.",
				TargetType:  ruleTemplateTargetTypeDisk,
				Targets:     selfTestViews,
			},
			AutoCreateRules: true,
			ActiveTargets:   selfTestActive,
			TargetDevices:   selfTestDevices,
		},
	}
}

func (s *Service) syncAutoManagedRules(tx *gorm.DB, definitions []*ruleTemplateDefinition) error {
	aliasByDevice := make(map[string]string)
	for _, definition := range definitions {
		for key, device := range definition.TargetDevices {
			device = normalizeRuleTargetKey(device)
			key = normalizeRuleTargetKey(key)
			if device != "" && key != "" && device != key {
				aliasByDevice[device] = key
			}
		}
	}
	aliases := make([]diskSmartIdentityAlias, 0, len(aliasByDevice))
	for device, key := range aliasByDevice {
		aliases = append(aliases, diskSmartIdentityAlias{device: device, key: key})
	}
	if err := s.migrateDiskSmartIdentityAliases(tx, aliases); err != nil {
		return err
	}
	expectedKinds := make(map[string]string)

	for _, definition := range definitions {
		if !definition.AutoCreateRules {
			continue
		}

		for _, target := range definition.View.Targets {
			kind, err := ruleKindForTemplateTarget(definition.View.Key, target.Key)
			if err != nil {
				return err
			}

			expectedKinds[kind] = definition.DefaultConfig
		}
	}
	if len(expectedKinds) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(expectedKinds))
	for kind := range expectedKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	var existing []models.NotificationKindRule
	if err := tx.Where("kind IN ?", kinds).Find(&existing).Error; err != nil {
		return err
	}
	for _, rule := range existing {
		defaultConfig := expectedKinds[rule.Kind]
		delete(expectedKinds, rule.Kind)
		if !rule.UserDisabled && strings.TrimSpace(rule.Config) == "" && defaultConfig != "" {
			if err := tx.Model(&rule).Update("config", defaultConfig).Error; err != nil {
				return err
			}
		}
	}
	missing := make([]models.NotificationKindRule, 0, len(expectedKinds))
	for _, kind := range kinds {
		defaultConfig, ok := expectedKinds[kind]
		if !ok {
			continue
		}
		missing = append(missing, models.NotificationKindRule{
			Kind:           kind,
			UIEnabled:      true,
			NtfyEnabled:    true,
			EmailEnabled:   true,
			DiscordEnabled: false,
			Config:         defaultConfig,
		})
	}
	if len(missing) > 0 {
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&missing).Error
	}
	return nil
}

func (s *Service) migrateDiskSmartIdentityAliases(tx *gorm.DB, aliases []diskSmartIdentityAlias) error {
	for _, alias := range aliases {
		for _, prefix := range []string{
			notifier.DiskSmartTemperatureKindPrefix,
			notifier.DiskSmartWearoutKindPrefix,
			notifier.DiskSmartHealthKindPrefix,
			notifier.DiskSmartNvmeKindPrefix,
			notifier.DiskSmartSelfTestKindPrefix,
		} {
			if err := s.migrateDiskSmartKindAlias(tx, prefix, alias); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) migrateDiskSmartKindAlias(tx *gorm.DB, prefix string, alias diskSmartIdentityAlias) error {
	oldKind := notifier.KindForDiskSmart(prefix, alias.device)
	newKind := notifier.KindForDiskSmart(prefix, alias.key)
	if oldKind == newKind {
		return nil
	}
	var oldRule models.NotificationKindRule
	oldErr := tx.Where("kind = ?", oldKind).First(&oldRule).Error
	if oldErr != nil && oldErr != gorm.ErrRecordNotFound {
		return oldErr
	}
	if oldErr == nil {
		var current models.NotificationKindRule
		currentErr := tx.Where("kind = ?", newKind).First(&current).Error
		switch currentErr {
		case nil:
			if notificationRuleShouldReplace(current, oldRule) {
				copyNotificationRuleSettings(&current, oldRule)
			}
			if err := tx.Save(&current).Error; err != nil {
				return err
			}
			if err := tx.Delete(&oldRule).Error; err != nil {
				return err
			}
		case gorm.ErrRecordNotFound:
			if err := tx.Model(&oldRule).Update("kind", newKind).Error; err != nil {
				return err
			}
		default:
			return currentErr
		}
	}
	if err := s.migrateDiskSmartNotificationAlias(tx, oldKind, newKind, alias); err != nil {
		return err
	}
	return tx.Model(&models.NotificationSuppression{}).Where("kind = ?", oldKind).Update("kind", newKind).Error
}

func (s *Service) migrateDiskSmartNotificationAlias(tx *gorm.DB, oldKind, newKind string, alias diskSmartIdentityAlias) error {
	var notifications []models.Notification
	if err := tx.Where("kind = ?", oldKind).Find(&notifications).Error; err != nil {
		return err
	}
	for i := range notifications {
		var fresh models.Notification
		if err := tx.First(&fresh, notifications[i].ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		notification := &fresh
		condition, ok := currentDiskSmartCondition(notification.Metadata["condition"])
		if !ok {
			condition, ok = legacyDiskSmartCondition(notification.Metadata["condition"])
		}
		if !ok {
			separator := strings.LastIndex(notification.Fingerprint, "|")
			if separator >= 0 {
				condition, ok = currentDiskSmartCondition(notification.Fingerprint[separator+1:])
				if !ok {
					condition, ok = legacyDiskSmartCondition(notification.Fingerprint[separator+1:])
				}
			}
		}
		if !ok {
			if err := tx.Model(notification).Update("kind", newKind).Error; err != nil {
				return err
			}
			continue
		}
		if notification.Metadata == nil {
			notification.Metadata = make(map[string]string)
		}
		notification.Kind = newKind
		notification.Metadata["device"] = alias.device
		notification.Metadata["disk_key"] = alias.key
		notification.Metadata["condition"] = condition
		fingerprint := alias.key + "|" + diskSmartConditionCategory(condition)
		var current models.Notification
		currentErr := tx.Where("fingerprint = ?", fingerprint).First(&current).Error
		if currentErr != nil && currentErr != gorm.ErrRecordNotFound {
			return currentErr
		}
		if currentErr == nil && current.ID != notification.ID {
			winningCondition := condition
			if value, valid := currentDiskSmartCondition(current.Metadata["condition"]); valid {
				winningCondition = value
			} else if value, valid := legacyDiskSmartCondition(current.Metadata["condition"]); valid {
				winningCondition = value
			}
			current.OccurrenceCount += notification.OccurrenceCount
			if current.FirstOccurredAt.IsZero() || (!notification.FirstOccurredAt.IsZero() && notification.FirstOccurredAt.Before(current.FirstOccurredAt)) {
				current.FirstOccurredAt = notification.FirstOccurredAt
			}
			if notification.LastOccurredAt.After(current.LastOccurredAt) {
				current.Kind = notification.Kind
				current.Title = notification.Title
				current.Body = notification.Body
				current.Severity = notification.Severity
				current.Source = notification.Source
				current.Metadata = notification.Metadata
				current.LastOccurredAt = notification.LastOccurredAt
				winningCondition = condition
			}
			if current.Metadata == nil {
				current.Metadata = make(map[string]string)
			}
			current.Metadata["device"] = alias.device
			current.Metadata["disk_key"] = alias.key
			current.Metadata["condition"] = winningCondition
			if current.DismissedAt == nil || notification.DismissedAt == nil {
				current.DismissedAt = nil
			}
			if err := tx.Save(&current).Error; err != nil {
				return err
			}
			if err := tx.Delete(notification).Error; err != nil {
				return err
			}
			continue
		}
		notification.Fingerprint = fingerprint
		if err := tx.Save(notification).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) migrateLegacyDiskSmartNotificationConditions(tx *gorm.DB, aliases []diskSmartIdentityAlias) error {
	aliasByDevice := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		aliasByDevice[alias.device] = alias.key
	}
	var notifications []models.Notification
	if err := tx.Where("source IN ?", []string{"system.disk.smart", "system.disk.smart.selftest"}).Find(&notifications).Error; err != nil {
		return err
	}
	for idx := range notifications {
		var fresh models.Notification
		if err := tx.First(&fresh, notifications[idx].ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		notification := &fresh
		rawCondition := notification.Metadata["condition"]
		condition, ok := legacyDiskSmartCondition(rawCondition)
		if !ok {
			condition, ok = currentDiskSmartCondition(rawCondition)
		}
		separator := strings.LastIndex(notification.Fingerprint, "|")
		if !ok && separator >= 0 {
			rawFingerprintCondition := notification.Fingerprint[separator+1:]
			condition, ok = legacyDiskSmartCondition(rawFingerprintCondition)
			if !ok {
				condition, ok = currentDiskSmartCondition(rawFingerprintCondition)
			}
		}
		if !ok {
			continue
		}
		device := strings.TrimSpace(strings.ToLower(notification.Metadata["device"]))
		if device == "" && separator > 0 {
			device = strings.TrimSpace(strings.ToLower(notification.Fingerprint[:separator]))
		}
		if device == "" {
			continue
		}
		target := device
		if key := aliasByDevice[device]; key != "" {
			target = key
		}
		if notification.Metadata == nil {
			notification.Metadata = make(map[string]string)
		}
		notification.Metadata["device"] = device
		notification.Metadata["disk_key"] = target
		notification.Metadata["condition"] = condition
		fingerprint := target + "|" + diskSmartConditionCategory(condition)

		var current models.Notification
		currentErr := tx.Where("fingerprint = ?", fingerprint).First(&current).Error
		if currentErr != nil && currentErr != gorm.ErrRecordNotFound {
			return currentErr
		}
		if currentErr == nil && current.ID != notification.ID {
			winningCondition := condition
			if value, valid := currentDiskSmartCondition(current.Metadata["condition"]); valid {
				winningCondition = value
			} else if value, valid := legacyDiskSmartCondition(current.Metadata["condition"]); valid {
				winningCondition = value
			}
			current.OccurrenceCount += notification.OccurrenceCount
			if current.FirstOccurredAt.IsZero() || (!notification.FirstOccurredAt.IsZero() && notification.FirstOccurredAt.Before(current.FirstOccurredAt)) {
				current.FirstOccurredAt = notification.FirstOccurredAt
			}
			if notification.LastOccurredAt.After(current.LastOccurredAt) {
				current.Kind = notification.Kind
				current.Title = notification.Title
				current.Body = notification.Body
				current.Severity = notification.Severity
				current.Source = notification.Source
				current.Metadata = notification.Metadata
				current.LastOccurredAt = notification.LastOccurredAt
				winningCondition = condition
			}
			if current.Metadata == nil {
				current.Metadata = make(map[string]string)
			}
			current.Metadata["device"] = device
			current.Metadata["disk_key"] = target
			current.Metadata["condition"] = winningCondition
			if current.DismissedAt == nil || notification.DismissedAt == nil {
				current.DismissedAt = nil
			} else if notification.DismissedAt.After(*current.DismissedAt) {
				current.DismissedAt = notification.DismissedAt
			}
			if err := tx.Save(&current).Error; err != nil {
				return err
			}
			if err := tx.Delete(notification).Error; err != nil {
				return err
			}
			continue
		}

		notification.Fingerprint = fingerprint
		if err := tx.Save(notification).Error; err != nil {
			return err
		}
	}
	return nil
}

func currentDiskSmartCondition(condition string) (string, bool) {
	condition = strings.TrimSpace(strings.ToLower(condition))
	switch condition {
	case "smart_unavailable", "smart_available", "temperature_critical", "temperature_warning", "temperature_normal", "health_failed", "health_recovered", "wearout_critical", "wearout_warning", "wearout_normal", "sector_issues", "sector_issues_cleared", "nvme_warning", "nvme_recovered", "self_test_started", "self_test_passed", "self_test_failed", "self_test_aborted", "self_test_completed_unknown", "self_test_schedule_failed", "self_test_schedule_missed", "self_test_device_unavailable", "self_test_capabilities_unavailable", "self_test_status_unavailable", "self_test_unsupported", "self_test_start_failed", "self_test_timeout_aborted", "self_test_result_unknown":
		return condition, true
	default:
		return "", false
	}
}

func diskSmartConditionCategory(condition string) string {
	if strings.HasPrefix(condition, "self_test_") {
		return "selftest"
	}
	switch condition {
	case "temperature_critical", "temperature_warning", "temperature_normal":
		return "temperature"
	case "wearout_critical", "wearout_warning", "wearout_normal":
		return "wearout"
	case "nvme_warning", "nvme_recovered":
		return "nvme"
	case "sector_issues", "sector_issues_cleared":
		return "sectors"
	case "smart_unavailable", "smart_available":
		return "availability"
	default:
		return "health"
	}
}

func legacyDiskSmartCondition(condition string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(condition)) {
	case "smart unavailable":
		return "smart_unavailable", true
	case "critical temperature":
		return "temperature_critical", true
	case "high temperature":
		return "temperature_warning", true
	case "temperature normal":
		return "temperature_normal", true
	case "smart health failed":
		return "health_failed", true
	case "smart health recovered":
		return "health_recovered", true
	case "critical wear-out":
		return "wearout_critical", true
	case "high wear-out":
		return "wearout_warning", true
	case "wear-out normal":
		return "wearout_normal", true
	case "sector issues":
		return "sector_issues", true
	case "sector issues cleared":
		return "sector_issues_cleared", true
	case "nvme warning":
		return "nvme_warning", true
	case "nvme recovered":
		return "nvme_recovered", true
	default:
		return "", false
	}
}

func (s *Service) migrateLegacyDiskSmartSelfTestKinds(tx *gorm.DB) error {
	legacyPrefix := RuleTemplateDiskSmartSelfTest
	newPrefix := notifier.DiskSmartSelfTestKindPrefix
	kinds := make(map[string]struct{})
	for _, model := range []any{
		&models.NotificationKindRule{},
		&models.Notification{},
		&models.NotificationSuppression{},
	} {
		var stored []string
		if err := tx.Model(model).Where("kind LIKE ?", legacyPrefix+"%").Pluck("kind", &stored).Error; err != nil {
			return err
		}
		for _, kind := range stored {
			kinds[kind] = struct{}{}
		}
	}

	for legacyKind := range kinds {
		normalized := strings.TrimSpace(strings.ToLower(legacyKind))
		if !strings.HasPrefix(normalized, legacyPrefix) || strings.HasPrefix(normalized, newPrefix) {
			continue
		}
		device := normalizeRuleTargetKey(normalized[len(legacyPrefix):])
		if device == "" || strings.HasPrefix(device, ".") {
			continue
		}
		newKind := notifier.KindForDiskSmart(newPrefix, device)

		var legacyRule models.NotificationKindRule
		legacyRuleErr := tx.Where("kind = ?", legacyKind).First(&legacyRule).Error
		if legacyRuleErr != nil && legacyRuleErr != gorm.ErrRecordNotFound {
			return legacyRuleErr
		}
		if legacyRuleErr == nil {
			var currentRule models.NotificationKindRule
			currentRuleErr := tx.Where("kind = ?", newKind).First(&currentRule).Error
			switch currentRuleErr {
			case nil:
				if notificationRuleShouldReplace(currentRule, legacyRule) {
					copyNotificationRuleSettings(&currentRule, legacyRule)
				}
				if err := tx.Save(&currentRule).Error; err != nil {
					return err
				}
				if err := tx.Delete(&legacyRule).Error; err != nil {
					return err
				}
			case gorm.ErrRecordNotFound:
				if err := tx.Model(&legacyRule).Update("kind", newKind).Error; err != nil {
					return err
				}
			default:
				return currentRuleErr
			}
		}

		if err := tx.Model(&models.Notification{}).Where("kind = ?", legacyKind).Update("kind", newKind).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.NotificationSuppression{}).Where("kind = ?", legacyKind).Update("kind", newKind).Error; err != nil {
			return err
		}
	}
	return nil
}

func notificationRuleHasAutomaticDefaults(rule models.NotificationKindRule) bool {
	if !rule.UIEnabled || !rule.NtfyEnabled || !rule.EmailEnabled || rule.DiscordEnabled || rule.UserDisabled {
		return false
	}
	config := strings.TrimSpace(rule.Config)
	if config == "" || config == "{}" {
		return true
	}
	templateKey, _, ok := resolveTemplateTargetFromKind(rule.Kind)
	if !ok {
		return false
	}
	var values map[string]float64
	if err := json.Unmarshal([]byte(config), &values); err != nil || len(values) != 2 {
		return false
	}
	switch templateKey {
	case RuleTemplateDiskSmartTemperature:
		return values[diskSmartConfigTemperatureWarningCelsius] == defaultTemperatureWarningCelsius && values[diskSmartConfigTemperatureCriticalCelsius] == defaultTemperatureCriticalCelsius
	case RuleTemplateDiskSmartWearout:
		return values[diskSmartConfigWearoutWarningPercent] == defaultWearoutWarningPercent && values[diskSmartConfigWearoutCriticalPercent] == defaultWearoutCriticalPercent
	default:
		return false
	}
}

func notificationRuleShouldReplace(current, candidate models.NotificationKindRule) bool {
	currentAutomatic := notificationRuleHasAutomaticDefaults(current)
	candidateAutomatic := notificationRuleHasAutomaticDefaults(candidate)
	if currentAutomatic != candidateAutomatic {
		return currentAutomatic
	}
	if currentAutomatic {
		return false
	}
	return candidate.UpdatedAt.After(current.UpdatedAt)
}

func copyNotificationRuleSettings(target *models.NotificationKindRule, source models.NotificationKindRule) {
	target.UIEnabled = source.UIEnabled
	target.NtfyEnabled = source.NtfyEnabled
	target.EmailEnabled = source.EmailEnabled
	target.DiscordEnabled = source.DiscordEnabled
	target.UserDisabled = source.UserDisabled
	target.Config = source.Config
}

func (s *Service) listManagedRuleRows(tx *gorm.DB) ([]models.NotificationKindRule, error) {
	var all []models.NotificationKindRule
	if err := tx.Order("id ASC").Find(&all).Error; err != nil {
		return nil, err
	}

	rules := make([]models.NotificationKindRule, 0, len(all))
	for _, rule := range all {
		if _, _, ok := resolveTemplateTargetFromKind(rule.Kind); !ok {
			continue
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

func (s *Service) buildRuleConfigView(definitions []*ruleTemplateDefinition, definitionsByKey map[string]*ruleTemplateDefinition, rules []models.NotificationKindRule) RuleConfigView {
	view := RuleConfigView{
		Rules:     make([]RuleConfigEntryView, 0, len(rules)),
		Templates: make([]RuleTemplateView, 0, len(definitions)),
	}

	for _, definition := range definitions {
		view.Templates = append(view.Templates, definition.View)
	}

	for _, rule := range rules {
		if rule.UserDisabled {
			continue
		}
		templateKey, targetKey, ok := resolveTemplateTargetFromKind(rule.Kind)
		if !ok {
			continue
		}

		targetLabel := targetKey
		templateLabel := templateKey
		active := false

		if definition, exists := definitionsByKey[templateKey]; exists {
			templateLabel = definition.View.Label
			if _, exists := definition.ActiveTargets[targetKey]; exists {
				active = true
			}
			for _, target := range definition.View.Targets {
				if target.Key == targetKey {
					targetLabel = target.Label
					break
				}
			}
		}

		view.Rules = append(view.Rules, RuleConfigEntryView{
			ID:             rule.ID,
			Kind:           rule.Kind,
			TemplateKey:    templateKey,
			TemplateLabel:  templateLabel,
			TargetKey:      targetKey,
			TargetLabel:    targetLabel,
			Active:         active,
			UIEnabled:      rule.UIEnabled,
			NtfyEnabled:    rule.NtfyEnabled,
			EmailEnabled:   rule.EmailEnabled,
			DiscordEnabled: rule.DiscordEnabled,
			Config:         rule.Config,
		})
	}

	sort.Slice(view.Rules, func(i, j int) bool {
		left := view.Rules[i]
		right := view.Rules[j]
		if left.TemplateLabel != right.TemplateLabel {
			return left.TemplateLabel < right.TemplateLabel
		}
		if left.TargetLabel != right.TargetLabel {
			return left.TargetLabel < right.TargetLabel
		}
		return left.ID < right.ID
	})

	return view
}

func (s *Service) resolveRuleUpdateKind(entry RuleConfigEntryUpdate) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(entry.Kind))
	pool := normalizeRuleTargetKey(entry.Pool)
	templateKey := normalizeRuleTemplateKey(entry.TemplateKey)
	targetKey := normalizeRuleTargetKey(entry.TargetKey)

	if kind == "" {
		switch {
		case pool != "":
			kind = notifier.KindForZFSPoolState(pool)
		case templateKey != "" || targetKey != "":
			resolvedKind, err := ruleKindForTemplateTarget(templateKey, targetKey)
			if err != nil {
				return "", err
			}
			kind = resolvedKind
		default:
			return "", fmt.Errorf("notification_rule_kind_required")
		}
	}

	resolvedTemplateKey, resolvedTargetKey, ok := resolveTemplateTargetFromKind(kind)
	if !ok {
		return "", fmt.Errorf("notification_rule_not_found")
	}

	if pool != "" {
		expected := notifier.KindForZFSPoolState(pool)
		if kind != expected {
			return "", fmt.Errorf("notification_rule_kind_mismatch")
		}
	}

	if templateKey != "" || targetKey != "" {
		if templateKey == "" {
			templateKey = resolvedTemplateKey
		}
		if targetKey == "" {
			targetKey = resolvedTargetKey
		}
		expected, err := ruleKindForTemplateTarget(templateKey, targetKey)
		if err != nil {
			return "", err
		}
		if expected != kind {
			return "", fmt.Errorf("notification_rule_kind_mismatch")
		}
	}

	return kind, nil
}

func (s *Service) ensureTransportConfigsDB(ctx context.Context) ([]models.NotificationTransportConfig, error) {
	var configs []models.NotificationTransportConfig
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := s.ensureTransportConfigs(tx)
		if err != nil {
			return err
		}
		configs = current
		return nil
	})
	if err != nil {
		return nil, err
	}

	return configs, nil
}

func (s *Service) ensureTransportConfigs(tx *gorm.DB) ([]models.NotificationTransportConfig, error) {
	var configs []models.NotificationTransportConfig
	if err := tx.Order("id ASC").Find(&configs).Error; err != nil {
		return nil, err
	}

	for idx := range configs {
		cfg := &configs[idx]
		if normalizeTransportConfig(cfg) {
			if err := tx.Save(cfg).Error; err != nil {
				return nil, err
			}
		}
	}

	return configs, nil
}

func normalizeTransportConfig(cfg *models.NotificationTransportConfig) bool {
	updated := false
	normalizedType := normalizeTransportType(cfg.Type)
	if normalizedType == "" {
		if cfg.NtfyEnabled && !cfg.EmailEnabled && !cfg.DiscordEnabled {
			normalizedType = TransportTypeNtfy
		} else if cfg.DiscordEnabled && !cfg.NtfyEnabled && !cfg.EmailEnabled {
			normalizedType = TransportTypeDiscord
		} else {
			normalizedType = TransportTypeSMTP
		}
	}
	if cfg.Type != normalizedType {
		cfg.Type = normalizedType
		updated = true
	}
	if strings.TrimSpace(cfg.NtfyBaseURL) == "" {
		cfg.NtfyBaseURL = defaultNtfyBaseURL
		updated = true
	}
	if cfg.SMTPPort <= 0 {
		cfg.SMTPPort = defaultSMTPPort
		updated = true
	}
	return updated
}

func (s *Service) listTransportConfigs(ctx context.Context) ([]models.NotificationTransportConfig, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("notifications_service_not_initialized")
	}

	return s.ensureTransportConfigsDB(ctx)
}

func (s *Service) listDeliveryTransportConfigs(ctx context.Context, transportID uint) ([]models.NotificationTransportConfig, error) {
	if transportID == 0 {
		return s.listTransportConfigs(ctx)
	}
	var cfg models.NotificationTransportConfig
	result := s.DB.WithContext(ctx).Where("id = ?", transportID).Limit(1).Find(&cfg)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return []models.NotificationTransportConfig{}, nil
	}
	if normalizeTransportConfig(&cfg) {
		if err := s.DB.WithContext(ctx).Save(&cfg).Error; err != nil {
			return nil, err
		}
	}
	return []models.NotificationTransportConfig{cfg}, nil
}

func (s *Service) resolveTransportForUpdate(tx *gorm.DB, id uint) (models.NotificationTransportConfig, error) {
	if id > 0 {
		var cfg models.NotificationTransportConfig
		result := tx.Where("id = ?", id).Limit(1).Find(&cfg)
		if result.Error != nil {
			return models.NotificationTransportConfig{}, result.Error
		}
		if result.RowsAffected == 0 {
			return models.NotificationTransportConfig{}, gorm.ErrRecordNotFound
		}
		return cfg, nil
	}

	return models.NotificationTransportConfig{}, nil
}

func normalizeRecipients(input []string) ([]string, error) {
	normalizedRecipients := make([]string, 0, len(input))
	seen := map[string]struct{}{}
	for _, recipient := range input {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if !utils.IsValidEmail(recipient) {
			return nil, fmt.Errorf("invalid_email_recipient: %s", recipient)
		}
		if _, ok := seen[recipient]; ok {
			continue
		}
		seen[recipient] = struct{}{}
		normalizedRecipients = append(normalizedRecipients, recipient)
	}
	sort.Strings(normalizedRecipients)
	return normalizedRecipients, nil
}

func (s *Service) toTransportConfigView(configs []models.NotificationTransportConfig) TransportConfigView {
	view := TransportConfigView{
		Transports: make([]TransportConfigEntryView, 0, len(configs)),
	}

	for _, cfg := range configs {
		entry := TransportConfigEntryView{
			ID:   cfg.ID,
			Name: strings.TrimSpace(cfg.Name),
			Type: normalizeTransportType(cfg.Type),
		}

		switch entry.Type {
		case TransportTypeNtfy:
			entry.Enabled = cfg.NtfyEnabled
			ntfy := s.toNtfyTransportConfigView(cfg)
			entry.Ntfy = &ntfy
		case TransportTypeSMTP:
			entry.Enabled = cfg.EmailEnabled
			email := s.toEmailTransportConfigView(cfg)
			entry.Email = &email
		case TransportTypeDiscord:
			entry.Enabled = cfg.DiscordEnabled
			discord := s.toDiscordTransportConfigView(cfg)
			entry.Discord = &discord
		default:
			entry.Type = TransportTypeSMTP
			entry.Enabled = cfg.EmailEnabled
			email := s.toEmailTransportConfigView(cfg)
			entry.Email = &email
		}

		view.Transports = append(view.Transports, entry)
	}

	return view
}

func (s *Service) toNtfyTransportConfigView(cfg models.NotificationTransportConfig) NtfyTransportConfigView {
	return NtfyTransportConfigView{
		BaseURL:      normalizeNtfyBaseURL(cfg.NtfyBaseURL),
		Topic:        strings.TrimSpace(cfg.NtfyTopic),
		HasAuthToken: strings.TrimSpace(cfg.NtfyAuthToken) != "",
	}
}

func (s *Service) toEmailTransportConfigView(cfg models.NotificationTransportConfig) EmailTransportConfigView {
	return EmailTransportConfigView{
		SMTPHost:     strings.TrimSpace(cfg.SMTPHost),
		SMTPPort:     cfg.SMTPPort,
		SMTPUsername: strings.TrimSpace(cfg.SMTPUsername),
		SMTPFrom:     strings.TrimSpace(cfg.SMTPFrom),
		SMTPUseTLS:   cfg.SMTPUseTLS,
		Recipients:   append([]string{}, cfg.EmailRecipients...),
		HasPassword:  strings.TrimSpace(cfg.SMTPPassword) != "",
	}
}

func (s *Service) toDiscordTransportConfigView(cfg models.NotificationTransportConfig) DiscordTransportConfigView {
	return DiscordTransportConfigView{
		WebhookURL: strings.TrimSpace(cfg.DiscordWebhookURL),
	}
}

func (s *Service) publishRefresh() {
	hub.SSE.Publish(hub.Event{
		Type:      "notifications-refresh",
		Timestamp: s.now(),
	})
}

func (s *Service) sendNtfy(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error {
	baseURL := normalizeNtfyBaseURL(cfg.NtfyBaseURL)
	topic := strings.TrimSpace(cfg.NtfyTopic)
	if topic == "" {
		return fmt.Errorf("ntfy_topic_required")
	}

	body := strings.TrimSpace(input.Body)
	if body == "" {
		body = input.Title
	}

	endpoint := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeNtfy).Str("topic", topic).Msg("ntfy_request_creation_failed")
		return err
	}

	req.Header.Set("Title", input.Title)
	req.Header.Set("Tags", ntfyTagForSeverity(input.Severity))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	res, err := s.httpClient.Do(req)
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeNtfy).Str("topic", topic).Msg("ntfy_request_failed")
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 400 {
		logger.L.Error().Int("status_code", res.StatusCode).Str("transport_type", TransportTypeNtfy).Str("topic", topic).Msg("ntfy_non_200_response")
		return fmt.Errorf("ntfy_send_failed_status_%d", res.StatusCode)
	}

	return nil
}

func (s *Service) sendEmail(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error {
	host := strings.TrimSpace(cfg.SMTPHost)
	if host == "" {
		return fmt.Errorf("smtp_host_required")
	}
	if len(cfg.EmailRecipients) == 0 {
		return fmt.Errorf("smtp_recipients_required")
	}

	from := strings.TrimSpace(cfg.SMTPFrom)
	if from == "" {
		return fmt.Errorf("smtp_from_required")
	}

	port := cfg.SMTPPort
	if port <= 0 {
		port = defaultSMTPPort
	}

	subject := fmt.Sprintf("Sylve | %s", input.Title)
	htmlBody := buildEmailHTML(input, s.now())

	msg := strings.Builder{}
	msg.WriteString("From: ")
	msg.WriteString(from)
	msg.WriteString("\r\n")
	msg.WriteString("To: ")
	msg.WriteString(strings.Join(cfg.EmailRecipients, ","))
	msg.WriteString("\r\n")
	msg.WriteString("Subject: ")
	msg.WriteString(subject)
	msg.WriteString("\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	client, conn, err := dialSMTPClient(ctx, host, port, cfg.SMTPUseTLS)
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Int("smtp_port", port).Msg("smtp_dial_failed")
		return err
	}
	defer closeSMTPClient(client, conn)

	var auth smtp.Auth
	username := strings.TrimSpace(cfg.SMTPUsername)
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
		if ok, _ := client.Extension("AUTH"); !ok {
			logger.L.Error().Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_auth_not_supported")
			return fmt.Errorf("smtp_auth_not_supported")
		}
		if err := client.Auth(auth); err != nil {
			logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_auth_failed")
			return err
		}
	}

	if err := client.Mail(from); err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Str("smtp_from", from).Msg("smtp_mail_failed")
		return err
	}

	for _, recipient := range cfg.EmailRecipients {
		if err := client.Rcpt(recipient); err != nil {
			logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Str("recipient", recipient).Msg("smtp_rcpt_failed")
			return err
		}
	}

	wc, err := client.Data()
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_data_failed")
		return err
	}

	if _, err := wc.Write([]byte(msg.String())); err != nil {
		_ = wc.Close()
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_write_failed")
		return err
	}
	if err := wc.Close(); err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_close_failed")
		return err
	}

	if err := client.Quit(); err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeSMTP).Str("smtp_host", host).Msg("smtp_quit_failed")
		return err
	}

	return nil
}

func (s *Service) sendDiscord(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, webhookURL string) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return fmt.Errorf("discord_webhook_url_required")
	}

	color := discordColorForSeverity(input.Severity)
	severityLabel := strings.ToUpper(strings.TrimSpace(input.Severity)[:1]) + strings.TrimSpace(input.Severity)[1:]
	if severityLabel == "" {
		severityLabel = "Info"
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("[%s] %s", severityLabel, input.Title),
				"description": input.Body,
				"color":       color,
				"timestamp":   s.now().UTC().Format(time.RFC3339),
				"fields": []map[string]interface{}{
					{
						"name":   "Severity",
						"value":  severityLabel,
						"inline": true,
					},
					{
						"name":   "Kind",
						"value":  input.Kind,
						"inline": true,
					},
				},
				"footer": map[string]interface{}{
					"text": "Sylve",
				},
			},
		},
	}

	if source := strings.TrimSpace(input.Source); source != "" {
		payload["embeds"].([]map[string]interface{})[0]["fields"] = append(
			payload["embeds"].([]map[string]interface{})[0]["fields"].([]map[string]interface{}),
			map[string]interface{}{
				"name":   "Source",
				"value":  source,
				"inline": true,
			},
		)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeDiscord).Msg("discord_marshal_failed")
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, strings.NewReader(string(body)))
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeDiscord).Msg("discord_request_creation_failed")
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		logger.L.Error().Err(err).Str("transport_type", TransportTypeDiscord).Msg("discord_request_failed")
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 400 {
		logger.L.Error().Int("status_code", res.StatusCode).Str("transport_type", TransportTypeDiscord).Msg("discord_non_200_response")
		return fmt.Errorf("discord_send_failed_status_%d", res.StatusCode)
	}

	return nil
}

func dialSMTPClient(ctx context.Context, host string, port int, useTLS bool) (*smtp.Client, net.Conn, error) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error

	if useTLS && port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	if useTLS && port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			_ = conn.Close()
			return nil, nil, fmt.Errorf("smtp_starttls_not_supported")
		}

		if err := client.StartTLS(&tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			_ = client.Close()
			_ = conn.Close()
			return nil, nil, err
		}
	}

	return client, conn, nil
}

func closeSMTPClient(client *smtp.Client, conn net.Conn) {
	if client != nil {
		_ = client.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func normalizeInput(input notifier.EventInput) notifier.EventInput {
	normalized := notifier.EventInput{
		Kind:        strings.TrimSpace(strings.ToLower(input.Kind)),
		Title:       strings.TrimSpace(input.Title),
		Body:        strings.TrimSpace(input.Body),
		Severity:    normalizeSeverity(input.Severity),
		Source:      strings.TrimSpace(input.Source),
		Fingerprint: strings.TrimSpace(input.Fingerprint),
		Metadata:    map[string]string{},
		Channels:    append([]string(nil), input.Channels...),
		TransportID: input.TransportID,
	}

	for key, value := range input.Metadata {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		normalized.Metadata[k] = strings.TrimSpace(value)
	}

	return normalized
}

func notificationChannelSelected(channels []string, target string) bool {
	if len(channels) == 0 {
		return true
	}
	for _, channel := range channels {
		if strings.EqualFold(strings.TrimSpace(channel), target) {
			return true
		}
	}
	return false
}

func normalizeSeverity(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(models.NotificationSeverityCritical):
		return string(models.NotificationSeverityCritical)
	case string(models.NotificationSeverityError):
		return string(models.NotificationSeverityError)
	case string(models.NotificationSeverityWarning):
		return string(models.NotificationSeverityWarning)
	default:
		return string(models.NotificationSeverityInfo)
	}
}

func makeFingerprint(input notifier.EventInput) string {
	raw := strings.Join([]string{
		strings.TrimSpace(strings.ToLower(input.Kind)),
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Body),
		strings.TrimSpace(strings.ToLower(input.Severity)),
		strings.TrimSpace(strings.ToLower(input.Source)),
	}, "|")

	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func normalizeNtfyBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultNtfyBaseURL
	}
	return strings.TrimRight(value, "/")
}

func normalizeTransportType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case TransportTypeNtfy:
		return TransportTypeNtfy
	case TransportTypeSMTP:
		return TransportTypeSMTP
	case TransportTypeDiscord:
		return TransportTypeDiscord
	default:
		return ""
	}
}

func normalizeRuleTemplateKey(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func normalizeRuleTargetKey(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func resolveTemplateTargetFromKind(kind string) (string, string, bool) {
	if pool, ok := notifier.PoolFromZFSPoolStateKind(kind); ok {
		return RuleTemplateZFSPoolState, normalizeRuleTargetKey(pool), true
	}

	if prefix, diskName, ok := notifier.DiskNameFromSmartKind(kind); ok {
		switch prefix {
		case notifier.DiskSmartTemperatureKindPrefix:
			return RuleTemplateDiskSmartTemperature, normalizeRuleTargetKey(diskName), true
		case notifier.DiskSmartWearoutKindPrefix:
			return RuleTemplateDiskSmartWearout, normalizeRuleTargetKey(diskName), true
		case notifier.DiskSmartHealthKindPrefix:
			return RuleTemplateDiskSmartHealth, normalizeRuleTargetKey(diskName), true
		case notifier.DiskSmartNvmeKindPrefix:
			return RuleTemplateDiskSmartNvme, normalizeRuleTargetKey(diskName), true
		case notifier.DiskSmartSelfTestKindPrefix:
			return RuleTemplateDiskSmartSelfTest, normalizeRuleTargetKey(diskName), true
		}
	}

	return "", "", false
}

func ruleKindForTemplateTarget(templateKey, targetKey string) (string, error) {
	templateKey = normalizeRuleTemplateKey(templateKey)
	targetKey = normalizeRuleTargetKey(targetKey)

	if templateKey == "" {
		return "", fmt.Errorf("notification_rule_template_required")
	}
	if targetKey == "" {
		return "", fmt.Errorf("notification_rule_target_required")
	}

	switch templateKey {
	case RuleTemplateZFSPoolState:
		return notifier.KindForZFSPoolState(targetKey), nil
	case RuleTemplateDiskSmartTemperature:
		return notifier.KindForDiskSmart(notifier.DiskSmartTemperatureKindPrefix, targetKey), nil
	case RuleTemplateDiskSmartWearout:
		return notifier.KindForDiskSmart(notifier.DiskSmartWearoutKindPrefix, targetKey), nil
	case RuleTemplateDiskSmartHealth:
		return notifier.KindForDiskSmart(notifier.DiskSmartHealthKindPrefix, targetKey), nil
	case RuleTemplateDiskSmartNvme:
		return notifier.KindForDiskSmart(notifier.DiskSmartNvmeKindPrefix, targetKey), nil
	case RuleTemplateDiskSmartSelfTest:
		return notifier.KindForDiskSmart(notifier.DiskSmartSelfTestKindPrefix, targetKey), nil
	default:
		return "", fmt.Errorf("notification_rule_template_not_found")
	}
}

func normalizePoolNames(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, pool := range raw {
		normalized := strings.TrimSpace(strings.ToLower(pool))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	sort.Strings(out)
	return out
}

func suppressionKey(kind, fingerprint string) string {
	return strings.TrimSpace(strings.ToLower(kind)) + "|" + strings.TrimSpace(fingerprint)
}

func shouldPersistSuppressionForKind(kind string) bool {
	kind = strings.TrimSpace(strings.ToLower(kind))
	return !strings.HasPrefix(kind, notifier.ZFSPoolStateKindPrefix) && !notifier.IsDiskSmartKind(kind)
}

func ntfyTagForSeverity(severity string) string {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case "warning":
		return "warning"
	case "error":
		return "x"
	case "critical":
		return "rotating_light"
	default:
		return "white_check_mark"
	}
}

func discordColorForSeverity(severity string) int {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case "info":
		return 3447003
	case "warning":
		return 16776960
	case "error":
		return 15548997
	case "critical":
		return 10038562
	default:
		return 3447003
	}
}
