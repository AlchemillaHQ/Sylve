// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"strings"
	"testing"
	"time"
)

func TestNextCronTime(t *testing.T) {
	after := time.Date(2026, time.October, 18, 3, 15, 0, 0, time.Local)
	want := time.Date(2026, time.October, 25, 3, 0, 0, 0, time.Local).UTC()
	next, err := NextCronTime("0 3 * * 0", after)
	if err != nil || !next.Equal(want) || next.Location() != time.UTC {
		t.Fatalf("next local occurrence: got %v, want %v: %v", next, want, err)
	}
	for _, expression := range []string{"", "* * * * 8", "0 3 31 2 *", "0 0 0 * * *", "@hourly", "@every 1h", "CRON_TZ=UTC 0 3 * * *", strings.Repeat(" ", 257) + "* * * * *"} {
		if _, err := NextCronTime(expression, after); err == nil {
			t.Fatalf("accepted invalid five-field cron: %q", expression)
		}
	}
}
