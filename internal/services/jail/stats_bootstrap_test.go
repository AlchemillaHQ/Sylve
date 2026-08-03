// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newJailStatsTestService(t *testing.T) (*Service, jailModels.Jail) {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.JailStats{})
	jail := jailModels.Jail{CTID: 104, Name: "stats-jail", Type: jailModels.JailTypeFreeBSD}
	if err := database.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	return &Service{DB: database}, jail
}

func TestGetJailUsageBootstrapReturnsConsistentWeeklySnapshot(t *testing.T) {
	service, jail := newJailStatsTestService(t)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	stats := []jailModels.JailStats{
		{JailID: jail.ID, CPUUsage: 1, CreatedAt: now.Add(-60 * 24 * time.Hour)},
		{JailID: jail.ID, CPUUsage: 2, CreatedAt: now.Add(-45 * 24 * time.Hour)},
		{JailID: jail.ID, CPUUsage: 3, CreatedAt: now.Add(-35 * 24 * time.Hour)},
	}
	if err := service.DB.Create(&stats).Error; err != nil {
		t.Fatalf("seed jail stats: %v", err)
	}

	got, err := service.getJailUsageBootstrapAt(jail.CTID, now)
	if err != nil {
		t.Fatalf("get jail usage bootstrap: %v", err)
	}
	if got.ResolvedStep == nil || *got.ResolvedStep != db.GFSStepWeekly {
		t.Fatalf("resolved step = %v, want weekly", got.ResolvedStep)
	}
	if got.HistoryState != db.StatsHistoryAvailable {
		t.Fatalf("history state = %q, want available", got.HistoryState)
	}
	if len(got.Points) != 3 || got.Points[2].CPUUsage != 3 {
		t.Fatalf("points = %+v, want all points in the weekly window", got.Points)
	}
	if got.LastSampleAt == nil || !got.LastSampleAt.Equal(stats[2].CreatedAt) {
		t.Fatalf("last sample = %v, want %v", got.LastSampleAt, stats[2].CreatedAt)
	}
}

func TestGetJailUsageBootstrapNoHistory(t *testing.T) {
	service, jail := newJailStatsTestService(t)

	got, err := service.getJailUsageBootstrapAt(jail.CTID, time.Now())
	if err != nil {
		t.Fatalf("get empty jail usage bootstrap: %v", err)
	}
	if got.HistoryState != db.StatsHistoryNeverRecorded || got.Points == nil {
		t.Fatalf("bootstrap = %+v, want never-recorded with a non-nil points array", got)
	}
}

func TestGetJailUsageBootstrapNotFound(t *testing.T) {
	service, _ := newJailStatsTestService(t)

	_, err := service.getJailUsageBootstrapAt(999, time.Now())
	if err == nil || !strings.Contains(err.Error(), "jail_not_found") {
		t.Fatalf("error = %v, want jail_not_found", err)
	}
}

func TestGetJailUsageEmptyExplicitRangeSerializesAsArray(t *testing.T) {
	service, jail := newJailStatsTestService(t)

	got, err := service.GetJailUsage(jail.CTID, db.GFSStepHourly)
	if err != nil {
		t.Fatalf("get explicit jail usage: %v", err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal explicit jail usage: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("explicit empty jail usage JSON = %s, want []", encoded)
	}
}
