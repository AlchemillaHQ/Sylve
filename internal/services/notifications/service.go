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
	"errors"
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
	defaultNtfyBaseURL        = "https://ntfy.sh"
	defaultNtfySecretName     = "notifications_ntfy_token"
	defaultSMTPPasswordSecret = "notifications_smtp_password"
	defaultSMTPPort           = 587
	defaultListLimit          = 50
	maxListLimit              = 500
)

type SecretStore interface {
	GetSecret(name string) (string, error)
	UpsertSecret(name string, data string) error
}

type NtfySender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, token string) error

type EmailSender func(ctx context.Context, cfg models.NotificationTransportConfig, input notifier.EventInput, password string) error

type Service struct {
	DB         *gorm.DB
	secrets    SecretStore
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
	Ntfy  NtfyTransportConfigView  `json:"ntfy"`
	Email EmailTransportConfigView `json:"email"`
}

type NtfyTransportConfigView struct {
	Enabled      bool   `json:"enabled"`
	BaseURL      string `json:"baseUrl"`
	Topic        string `json:"topic"`
	HasAuthToken bool   `json:"hasAuthToken"`
}

type EmailTransportConfigView struct {
	Enabled      bool     `json:"enabled"`
	SMTPHost     string   `json:"smtpHost"`
	SMTPPort     int      `json:"smtpPort"`
	SMTPUsername string   `json:"smtpUsername"`
	SMTPFrom     string   `json:"smtpFrom"`
	SMTPUseTLS   bool     `json:"smtpUseTls"`
	Recipients   []string `json:"recipients"`
	HasPassword  bool     `json:"hasPassword"`
}

type TransportConfigUpdate struct {
	Ntfy  NtfyTransportConfigUpdate  `json:"ntfy"`
	Email EmailTransportConfigUpdate `json:"email"`
}

type NtfyTransportConfigUpdate struct {
	Enabled   bool    `json:"enabled"`
	BaseURL   string  `json:"baseUrl"`
	Topic     string  `json:"topic"`
	AuthToken *string `json:"authToken,omitempty"`
}

type EmailTransportConfigUpdate struct {
	Enabled      bool     `json:"enabled"`
	SMTPHost     string   `json:"smtpHost"`
	SMTPPort     int      `json:"smtpPort"`
	SMTPUsername string   `json:"smtpUsername"`
	SMTPFrom     string   `json:"smtpFrom"`
	SMTPUseTLS   bool     `json:"smtpUseTls"`
	Recipients   []string `json:"recipients"`
	SMTPPassword *string  `json:"smtpPassword,omitempty"`
}

