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
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

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
