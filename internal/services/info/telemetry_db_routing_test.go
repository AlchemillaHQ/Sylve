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

func TestGetRAMUsageHistoricalReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.RAM{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.RAM{})

	now := time.Now().UTC().Truncate(time.Second)

	if err := mainDB.Create(&infoModels.RAM{
		ID:        1,
		Usage:     10.0,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed main db ram row: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.RAM{
		ID:        2,
		Usage:     20.0,
		CreatedAt: now.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry db ram row: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	history, err := svc.GetRAMUsageHistorical()
	if err != nil {
		t.Fatalf("GetRAMUsageHistorical returned error: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected one telemetry row, got %d", len(history))
	}

	if history[0].ID != 2 {
		t.Fatalf("expected telemetry row ID 2, got %d", history[0].ID)
	}
}

func TestGetSwapUsageHistoricalReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.Swap{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.Swap{})

	now := time.Now().UTC().Truncate(time.Second)

	if err := mainDB.Create(&infoModels.Swap{
		ID:        1,
		Usage:     5.0,
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed main db swap row: %v", err)
	}

	if err := telemetryDB.Create(&infoModels.Swap{
		ID:        2,
		Usage:     15.0,
		CreatedAt: now.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry db swap row: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	history, err := svc.GetSwapUsageHistorical()
	if err != nil {
		t.Fatalf("GetSwapUsageHistorical returned error: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected one telemetry row, got %d", len(history))
	}

	if history[0].ID != 2 {
		t.Fatalf("expected telemetry row ID 2, got %d", history[0].ID)
	}
}

func TestGetNetworkInterfacesHistoricalReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &infoModels.NetworkInterface{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &infoModels.NetworkInterface{})

	now := time.Now().UTC().Truncate(time.Second)

	mainRows := []infoModels.NetworkInterface{
		{ID: 1, Name: "igb0", Network: "link#1", IsDelta: false, ReceivedBytes: 100, SentBytes: 100, CreatedAt: now, UpdatedAt: now},
		{ID: 2, Name: "igb0", Network: "link#1", IsDelta: false, ReceivedBytes: 200, SentBytes: 300, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	}
	if err := mainDB.Create(&mainRows).Error; err != nil {
		t.Fatalf("failed to seed main db network rows: %v", err)
	}

	telemetryRows := []infoModels.NetworkInterface{
		{ID: 3, Name: "igb1", Network: "link#2", IsDelta: false, ReceivedBytes: 1000, SentBytes: 2000, CreatedAt: now, UpdatedAt: now},
		{ID: 4, Name: "igb1", Network: "link#2", IsDelta: false, ReceivedBytes: 1250, SentBytes: 2600, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	}
	if err := telemetryDB.Create(&telemetryRows).Error; err != nil {
		t.Fatalf("failed to seed telemetry db network rows: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	history, err := svc.GetNetworkInterfacesHistorical()
	if err != nil {
		t.Fatalf("GetNetworkInterfacesHistorical returned error: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected one telemetry bucket row, got %d", len(history))
	}

	if history[0].ReceivedBytes != 250 {
		t.Fatalf("expected telemetry received delta 250, got %d", history[0].ReceivedBytes)
	}
	if history[0].SentBytes != 600 {
		t.Fatalf("expected telemetry sent delta 600, got %d", history[0].SentBytes)
	}
}
