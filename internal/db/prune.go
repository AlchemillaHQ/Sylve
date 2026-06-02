// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"fmt"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const (
	BackupEventRetentionDays       = 90
	BackupEventMaxRows             = 10000
	ReplicationEventRetentionDays  = 90
	ReplicationEventMaxRows        = 10000
)

func EnforceBackupEventRetention(db *gorm.DB, now time.Time) error {
	if !db.Migrator().HasTable(&clusterModels.BackupEvent{}) {
		return nil
	}

	cutoff := now.Add(-BackupEventRetentionDays * 24 * time.Hour)

	if err := db.
		Where("completed_at IS NOT NULL AND completed_at < ?", cutoff).
		Delete(&clusterModels.BackupEvent{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_backup_events: %w", err)
	}

	var count int64
	if err := db.Model(&clusterModels.BackupEvent{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_count_backup_events: %w", err)
	}

	if count <= BackupEventMaxRows {
		return nil
	}

	keepNewest := db.
		Model(&clusterModels.BackupEvent{}).
		Select("id").
		Order("started_at DESC, id DESC").
		Limit(BackupEventMaxRows)

	if err := db.
		Where("id NOT IN (?)", keepNewest).
		Delete(&clusterModels.BackupEvent{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_enforce_backup_event_hard_cap: %w", err)
	}

	return nil
}

func EnforceReplicationEventRetention(db *gorm.DB, now time.Time) error {
	if !db.Migrator().HasTable(&clusterModels.ReplicationEvent{}) {
		return nil
	}

	cutoff := now.Add(-ReplicationEventRetentionDays * 24 * time.Hour)

	if err := db.
		Where("completed_at IS NOT NULL AND completed_at < ?", cutoff).
		Delete(&clusterModels.ReplicationEvent{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_replication_events: %w", err)
	}

	var count int64
	if err := db.Model(&clusterModels.ReplicationEvent{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_count_replication_events: %w", err)
	}

	if count <= ReplicationEventMaxRows {
		return nil
	}

	keepNewest := db.
		Model(&clusterModels.ReplicationEvent{}).
		Select("id").
		Order("started_at DESC, id DESC").
		Limit(ReplicationEventMaxRows)

	if err := db.
		Where("id NOT IN (?)", keepNewest).
		Delete(&clusterModels.ReplicationEvent{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_enforce_replication_event_hard_cap: %w", err)
	}

	return nil
}

func CleanupOrphanBackupEvents(db *gorm.DB) error {
	deleteResult := db.Where(
		"job_id IS NOT NULL AND job_id NOT IN (?)",
		db.Model(&clusterModels.BackupJob{}).Select("id"),
	).Delete(&clusterModels.BackupEvent{})
	if deleteResult.Error != nil {
		return deleteResult.Error
	}

	if deleteResult.RowsAffected > 0 {
		logger.L.Info().Int64("count", deleteResult.RowsAffected).Msg("Removed orphan backup events")
	}

	return nil
}

func PruneJobs(db *gorm.DB) error {
	if err := CleanupOrphanBackupEvents(db); err != nil {
		return err
	}

	if err := EnforceBackupEventRetention(db, time.Now()); err != nil {
		return err
	}

	if err := EnforceReplicationEventRetention(db, time.Now()); err != nil {
		return err
	}

	if err := EnforceAuditRecordRetention(db, time.Now()); err != nil {
		return err
	}

	if err := EnforceNotificationRetention(db, time.Now()); err != nil {
		return err
	}

	return nil
}

func StartPruneWorker(ctx context.Context, db *gorm.DB) {
	cleanup := func() {
		if err := PruneJobs(db); err != nil {
			logger.L.Error().Err(err).Msg("periodic_prune_jobs_failed")
		}
	}

	go func() {
		cleanup()

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.L.Debug().Msg("Stopped prune worker")
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}