func NewService(db *gorm.DB, secrets SecretStore) *Service {
	s := &Service{
		DB:      db,
		secrets: secrets,
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
	var cfg models.NotificationTransportConfig
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

		cfg, err = s.ensureTransportConfig(tx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return notifier.EmitResult{}, err
	}

	if result.Suppressed {
		return result, nil
	}

	s.publishRefresh()

	if cfg.NtfyEnabled && kindRule.NtfyEnabled {
		token := s.getSecret(cfg.NtfyAuthTokenSecretName)
		if err := s.ntfySender(ctx, cfg, normalized, token); err == nil {
			result.SentNtfy = true
		}
	}

	if cfg.EmailEnabled && kindRule.EmailEnabled && len(cfg.EmailRecipients) > 0 {
		password := s.getSecret(cfg.SMTPPasswordSecretName)
		if err := s.emailSender(ctx, cfg, normalized, password); err == nil {
			result.SentEmail = true
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

func (s *Service) GetTransportConfig(ctx context.Context) (TransportConfigView, error) {
	if s == nil || s.DB == nil {
		return TransportConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}

	cfg, err := s.ensureTransportConfigDB(ctx)
	if err != nil {
		return TransportConfigView{}, err
	}

	return s.toTransportConfigView(cfg), nil
}

func (s *Service) UpdateTransportConfig(ctx context.Context, input TransportConfigUpdate) (TransportConfigView, error) {
	if s == nil || s.DB == nil {
		return TransportConfigView{}, fmt.Errorf("notifications_service_not_initialized")
	}

	normalizedRecipients := make([]string, 0, len(input.Email.Recipients))
	seen := map[string]struct{}{}
	for _, recipient := range input.Email.Recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if !utils.IsValidEmail(recipient) {
			return TransportConfigView{}, fmt.Errorf("invalid_email_recipient: %s", recipient)
		}
		if _, ok := seen[recipient]; ok {
			continue
		}
		seen[recipient] = struct{}{}
		normalizedRecipients = append(normalizedRecipients, recipient)
	}
	sort.Strings(normalizedRecipients)

	var updated models.NotificationTransportConfig

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cfg, err := s.ensureTransportConfig(tx)
		if err != nil {
			return err
		}

		cfg.NtfyEnabled = input.Ntfy.Enabled
		cfg.NtfyBaseURL = normalizeNtfyBaseURL(input.Ntfy.BaseURL)
		cfg.NtfyTopic = strings.TrimSpace(input.Ntfy.Topic)
		cfg.EmailEnabled = input.Email.Enabled
		cfg.SMTPHost = strings.TrimSpace(input.Email.SMTPHost)
		cfg.SMTPPort = input.Email.SMTPPort
		if cfg.SMTPPort <= 0 {
			cfg.SMTPPort = defaultSMTPPort
		}
		cfg.SMTPUsername = strings.TrimSpace(input.Email.SMTPUsername)
		cfg.SMTPFrom = strings.TrimSpace(input.Email.SMTPFrom)
		if cfg.SMTPFrom != "" && !utils.IsValidEmail(cfg.SMTPFrom) {
			return fmt.Errorf("invalid_smtp_from_email")
		}
		cfg.SMTPUseTLS = input.Email.SMTPUseTLS
		cfg.EmailRecipients = normalizedRecipients
		cfg.NtfyAuthTokenSecretName = ensureSecretName(cfg.NtfyAuthTokenSecretName, defaultNtfySecretName)
		cfg.SMTPPasswordSecretName = ensureSecretName(cfg.SMTPPasswordSecretName, defaultSMTPPasswordSecret)

		if input.Ntfy.AuthToken != nil {
			if err := upsertSecretTx(tx, cfg.NtfyAuthTokenSecretName, strings.TrimSpace(*input.Ntfy.AuthToken)); err != nil {
				return err
			}
		}

		if input.Email.SMTPPassword != nil {
			if err := upsertSecretTx(tx, cfg.SMTPPasswordSecretName, strings.TrimSpace(*input.Email.SMTPPassword)); err != nil {
				return err
			}
		}

		if err := tx.Save(&cfg).Error; err != nil {
			return err
		}

		updated = cfg
		return nil
	})
	if err != nil {
		return TransportConfigView{}, err
	}

	return s.toTransportConfigView(updated), nil
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

func (s *Service) ensureTransportConfigDB(ctx context.Context) (models.NotificationTransportConfig, error) {
	var cfg models.NotificationTransportConfig
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := s.ensureTransportConfig(tx)
		if err != nil {
			return err
		}
		cfg = current
		return nil
	})
	if err != nil {
		return models.NotificationTransportConfig{}, err
	}

	return cfg, nil
}

