// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
	"gorm.io/gorm"
)

const (
	NotificationDismissedRetentionDays   = 30
	NotificationSuppressionRetentionDays = 90
	DiskSmartSelfTestEventRetentionDays  = 30
)

func EnforceNotificationRetention(db *gorm.DB, now time.Time) error {
	if db == nil {
		return fmt.Errorf("db_not_initialized")
	}

	if db.Migrator().HasTable(&models.Notification{}) {
		dismissedCutoff := now.Add(-NotificationDismissedRetentionDays * 24 * time.Hour)
		if err := db.
			Where("dismissed_at IS NOT NULL").
			Where("dismissed_at < ?", dismissedCutoff).
			Delete(&models.Notification{}).
			Error; err != nil {
			return fmt.Errorf("failed_to_prune_expired_notifications: %w", err)
		}
	}

	if db.Migrator().HasTable(&models.NotificationSuppression{}) {
		suppressionCutoff := now.Add(-NotificationSuppressionRetentionDays * 24 * time.Hour)
		if err := db.
			Where("created_at < ?", suppressionCutoff).
			Delete(&models.NotificationSuppression{}).
			Error; err != nil {
			return fmt.Errorf("failed_to_prune_expired_notification_suppressions: %w", err)
		}

		if err := db.
			Where("kind LIKE ?", notifier.ZFSPoolStateKindPrefix+"%").
			Delete(&models.NotificationSuppression{}).
			Error; err != nil {
			return fmt.Errorf("failed_to_prune_zfs_notification_suppressions: %w", err)
		}

		for _, prefix := range []string{
			notifier.DiskSmartTemperatureKindPrefix,
			notifier.DiskSmartWearoutKindPrefix,
			notifier.DiskSmartHealthKindPrefix,
			notifier.DiskSmartNvmeKindPrefix,
			notifier.DiskSmartSelfTestKindPrefix,
		} {
			if err := db.
				Where("kind LIKE ?", prefix+"%").
				Delete(&models.NotificationSuppression{}).
				Error; err != nil {
				return fmt.Errorf("failed_to_prune_disk_smart_notification_suppressions: %w", err)
			}
		}
	}

	if db.Migrator().HasTable(&models.DiskSmartSelfTestEvent{}) {
		cutoff := now.UTC().Add(-DiskSmartSelfTestEventRetentionDays * 24 * time.Hour)
		if err := db.
			Where("dead_lettered_at IS NOT NULL").
			Where("dead_lettered_at < ?", cutoff).
			Delete(&models.DiskSmartSelfTestEvent{}).
			Error; err != nil {
			return fmt.Errorf("failed_to_prune_dead_lettered_disk_smart_self_test_events: %w", err)
		}
	}

	return nil
}
