// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPoolScrubBusy = errors.New("pool_scrub_scheduler_busy")
var ErrPoolScrubActive = errors.New("pool_scrub_active")

// The lease coordinates starts, reconciliation, and pool-scope mutations across
// service instances. Callers bound their work to less than the lease duration.
const PoolScrubLeaseDuration = 10 * time.Minute

func migratePoolScrubScheduleIndexes(database *gorm.DB) error {
	if !database.Migrator().HasTable(&zfsModels.PoolScrubSchedule{}) {
		return nil
	}
	return database.Transaction(func(tx *gorm.DB) error {
		indexes, err := tx.Migrator().GetIndexes(&zfsModels.PoolScrubSchedule{})
		if err != nil {
			return fmt.Errorf("read pool scrub schedule indexes: %w", err)
		}
		for _, index := range indexes {
			unique, known := index.Unique()
			columns := index.Columns()
			if !known || !unique || len(columns) != 1 || columns[0] != "pool_guid" {
				continue
			}
			// AutoMigrate keeps the original one-schedule-per-pool index.
			if err := tx.Migrator().DropIndex(&zfsModels.PoolScrubSchedule{}, index.Name()); err != nil {
				return fmt.Errorf("drop unique pool scrub schedule index: %w", err)
			}
		}
		if !tx.Migrator().HasIndex(&zfsModels.PoolScrubSchedule{}, "PoolGUID") {
			if err := tx.Migrator().CreateIndex(&zfsModels.PoolScrubSchedule{}, "PoolGUID"); err != nil {
				return fmt.Errorf("create pool scrub schedule lookup index: %w", err)
			}
		}
		return nil
	})
}

func RecordPoolScrubCompletion(tx *gorm.DB, guid string, completedAt time.Time) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "pool_guid"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_completed_at"}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "pool_scrub_cooldowns.last_completed_at < excluded.last_completed_at"},
		}},
	}).Create(&zfsModels.PoolScrubCooldown{PoolGUID: guid, LastCompletedAt: completedAt.UTC()}).Error
}

func backfillPoolScrubCooldowns(database *gorm.DB) error {
	if !database.Migrator().HasTable(&zfsModels.PoolScrubRun{}) || !database.Migrator().HasTable(&zfsModels.PoolScrubCooldown{}) {
		return nil
	}
	return database.Transaction(func(tx *gorm.DB) error {
		var runs []zfsModels.PoolScrubRun
		if err := tx.Select("pool_guid", "completed_at").
			Where("started_at IS NOT NULL AND completed_at >= started_at AND completed_at <= ? AND status IN ?",
				time.Now().UTC().Add(time.Minute), []string{"passed", "failed", "cancelled"}).Find(&runs).Error; err != nil {
			return fmt.Errorf("read completed scrubs for cooldown migration: %w", err)
		}
		for _, run := range runs {
			if err := RecordPoolScrubCompletion(tx, run.PoolGUID, *run.CompletedAt); err != nil {
				return fmt.Errorf("backfill pool scrub cooldown: %w", err)
			}
		}
		return nil
	})
}

func AcquirePoolScrubLease(ctx context.Context, database *gorm.DB) (string, error) {
	lease := zfsModels.PoolScrubSchedulerLease{ID: 1}
	if err := database.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&lease).Error; err != nil {
		return "", err
	}
	now := time.Now().UTC()
	token := utils.GenerateRandomUUID()
	claim := database.WithContext(ctx).Model(&lease).
		Where("id = 1 AND (token = '' OR expires_at <= ?)", now).
		Updates(map[string]any{"token": token, "expires_at": now.Add(PoolScrubLeaseDuration)})
	if claim.Error != nil {
		return "", claim.Error
	}
	if claim.RowsAffected != 1 {
		return "", ErrPoolScrubBusy
	}
	return token, nil
}

func ReleasePoolScrubLease(ctx context.Context, database *gorm.DB, token string) error {
	return database.WithContext(ctx).Model(&zfsModels.PoolScrubSchedulerLease{}).
		Where("id = 1 AND token = ?", token).
		Updates(map[string]any{"token": "", "expires_at": time.Time{}}).Error
}
