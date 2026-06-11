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
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func testReadsFromTelemetryDB[T any](
	t *testing.T,
	models []any,
	seedMain func(*gorm.DB) error,
	seedTelemetry func(*gorm.DB) error,
	query func(*Service) ([]T, error),
	check func(*testing.T, []T),
) {
	t.Helper()

	mainDB := testutil.NewSQLiteTestDB(t, models...)
	telemetryDB := testutil.NewSQLiteTestDB(t, models...)

	if err := seedMain(mainDB); err != nil {
		t.Fatalf("failed to seed main: %v", err)
	}
	if err := seedTelemetry(telemetryDB); err != nil {
		t.Fatalf("failed to seed telemetry: %v", err)
	}

	svc := &Service{DB: mainDB, TelemetryDB: telemetryDB}
	results, err := query(svc)
	if err != nil {
		t.Fatalf("query returned error: %v", err)
	}
	check(t, results)
}

func TestDBRoutingReadsFromTelemetryDB(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("audit", func(t *testing.T) {
		testReadsFromTelemetryDB(t,
			[]any{&infoModels.AuditRecord{}},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.AuditRecord{
					ID: 1, User: "legacy-main", AuthType: "password", Node: "node-a",
					Started: now, Ended: now, Action: `{"method":"GET"}`, Status: "success",
					CreatedAt: now, UpdatedAt: now, Version: 2,
				}).Error
			},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.AuditRecord{
					ID: 2, User: "telemetry", AuthType: "token", Node: "node-a",
					Started: now.Add(time.Second), Ended: now.Add(time.Second),
					Action: `{"method":"POST"}`, Status: "success",
					CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), Version: 2,
				}).Error
			},
			func(svc *Service) ([]infoModels.AuditRecord, error) {
				return svc.GetAuditRecords(10)
			},
			func(t *testing.T, results []infoModels.AuditRecord) {
				if len(results) != 1 {
					t.Fatalf("expected one telemetry row, got %d", len(results))
				}
				if results[0].ID != 2 {
					t.Fatalf("expected telemetry row ID 2, got %d", results[0].ID)
				}
			},
		)
	})

	t.Run("cpu", func(t *testing.T) {
		testReadsFromTelemetryDB(t,
			[]any{&infoModels.CPU{}},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.CPU{ID: 1, Usage: 10.0, CreatedAt: now}).Error
			},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.CPU{ID: 2, Usage: 20.0, CreatedAt: now.Add(time.Second)}).Error
			},
			func(svc *Service) ([]infoModels.CPU, error) {
				return svc.GetCPUUsageHistorical()
			},
			func(t *testing.T, results []infoModels.CPU) {
				if len(results) != 1 {
					t.Fatalf("expected one telemetry row, got %d", len(results))
				}
				if results[0].ID != 2 {
					t.Fatalf("expected telemetry row ID 2, got %d", results[0].ID)
				}
			},
		)
	})

	t.Run("ram", func(t *testing.T) {
		testReadsFromTelemetryDB(t,
			[]any{&infoModels.RAM{}},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.RAM{ID: 1, Usage: 10.0, CreatedAt: now}).Error
			},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.RAM{ID: 2, Usage: 20.0, CreatedAt: now.Add(time.Second)}).Error
			},
			func(svc *Service) ([]infoModels.RAM, error) {
				return svc.GetRAMUsageHistorical()
			},
			func(t *testing.T, results []infoModels.RAM) {
				if len(results) != 1 {
					t.Fatalf("expected one telemetry row, got %d", len(results))
				}
				if results[0].ID != 2 {
					t.Fatalf("expected telemetry row ID 2, got %d", results[0].ID)
				}
			},
		)
	})

	t.Run("swap", func(t *testing.T) {
		testReadsFromTelemetryDB(t,
			[]any{&infoModels.Swap{}},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.Swap{ID: 1, Usage: 5.0, CreatedAt: now}).Error
			},
			func(db *gorm.DB) error {
				return db.Create(&infoModels.Swap{ID: 2, Usage: 15.0, CreatedAt: now.Add(time.Second)}).Error
			},
			func(svc *Service) ([]infoModels.Swap, error) {
				return svc.GetSwapUsageHistorical()
			},
			func(t *testing.T, results []infoModels.Swap) {
				if len(results) != 1 {
					t.Fatalf("expected one telemetry row, got %d", len(results))
				}
				if results[0].ID != 2 {
					t.Fatalf("expected telemetry row ID 2, got %d", results[0].ID)
				}
			},
		)
	})

	t.Run("network", func(t *testing.T) {
		testReadsFromTelemetryDB(t,
			[]any{&infoModels.NetworkInterface{}},
			func(db *gorm.DB) error {
				return db.Create(&[]infoModels.NetworkInterface{
					{ID: 1, Name: "igb0", Network: "link#1", IsDelta: false, ReceivedBytes: 100, SentBytes: 100, CreatedAt: now, UpdatedAt: now},
					{ID: 2, Name: "igb0", Network: "link#1", IsDelta: false, ReceivedBytes: 200, SentBytes: 300, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
				}).Error
			},
			func(db *gorm.DB) error {
				return db.Create(&[]infoModels.NetworkInterface{
					{ID: 3, Name: "igb1", Network: "link#2", IsDelta: false, ReceivedBytes: 1000, SentBytes: 2000, CreatedAt: now, UpdatedAt: now},
					{ID: 4, Name: "igb1", Network: "link#2", IsDelta: false, ReceivedBytes: 1250, SentBytes: 2600, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
				}).Error
			},
			func(svc *Service) ([]infoServiceInterfaces.HistoricalNetworkInterface, error) {
				return svc.GetNetworkInterfacesHistorical()
			},
			func(t *testing.T, results []infoServiceInterfaces.HistoricalNetworkInterface) {
				if len(results) != 1 {
					t.Fatalf("expected one telemetry bucket row, got %d", len(results))
				}
				if results[0].ReceivedBytes != 250 {
					t.Fatalf("expected telemetry received delta 250, got %d", results[0].ReceivedBytes)
				}
				if results[0].SentBytes != 600 {
					t.Fatalf("expected telemetry sent delta 600, got %d", results[0].SentBytes)
				}
			},
		)
	})
}
