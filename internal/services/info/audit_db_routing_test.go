// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetAuditRecordsReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})

	now := time.Now().UTC().Truncate(time.Second)

	if err := mainDB.Create(&infoModels.AuditRecord{
		ID:        1,
		User:      "legacy-main",
		AuthType:  "password",
		Node:      "node-a",
		Started:   now,
		Ended:     now,
		Action:    "{\"method\":\"GET\"}",
		Duration:  0,
		Status:    "success",
		CreatedAt: now,
		UpdatedAt: now,
		Version:   2,
	}).Error; err != nil {
		t.Fatalf("failed to seed main db audit row: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.AuditRecord{
		ID:        2,
		User:      "telemetry",
		AuthType:  "token",
		Node:      "node-a",
		Started:   now.Add(time.Second),
		Ended:     now.Add(time.Second),
		Action:    "{\"method\":\"POST\"}",
		Duration:  0,
		Status:    "success",
		CreatedAt: now.Add(time.Second),
		UpdatedAt: now.Add(time.Second),
		Version:   2,
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry db audit row: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	records, err := svc.GetAuditRecords(10)
	if err != nil {
		t.Fatalf("GetAuditRecords returned error: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected one telemetry row, got %d", len(records))
	}

	if records[0].ID != 2 {
		t.Fatalf("expected telemetry row ID 2, got %d", records[0].ID)
	}
}
