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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	hub "github.com/alchemillahq/sylve/internal/events"
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
	TransportTypeNtfy = "ntfy"
	TransportTypeSMTP = "smtp"
)

type NtfySender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error

type EmailSender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error

type Service struct {
	DB         *gorm.DB
	httpClient *http.Client
	now        func() time.Time

	ntfySender  NtfySender
	emailSender EmailSender
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
	ID      uint                      `json:"id"`
	Name    string                    `json:"name"`
	Type    string                    `json:"type"`
	Enabled bool                      `json:"enabled"`
	Ntfy    *NtfyTransportConfigView  `json:"ntfy,omitempty"`
	Email   *EmailTransportConfigView `json:"email,omitempty"`
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

type TransportConfigUpdate struct {
	Transports []TransportConfigEntryUpdate `json:"transports"`
}

type TransportConfigEntryUpdate struct {
	ID      uint                        `json:"id"`
	Name    string                      `json:"name"`
	Type    string                      `json:"type"`
	Enabled bool                        `json:"enabled"`
	Ntfy    *NtfyTransportConfigUpdate  `json:"ntfy,omitempty"`
	Email   *EmailTransportConfigUpdate `json:"email,omitempty"`
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

type RuleConfigView struct {
	Rules []RuleConfigEntryView `json:"rules"`
}

type RuleConfigEntryView struct {
	Kind         string `json:"kind"`
	Pool         string `json:"pool"`
	UIEnabled    bool   `json:"uiEnabled"`
	NtfyEnabled  bool   `json:"ntfyEnabled"`
	EmailEnabled bool   `json:"emailEnabled"`
}

type RuleConfigUpdate struct {
	Rules []RuleConfigEntryUpdate `json:"rules"`
}

type RuleConfigEntryUpdate struct {
	Kind         string `json:"kind"`
	Pool         string `json:"pool"`
	UIEnabled    bool   `json:"uiEnabled"`
	NtfyEnabled  bool   `json:"ntfyEnabled"`
	EmailEnabled bool   `json:"emailEnabled"`
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

	return s
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

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error

		kindRule, err = s.ensureKindRule(tx, normalized.Kind)
		if err != nil {
			return err
		}

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

		if kindRule.UIEnabled {
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

	if result.Suppressed {
		return result, nil
	}

	if kindRule.UIEnabled {
		s.publishRefresh()
	}

	transportConfigs, err := s.listTransportConfigs(ctx)
	if err != nil {
		return result, nil
	}

	for _, cfg := range transportConfigs {
		switch normalizeTransportType(cfg.Type) {
		case TransportTypeNtfy:
			if !cfg.NtfyEnabled || !kindRule.NtfyEnabled {
				continue
			}
			token := strings.TrimSpace(cfg.NtfyAuthToken)
			if err := s.ntfySender(ctx, cfg, normalized, token); err == nil {
				result.SentNtfy = true
			}
		case TransportTypeSMTP:
			if !cfg.EmailEnabled || !kindRule.EmailEnabled || len(cfg.EmailRecipients) == 0 {
				continue
			}
			password := strings.TrimSpace(cfg.SMTPPassword)
			if err := s.emailSender(ctx, cfg, normalized, password); err == nil {
				result.SentEmail = true
			}
		}
	}

	return result, nil
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

		if notif.DismissedAt == nil {
			if err := tx.Model(&models.Notification{}).Where("id = ?", notif.ID).Updates(map[string]any{
				"dismissed_at": now,
				"updated_at":   now,
			}).Error; err != nil {
				return err
			}
		}

		suppression := models.NotificationSuppression{
			Fingerprint: suppressionKey(notif.Kind, notif.Fingerprint),
			Kind:        notif.Kind,
		}

		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&suppression).Error; err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	s.publishRefresh()
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
	default:
		return fmt.Errorf("invalid_transport_type")
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
			if transportType != TransportTypeNtfy && transportType != TransportTypeSMTP {
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

				if entry.Email.SMTPPassword != nil {
					cfg.SMTPPassword = strings.TrimSpace(*entry.Email.SMTPPassword)
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

	var view RuleConfigView
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		pools, rulesByKind, err := s.ensureActivePoolRules(tx)
		if err != nil {
			return err
		}

		view = s.toRuleConfigView(pools, rulesByKind)
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

	entries := append([]RuleConfigEntryUpdate{}, input.Rules...)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		pools, rulesByKind, err := s.ensureActivePoolRules(tx)
		if err != nil {
			return err
		}

		activeKinds := make(map[string]struct{}, len(pools))
		for _, pool := range pools {
			activeKinds[notifier.KindForZFSPoolState(pool)] = struct{}{}
		}

		updatesByKind := make(map[string]RuleConfigEntryUpdate, len(entries))
		for _, entry := range entries {
			kind := strings.TrimSpace(strings.ToLower(entry.Kind))
			pool := strings.TrimSpace(strings.ToLower(entry.Pool))

			if kind == "" && pool != "" {
				kind = notifier.KindForZFSPoolState(pool)
			}
			if pool == "" {
				if derivedPool, ok := notifier.PoolFromZFSPoolStateKind(kind); ok {
					pool = derivedPool
				}
			}
			if kind == "" || pool == "" {
				return fmt.Errorf("notification_rule_kind_required")
			}

			expectedKind := notifier.KindForZFSPoolState(pool)
			if kind != expectedKind {
				return fmt.Errorf("notification_rule_kind_mismatch")
			}
			if _, ok := activeKinds[kind]; !ok {
				return fmt.Errorf("notification_rule_not_found")
			}
			if _, exists := updatesByKind[kind]; exists {
				return fmt.Errorf("duplicate_notification_rule_kind")
			}

			entry.Kind = kind
			entry.Pool = pool
			updatesByKind[kind] = entry
		}

		for kind, update := range updatesByKind {
			rule, ok := rulesByKind[kind]
			if !ok {
				return fmt.Errorf("notification_rule_not_found")
			}

			rule.UIEnabled = update.UIEnabled
			rule.NtfyEnabled = update.NtfyEnabled
			rule.EmailEnabled = update.EmailEnabled
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

func (s *Service) ensureKindRule(tx *gorm.DB, kind string) (models.NotificationKindRule, error) {
	kind = strings.TrimSpace(kind)
	var rule models.NotificationKindRule
	err := tx.Where("kind = ?", kind).First(&rule).Error
	if err == nil {
		return rule, nil
	}

	if err != gorm.ErrRecordNotFound {
		return models.NotificationKindRule{}, err
	}

	rule = models.NotificationKindRule{
		Kind:         kind,
		UIEnabled:    true,
		NtfyEnabled:  true,
		EmailEnabled: true,
	}
	if err := tx.Create(&rule).Error; err != nil {
		return models.NotificationKindRule{}, err
	}

	return rule, nil
}

func (s *Service) ensureActivePoolRules(tx *gorm.DB) ([]string, map[string]models.NotificationKindRule, error) {
	pools, err := s.listActivePools(tx)
	if err != nil {
		return nil, nil, err
	}

	rulesByKind := make(map[string]models.NotificationKindRule, len(pools))
	for _, pool := range pools {
		kind := notifier.KindForZFSPoolState(pool)
		rule, err := s.ensureKindRule(tx, kind)
		if err != nil {
			return nil, nil, err
		}
		rulesByKind[rule.Kind] = rule
	}

	return pools, rulesByKind, nil
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
		updated := false
		cfg := &configs[idx]
		normalizedType := normalizeTransportType(cfg.Type)
		if normalizedType == "" {
			if cfg.NtfyEnabled && !cfg.EmailEnabled {
				normalizedType = TransportTypeNtfy
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
		if updated {
			if err := tx.Save(cfg).Error; err != nil {
				return nil, err
			}
		}
	}

	return configs, nil
}

func (s *Service) listTransportConfigs(ctx context.Context) ([]models.NotificationTransportConfig, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("notifications_service_not_initialized")
	}

	return s.ensureTransportConfigsDB(ctx)
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

func (s *Service) toRuleConfigView(pools []string, rulesByKind map[string]models.NotificationKindRule) RuleConfigView {
	view := RuleConfigView{
		Rules: make([]RuleConfigEntryView, 0, len(pools)),
	}

	for _, pool := range pools {
		kind := notifier.KindForZFSPoolState(pool)
		rule, ok := rulesByKind[kind]
		if !ok {
			rule = models.NotificationKindRule{
				Kind:         kind,
				UIEnabled:    true,
				NtfyEnabled:  true,
				EmailEnabled: true,
			}
		}

		view.Rules = append(view.Rules, RuleConfigEntryView{
			Kind:         kind,
			Pool:         pool,
			UIEnabled:    rule.UIEnabled,
			NtfyEnabled:  rule.NtfyEnabled,
			EmailEnabled: rule.EmailEnabled,
		})
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
		return err
	}

	req.Header.Set("Title", input.Title)
	req.Header.Set("Tags", strings.TrimSpace(input.Severity))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	res, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 400 {
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

	subject := fmt.Sprintf("[Sylve][%s] %s", strings.ToUpper(strings.TrimSpace(input.Severity)), input.Title)
	body := strings.TrimSpace(input.Body)
	if body == "" {
		body = input.Title
	}

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
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)
	if strings.TrimSpace(input.Source) != "" {
		msg.WriteString("\n\nSource: ")
		msg.WriteString(strings.TrimSpace(input.Source))
	}
	if strings.TrimSpace(input.Kind) != "" {
		msg.WriteString("\nKind: ")
		msg.WriteString(strings.TrimSpace(input.Kind))
	}

	client, conn, err := dialSMTPClient(ctx, host, port, cfg.SMTPUseTLS)
	if err != nil {
		return err
	}
	defer closeSMTPClient(client, conn)

	var auth smtp.Auth
	username := strings.TrimSpace(cfg.SMTPUsername)
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp_auth_not_supported")
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}

	for _, recipient := range cfg.EmailRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}

	wc, err := client.Data()
	if err != nil {
		return err
	}

	if _, err := wc.Write([]byte(msg.String())); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}

	if err := client.Quit(); err != nil {
		return err
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
	default:
		return ""
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
