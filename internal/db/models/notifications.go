// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import "time"

type NotificationSeverity string

const (
	NotificationSeverityInfo     NotificationSeverity = "info"
	NotificationSeverityWarning  NotificationSeverity = "warning"
	NotificationSeverityError    NotificationSeverity = "error"
	NotificationSeverityCritical NotificationSeverity = "critical"
)

type Notification struct {
	ID              uint                 `json:"id" gorm:"primaryKey"`
	Kind            string               `json:"kind" gorm:"index;not null"`
	Title           string               `json:"title" gorm:"not null"`
	Body            string               `json:"body" gorm:"type:text"`
	Severity        NotificationSeverity `json:"severity" gorm:"index;not null;default:info"`
	Source          string               `json:"source" gorm:"index"`
	Fingerprint     string               `json:"fingerprint" gorm:"uniqueIndex;not null"`
	Metadata        map[string]string    `json:"metadata" gorm:"serializer:json;type:json"`
	OccurrenceCount int                  `json:"occurrenceCount" gorm:"not null;default:1"`
	FirstOccurredAt time.Time            `json:"firstOccurredAt" gorm:"not null;index"`
	LastOccurredAt  time.Time            `json:"lastOccurredAt" gorm:"not null;index"`
	DismissedAt     *time.Time           `json:"dismissedAt" gorm:"index"`
	CreatedAt       time.Time            `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt       time.Time            `json:"updatedAt" gorm:"autoUpdateTime"`
}

type NotificationSuppression struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Fingerprint string    `json:"fingerprint" gorm:"uniqueIndex;not null"`
	Kind        string    `json:"kind" gorm:"index"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

type NotificationKindRule struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Kind         string    `json:"kind" gorm:"uniqueIndex;not null"`
	UIEnabled    bool      `json:"uiEnabled" gorm:"not null;default:true"`
	NtfyEnabled  bool      `json:"ntfyEnabled" gorm:"not null;default:true"`
	EmailEnabled bool      `json:"emailEnabled" gorm:"not null;default:true"`
	CreatedAt    time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt    time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type NotificationTransportConfig struct {
	ID                      uint      `json:"id" gorm:"primaryKey"`
	NtfyEnabled             bool      `json:"ntfyEnabled" gorm:"not null;default:false"`
	NtfyBaseURL             string    `json:"ntfyBaseUrl" gorm:"not null;default:https://ntfy.sh"`
	NtfyTopic               string    `json:"ntfyTopic"`
	NtfyAuthTokenSecretName string    `json:"-" gorm:"not null;default:notifications_ntfy_token"`
	EmailEnabled            bool      `json:"emailEnabled" gorm:"not null;default:false"`
	SMTPHost                string    `json:"smtpHost"`
	SMTPPort                int       `json:"smtpPort" gorm:"not null;default:587"`
	SMTPUsername            string    `json:"smtpUsername"`
	SMTPFrom                string    `json:"smtpFrom"`
	SMTPUseTLS              bool      `json:"smtpUseTls" gorm:"not null;default:true"`
	SMTPPasswordSecretName  string    `json:"-" gorm:"not null;default:notifications_smtp_password"`
	EmailRecipients         []string  `json:"emailRecipients" gorm:"serializer:json;type:json"`
	CreatedAt               time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt               time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
