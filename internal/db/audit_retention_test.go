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

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestEnforceAuditRecordRetentionHardCapKeepsNewest(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})

	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	total := AuditRecordMaxRows + 10

	rows := make([]infoModels.AuditRecord, 0, total)
	for i := 0; i < total; i++ {
		createdAt := now.Add(-time.Duration(total-i) * time.Minute)
		rows = append(rows, auditRecordAt(createdAt, "user"))
	}

	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("failed_to_seed_audit_records: %v", err)
	}

	if err := EnforceAuditRecordRetention(db, now); err != nil {
		t.Fatalf("enforce_retention_failed: %v", err)
	}

	var count int64
	if err := db.Model(&infoModels.AuditRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("failed_to_count_audit_records: %v", err)
	}
	if count != AuditRecordMaxRows {
		t.Fatalf("expected_%d_rows_after_cap_got_%d", AuditRecordMaxRows, count)
	}

	var oldestRemaining infoModels.AuditRecord
	if err := db.Order("created_at ASC, id ASC").First(&oldestRemaining).Error; err != nil {
		t.Fatalf("failed_to_fetch_oldest_remaining_record: %v", err)
	}
	if !oldestRemaining.CreatedAt.Equal(rows[10].CreatedAt) {
		t.Fatalf("expected_oldest_remaining_created_at_%s_got_%s", rows[10].CreatedAt, oldestRemaining.CreatedAt)
	}

	var newestRemaining infoModels.AuditRecord
	if err := db.Order("created_at DESC, id DESC").First(&newestRemaining).Error; err != nil {
		t.Fatalf("failed_to_fetch_newest_remaining_record: %v", err)
	}
	if !newestRemaining.CreatedAt.Equal(rows[len(rows)-1].CreatedAt) {
		t.Fatalf("expected_newest_remaining_created_at_%s_got_%s", rows[len(rows)-1].CreatedAt, newestRemaining.CreatedAt)
	}
}

func TestEnforceAuditRecordRetentionDeletesExpiredRows(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})

	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-(AuditRecordRetentionDays*24*time.Hour + time.Hour))
	fresh := now.Add(-24 * time.Hour)

	rows := []infoModels.AuditRecord{
		auditRecordAt(expired, "expired-user"),
		auditRecordAt(fresh, "fresh-user"),
	}

	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed_to_seed_expired_and_fresh_records: %v", err)
	}

	if err := EnforceAuditRecordRetention(db, now); err != nil {
		t.Fatalf("enforce_retention_failed: %v", err)
	}

	var kept []infoModels.AuditRecord
	if err := db.Order("created_at ASC, id ASC").Find(&kept).Error; err != nil {
		t.Fatalf("failed_to_fetch_kept_audit_records: %v", err)
	}

	if len(kept) != 1 {
		t.Fatalf("expected_1_audit_record_after_retention_got_%d", len(kept))
	}
	if kept[0].User != "fresh-user" {
		t.Fatalf("expected_fresh_record_to_remain_got_%q", kept[0].User)
	}
}

func auditRecordAt(createdAt time.Time, user string) infoModels.AuditRecord {
	return infoModels.AuditRecord{
		User:      user,
		AuthType:  "token",
		Node:      "node",
		Started:   createdAt,
		Ended:     createdAt.Add(time.Second),
		Action:    `{"method":"POST","path":"/api/test"}`,
		Duration:  time.Second,
		Status:    "success",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Version:   2,
	}
}
