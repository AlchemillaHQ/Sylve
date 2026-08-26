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
	"strings"
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestAsyncAuditExactOperationCorrelationAndTerminalCAS(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	started := time.Now().UTC().Add(-time.Second)
	records := []infoModels.AuditRecord{
		{Status: "started", Started: started, Action: `{"path":"/first"}`},
		{Status: "started", Started: started, Action: `{"path":"/second"}`},
	}
	if err := database.Create(&records).Error; err != nil {
		t.Fatalf("create request audits: %v", err)
	}

	first, err := PrepareAsyncAuditRecord(
		database,
		ContextWithAuditRecordID(context.Background(), records[0].ID),
		"backup_target_restore",
		42,
		"target-restore:node-a:first",
	)
	if err != nil {
		t.Fatalf("prepare first audit: %v", err)
	}
	second, err := PrepareAsyncAuditRecord(
		database,
		ContextWithAuditRecordID(context.Background(), records[1].ID),
		"backup_target_restore",
		42,
		"target-restore:node-a:second",
	)
	if err != nil {
		t.Fatalf("prepare second audit: %v", err)
	}

	if err := FinalizeAsyncAuditOperation(database, first, "failed", "first failed", map[string]any{
		"eventId": 11,
		"status":  "failed",
	}); err != nil {
		t.Fatalf("finalize first operation: %v", err)
	}
	if err := database.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("reload audits: %v", err)
	}
	if records[0].Status != "failed" || records[0].Error != "first failed" ||
		!strings.Contains(records[0].Action, `"eventId":11`) {
		t.Fatalf("first audit = %+v", records[0])
	}
	if records[1].Status != "pending" || records[1].AsyncOperationID != second.OperationID {
		t.Fatalf("unrelated concurrent audit was finalized: %+v", records[1])
	}

	// A stale terminal writer cannot change the first outcome.
	if err := FinalizeAsyncAuditOperation(database, first, "success", "", map[string]any{"status": "success"}); err != nil {
		t.Fatalf("replay terminal finalization: %v", err)
	}
	if err := database.First(&records[0], records[0].ID).Error; err != nil {
		t.Fatalf("reload first audit: %v", err)
	}
	if records[0].Status != "failed" || records[0].Error != "first failed" {
		t.Fatalf("terminal audit was overwritten: %+v", records[0])
	}
}

func TestPrepareAsyncAuditWithoutRequestContextIsOptional(t *testing.T) {
	ref, err := PrepareAsyncAuditRecord(nil, context.Background(), "backup_restore", 7, "restore:node-a:token")
	if err != nil {
		t.Fatalf("background preparation: %v", err)
	}
	if ref.RecordID != 0 || ref.OperationID != "restore:node-a:token" {
		t.Fatalf("background ref = %+v", ref)
	}
}
