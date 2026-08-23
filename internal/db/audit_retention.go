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

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"gorm.io/gorm"
)

const (
	AuditRecordRetentionDays = 180
	AuditRecordMaxRows       = 25000
)

func EnforceAuditRecordRetention(db *gorm.DB, now time.Time) error {
	if !db.Migrator().HasTable(&infoModels.AuditRecord{}) {
		return nil
	}

	cutoff := now.Add(-AuditRecordRetentionDays * 24 * time.Hour)

	activeStatuses := []string{"pending"}
	if err := db.
		Where("created_at < ? AND status NOT IN ?", cutoff, activeStatuses).
		Delete(&infoModels.AuditRecord{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_prune_expired_audit_records: %w", err)
	}

	var count int64
	if err := db.Model(&infoModels.AuditRecord{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_count_audit_records: %w", err)
	}

	if count <= AuditRecordMaxRows {
		return nil
	}

	var activeCount int64
	if err := db.Model(&infoModels.AuditRecord{}).
		Where("status IN ?", activeStatuses).
		Count(&activeCount).Error; err != nil {
		return fmt.Errorf("failed_to_count_active_audit_records: %w", err)
	}
	if activeCount >= int64(AuditRecordMaxRows) {
		return nil
	}
	terminalBudget := AuditRecordMaxRows - int(activeCount)
	keepNewestTerminal := db.
		Model(&infoModels.AuditRecord{}).
		Select("id").
		Where("status NOT IN ?", activeStatuses).
		Order("created_at DESC, id DESC").
		Limit(terminalBudget)

	if err := db.
		Where("status NOT IN ? AND id NOT IN (?)", activeStatuses, keepNewestTerminal).
		Delete(&infoModels.AuditRecord{}).
		Error; err != nil {
		return fmt.Errorf("failed_to_enforce_audit_record_hard_cap: %w", err)
	}

	return nil
}
