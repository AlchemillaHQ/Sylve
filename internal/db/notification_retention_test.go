// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestEnforceNotificationRetentionPrunesOldDismissedNotifications(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.Notification{})

	now := time.Date(2026, time.April, 24, 12, 0, 0, 0, time.UTC)
	oldDismissedAt := now.Add(-(NotificationDismissedRetentionDays*24*time.Hour + time.Hour))
	freshDismissedAt := now.Add(-24 * time.Hour)
	activeOccurredAt := now.Add(-48 * time.Hour)

	oldDismissed := models.Notification{
		Kind:            "test.kind",
		Title:           "old dismissed",
		Body:            "old",
		Severity:        models.NotificationSeverityWarning,
		Source:          "test",
		Fingerprint:     "old-dismissed",
		Metadata:        map[string]string{},
		OccurrenceCount: 1,
		FirstOccurredAt: oldDismissedAt,
		LastOccurredAt:  oldDismissedAt,
		DismissedAt:     &oldDismissedAt,
		CreatedAt:       oldDismissedAt,
		UpdatedAt:       oldDismissedAt,
	}
	freshDismissed := models.Notification{
		Kind:            "test.kind",
		Title:           "fresh dismissed",
		Body:            "fresh",
		Severity:        models.NotificationSeverityWarning,
		Source:          "test",
		Fingerprint:     "fresh-dismissed",
		Metadata:        map[string]string{},
		OccurrenceCount: 1,
		FirstOccurredAt: freshDismissedAt,
		LastOccurredAt:  freshDismissedAt,
		DismissedAt:     &freshDismissedAt,
		CreatedAt:       freshDismissedAt,
		UpdatedAt:       freshDismissedAt,
	}
	active := models.Notification{
		Kind:            "test.kind",
		Title:           "active",
		Body:            "active",
		Severity:        models.NotificationSeverityInfo,
		Source:          "test",
		Fingerprint:     "active",
		Metadata:        map[string]string{},
		OccurrenceCount: 1,
		FirstOccurredAt: activeOccurredAt,
		LastOccurredAt:  activeOccurredAt,
		DismissedAt:     nil,
		CreatedAt:       activeOccurredAt,
		UpdatedAt:       activeOccurredAt,
	}

	if err := db.Create(&[]models.Notification{oldDismissed, freshDismissed, active}).Error; err != nil {
		t.Fatalf("failed_to_seed_notifications: %v", err)
	}

	if err := EnforceNotificationRetention(db, now); err != nil {
		t.Fatalf("enforce_notification_retention_failed: %v", err)
	}

	var kept []models.Notification
	if err := db.Order("fingerprint ASC").Find(&kept).Error; err != nil {
		t.Fatalf("failed_to_fetch_notifications: %v", err)
	}

	if len(kept) != 2 {
		t.Fatalf("expected_2_notifications_remaining_got_%d", len(kept))
	}
	if kept[0].Fingerprint != "active" || kept[1].Fingerprint != "fresh-dismissed" {
		t.Fatalf("unexpected_remaining_notifications: %s, %s", kept[0].Fingerprint, kept[1].Fingerprint)
	}
}

func TestEnforceNotificationRetentionPrunesSuppressionsByAgeAndZFSPrefix(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &models.NotificationSuppression{})

	now := time.Date(2026, time.April, 24, 12, 0, 0, 0, time.UTC)
	oldCreatedAt := now.Add(-(NotificationSuppressionRetentionDays*24*time.Hour + time.Hour))
	freshCreatedAt := now.Add(-2 * time.Hour)

	rows := []models.NotificationSuppression{
		{
			Fingerprint: "storage.disks|disk-ada0-failure",
			Kind:        "storage.disks",
			CreatedAt:   oldCreatedAt,
		},
		{
			Fingerprint: "storage.disks|disk-ada1-failure",
			Kind:        "storage.disks",
			CreatedAt:   freshCreatedAt,
		},
		{
			Fingerprint: notifier.KindForZFSPoolState("test") + "|test|vdev0|degraded",
			Kind:        notifier.KindForZFSPoolState("test"),
			CreatedAt:   freshCreatedAt,
		},
	}

	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed_to_seed_suppressions: %v", err)
	}

	if err := EnforceNotificationRetention(db, now); err != nil {
		t.Fatalf("enforce_notification_retention_failed: %v", err)
	}

	var kept []models.NotificationSuppression
	if err := db.Order("fingerprint ASC").Find(&kept).Error; err != nil {
		t.Fatalf("failed_to_fetch_suppressions: %v", err)
	}

	if len(kept) != 1 {
		t.Fatalf("expected_1_suppression_remaining_got_%d", len(kept))
	}
	if kept[0].Kind != "storage.disks" || kept[0].Fingerprint != "storage.disks|disk-ada1-failure" {
		t.Fatalf("unexpected_remaining_suppression: kind=%s fingerprint=%s", kept[0].Kind, kept[0].Fingerprint)
	}
}
