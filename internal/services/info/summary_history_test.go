// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"context"
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetSummaryHistoryBootstrapAndDelta(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t,
		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.NetworkInterface{},
	)
	now := time.Now().UTC().Truncate(time.Second)

	if err := database.Create(&[]infoModels.CPU{
		{ID: 1, Usage: 10, CreatedAt: now},
		{ID: 2, Usage: 20, CreatedAt: now.Add(time.Second)},
	}).Error; err != nil {
		t.Fatalf("seed CPU history: %v", err)
	}
	if err := database.Create(&[]infoModels.RAM{
		{ID: 1, Usage: 30, CreatedAt: now},
		{ID: 2, Usage: 40, CreatedAt: now.Add(time.Second)},
	}).Error; err != nil {
		t.Fatalf("seed RAM history: %v", err)
	}
	if err := database.Create(&[]infoModels.NetworkInterface{
		{ID: 1, IsDelta: true, ReceivedBytes: 100, SentBytes: 200, CreatedAt: now},
		{ID: 2, IsDelta: false, ReceivedBytes: 999, SentBytes: 999, CreatedAt: now.Add(time.Second)},
		{ID: 3, IsDelta: true, ReceivedBytes: 300, SentBytes: 400, CreatedAt: now.Add(2 * time.Second)},
	}).Error; err != nil {
		t.Fatalf("seed network history: %v", err)
	}

	service := &Service{TelemetryDB: database}
	bootstrap, err := service.GetSummaryHistory(context.Background(), nil)
	if err != nil {
		t.Fatalf("bootstrap summary history: %v", err)
	}

	if len(bootstrap.CPU) != 2 || len(bootstrap.RAM) != 2 || len(bootstrap.Network) != 2 {
		t.Fatalf(
			"unexpected bootstrap sizes: cpu=%d ram=%d network=%d",
			len(bootstrap.CPU), len(bootstrap.RAM), len(bootstrap.Network),
		)
	}
	if bootstrap.Cursors.CPU != 2 || bootstrap.Cursors.RAM != 2 || bootstrap.Cursors.Network != 3 {
		t.Fatalf("unexpected bootstrap cursors: %+v", bootstrap.Cursors)
	}
	if bootstrap.Network[1].ID != 3 || bootstrap.Network[1].ReceivedBytes != 300 {
		t.Fatalf("unexpected network bootstrap point: %+v", bootstrap.Network[1])
	}

	if err := database.Create(&infoModels.CPU{ID: 3, Usage: 50, CreatedAt: now.Add(3 * time.Second)}).Error; err != nil {
		t.Fatalf("append CPU history: %v", err)
	}
	if err := database.Create(&infoModels.RAM{ID: 3, Usage: 60, CreatedAt: now.Add(3 * time.Second)}).Error; err != nil {
		t.Fatalf("append RAM history: %v", err)
	}
	if err := database.Create(&infoModels.NetworkInterface{
		ID: 4, IsDelta: true, ReceivedBytes: 500, SentBytes: 600, CreatedAt: now.Add(3 * time.Second),
	}).Error; err != nil {
		t.Fatalf("append network history: %v", err)
	}

	delta, err := service.GetSummaryHistory(context.Background(), &bootstrap.Cursors)
	if err != nil {
		t.Fatalf("delta summary history: %v", err)
	}
	if len(delta.CPU) != 1 || delta.CPU[0].ID != 3 {
		t.Fatalf("unexpected CPU delta: %+v", delta.CPU)
	}
	if len(delta.RAM) != 1 || delta.RAM[0].ID != 3 {
		t.Fatalf("unexpected RAM delta: %+v", delta.RAM)
	}
	if len(delta.Network) != 1 || delta.Network[0].ID != 4 {
		t.Fatalf("unexpected network delta: %+v", delta.Network)
	}
	if delta.Cursors.CPU != 3 || delta.Cursors.RAM != 3 || delta.Cursors.Network != 4 {
		t.Fatalf("unexpected delta cursors: %+v", delta.Cursors)
	}
}

func TestGetSummaryHistoryEmptyDeltaRetainsCursors(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t,
		&infoModels.CPU{},
		&infoModels.RAM{},
		&infoModels.NetworkInterface{},
	)
	service := &Service{TelemetryDB: database}
	after := infoServiceInterfaces.SummaryHistoryCursors{CPU: 10, RAM: 20, Network: 30}

	delta, err := service.GetSummaryHistory(context.Background(), &after)
	if err != nil {
		t.Fatalf("empty delta summary history: %v", err)
	}
	if len(delta.CPU) != 0 || len(delta.RAM) != 0 || len(delta.Network) != 0 {
		t.Fatalf("expected empty delta, got %+v", delta)
	}
	if delta.Cursors != after {
		t.Fatalf("expected cursors %+v, got %+v", after, delta.Cursors)
	}
}
