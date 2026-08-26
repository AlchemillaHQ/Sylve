// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import "testing"

func TestResolveLogicalCoreCount(t *testing.T) {
	tests := []struct {
		name       string
		systemWide int
		fallback   int
		want       int16
	}{
		{
			name:       "prefer system-wide count on multi-socket host",
			systemWide: 56,
			fallback:   28,
			want:       56,
		},
		{
			name:       "use fallback when system-wide count is unavailable",
			systemWide: 0,
			fallback:   28,
			want:       28,
		},
		{
			name:       "use minimum when neither detector succeeds",
			systemWide: 0,
			fallback:   0,
			want:       1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLogicalCoreCount(tt.systemWide, tt.fallback); got != tt.want {
				t.Fatalf("resolveLogicalCoreCount(%d, %d) = %d, want %d", tt.systemWide, tt.fallback, got, tt.want)
			}
		})
	}
}
