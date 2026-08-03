// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type bootstrapTestPoint struct {
	ID        uint      `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

func TestResolveStatsAvailability(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		latestAt  *time.Time
		wantStep  *GFSStep
		wantState StatsHistoryState
	}{
		{
			name:      "never recorded",
			wantState: StatsHistoryNeverRecorded,
		},
		{
			name:      "exactly 24 hours resolves hourly",
			latestAt:  timePointer(now.Add(-24 * time.Hour)),
			wantStep:  statsStepPointer(GFSStepHourly),
			wantState: StatsHistoryAvailable,
		},
		{
			name:      "after 24 hours resolves daily",
			latestAt:  timePointer(now.Add(-24*time.Hour - time.Second)),
			wantStep:  statsStepPointer(GFSStepDaily),
			wantState: StatsHistoryAvailable,
		},
		{
			name:      "exactly 30 days resolves daily",
			latestAt:  timePointer(now.Add(-30 * day)),
			wantStep:  statsStepPointer(GFSStepDaily),
			wantState: StatsHistoryAvailable,
		},
		{
			name:      "after 30 days resolves weekly",
			latestAt:  timePointer(now.Add(-30*day - time.Second)),
			wantStep:  statsStepPointer(GFSStepWeekly),
			wantState: StatsHistoryAvailable,
		},
		{
			name:      "exactly 70 days resolves weekly",
			latestAt:  timePointer(now.Add(-70 * day)),
			wantStep:  statsStepPointer(GFSStepWeekly),
			wantState: StatsHistoryAvailable,
		},
		{
			name:      "after 70 days is outside supported range",
			latestAt:  timePointer(now.Add(-70*day - time.Second)),
			wantState: StatsHistoryOutsideSupportedRange,
		},
		{
			name:      "future timestamp is clamped for resolution",
			latestAt:  timePointer(now.Add(time.Hour)),
			wantStep:  statsStepPointer(GFSStepHourly),
			wantState: StatsHistoryAvailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStatsAvailability(now, tt.latestAt)
			if !equalOptionalStep(got.ResolvedStep, tt.wantStep) {
				t.Fatalf("resolved step: got %v, want %v", got.ResolvedStep, tt.wantStep)
			}
			if got.HistoryState != tt.wantState {
				t.Fatalf("history state: got %q, want %q", got.HistoryState, tt.wantState)
			}
		})
	}
}

func TestBuildStatsBootstrapPackagesResolvedWindow(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	latestAt := now.Add(-3 * day)
	points := []bootstrapTestPoint{
		{ID: 2, CreatedAt: now.Add(-20 * day)},
		{ID: 3, CreatedAt: now.Add(-3 * day)},
	}

	got := BuildStatsBootstrap(now, points, &latestAt)
	if got.ResolvedStep == nil || *got.ResolvedStep != GFSStepDaily {
		t.Fatalf("resolved step: got %v, want %q", got.ResolvedStep, GFSStepDaily)
	}
	if len(got.Points) != 2 || got.Points[0].ID != 2 || got.Points[1].ID != 3 {
		t.Fatalf("points: got %+v, want IDs 2 and 3", got.Points)
	}
}

func TestBuildStatsBootstrapEmptyJSONUsesArraysAndNulls(t *testing.T) {
	got := BuildStatsBootstrap[bootstrapTestPoint](time.Now(), nil, nil)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal bootstrap: %v", err)
	}

	jsonText := string(encoded)
	for _, fragment := range []string{
		`"points":[]`,
		`"resolvedStep":null`,
		`"lastSampleAt":null`,
		`"historyState":"never-recorded"`,
	} {
		if !strings.Contains(jsonText, fragment) {
			t.Fatalf("bootstrap JSON %s does not contain %s", jsonText, fragment)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func equalOptionalStep(a, b *GFSStep) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
