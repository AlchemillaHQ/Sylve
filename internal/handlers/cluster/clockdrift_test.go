// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/services/auth"
)

func TestMeasureClockOffset(t *testing.T) {
	start := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	responseAt := start.Add(100 * time.Millisecond)
	midpoint := start.Add(50 * time.Millisecond)

	tests := []struct {
		name       string
		serverTime string
		start      time.Time
		responseAt time.Time
		want       time.Duration
		wantKnown  bool
	}{
		{
			name:       "peer ahead",
			serverTime: midpoint.Add(90 * time.Second).Format(time.RFC3339Nano),
			start:      start, responseAt: responseAt, want: 90 * time.Second, wantKnown: true,
		},
		{
			name:       "peer behind",
			serverTime: midpoint.Add(-6 * time.Minute).Format(time.RFC3339Nano),
			start:      start, responseAt: responseAt, want: -6 * time.Minute, wantKnown: true,
		},
		{name: "missing", start: start, responseAt: responseAt},
		{name: "malformed", serverTime: "yesterday", start: start, responseAt: responseAt},
		{
			name:       "slow response",
			serverTime: midpoint.Format(time.RFC3339Nano),
			start:      start, responseAt: start.Add(3 * time.Second),
		},
		{
			name:       "reversed window",
			serverTime: midpoint.Format(time.RFC3339Nano),
			start:      start, responseAt: start.Add(-time.Second),
		},
		{
			name:       "zero request start",
			serverTime: midpoint.Format(time.RFC3339Nano),
			responseAt: responseAt,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, known := measureClockOffset(test.serverTime, test.start, test.responseAt)
			if known != test.wantKnown {
				t.Fatalf("known = %v, want %v", known, test.wantKnown)
			}
			if known && got != test.want {
				t.Fatalf("offset = %s, want %s", got, test.want)
			}
		})
	}
}

func TestJoinClockDriftBlocked(t *testing.T) {
	behindLimit := auth.ClusterTokenFutureSkew - joinClockBehindMargin
	aheadLimit := -(auth.ClusterTokenTTL - joinClockAheadMargin)

	tests := []struct {
		name   string
		offset time.Duration
		want   bool
	}{
		{name: "in sync", offset: 0},
		{name: "inside behind", offset: behindLimit - time.Millisecond},
		{name: "at behind limit", offset: behindLimit},
		{name: "over behind", offset: behindLimit + time.Millisecond, want: true},
		{name: "inside ahead", offset: aheadLimit + time.Millisecond},
		{name: "at ahead limit", offset: aheadLimit},
		{name: "over ahead", offset: aheadLimit - time.Millisecond, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := joinClockDriftBlocked(test.offset); got != test.want {
				t.Fatalf("blocked = %v, want %v for offset %s", got, test.want, test.offset)
			}
		})
	}
}

func TestFormatClockOffset(t *testing.T) {
	tests := []struct {
		offset time.Duration
		want   string
	}{
		{offset: 0, want: "+0s"},
		{offset: 90 * time.Second, want: "+1m30s"},
		{offset: -4 * time.Minute, want: "-4m0s"},
	}

	for _, test := range tests {
		if got := formatClockOffset(test.offset); got != test.want {
			t.Fatalf("formatClockOffset(%s) = %q, want %q", test.offset, got, test.want)
		}
	}
}