func (s *Service) ensureTransportConfig(tx *gorm.DB) (models.NotificationTransportConfig, error) {
	var cfg models.NotificationTransportConfig
	err := tx.First(&cfg).Error
	if err == nil {
		updated := false
		if strings.TrimSpace(cfg.NtfyBaseURL) == "" {
			cfg.NtfyBaseURL = defaultNtfyBaseURL
			updated = true
		}
		if cfg.SMTPPort <= 0 {
			cfg.SMTPPort = defaultSMTPPort
			updated = true
		}
		if strings.TrimSpace(cfg.NtfyAuthTokenSecretName) == "" {
			cfg.NtfyAuthTokenSecretName = defaultNtfySecretName
			updated = true
		}
		if strings.TrimSpace(cfg.SMTPPasswordSecretName) == "" {
			cfg.SMTPPasswordSecretName = defaultSMTPPasswordSecret
			updated = true
		}
		if updated {
			if saveErr := tx.Save(&cfg).Error; saveErr != nil {
				return models.NotificationTransportConfig{}, saveErr
			}
		}

		return cfg, nil
	}

	if err != gorm.ErrRecordNotFound {
		return models.NotificationTransportConfig{}, err
	}

	cfg = models.NotificationTransportConfig{
		NtfyEnabled:             false,
		NtfyBaseURL:             defaultNtfyBaseURL,
		NtfyTopic:               "",
		NtfyAuthTokenSecretName: defaultNtfySecretName,
		EmailEnabled:            false,
		SMTPHost:                "",
		SMTPPort:                defaultSMTPPort,
		SMTPUsername:            "",
		SMTPFrom:                "",
		SMTPUseTLS:              true,
		SMTPPasswordSecretName:  defaultSMTPPasswordSecret,
		EmailRecipients:         []string{},
	}

	if err := tx.Create(&cfg).Error; err != nil {
		return models.NotificationTransportConfig{}, err
	}

	return cfg, nil
}

func (s *Service) toTransportConfigView(cfg models.NotificationTransportConfig) TransportConfigView {
	return TransportConfigView{
		Ntfy: NtfyTransportConfigView{
			Enabled:      cfg.NtfyEnabled,
			BaseURL:      normalizeNtfyBaseURL(cfg.NtfyBaseURL),
			Topic:        strings.TrimSpace(cfg.NtfyTopic),
			HasAuthToken: s.hasSecret(cfg.NtfyAuthTokenSecretName),
		},
		Email: EmailTransportConfigView{
			Enabled:      cfg.EmailEnabled,
			SMTPHost:     strings.TrimSpace(cfg.SMTPHost),
			SMTPPort:     cfg.SMTPPort,
			SMTPUsername: strings.TrimSpace(cfg.SMTPUsername),
			SMTPFrom:     strings.TrimSpace(cfg.SMTPFrom),
			SMTPUseTLS:   cfg.SMTPUseTLS,
			Recipients:   append([]string{}, cfg.EmailRecipients...),
			HasPassword:  s.hasSecret(cfg.SMTPPasswordSecretName),
		},
	}
}

func (s *Service) upsertSecret(name, value string) error {
	if s.secrets == nil {
		return fmt.Errorf("secret_store_not_available")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("secret_name_required")
	}
	return s.secrets.UpsertSecret(name, value)
}

func upsertSecretTx(tx *gorm.DB, name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("secret_name_required")
	}

	var secret models.SystemSecrets
	err := tx.Where("name = ?", name).First(&secret).Error
	if err == nil {
		if secret.Data == value {
			return nil
		}

		return tx.Model(&secret).Update("data", value).Error
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&models.SystemSecrets{
			Name: name,
			Data: value,
		}).Error
	}

	return err
}

func (s *Service) getSecret(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	if s.DB != nil {
		var secret models.SystemSecrets
		if err := s.DB.Where("name = ?", name).First(&secret).Error; err == nil {
			return strings.TrimSpace(secret.Data)
		}
	}

	if s.secrets != nil {
		value, err := s.secrets.GetSecret(name)
		if err == nil {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func (s *Service) hasSecret(name string) bool {
	return s.getSecret(name) != ""
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

func ensureSecretName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func suppressionKey(kind, fingerprint string) string {
	return strings.TrimSpace(strings.ToLower(kind)) + "|" + strings.TrimSpace(fingerprint)
}
