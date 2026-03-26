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

func TestGetCPUUsageHistoricalReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.CPU{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.CPU{})

	now := time.Now().UTC().Truncate(time.Second)

	if err := mainDB.Create(&infoModels.CPU{
		ID:        1,
		Usage:     10.0,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed main db cpu row: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.CPU{
		ID:        2,
		Usage:     20.0,
		CreatedAt: now.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry db cpu row: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	history, err := svc.GetCPUUsageHistorical()
	if err != nil {
		t.Fatalf("GetCPUUsageHistorical returned error: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected one telemetry row, got %d", len(history))
	}

	if history[0].ID != 2 {
		t.Fatalf("expected telemetry row ID 2, got %d", history[0].ID)
	}
}
