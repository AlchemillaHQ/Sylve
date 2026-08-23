// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import "time"

type StatsHistoryState string

const (
	StatsHistoryAvailable             StatsHistoryState = "available"
	StatsHistoryNeverRecorded         StatsHistoryState = "never-recorded"
	StatsHistoryOutsideSupportedRange StatsHistoryState = "outside-supported-range"
)

type StatsAvailability struct {
	ResolvedStep *GFSStep          `json:"resolvedStep"`
	LastSampleAt *time.Time        `json:"lastSampleAt"`
	HistoryState StatsHistoryState `json:"historyState"`
}

type StatsBootstrap[T any] struct {
	Points []T `json:"points"`
	StatsAvailability
}

func statsStepPointer(step GFSStep) *GFSStep {
	return &step
}

func ResolveStatsAvailability(now time.Time, latestAt *time.Time) StatsAvailability {
	if latestAt == nil {
		return StatsAvailability{
			ResolvedStep: nil,
			LastSampleAt: nil,
			HistoryState: StatsHistoryNeverRecorded,
		}
	}

	lastSampleAt := *latestAt
	comparisonTime := lastSampleAt
	if comparisonTime.After(now) {
		comparisonTime = now
	}
	age := now.Sub(comparisonTime)

	availability := StatsAvailability{
		LastSampleAt: &lastSampleAt,
		HistoryState: StatsHistoryAvailable,
	}

	switch {
	case age <= 24*time.Hour:
		availability.ResolvedStep = statsStepPointer(GFSStepHourly)
	case age <= 30*day:
		availability.ResolvedStep = statsStepPointer(GFSStepDaily)
	case age <= 70*day:
		availability.ResolvedStep = statsStepPointer(GFSStepWeekly)
	default:
		availability.ResolvedStep = nil
		availability.HistoryState = StatsHistoryOutsideSupportedRange
	}

	return availability
}

func BuildStatsBootstrap[T any](
	now time.Time,
	points []T,
	latestAt *time.Time,
) StatsBootstrap[T] {
	if points == nil {
		points = make([]T, 0)
	}

	return StatsBootstrap[T]{
		Points:            points,
		StatsAvailability: ResolveStatsAvailability(now, latestAt),
	}
}
