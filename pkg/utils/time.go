// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"regexp"
	"strconv"
	"time"
)

func ParseZfsTimeUnit(value string) int64 {
	if value == "-" {
		return 0
	}
	re := regexp.MustCompile(`([\d.]+)([a-zA-Z]*)`)
	matches := re.FindStringSubmatch(value)
	if len(matches) != 3 {
		return 0
	}
	num, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}
	unit := matches[2]
	switch unit {
	case "us":
		return int64(num)
	case "ms":
		return int64(num * 1000)
	case "s":
		return int64(num * 1000000)
	default:
		return int64(num)
	}
}

// ComputeLocalBoundary returns the local, wall-clock boundary for your supported intervals.
// - 60s:   previous minute boundary (last minute)
// - 3600s: start of this hour
// - 86400: start of today (local midnight)
// - 604800: start of ISO week (Monday 00:00 local)
// - 30*86400: first of this month 00:00
// - 365*86400: Jan 1 this year 00:00
// NOTE: do NOT use time.Truncate for hour/day/week/month/year in half-hour timezones.
func ComputeLocalBoundary(intervalSec int, now time.Time) time.Time {
	loc := time.Local
	n := now.In(loc)

	switch intervalSec {
	case 60:
		// previous minute boundary
		return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), 0, 0, loc).Add(-time.Minute)

	case 3600:
		// start of this hour (HH:00 local)
		return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), 0, 0, 0, loc)

	case 86400:
		// start of today
		return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)

	case 7 * 86400:
		// start of ISO week (Mon 00:00 local)
		wd := int(n.Weekday())
		if wd == 0 {
			wd = 7
		} // Sunday -> 7
		monday := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(wd - 1))
		return monday

	case 30 * 86400:
		// start of this month
		return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)

	case 365 * 86400:
		// start of this year
		return time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, loc)

	default:
		// Fallback for other durations: if it's a multiple of minutes, snap by fields; else use Truncate.
		d := time.Duration(intervalSec) * time.Second
		if d%time.Minute == 0 {
			mins := int(d / time.Minute) // e.g., 5, 15, 120
			// floor minute to nearest multiple
			m := (n.Minute() / mins) * mins
			return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), m, 0, 0, loc)
		}
		// As a last resort; may not be wall-clock aligned in half-hour zones.
		return n.Add(-time.Duration(n.Second()) * time.Second).Truncate(d)
	}
}
